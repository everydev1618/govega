package serve

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/mcp"
)

// --- Config Upload/Get Handlers ---

// ConfigUploadResult describes the outcome of a YAML config upload.
type ConfigUploadResult struct {
	Name           string   `json:"name,omitempty"`
	AgentsCreated  []string `json:"agents_created,omitempty"`
	AgentsUpdated  []string `json:"agents_updated,omitempty"`
	AgentsSkipped  []string `json:"agents_skipped,omitempty"`
	MCPConnected   []string `json:"mcp_connected,omitempty"`
	MCPFailed      []string `json:"mcp_failed,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// ConfigResponse returns the current running configuration.
type ConfigResponse struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Agents      []ConfigAgentInfo   `json:"agents"`
	MCPServers  []ConfigMCPInfo     `json:"mcp_servers"`
	Settings    *ConfigSettingsInfo `json:"settings,omitempty"`
}

// ConfigAgentInfo is a summary of an agent for the config endpoint.
type ConfigAgentInfo struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Model       string   `json:"model"`
	Tools       []string `json:"tools,omitempty"`
	Team        []string `json:"team,omitempty"`
	Source      string   `json:"source"` // "yaml", "composed", or "builtin"
}

// ConfigMCPInfo describes a connected MCP server.
type ConfigMCPInfo struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Transport string `json:"transport,omitempty"`
}

// ConfigSettingsInfo surfaces key settings.
type ConfigSettingsInfo struct {
	DefaultModel string `json:"default_model,omitempty"`
}

// handleGetConfig returns the current running configuration.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	doc := s.interp.Document()

	// Build agent list.
	composedMap := make(map[string]bool)
	if composed, err := s.store.ListComposedAgents(); err == nil {
		for _, a := range composed {
			composedMap[a.Name] = true
		}
	}

	builtins := map[string]bool{"mother": true, "hermes": true}

	var agents []ConfigAgentInfo
	for name, def := range doc.Agents {
		if name == "mother" {
			continue
		}
		source := "yaml"
		if composedMap[name] {
			source = "composed"
		}
		if builtins[name] {
			source = "builtin"
		}
		agents = append(agents, ConfigAgentInfo{
			Name:        name,
			DisplayName: def.DisplayName,
			Model:       def.Model,
			Tools:       def.Tools,
			Team:        def.Team,
			Source:      source,
		})
	}

	// Build MCP server list.
	t := s.interp.Tools()
	var mcpServers []ConfigMCPInfo
	for _, st := range t.MCPServerStatuses() {
		mcpServers = append(mcpServers, ConfigMCPInfo{
			Name:      st.Name,
			Connected: st.Connected,
			Transport: st.Transport,
		})
	}
	// Include builtin servers.
	listed := make(map[string]bool, len(mcpServers))
	for _, m := range mcpServers {
		listed[m.Name] = true
	}
	for _, entry := range mcp.DefaultRegistry {
		if entry.BuiltinGo && !listed[entry.Name] && t.BuiltinServerConnected(entry.Name) {
			mcpServers = append(mcpServers, ConfigMCPInfo{
				Name:      entry.Name,
				Connected: true,
				Transport: "builtin",
			})
		}
	}

	var settings *ConfigSettingsInfo
	if doc.Settings != nil && doc.Settings.DefaultModel != "" {
		settings = &ConfigSettingsInfo{
			DefaultModel: doc.Settings.DefaultModel,
		}
	}

	writeJSON(w, http.StatusOK, ConfigResponse{
		Name:        doc.Name,
		Description: doc.Description,
		Agents:      agents,
		MCPServers:  mcpServers,
		Settings:    settings,
	})
}

// handleConfigUpload accepts a multipart .vega.yaml file upload, parses it,
// and creates/updates agents and MCP servers from the configuration.
func (s *Server) handleConfigUpload(w http.ResponseWriter, r *http.Request) {
	// Limit upload size to 2MB.
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "file upload required (multipart field 'file')"})
		return
	}
	defer file.Close()

	// Validate filename.
	if !strings.HasSuffix(header.Filename, ".yaml") && !strings.HasSuffix(header.Filename, ".yml") {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "file must be a .yaml or .yml file"})
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "failed to read uploaded file: " + err.Error()})
		return
	}

	// Parse the YAML.
	parser := dsl.NewParser()
	doc, err := parser.Parse(data)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Error: "invalid YAML: " + err.Error()})
		return
	}

	result := ConfigUploadResult{
		Name: doc.Name,
	}

	// Update document name/description if provided.
	currentDoc := s.interp.Document()
	if doc.Name != "" {
		currentDoc.Name = doc.Name
	}
	if doc.Description != "" {
		currentDoc.Description = doc.Description
	}

	// Update settings if provided.
	if doc.Settings != nil {
		if doc.Settings.DefaultModel != "" {
			if currentDoc.Settings == nil {
				currentDoc.Settings = &dsl.Settings{}
			}
			currentDoc.Settings.DefaultModel = doc.Settings.DefaultModel
		}
	}

	// Connect MCP servers defined in the YAML.
	if doc.Settings != nil && doc.Settings.MCP != nil {
		s.connectUploadedMCPServers(r.Context(), doc.Settings.MCP.Servers, &result)
	}

	// Create or update each agent.
	for name, agentDef := range doc.Agents {
		s.upsertUploadedAgent(name, agentDef, &result)
	}

	status := http.StatusOK
	if len(result.Errors) > 0 && len(result.AgentsCreated) == 0 && len(result.AgentsUpdated) == 0 {
		status = http.StatusUnprocessableEntity
	}

	writeJSON(w, status, result)
}

// connectUploadedMCPServers connects MCP servers from an uploaded YAML config.
func (s *Server) connectUploadedMCPServers(ctx context.Context, servers []dsl.MCPServerDef, result *ConfigUploadResult) {
	t := s.interp.Tools()

	for _, serverDef := range servers {
		if serverDef.Name == "" {
			result.Errors = append(result.Errors, "MCP server with empty name skipped")
			continue
		}

		// Skip if already connected.
		if t.MCPServerConnected(serverDef.Name) || t.BuiltinServerConnected(serverDef.Name) {
			result.MCPConnected = append(result.MCPConnected, serverDef.Name+" (already connected)")
			continue
		}

		var cfg mcp.ServerConfig

		if serverDef.FromRegistry {
			entry, ok := mcp.Lookup(serverDef.Name)
			if !ok {
				result.MCPFailed = append(result.MCPFailed, serverDef.Name)
				result.Errors = append(result.Errors, fmt.Sprintf("MCP server %q not found in registry", serverDef.Name))
				continue
			}
			envMap := expandEnvMap(serverDef.Env)
			cfg = entry.ToServerConfig(envMap)
		} else {
			cfg = mcp.ServerConfig{
				Name:    serverDef.Name,
				Command: serverDef.Command,
				Args:    serverDef.Args,
				URL:     serverDef.URL,
				Headers: serverDef.Headers,
				Env:     expandEnvMap(serverDef.Env),
			}
			if serverDef.Transport != "" {
				cfg.Transport = mcp.TransportType(serverDef.Transport)
			}
		}

		if serverDef.Timeout != "" {
			if d, err := time.ParseDuration(serverDef.Timeout); err == nil {
				cfg.Timeout = d
			}
		}

		connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := t.ConnectMCPServer(connectCtx, cfg)
		cancel()

		if err != nil {
			result.MCPFailed = append(result.MCPFailed, serverDef.Name)
			result.Errors = append(result.Errors, fmt.Sprintf("MCP %s: %s", serverDef.Name, err.Error()))
			continue
		}

		// Persist the MCP server config for auto-reconnect.
		s.persistMCPServer(ConnectMCPRequest{
			Name:      serverDef.Name,
			Command:   serverDef.Command,
			Args:      serverDef.Args,
			URL:       serverDef.URL,
			Headers:   serverDef.Headers,
			Env:       serverDef.Env,
			Transport: serverDef.Transport,
		})

		result.MCPConnected = append(result.MCPConnected, serverDef.Name)
		slog.Info("config upload: connected MCP server", "server", serverDef.Name)
	}
}

// upsertUploadedAgent creates or updates a single agent from an uploaded YAML.
func (s *Server) upsertUploadedAgent(name string, agentDef *dsl.Agent, result *ConfigUploadResult) {
	doc := s.interp.Document()

	// Skip meta-agents.
	if name == "mother" || name == "hermes" {
		result.AgentsSkipped = append(result.AgentsSkipped, name+" (reserved)")
		return
	}

	// Ensure the agent has a name set.
	if agentDef.Name == "" {
		agentDef.Name = name
	}

	// Apply default model from current settings if not specified.
	if agentDef.Model == "" && doc.Settings != nil {
		agentDef.Model = doc.Settings.DefaultModel
	}

	// Check if agent already exists.
	_, exists := doc.Agents[name]

	if exists {
		// Update: remove old agent, then re-add.
		if err := s.interp.RemoveAgent(name); err != nil {
			slog.Warn("config upload: failed to remove agent for update", "agent", name, "error", err)
		}

		if err := s.interp.AddAgent(name, agentDef); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("agent %s update failed: %s", name, err.Error()))
			return
		}

		// Update persistence.
		s.store.DeleteComposedAgent(name)
		s.persistComposedAgent(name, agentDef)

		result.AgentsUpdated = append(result.AgentsUpdated, name)
		slog.Info("config upload: updated agent", "agent", name)
	} else {
		// Create new agent.
		if err := s.interp.AddAgent(name, agentDef); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("agent %s creation failed: %s", name, err.Error()))
			return
		}

		s.persistComposedAgent(name, agentDef)

		result.AgentsCreated = append(result.AgentsCreated, name)
		s.broker.Publish(BrokerEvent{
			Type:      "agent.created",
			Agent:     name,
			Timestamp: time.Now(),
		})
		slog.Info("config upload: created agent", "agent", name)
	}
}

// persistComposedAgent saves an agent definition to the composed_agents table.
func (s *Server) persistComposedAgent(name string, def *dsl.Agent) {
	var skills []string
	if def.Skills != nil {
		skills = def.Skills.Directories
	}
	ca := ComposedAgent{
		Name:        name,
		DisplayName: def.DisplayName,
		Title:       def.Title,
		Avatar:      def.Avatar,
		Model:       def.Model,
		System:      def.System,
		Tools:       def.Tools,
		Team:        def.Team,
		Skills:      skills,
		Temperature: def.Temperature,
		CreatedAt:   time.Now(),
	}
	if err := s.store.InsertComposedAgent(ca); err != nil {
		slog.Error("config upload: failed to persist agent", "agent", name, "error", err)
	}
}

// expandEnvMap copies a string map, expanding $VAR references in values.
func expandEnvMap(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	result := make(map[string]string, len(env))
	for k, v := range env {
		result[k] = v
	}
	return result
}

