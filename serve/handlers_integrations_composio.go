package serve

// --- Brokered integrations catalog (Composio under the hood) ---
//
// Generic third-party app connections (Slack, GitHub, Jira, …). The wire
// contract (docs/openapi.yaml → "Integrations: brokered catalog") is
// provider-agnostic; this implementation brokers Composio:
//
//   - consent:   POST /api/v3/connected_accounts/link → hosted redirect URL
//   - status:    GET  /api/v3/connected_accounts?user_ids=<tenant>
//   - execution: each ACTIVE connected account is self-connected as an MCP
//     server (transport http) pointing at the toolkit's Composio MCP config
//     with ?user_id=<tenant>&connected_account_id=<ca> — so two GitHub
//     accounts become two MCP servers and agents pick per-account.
//
// Multi-account: a toolkit holds any number of connections. MCP servers are
// named "composio-<toolkit>" (first) / "composio-<toolkit>-N" — prefixed so
// they can never collide with MCP registry names (a registry name like
// "github" would silently launch the builtin server instead).
//
// Config: COMPOSIO_API_KEY enables the broker (unset → catalog lists with
// zero connections). COMPOSIO_USER_ID overrides the Composio user key
// (defaults to the tenant id, then "default"). COMPOSIO_BASE_URL is a test
// seam.
//
// Settings keys (sensitive=false; no secrets — the API key stays in env and
// MCP headers):
//   composio/auth_config/<toolkit>  → Composio auth config id (ac_…)
//   composio/mcp_config/<toolkit>   → Composio MCP server config id + URL
//   composio/connection/<ca_id>     → govega MCP server name for that account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/everydev1618/govega/internal/envcompat"
	"github.com/everydev1618/govega/mcp"
)

// --- Wire types (mirror docs/openapi.yaml) ---

type IntegrationConnectionResponse struct {
	ID           string  `json:"id"`
	AccountLabel *string `json:"account_label"`
	Status       string  `json:"status"`
	ConnectedAt  *string `json:"connected_at"`
}

type IntegrationResponse struct {
	ID          string                          `json:"id"`
	Name        string                          `json:"name"`
	Description string                          `json:"description"`
	IconURL     string                          `json:"icon_url"`
	Category    string                          `json:"category"`
	IsPopular   bool                            `json:"is_popular"`
	Connections []IntegrationConnectionResponse `json:"connections"`
}

type ConnectIntegrationRequest struct {
	CallbackURL string `json:"callback_url"`
}

type ConnectIntegrationResponse struct {
	RedirectURL string `json:"redirect_url"`
}

// --- Catalog ---

type integrationCatalogEntry struct {
	ID          string
	Name        string
	Description string
	Category    string
	IsPopular   bool
}

func (e integrationCatalogEntry) iconURL() string {
	return "https://logos.composio.dev/api/" + e.ID
}

var integrationCatalog = []integrationCatalogEntry{
	{ID: "github", Name: "GitHub", Description: "Source control and collaboration", Category: "dev_tools", IsPopular: true},
	{ID: "gitlab", Name: "GitLab", Description: "DevOps lifecycle platform", Category: "dev_tools"},
	{ID: "vercel", Name: "Vercel", Description: "Frontend deployment platform", Category: "dev_tools"},
	{ID: "sentry", Name: "Sentry", Description: "Error tracking and monitoring", Category: "dev_tools"},
	{ID: "slack", Name: "Slack", Description: "Team messaging and collaboration", Category: "communication", IsPopular: true},
	{ID: "microsoft_teams", Name: "Microsoft Teams", Description: "Enterprise communication platform", Category: "communication"},
	{ID: "discord", Name: "Discord", Description: "Community and team chat", Category: "communication"},
	{ID: "jira", Name: "Jira", Description: "Project tracking and management", Category: "project_mgmt", IsPopular: true},
	{ID: "linear", Name: "Linear", Description: "Modern issue tracking", Category: "project_mgmt"},
	{ID: "asana", Name: "Asana", Description: "Work management platform", Category: "project_mgmt"},
	{ID: "notion", Name: "Notion", Description: "All-in-one workspace", Category: "project_mgmt"},
	{ID: "datadog", Name: "Datadog", Description: "Infrastructure monitoring and analytics", Category: "analytics"},
	{ID: "amplitude", Name: "Amplitude", Description: "Product analytics platform", Category: "analytics"},
	{ID: "posthog", Name: "PostHog", Description: "Open-source product analytics", Category: "analytics"},
	{ID: "salesforce", Name: "Salesforce", Description: "Customer relationship management", Category: "crm"},
	{ID: "hubspot", Name: "HubSpot", Description: "Marketing and sales platform", Category: "crm"},
	{ID: "googlecalendar", Name: "Google Calendar", Description: "Scheduling and calendar management", Category: "communication"},
	{ID: "gmail", Name: "Gmail", Description: "Email reading, search, and sending", Category: "communication", IsPopular: true},
	{ID: "pagerduty", Name: "PagerDuty", Description: "Incident management", Category: "monitoring"},
	{ID: "grafana", Name: "Grafana", Description: "Observability and dashboards", Category: "monitoring"},
}

func catalogEntry(toolkit string) (integrationCatalogEntry, bool) {
	for _, e := range integrationCatalog {
		if e.ID == toolkit {
			return e, true
		}
	}
	return integrationCatalogEntry{}, false
}

// --- Composio client ---

const composioDefaultBaseURL = "https://backend.composio.dev"

type composioClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// composio returns a configured client or nil when COMPOSIO_API_KEY is unset.
func (s *Server) composio() *composioClient {
	key := os.Getenv("COMPOSIO_API_KEY")
	if key == "" {
		return nil
	}
	base := os.Getenv("COMPOSIO_BASE_URL")
	if base == "" {
		base = composioDefaultBaseURL
	}
	return &composioClient{
		apiKey:  key,
		baseURL: strings.TrimRight(base, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// composioUserID is the Composio "user" every connection is keyed under —
// one per tenant (workspace-level connections).
func (s *Server) composioUserID() string {
	if v := os.Getenv("COMPOSIO_USER_ID"); v != "" {
		return v
	}
	if v := envcompat.Get("VEGA_TENANT_ID"); v != "" {
		return v
	}
	return "default"
}

func (c *composioClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("composio %s %s: status %d: %s", method, path, resp.StatusCode, truncate(string(data), 2000))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type composioAccount struct {
	ID      string  `json:"id"`
	Alias   *string `json:"alias"`
	WordID  string  `json:"word_id"`
	UserID  string  `json:"user_id"`
	Status  string  `json:"status"`
	Toolkit struct {
		Slug string `json:"slug"`
	} `json:"toolkit"`
	CreatedAt string `json:"created_at"`
}

func (c *composioClient) listAccounts(ctx context.Context, userID string) ([]composioAccount, error) {
	var out struct {
		Items []composioAccount `json:"items"`
	}
	path := "/api/v3/connected_accounts?limit=100&user_ids=" + url.QueryEscape(userID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *composioClient) createLink(ctx context.Context, authConfigID, userID, callbackURL string) (string, error) {
	body := map[string]string{
		"auth_config_id": authConfigID,
		"user_id":        userID,
		"callback_url":   callbackURL,
	}
	var out struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v3/connected_accounts/link", body, &out); err != nil {
		return "", err
	}
	if out.RedirectURL == "" {
		return "", fmt.Errorf("composio link: empty redirect_url")
	}
	return out.RedirectURL, nil
}

func (c *composioClient) deleteAccount(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/api/v3/connected_accounts/"+url.PathEscape(id), nil, nil)
	// Idempotent: a 404 means it's already gone.
	if err != nil && strings.Contains(err.Error(), "status 404") {
		return nil
	}
	return err
}

// ensureAuthConfig returns the Composio auth config id for a toolkit,
// creating a Composio-managed one on first use and caching it in settings.
func (s *Server) ensureAuthConfig(ctx context.Context, c *composioClient, toolkit string) (string, error) {
	key := "composio/auth_config/" + toolkit
	if setting, err := s.store.GetSetting(key); err == nil && setting != nil && setting.Value != "" {
		return setting.Value, nil
	}

	// Reuse an existing config if one was created out-of-band.
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v3/auth_configs?toolkit_slug="+url.QueryEscape(toolkit), nil, &list); err == nil && len(list.Items) > 0 {
		id := list.Items[0].ID
		s.saveComposioSetting(key, id)
		return id, nil
	}

	body := map[string]any{
		"toolkit":     map[string]string{"slug": toolkit},
		"auth_config": map[string]string{"type": "use_composio_managed_auth"},
	}
	var created struct {
		AuthConfig struct {
			ID string `json:"id"`
		} `json:"auth_config"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v3/auth_configs", body, &created); err != nil {
		return "", err
	}
	if created.AuthConfig.ID == "" {
		return "", fmt.Errorf("composio auth config create: empty id")
	}
	s.saveComposioSetting(key, created.AuthConfig.ID)
	return created.AuthConfig.ID, nil
}

// ensureMCPConfig returns the toolkit's Composio MCP server base URL,
// creating the server config (restricted to Composio's "important" tools)
// on first use and caching it in settings.
func (s *Server) ensureMCPConfig(ctx context.Context, c *composioClient, toolkit, authConfigID string) (string, error) {
	key := "composio/mcp_config/" + toolkit
	if setting, err := s.store.GetSetting(key); err == nil && setting != nil && setting.Value != "" {
		return setting.Value, nil
	}

	// Restrict to the curated "important" subset — the full toolkit can be
	// hundreds of tools, which would flood agents' tool lists.
	var toolsResp struct {
		Items []struct {
			Slug string `json:"slug"`
		} `json:"items"`
	}
	var allowed []string
	if err := c.do(ctx, http.MethodGet, "/api/v3/tools?important=true&limit=200&toolkit_slug="+url.QueryEscape(toolkit), nil, &toolsResp); err == nil {
		for _, t := range toolsResp.Items {
			allowed = append(allowed, t.Slug)
		}
	}

	create := func(tools []string) (struct {
		ID     string `json:"id"`
		MCPURL string `json:"mcp_url"`
	}, error) {
		body := map[string]any{
			"name":            "apex-" + toolkit,
			"auth_config_ids": []string{authConfigID},
		}
		if len(tools) > 0 {
			body["allowed_tools"] = tools
		}
		var out struct {
			ID     string `json:"id"`
			MCPURL string `json:"mcp_url"`
		}
		err := c.do(ctx, http.MethodPost, "/api/v3/mcp/servers", body, &out)
		return out, err
	}

	created, err := create(allowed)
	if err != nil && len(allowed) > 0 {
		// Composio's "important" list can contain slugs its MCP server API
		// rejects ("Invalid tools provided … X, Y"). Strip the named slugs
		// and retry once.
		if rejected := rejectedToolSlugs(err.Error(), allowed); len(rejected) > 0 {
			kept := allowed[:0:0]
			for _, t := range allowed {
				if !rejected[t] {
					kept = append(kept, t)
				}
			}
			slog.Warn("composio: retrying mcp config without rejected tools", "toolkit", toolkit, "rejected", len(rejected))
			created, err = create(kept)
		}
	}
	if err != nil {
		return "", err
	}
	if created.MCPURL == "" {
		return "", fmt.Errorf("composio mcp server create: empty mcp_url")
	}
	s.saveComposioSetting(key, created.MCPURL)
	return created.MCPURL, nil
}

// rejectedToolSlugs returns which of the requested slugs an "Invalid tools
// provided" error message names.
func rejectedToolSlugs(errMsg string, requested []string) map[string]bool {
	if !strings.Contains(errMsg, "Invalid tools") {
		return nil
	}
	out := map[string]bool{}
	for _, t := range requested {
		if strings.Contains(errMsg, t) {
			out[t] = true
		}
	}
	return out
}

func (s *Server) saveComposioSetting(key, value string) {
	if err := s.store.UpsertSetting(Setting{Key: key, Value: value}); err != nil {
		slog.Error("composio: failed to save setting", "key", key, "error", err)
	}
}

// --- Connection ↔ MCP server mapping ---

func composioConnectionKey(connectionID string) string {
	return "composio/connection/" + connectionID
}

// composioServerName picks "composio-<toolkit>", suffixing -2, -3, … until
// the name is free among connected MCP servers.
func (s *Server) composioServerName(toolkit string) string {
	base := "composio-" + toolkit
	t := s.interp.Tools()
	name := base
	for n := 2; ; n++ {
		if !t.MCPServerConnected(name) && !t.BuiltinServerConnected(name) {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
}

// syncConnectionMCP makes sure an ACTIVE connected account has its MCP
// server connected and persisted. Best-effort: failures are logged and the
// account still lists.
func (s *Server) syncConnectionMCP(ctx context.Context, c *composioClient, acct composioAccount) {
	key := composioConnectionKey(acct.ID)
	if setting, err := s.store.GetSetting(key); err == nil && setting != nil && setting.Value != "" {
		return // already wired
	}

	authConfigID, err := s.ensureAuthConfig(ctx, c, acct.Toolkit.Slug)
	if err != nil {
		slog.Error("composio: auth config for mcp sync failed", "toolkit", acct.Toolkit.Slug, "error", err)
		return
	}
	mcpBase, err := s.ensureMCPConfig(ctx, c, acct.Toolkit.Slug, authConfigID)
	if err != nil {
		slog.Error("composio: mcp config failed", "toolkit", acct.Toolkit.Slug, "error", err)
		return
	}

	// The published mcp_url 307-redirects to <url>/mcp — connect directly.
	serverURL := fmt.Sprintf("%s/mcp?user_id=%s&connected_account_id=%s",
		strings.TrimRight(mcpBase, "/"), url.QueryEscape(acct.UserID), url.QueryEscape(acct.ID))

	name := s.composioServerName(acct.Toolkit.Slug)
	cfg := mcp.ServerConfig{
		Name:      name,
		Transport: mcp.TransportHTTP,
		URL:       serverURL,
		Headers:   map[string]string{"x-api-key": c.apiKey},
		Timeout:   30 * time.Second,
	}

	connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, err := s.interp.Tools().ConnectMCPServer(connectCtx, cfg); err != nil {
		slog.Error("composio: mcp connect failed", "server", name, "error", err)
		return
	}

	s.persistMCPServer(ConnectMCPRequest{
		Name:      name,
		Transport: "http",
		URL:       serverURL,
		Headers:   cfg.Headers,
		Timeout:   30,
	})
	s.saveComposioSetting(key, name)

	// Live agent processes snapshot their toolset at spawn (govega#57 bug
	// class) — without a reset they never see the just-connected tools
	// until the server restarts. Definitions and chat history survive;
	// each agent respawns lazily on its next message.
	s.interp.ResetAllAgents()
	slog.Info("composio: connected mcp server for account", "server", name, "account", acct.ID)
}

// teardownConnectionMCP disconnects and forgets the MCP server backing a
// connection. Idempotent.
func (s *Server) teardownConnectionMCP(connectionID string) {
	key := composioConnectionKey(connectionID)
	setting, err := s.store.GetSetting(key)
	if err != nil || setting == nil || setting.Value == "" {
		return
	}
	name := setting.Value
	t := s.interp.Tools()
	if t.MCPServerConnected(name) {
		if err := t.DisconnectMCPServer(name); err != nil {
			slog.Error("composio: mcp disconnect failed", "server", name, "error", err)
		}
	}
	if sqlStore, ok := s.store.(*SQLiteStore); ok {
		sqlStore.DeleteMCPServer(name)
	}
	if err := s.store.DeleteSetting(key); err != nil {
		slog.Error("composio: failed to delete connection mapping", "key", key, "error", err)
	}

	// Mirror of the reset in syncConnectionMCP: agents holding the removed
	// tools in their snapshot would keep calling a dead server.
	s.interp.ResetAllAgents()
}

func composioStatusToWire(status string) string {
	switch strings.ToUpper(status) {
	case "ACTIVE":
		return "active"
	case "INITIATED", "INITIALIZING":
		return "pending"
	default: // EXPIRED, FAILED, INACTIVE, …
		return "expired"
	}
}

// --- Handlers ---

// handleListIntegrations returns the catalog merged with this workspace's
// Composio connected accounts, lazily wiring MCP servers for accounts that
// became ACTIVE since the last call (that's how the consent round-trip
// completes server-side without a webhook).
func (s *Server) handleListIntegrations(w http.ResponseWriter, r *http.Request) {
	resp := make([]IntegrationResponse, 0, len(integrationCatalog))
	byToolkit := map[string][]IntegrationConnectionResponse{}

	if c := s.composio(); c != nil {
		accounts, err := c.listAccounts(r.Context(), s.composioUserID())
		if err != nil {
			slog.Error("composio: list accounts failed", "error", err)
			writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "connectivity provider error"})
			return
		}
		for _, acct := range accounts {
			status := composioStatusToWire(acct.Status)
			if status == "active" {
				s.syncConnectionMCP(r.Context(), c, acct)
			}
			label := acct.Alias
			if label == nil || *label == "" {
				if acct.WordID != "" {
					wid := acct.WordID
					label = &wid
				}
			}
			var connectedAt *string
			if status == "active" && acct.CreatedAt != "" {
				ca := acct.CreatedAt
				connectedAt = &ca
			}
			byToolkit[acct.Toolkit.Slug] = append(byToolkit[acct.Toolkit.Slug], IntegrationConnectionResponse{
				ID:           acct.ID,
				AccountLabel: label,
				Status:       status,
				ConnectedAt:  connectedAt,
			})
		}
	}

	for _, e := range integrationCatalog {
		conns := byToolkit[e.ID]
		if conns == nil {
			conns = []IntegrationConnectionResponse{}
		}
		resp = append(resp, IntegrationResponse{
			ID:          e.ID,
			Name:        e.Name,
			Description: e.Description,
			IconURL:     e.iconURL(),
			Category:    e.Category,
			IsPopular:   e.IsPopular,
			Connections: conns,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleConnectIntegration starts the OAuth consent flow for a NEW account
// on a toolkit and returns the hosted consent URL.
func (s *Server) handleConnectIntegration(w http.ResponseWriter, r *http.Request) {
	toolkit := r.PathValue("toolkit")
	if _, ok := catalogEntry(toolkit); !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "unknown integration " + toolkit})
		return
	}

	var req ConnectIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.CallbackURL) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "callback_url is required"})
		return
	}

	c := s.composio()
	if c == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "integrations broker not configured (COMPOSIO_API_KEY unset)"})
		return
	}

	authConfigID, err := s.ensureAuthConfig(r.Context(), c, toolkit)
	if err != nil {
		slog.Error("composio: ensure auth config failed", "toolkit", toolkit, "error", err)
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "connectivity provider error"})
		return
	}

	redirectURL, err := c.createLink(r.Context(), authConfigID, s.composioUserID(), req.CallbackURL)
	if err != nil {
		slog.Error("composio: create link failed", "toolkit", toolkit, "error", err)
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "connectivity provider error"})
		return
	}

	writeJSON(w, http.StatusOK, ConnectIntegrationResponse{RedirectURL: redirectURL})
}

// handleDisconnectIntegration removes one connected account and its MCP
// server. Idempotent.
func (s *Server) handleDisconnectIntegration(w http.ResponseWriter, r *http.Request) {
	toolkit := r.PathValue("toolkit")
	connectionID := r.PathValue("connection_id")
	if _, ok := catalogEntry(toolkit); !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "unknown integration " + toolkit})
		return
	}
	if connectionID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "connection_id is required"})
		return
	}

	c := s.composio()
	if c == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "integrations broker not configured (COMPOSIO_API_KEY unset)"})
		return
	}

	if err := c.deleteAccount(r.Context(), connectionID); err != nil {
		slog.Error("composio: delete account failed", "connection", connectionID, "error", err)
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "connectivity provider error"})
		return
	}
	s.teardownConnectionMCP(connectionID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
