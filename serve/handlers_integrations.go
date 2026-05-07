package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// --- Telegram ---

type telegramConfigureRequest struct {
	Token string `json:"token"`
	Agent string `json:"agent"`
}

func (s *Server) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.TelegramSnapshot())
}

func (s *Server) handleTelegramConfigure(w http.ResponseWriter, r *http.Request) {
	var req telegramConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Agent = strings.TrimSpace(req.Agent)
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "token is required"})
		return
	}

	// Persist before starting the bot so a process restart picks them up.
	if err := s.store.UpsertSetting(Setting{
		Key:       telegramTokenSettingKey,
		Value:     req.Token,
		Sensitive: true,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "save token: " + err.Error()})
		return
	}
	agentName := req.Agent
	if agentName == "" {
		agentName = s.cfg.Orchestrator.Name
	}
	if err := s.store.UpsertSetting(Setting{
		Key:   telegramAgentSettingKey,
		Value: agentName,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "save agent: " + err.Error()})
		return
	}

	if err := s.ConfigureTelegram(nil, req.Token, agentName); err != nil {
		// Roll back the persisted token so a bad value doesn't auto-revive
		// the bot on next restart.
		_ = s.store.DeleteSetting(telegramTokenSettingKey)
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "telegram: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.TelegramSnapshot())
}

func (s *Server) handleTelegramDisable(w http.ResponseWriter, r *http.Request) {
	s.StopTelegram()
	if err := s.store.DeleteSetting(telegramTokenSettingKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "delete token: " + err.Error()})
		return
	}
	_ = s.store.DeleteSetting(telegramAgentSettingKey)
	writeJSON(w, http.StatusOK, s.TelegramSnapshot())
}

// --- Gmail ---

// GmailStatus describes the current Gmail builtin server state for the
// integrations API. The fields mirror the keys we read at runtime.
type GmailStatus struct {
	Connected         bool `json:"connected"`
	HasClientID       bool `json:"has_client_id"`
	HasClientSecret   bool `json:"has_client_secret"`
	HasRefreshToken   bool `json:"has_refresh_token"`
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

