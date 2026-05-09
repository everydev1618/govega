package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// vapiServer is a built-in Go MCP server that wraps the Vapi.ai REST API.
// It exposes outbound-call primitives so any agent can place a phone call,
// poll its status, and inspect transcripts. Inbound voice (Iris-as-assistant)
// is handled separately via the /api/v1/integrations/vapi/* webhook routes.
//
// Required env: VAPI_API_KEY (a single static key from dashboard.vapi.ai).
// Optional env: VAPI_DEFAULT_PHONE_NUMBER_ID — when set, start_call no
// longer requires phone_number_id explicitly.
func vapiServer() *builtinMCPServer {
	return &builtinMCPServer{
		tools: map[string]ToolDef{
			"start_call": {
				Description: "Place an outbound phone call. Vapi runs the entire conversation (STT/LLM/TTS) using the named assistant — your code is not in the audio path. Returns the call id; poll with get_call to follow progress.",
				Fn:          ToolFunc(vapiStartCall),
				Params: map[string]ParamDef{
					"assistant_id": {
						Type:        "string",
						Description: "Vapi assistant id to drive the call. Get one with list_assistants.",
						Required:    true,
					},
					"to": {
						Type:        "string",
						Description: "Recipient phone number in E.164 format (e.g. +15551234567).",
						Required:    true,
					},
					"phone_number_id": {
						Type:        "string",
						Description: "Vapi phone number id used as the caller. Falls back to VAPI_DEFAULT_PHONE_NUMBER_ID when omitted.",
					},
					"context": {
						Type:        "object",
						Description: "Per-call variables passed as assistantOverrides.variableValues. Use to inject tenant_id, user_id, lead context, etc., that the assistant's prompt references via {{var_name}}.",
					},
				},
			},
			"get_call": {
				Description: "Fetch a single call by id — returns status (queued/ringing/in-progress/ended), recording URL, transcript when available.",
				Fn:          ToolFunc(vapiGetCall),
				Params: map[string]ParamDef{
					"call_id": {
						Type:        "string",
						Description: "Call id from start_call.",
						Required:    true,
					},
				},
			},
			"list_calls": {
				Description: "List recent calls. Filter by assistant or recency to review what an agent has been doing on the phones.",
				Fn:          ToolFunc(vapiListCalls),
				Params: map[string]ParamDef{
					"limit": {
						Type:        "integer",
						Description: "Maximum number of calls to return (default 25, max 100).",
					},
					"assistant_id": {
						Type:        "string",
						Description: "Only return calls placed via this assistant.",
					},
					"since": {
						Type:        "string",
						Description: "RFC3339 timestamp; only return calls created after this. Maps to Vapi's createdAtGt query.",
					},
				},
			},
			"list_assistants": {
				Description: "List all Vapi assistants the API key can see. Use this to discover assistant_id values for start_call.",
				Fn:          ToolFunc(vapiListAssistants),
				Params:      map[string]ParamDef{},
			},
		},
	}
}

// vapiAPIBase is overridable in tests via httptest. Production never changes it.
var vapiAPIBase = "https://api.vapi.ai"

// vapiRequest performs an authenticated HTTP request against the Vapi REST API.
// path is appended to vapiAPIBase. body is optional — when non-nil it's JSON-encoded.
func vapiRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	apiKey := os.Getenv("VAPI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("vapi: VAPI_API_KEY is not set")
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("vapi: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, vapiAPIBase+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("vapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vapi: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("vapi: %s %s -> %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

func vapiStartCall(ctx context.Context, params map[string]any) (string, error) {
	assistantID, _ := params["assistant_id"].(string)
	to, _ := params["to"].(string)
	if assistantID == "" {
		return "", fmt.Errorf("assistant_id is required")
	}
	if to == "" {
		return "", fmt.Errorf("to (E.164 phone number) is required")
	}

	phoneNumberID, _ := params["phone_number_id"].(string)
	if phoneNumberID == "" {
		phoneNumberID = os.Getenv("VAPI_DEFAULT_PHONE_NUMBER_ID")
	}
	if phoneNumberID == "" {
		return "", fmt.Errorf("phone_number_id is required (or set VAPI_DEFAULT_PHONE_NUMBER_ID)")
	}

	payload := map[string]any{
		"assistantId":   assistantID,
		"phoneNumberId": phoneNumberID,
		"customer":      map[string]any{"number": to},
	}

	if vars, ok := params["context"].(map[string]any); ok && len(vars) > 0 {
		payload["assistantOverrides"] = map[string]any{
			"variableValues": vars,
		}
	}

	body, err := vapiRequest(ctx, http.MethodPost, "/call", payload)
	if err != nil {
		return "", err
	}

	// Echo the response verbatim so the agent sees Vapi's full call object,
	// including queued/ringing status and any provider-side warnings.
	pretty, err := prettyJSON(body)
	if err != nil {
		return string(body), nil
	}
	return pretty, nil
}

func vapiGetCall(ctx context.Context, params map[string]any) (string, error) {
	id, _ := params["call_id"].(string)
	if id == "" {
		return "", fmt.Errorf("call_id is required")
	}
	body, err := vapiRequest(ctx, http.MethodGet, "/call/"+url.PathEscape(id), nil)
	if err != nil {
		return "", err
	}
	pretty, err := prettyJSON(body)
	if err != nil {
		return string(body), nil
	}
	return pretty, nil
}

func vapiListCalls(ctx context.Context, params map[string]any) (string, error) {
	qs := url.Values{}

	limit := 25
	if v, ok := toInt(params["limit"]); ok && v > 0 {
		if v > 100 {
			v = 100
		}
		limit = v
	}
	qs.Set("limit", strconv.Itoa(limit))

	if id, _ := params["assistant_id"].(string); id != "" {
		qs.Set("assistantId", id)
	}
	if since, _ := params["since"].(string); since != "" {
		qs.Set("createdAtGt", since)
	}

	body, err := vapiRequest(ctx, http.MethodGet, "/call?"+qs.Encode(), nil)
	if err != nil {
		return "", err
	}
	pretty, err := prettyJSON(body)
	if err != nil {
		return string(body), nil
	}
	return pretty, nil
}

func vapiListAssistants(ctx context.Context, _ map[string]any) (string, error) {
	body, err := vapiRequest(ctx, http.MethodGet, "/assistant", nil)
	if err != nil {
		return "", err
	}
	pretty, err := prettyJSON(body)
	if err != nil {
		return string(body), nil
	}
	return pretty, nil
}

// prettyJSON re-encodes a JSON byte slice with 2-space indentation for
// readable agent output. Falls back via the caller when the input isn't JSON.
func prettyJSON(b []byte) (string, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
