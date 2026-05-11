package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
)

// --- Per-agent tool toggle — refs govega#44 ---
//
// PATCH /api/v1/agents/{name}/tools/{tool} with body {"enabled": bool}.
// Single-tool granularity so apex-host-mgmt's Integrations tab can flip
// one toggle without a read-modify-write of the full tools array racing
// against a sibling toggle. The handler does the lookup, mutation, and
// re-spawn server-side; only the mutated tools list is echoed back.

func (s *Server) handleToggleAgentTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	toolName := r.PathValue("tool")
	if name == "" || toolName == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "agent and tool are required"})
		return
	}
	if name == s.cfg.Builder.Name {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: s.cfg.Builder.DisplayName + " cannot be modified"})
		return
	}

	var req ToggleAgentToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	// Resolve the current tool list. Prefer composed_agents (the runtime
	// source of truth after any PUT); fall back to the in-memory doc def
	// for YAML-defined agents that haven't been edited via API yet.
	current, err := s.currentAgentTools(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}

	updated := toggleToolInList(current, toolName, req.Enabled)
	if slicesEqual(current, updated) {
		// No-op — already in the requested state. Still return 200 with
		// the current list so the FE can confirm.
		writeJSON(w, http.StatusOK, AgentToolsResponse{Name: name, Tools: updated})
		return
	}

	if err := s.persistAgentTools(name, updated); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, AgentToolsResponse{Name: name, Tools: updated})
}

// currentAgentTools returns the agent's current tool list, preferring the
// composed_agents row (post-edit truth) over the in-memory doc def
// (YAML-only).
func (s *Server) currentAgentTools(name string) ([]string, error) {
	if composed, err := s.store.ListComposedAgents(); err == nil {
		for _, a := range composed {
			if a.Name == name && len(a.Tools) > 0 {
				return append([]string(nil), a.Tools...), nil
			}
		}
	}
	if def, ok := s.interp.Document().Agents[name]; ok {
		return append([]string(nil), def.Tools...), nil
	}
	return nil, fmt.Errorf("agent %q not found", name)
}

// persistAgentTools rebuilds the agent definition with the supplied tool
// list and saves it. Mirrors handleUpdateAgent's update path but isolated
// to tool mutation so it's safe to call from the toggle endpoint.
func (s *Server) persistAgentTools(name string, tools []string) error {
	// Look up the existing composed row, or promote from YAML if this is
	// the first edit (mirrors handleUpdateAgent's promote-on-first-PUT).
	composed, _ := s.store.ListComposedAgents()
	var existing *ComposedAgent
	for i := range composed {
		if composed[i].Name == name {
			existing = &composed[i]
			break
		}
	}
	if existing == nil {
		if def, ok := s.interp.Document().Agents[name]; ok {
			now := time.Now().UTC()
			hydrated := ComposedAgent{
				Name:           name,
				DisplayName:    def.DisplayName,
				Title:          def.Title,
				Description:    def.Description,
				Avatar:         def.Avatar,
				Icon:           def.Icon,
				AvatarGradient: def.AvatarGradient,
				Model:          def.Model,
				System:         def.System,
				Tools:          def.Tools,
				Team:           def.Team,
				Temperature:    def.Temperature,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			existing = &hydrated
		}
	}
	if existing == nil {
		return fmt.Errorf("agent %q not found", name)
	}
	existing.Tools = append([]string(nil), tools...)

	// Rebuild the dsl.Agent the same way handleUpdateAgent does, but
	// honor the explicit tool list instead of re-deriving from skills.
	if err := s.interp.RemoveAgent(name); err != nil {
		// Not fatal — interpreter may not have a registration if the
		// agent has been YAML-only up to this point.
		_ = err
	}
	system := existing.System
	if system == "" {
		system = dsl.DefaultAgentSystem(existing.DisplayName, name)
	}
	if len(existing.Team) > 0 {
		dsl.RegisterDelegateTool(s.interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
			return s.interp.SendToAgent(ctx, agent, message)
		}, func(ctx context.Context) []string {
			proc := vega.ProcessFromContext(ctx)
			if proc != nil && proc.Agent != nil {
				if def, ok := s.interp.Document().Agents[proc.Agent.Name]; ok {
					return def.Team
				}
			}
			return nil
		})
		system = dsl.BuildTeamPrompt(system, existing.Team, nil, false)
	}
	agentDef := &dsl.Agent{
		Name:           name,
		DisplayName:    existing.DisplayName,
		Title:          existing.Title,
		Description:    existing.Description,
		Avatar:         existing.Avatar,
		Icon:           existing.Icon,
		AvatarGradient: existing.AvatarGradient,
		Model:          existing.Model,
		System:         system,
		Tools:          existing.Tools,
		Temperature:    existing.Temperature,
	}
	if err := s.interp.AddAgent(name, agentDef); err != nil {
		return fmt.Errorf("re-spawn agent: %w", err)
	}
	existing.UpdatedAt = time.Now().UTC()
	return s.store.InsertComposedAgent(*existing)
}

// toggleToolInList returns a new tool list with `tool` added (enabled=true)
// or removed (enabled=false). If the tool is already in the requested
// state, returns a copy of the input unchanged.
func toggleToolInList(current []string, tool string, enabled bool) []string {
	idx := slices.Index(current, tool)
	if enabled {
		if idx >= 0 {
			return append([]string(nil), current...)
		}
		return append(append([]string(nil), current...), tool)
	}
	if idx < 0 {
		return append([]string(nil), current...)
	}
	out := make([]string, 0, len(current)-1)
	out = append(out, current[:idx]...)
	out = append(out, current[idx+1:]...)
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
