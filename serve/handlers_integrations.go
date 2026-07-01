package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// --- Telegram (multi-bot) ---

type telegramConfigureRequest struct {
	Token string `json:"token"`
	Agent string `json:"agent"`
	Label string `json:"label,omitempty"`
}

// handleTelegramStatus returns the list of configured bots (running or not).
// Tokens are never included.
func (s *Server) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.TelegramBotsSnapshot())
}

// handleTelegramConfigure adds a new bot or replaces an existing one with
// the same bot id (the numeric prefix of the token).
func (s *Server) handleTelegramConfigure(w http.ResponseWriter, r *http.Request) {
	var req telegramConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Agent = strings.TrimSpace(req.Agent)
	req.Label = strings.TrimSpace(req.Label)
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "token is required"})
		return
	}

	agentName := req.Agent
	if agentName == "" {
		agentName = s.cfg.Orchestrator.Name
	}

	// Validate the agent exists. The bot derives per-user clone names as
	// "<agent>:<userid>" so a bogus base produces hard failures.
	doc := s.interp.Document()
	if _, ok := doc.Agents[agentName]; !ok {
		var names []string
		for n, def := range doc.Agents {
			if def.IsMeta && n != s.cfg.Orchestrator.Name {
				continue
			}
			names = append(names, n)
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "agent " + strconv.Quote(agentName) + " does not exist; available: " + strings.Join(names, ", "),
		})
		return
	}

	if _, err := s.AddTelegramBot(nil, req.Token, agentName, req.Label); err != nil {
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "telegram: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.TelegramBotsSnapshot())
}

// handleTelegramRemove stops + drops one bot by id.
func (s *Server) handleTelegramRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "id is required"})
		return
	}
	if err := s.RemoveTelegramBot(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.TelegramBotsSnapshot())
}

// --- Discord (multi-bot) ---

type discordConfigureRequest struct {
	Token string `json:"token"`
	Agent string `json:"agent"`
	Label string `json:"label,omitempty"`
}

// handleDiscordStatus returns the list of configured bots (running or not).
// Tokens are never included.
func (s *Server) handleDiscordStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.DiscordBotsSnapshot())
}

// handleDiscordConfigure adds a new bot or replaces an existing one with the
// same bot id (the snowflake derived from the token).
func (s *Server) handleDiscordConfigure(w http.ResponseWriter, r *http.Request) {
	var req discordConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Agent = strings.TrimSpace(req.Agent)
	req.Label = strings.TrimSpace(req.Label)
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "token is required"})
		return
	}

	agentName := req.Agent
	if agentName == "" {
		agentName = s.cfg.Orchestrator.Name
	}

	doc := s.interp.Document()
	if _, ok := doc.Agents[agentName]; !ok {
		var names []string
		for n, def := range doc.Agents {
			if def.IsMeta && n != s.cfg.Orchestrator.Name {
				continue
			}
			names = append(names, n)
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "agent " + strconv.Quote(agentName) + " does not exist; available: " + strings.Join(names, ", "),
		})
		return
	}

	if _, err := s.AddDiscordBot(nil, req.Token, agentName, req.Label); err != nil {
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "discord: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.DiscordBotsSnapshot())
}

// handleDiscordRemove stops + drops one bot by id.
func (s *Server) handleDiscordRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "id is required"})
		return
	}
	if err := s.RemoveDiscordBot(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.DiscordBotsSnapshot())
}

// --- Gmail ---

// GmailStatus describes the current Gmail builtin server state for the
// integrations API. The fields mirror the keys we read at runtime.
type GmailStatus struct {
	Connected       bool `json:"connected"`
	HasClientID     bool `json:"has_client_id"`
	HasClientSecret bool `json:"has_client_secret"`
	HasRefreshToken bool `json:"has_refresh_token"`
}

const (
	gmailClientIDKey     = "GMAIL_CLIENT_ID"
	gmailClientSecretKey = "GMAIL_CLIENT_SECRET"
	gmailRefreshTokenKey = "GMAIL_REFRESH_TOKEN"
)

func (s *Server) gmailSnapshot() GmailStatus {
	t := s.interp.Tools()
	connected := t.BuiltinServerConnected("gmail")
	settings, _ := s.store.ListSettings()
	has := func(bare string) bool {
		nsKey := mcpSettingKey("gmail", bare)
		for _, st := range settings {
			if st.Key == nsKey || st.Key == bare {
				return st.Value != ""
			}
		}
		return false
	}
	return GmailStatus{
		Connected:       connected,
		HasClientID:     has(gmailClientIDKey),
		HasClientSecret: has(gmailClientSecretKey),
		HasRefreshToken: has(gmailRefreshTokenKey),
	}
}

func (s *Server) handleGmailStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.gmailSnapshot())
}

type gmailConfigureRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

// handleGmailConfigure persists Gmail credentials and connects the builtin
// server. Equivalent to POST /api/mcp/servers with name=gmail and the
// three creds in the env map, but with a friendlier shape for the
// Settings UI.
func (s *Server) handleGmailConfigure(w http.ResponseWriter, r *http.Request) {
	var req gmailConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.ClientID) == "" || strings.TrimSpace(req.ClientSecret) == "" || strings.TrimSpace(req.RefreshToken) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "client_id, client_secret, and refresh_token are all required"})
		return
	}

	connectReq := ConnectMCPRequest{
		Name: "gmail",
		Env: map[string]string{
			gmailClientIDKey:     req.ClientID,
			gmailClientSecretKey: req.ClientSecret,
			gmailRefreshTokenKey: req.RefreshToken,
		},
	}

	// If already connected, disconnect first so we re-init with new creds.
	t := s.interp.Tools()
	if t.BuiltinServerConnected("gmail") {
		_ = t.DisconnectBuiltinServer("gmail")
	}

	// Persist creds as namespaced sensitive settings.
	for k, v := range connectReq.Env {
		if err := s.store.UpsertSetting(Setting{
			Key:       mcpSettingKey("gmail", k),
			Value:     v,
			Sensitive: true,
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "save " + k + ": " + err.Error()})
			return
		}
	}
	s.refreshToolSettings()

	// Build env map from settings + request and apply to os.Setenv (the
	// builtin gmail server reads via os.Getenv).
	envMap := s.buildMCPEnvMap("gmail", connectReq.Env)
	for k, v := range envMap {
		os.Setenv(k, v)
	}

	if _, err := t.ConnectBuiltinServer(r.Context(), "gmail"); err != nil {
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "connect gmail: " + err.Error()})
		return
	}

	// Persist server-config so autoConnectPersistedServers picks it up
	// after a process restart.
	s.persistMCPServer(connectReq)

	writeJSON(w, http.StatusOK, s.gmailSnapshot())
}

func (s *Server) handleGmailDisable(w http.ResponseWriter, r *http.Request) {
	t := s.interp.Tools()
	if t.BuiltinServerConnected("gmail") {
		if err := t.DisconnectBuiltinServer("gmail"); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "disconnect: " + err.Error()})
			return
		}
	}
	// Drop persisted creds.
	for _, k := range []string{gmailClientIDKey, gmailClientSecretKey, gmailRefreshTokenKey} {
		_ = s.store.DeleteSetting(mcpSettingKey("gmail", k))
	}
	// Drop the persisted server entry.
	if sqlStore, ok := s.store.(*SQLiteStore); ok {
		_ = sqlStore.DeleteMCPServer("gmail")
	}
	writeJSON(w, http.StatusOK, s.gmailSnapshot())
}
