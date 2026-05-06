package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// gmailServer is a built-in Go MCP server that talks to the Gmail API
// using a bring-your-own OAuth refresh token. The required env vars are:
//   GMAIL_CLIENT_ID
//   GMAIL_CLIENT_SECRET
//   GMAIL_REFRESH_TOKEN
//
// Generate them via Google's OAuth Playground (https://developers.google.com/oauthplayground)
// with the gmail.modify scope. This is the lowest-friction path for testing
// Gmail tools without standing up a full OAuth flow on the server side.
//
// All tools call /gmail/v1/users/me/... — i.e. the authenticated user's
// own mailbox. Multi-user access requires a different auth strategy
// (OAuth code flow per user, or a domain-wide delegated service account).
func gmailServer() *builtinMCPServer {
	return &builtinMCPServer{
		tools: map[string]ToolDef{
			"list_messages": {
				Description: "Search Gmail and return a list of matching messages. Uses Gmail's standard search syntax (e.g. 'from:foo@bar.com', 'is:unread', 'newer_than:7d', 'label:newsletters'). Returns id, threadId, snippet, and key headers — call get_message to read the body. Default returns up to 25 messages.",
				Fn:          ToolFunc(gmailListMessages),
				Params: map[string]ParamDef{
					"query": {
						Type:        "string",
						Description: "Gmail search query (e.g. 'is:unread newer_than:1d'). Empty = all messages.",
					},
					"max_results": {
						Type:        "integer",
						Description: "Maximum number of messages to return (default 25, max 100).",
					},
				},
			},
			"get_message": {
				Description: "Fetch the full contents of a Gmail message by id. Returns headers (From, To, Subject, Date, Message-ID), label ids, snippet, and the plain-text body (HTML stripped). Use list_messages first to find the id.",
				Fn:          ToolFunc(gmailGetMessage),
				Params: map[string]ParamDef{
					"id": {
						Type:        "string",
						Description: "Message id from list_messages.",
						Required:    true,
					},
				},
			},
			"list_labels": {
				Description: "List every label in the mailbox (system labels like INBOX/STARRED and user-created labels). Use this before modify_labels so you know what labels already exist.",
				Fn:          ToolFunc(gmailListLabels),
				Params:      map[string]ParamDef{},
			},
			"create_label": {
				Description: "Create a new user-defined label. Fails if the label already exists. Returns the new label's id.",
				Fn:          ToolFunc(gmailCreateLabel),
				Params: map[string]ParamDef{
					"name": {
						Type:        "string",
						Description: "Label name. Use slashes for nesting (e.g. 'clients/acme').",
						Required:    true,
					},
				},
			},
			"modify_labels": {
				Description: "Add and/or remove labels on a message. Accepts label NAMES (not ids) — system labels like 'INBOX', 'STARRED', 'UNREAD', 'TRASH' work as-is. To archive a message, remove 'INBOX'. To mark as read, remove 'UNREAD'. Returns the message's updated label set.",
				Fn:          ToolFunc(gmailModifyLabels),
				Params: map[string]ParamDef{
					"id": {
						Type:        "string",
						Description: "Message id.",
						Required:    true,
					},
					"add_labels": {
						Type:        "array",
						Description: "Label names to add (e.g. ['STARRED', 'clients/acme']).",
					},
					"remove_labels": {
						Type:        "array",
						Description: "Label names to remove (e.g. ['INBOX'] to archive, ['UNREAD'] to mark read).",
					},
				},
			},
			"draft": {
				Description: "Create a Gmail draft (NOT sent — appears in the user's Drafts folder for review). Returns the draft id. To draft a reply, pass thread_id and in_reply_to from the source message's headers.",
				Fn:          ToolFunc(gmailDraft),
				Params: map[string]ParamDef{
					"to": {
						Type:        "string",
						Description: "Recipient email address (or comma-separated list).",
						Required:    true,
					},
					"subject": {
						Type:        "string",
						Description: "Subject line.",
						Required:    true,
					},
					"body": {
						Type:        "string",
						Description: "Plain-text body of the email.",
						Required:    true,
					},
					"thread_id": {
						Type:        "string",
						Description: "Optional Gmail thread id, set when drafting a reply so the draft attaches to the existing thread.",
					},
					"in_reply_to": {
						Type:        "string",
						Description: "Optional Message-ID header value of the message being replied to (sets In-Reply-To and References headers).",
					},
				},
			},
		},
	}
}

// --- Token management ---

// gmailTokenState caches the most recent access token across tool calls so
// we don't hit Google's /token endpoint for every API request.
var (
	gmailTokenMu      sync.Mutex
	gmailAccessToken  string
	gmailTokenExpires time.Time
)

const gmailAPIBase = "https://gmail.googleapis.com/gmail/v1/users/me"

// gmailAccessTokenForCtx returns a valid access token, refreshing via the
// stored refresh token when needed. Tokens are cached in-process and reused
// until ~60s before their expiry.
func gmailAccessTokenForCtx(ctx context.Context) (string, error) {
	gmailTokenMu.Lock()
	defer gmailTokenMu.Unlock()

	if gmailAccessToken != "" && time.Now().Before(gmailTokenExpires.Add(-60*time.Second)) {
		return gmailAccessToken, nil
	}

	clientID := os.Getenv("GMAIL_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_CLIENT_SECRET")
	refreshToken := os.Getenv("GMAIL_REFRESH_TOKEN")
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return "", fmt.Errorf("gmail: missing GMAIL_CLIENT_ID / GMAIL_CLIENT_SECRET / GMAIL_REFRESH_TOKEN")
	}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("gmail: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gmail: refresh token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gmail: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("gmail: decode token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("gmail: empty access_token in response: %s", string(body))
	}

	gmailAccessToken = tok.AccessToken
	gmailTokenExpires = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return gmailAccessToken, nil
}

// gmailRequest performs an authenticated HTTP request against the Gmail
// API. Path is appended to gmailAPIBase. body is optional — when non-nil
// it's marshaled as JSON.
func gmailRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	tok, err := gmailAccessTokenForCtx(ctx)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("gmail: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, gmailAPIBase+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("gmail: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmail: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gmail: %s %s -> %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// --- Label name resolution ---

// gmailLabelCache stores the most recent name→id mapping. Refreshed on
// every modify_labels / create_label call to stay in sync.
var (
	gmailLabelMu       sync.Mutex
	gmailLabelNameToID map[string]string
)

func gmailRefreshLabelCache(ctx context.Context) error {
	body, err := gmailRequest(ctx, http.MethodGet, "/labels", nil)
	if err != nil {
		return err
	}
	var list struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("gmail: decode labels: %w", err)
	}
	m := make(map[string]string, len(list.Labels))
	for _, l := range list.Labels {
		m[l.Name] = l.ID
	}
	gmailLabelMu.Lock()
	gmailLabelNameToID = m
	gmailLabelMu.Unlock()
	return nil
}

// gmailLabelID resolves a user-facing label name to its Gmail id. System
// labels (INBOX, STARRED, UNREAD, TRASH, SPAM, IMPORTANT, SENT, DRAFT,
// CATEGORY_*, etc.) use their name as the id directly. Unknown user
// labels return ("", false) so the caller can return a useful error.
func gmailLabelID(name string) (string, bool) {
	if isGmailSystemLabel(name) {
		return name, true
	}
	gmailLabelMu.Lock()
	defer gmailLabelMu.Unlock()
	id, ok := gmailLabelNameToID[name]
	return id, ok
}

func isGmailSystemLabel(name string) bool {
	switch name {
	case "INBOX", "STARRED", "UNREAD", "TRASH", "SPAM", "IMPORTANT",
		"SENT", "DRAFT", "CHAT",
		"CATEGORY_PERSONAL", "CATEGORY_SOCIAL", "CATEGORY_PROMOTIONS",
		"CATEGORY_UPDATES", "CATEGORY_FORUMS":
		return true
	}
	return false
}

// --- Tools ---

func gmailListMessages(ctx context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	maxResults := 25
	if v, ok := toInt(params["max_results"]); ok && v > 0 {
		if v > 100 {
			v = 100
		}
		maxResults = v
	}

	qs := url.Values{}
	if q != "" {
		qs.Set("q", q)
	}
	qs.Set("maxResults", fmt.Sprintf("%d", maxResults))

	body, err := gmailRequest(ctx, http.MethodGet, "/messages?"+qs.Encode(), nil)
	if err != nil {
		return "", err
	}

	var list struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
		ResultSizeEstimate int `json:"resultSizeEstimate"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("gmail: decode messages list: %w", err)
	}

	if len(list.Messages) == 0 {
		return "No messages match the query.", nil
	}

	type summary struct {
		ID      string `json:"id"`
		Thread  string `json:"thread_id"`
		Subject string `json:"subject"`
		From    string `json:"from"`
		Date    string `json:"date"`
		Snippet string `json:"snippet"`
	}

	out := make([]summary, 0, len(list.Messages))
	for _, m := range list.Messages {
		meta, err := gmailRequest(ctx, http.MethodGet,
			fmt.Sprintf("/messages/%s?format=metadata&metadataHeaders=From&metadataHeaders=Subject&metadataHeaders=Date", m.ID), nil)
		if err != nil {
			continue
		}
		var detail struct {
			Snippet string `json:"snippet"`
			Payload struct {
				Headers []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"headers"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(meta, &detail); err != nil {
			continue
		}
		s := summary{ID: m.ID, Thread: m.ThreadID, Snippet: detail.Snippet}
		for _, h := range detail.Payload.Headers {
			switch h.Name {
			case "From":
				s.From = h.Value
			case "Subject":
				s.Subject = h.Value
			case "Date":
				s.Date = h.Value
			}
		}
		out = append(out, s)
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

func gmailGetMessage(ctx context.Context, params map[string]any) (string, error) {
	id, _ := params["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	body, err := gmailRequest(ctx, http.MethodGet, "/messages/"+url.PathEscape(id)+"?format=full", nil)
	if err != nil {
		return "", err
	}

	var msg struct {
		ID       string   `json:"id"`
		ThreadID string   `json:"threadId"`
		LabelIDs []string `json:"labelIds"`
		Snippet  string   `json:"snippet"`
		Payload  gmailPayload
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return "", fmt.Errorf("gmail: decode message: %w", err)
	}

	headers := map[string]string{}
	for _, h := range msg.Payload.Headers {
		headers[h.Name] = h.Value
	}

	plain := extractPlainBody(msg.Payload)

	out := struct {
		ID       string            `json:"id"`
		ThreadID string            `json:"thread_id"`
		LabelIDs []string          `json:"label_ids"`
		Headers  map[string]string `json:"headers"`
		Snippet  string            `json:"snippet"`
		Body     string            `json:"body"`
	}{
		ID:       msg.ID,
		ThreadID: msg.ThreadID,
		LabelIDs: msg.LabelIDs,
		Headers:  headers,
		Snippet:  msg.Snippet,
		Body:     plain,
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// gmailPayload mirrors the recursive Gmail message payload structure.
type gmailPayload struct {
	MimeType string `json:"mimeType"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
		Size int    `json:"size"`
	} `json:"body"`
	Parts []gmailPayload `json:"parts"`
}

// extractPlainBody walks a multipart payload looking for the first
// text/plain part. Falls back to the first text/html part with HTML
// stripped via the existing fetch helper.
func extractPlainBody(p gmailPayload) string {
	if p.MimeType == "text/plain" && p.Body.Data != "" {
		return decodeBodyData(p.Body.Data)
	}
	if len(p.Parts) > 0 {
		for _, part := range p.Parts {
			if part.MimeType == "text/plain" && part.Body.Data != "" {
				return decodeBodyData(part.Body.Data)
			}
		}
		// Fallback: any nested text/plain.
		for _, part := range p.Parts {
			if found := extractPlainBody(part); found != "" {
				return found
			}
		}
		// Last resort: HTML body, stripped.
		for _, part := range p.Parts {
			if part.MimeType == "text/html" && part.Body.Data != "" {
				return stripHTML(decodeBodyData(part.Body.Data))
			}
		}
	}
	if p.MimeType == "text/html" && p.Body.Data != "" {
		return stripHTML(decodeBodyData(p.Body.Data))
	}
	return ""
}

func decodeBodyData(s string) string {
	// Gmail returns base64url-encoded bodies (no padding).
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		// Try with no padding.
		b, err = base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return ""
		}
	}
	return string(b)
}

func gmailListLabels(ctx context.Context, params map[string]any) (string, error) {
	if err := gmailRefreshLabelCache(ctx); err != nil {
		return "", err
	}
	gmailLabelMu.Lock()
	defer gmailLabelMu.Unlock()

	type entry struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	out := make([]entry, 0, len(gmailLabelNameToID))
	for name, id := range gmailLabelNameToID {
		out = append(out, entry{Name: name, ID: id})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

func gmailCreateLabel(ctx context.Context, params map[string]any) (string, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	body, err := gmailRequest(ctx, http.MethodPost, "/labels", map[string]any{
		"name":                  name,
		"labelListVisibility":   "labelShow",
		"messageListVisibility": "show",
	})
	if err != nil {
		return "", err
	}
	var label struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &label); err != nil {
		return "", fmt.Errorf("gmail: decode label: %w", err)
	}
	// Refresh the name→id cache so subsequent modify_labels calls find it.
	_ = gmailRefreshLabelCache(ctx)
	return fmt.Sprintf("Created label %q (id=%s).", label.Name, label.ID), nil
}

func gmailModifyLabels(ctx context.Context, params map[string]any) (string, error) {
	id, _ := params["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	addNames := toStringSlice(params["add_labels"])
	removeNames := toStringSlice(params["remove_labels"])
	if len(addNames) == 0 && len(removeNames) == 0 {
		return "", fmt.Errorf("at least one of add_labels or remove_labels is required")
	}

	// Refresh label cache so name resolution sees recent additions.
	if err := gmailRefreshLabelCache(ctx); err != nil {
		return "", err
	}

	addIDs := make([]string, 0, len(addNames))
	for _, name := range addNames {
		lid, ok := gmailLabelID(name)
		if !ok {
			return "", fmt.Errorf("label %q does not exist — call create_label first", name)
		}
		addIDs = append(addIDs, lid)
	}
	removeIDs := make([]string, 0, len(removeNames))
	for _, name := range removeNames {
		lid, ok := gmailLabelID(name)
		if !ok {
			return "", fmt.Errorf("label %q does not exist", name)
		}
		removeIDs = append(removeIDs, lid)
	}

	body, err := gmailRequest(ctx, http.MethodPost, "/messages/"+url.PathEscape(id)+"/modify", map[string]any{
		"addLabelIds":    addIDs,
		"removeLabelIds": removeIDs,
	})
	if err != nil {
		return "", err
	}
	var msg struct {
		ID       string   `json:"id"`
		LabelIDs []string `json:"labelIds"`
	}
	_ = json.Unmarshal(body, &msg)
	return fmt.Sprintf("Updated message %s. Labels now: %v", msg.ID, msg.LabelIDs), nil
}

// toStringSlice accepts []string, []any (with string elements), or a
// single string and returns a []string. Returns nil for any other input.
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case string:
		if s == "" {
			return nil
		}
		return []string{s}
	}
	return nil
}

func gmailDraft(ctx context.Context, params map[string]any) (string, error) {
	to, _ := params["to"].(string)
	subject, _ := params["subject"].(string)
	body, _ := params["body"].(string)
	threadID, _ := params["thread_id"].(string)
	inReplyTo, _ := params["in_reply_to"].(string)

	if to == "" || subject == "" || body == "" {
		return "", fmt.Errorf("to, subject, and body are required")
	}

	rfc := buildRFC2822(to, subject, body, inReplyTo)
	raw := base64.URLEncoding.EncodeToString([]byte(rfc))

	payload := map[string]any{
		"message": map[string]any{
			"raw": raw,
		},
	}
	if threadID != "" {
		payload["message"].(map[string]any)["threadId"] = threadID
	}

	respBody, err := gmailRequest(ctx, http.MethodPost, "/drafts", payload)
	if err != nil {
		return "", err
	}
	var draft struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &draft); err != nil {
		return "", fmt.Errorf("gmail: decode draft response: %w", err)
	}
	return fmt.Sprintf("Draft created (id=%s, message_id=%s, thread_id=%s). Visible in your Gmail Drafts folder for review and send.",
		draft.ID, draft.Message.ID, draft.Message.ThreadID), nil
}

func buildRFC2822(to, subject, body, inReplyTo string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "To: %s\r\n", to)
	fmt.Fprintf(&sb, "Subject: %s\r\n", subject)
	if inReplyTo != "" {
		fmt.Fprintf(&sb, "In-Reply-To: %s\r\n", inReplyTo)
		fmt.Fprintf(&sb, "References: %s\r\n", inReplyTo)
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
