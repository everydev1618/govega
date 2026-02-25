package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/tools"
	"github.com/everydev1618/vega-population/population"
	"gopkg.in/yaml.v3"
)

// --- Population Handlers ---

func (s *Server) handlePopulationSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	kind := r.URL.Query().Get("kind")

	opts := &population.SearchOptions{}
	if kind != "" {
		opts.Kind = population.ItemKind(kind)
	}

	results, err := s.popClient.Search(r.Context(), q, opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	resp := make([]PopulationSearchResult, 0, len(results))
	for _, r := range results {
		resp = append(resp, PopulationSearchResult{
			Kind:        string(r.Kind),
			Name:        r.Name,
			Version:     r.Version,
			Description: r.Description,
			Tags:        r.Tags,
			Score:       r.Score,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePopulationInfo(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name := r.PathValue("name")

	// Format the name with the appropriate prefix for the client.
	fullName := name
	switch kind {
	case "persona":
		fullName = "@" + name
	case "profile":
		fullName = "+" + name
	}

	info, err := s.popClient.Info(r.Context(), fullName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}

	// If installed, load the manifest to get the system_prompt.
	var systemPrompt string
	if info.InstalledPath != "" {
		manifest, err := population.LoadManifest(filepath.Join(info.InstalledPath, "vega.yaml"))
		if err == nil {
			systemPrompt = manifest.SystemPrompt
		}
	}

	resp := PopulationInfoResponse{
		Kind:              string(info.Kind),
		Name:              info.Name,
		Version:           info.Version,
		Description:       info.Description,
		Author:            info.Author,
		Tags:              info.Tags,
		Persona:           info.Persona,
		Skills:            info.Skills,
		RecommendedSkills: info.RecommendedSkills,
		SystemPrompt:      systemPrompt,
		Installed:         info.Installed,
		InstalledPath:     info.InstalledPath,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePopulationInstall(w http.ResponseWriter, r *http.Request) {
	var req PopulationInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "name is required"})
		return
	}

	if err := s.popClient.Install(r.Context(), req.Name, &population.InstallOptions{Force: true}); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "installed", "name": req.Name})
}

func (s *Server) handlePopulationInstalled(w http.ResponseWriter, r *http.Request) {
	kind := population.ItemKind(r.URL.Query().Get("kind"))

	items, err := s.popClient.List(kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	resp := make([]PopulationInstalledItem, 0, len(items))
	for _, item := range items {
		resp = append(resp, PopulationInstalledItem{
			Kind:    string(item.Kind),
			Name:    item.Name,
			Version: item.Version,
			Path:    item.Path,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Agent Composition Handlers ---

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "name is required"})
		return
	}

	// Build system prompt from persona if specified.
	system := req.System
	if req.Persona != "" && system == "" {
		info, err := s.popClient.Info(r.Context(), "@"+req.Persona)
		if err == nil && info.InstalledPath != "" {
			manifest, err := population.LoadManifest(filepath.Join(info.InstalledPath, "vega.yaml"))
			if err == nil {
				system = manifest.SystemPrompt
			}
		}
	}

	// Register skill tools and collect tool names.
	var toolNames []string
	for _, skillName := range req.Skills {
		names, err := s.registerSkillTools(skillName)
		if err != nil {
			slog.Warn("failed to register skill tools", "skill", skillName, "error", err)
			continue
		}
		toolNames = append(toolNames, names...)
	}

	// If the agent has a team, register the delegate tool and enrich the prompt.
	if len(req.Team) > 0 {
		dsl.RegisterDelegateTool(s.interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
			return s.interp.SendToAgent(ctx, agent, message)
		}, req.Team)
		toolNames = append(toolNames, "delegate")
		system = dsl.BuildTeamPrompt(system, req.Team, nil, false)
	}

	// Build DSL agent definition.
	agentDef := &dsl.Agent{
		Name:        req.Name,
		Model:       req.Model,
		System:      system,
		Tools:       toolNames,
		Temperature: req.Temperature,
	}

	if err := s.interp.AddAgent(req.Name, agentDef); err != nil {
		writeJSON(w, http.StatusConflict, ErrorResponse{Error: err.Error()})
		return
	}

	// Persist to SQLite.
	if err := s.store.InsertComposedAgent(ComposedAgent{
		Name:        req.Name,
		Model:       req.Model,
		Persona:     req.Persona,
		Skills:      req.Skills,
		Team:        req.Team,
		System:      system,
		Temperature: req.Temperature,
		CreatedAt:   time.Now(),
	}); err != nil {
		slog.Error("failed to persist composed agent", "agent", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "agent created in memory but failed to persist: " + err.Error()})
		return
	}

	// Get process ID from the spawned agent.
	agents := s.interp.Agents()
	procID := ""
	if proc, ok := agents[req.Name]; ok {
		procID = proc.ID
	}

	writeJSON(w, http.StatusCreated, CreateAgentResponse{
		Name:      req.Name,
		Model:     req.Model,
		Tools:     toolNames,
		ProcessID: procID,
	})
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if name == "mother" {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "Mother cannot be updated"})
		return
	}

	var req UpdateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	// Look up existing composed agent.
	composed, err := s.store.ListComposedAgents()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to list agents"})
		return
	}
	var existing *ComposedAgent
	for i := range composed {
		if composed[i].Name == name {
			existing = &composed[i]
			break
		}
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("composed agent %q not found", name)})
		return
	}

	// Merge updates.
	newName := name
	if req.Name != nil && *req.Name != "" {
		newName = *req.Name
	}
	if req.Model != nil {
		existing.Model = *req.Model
	}
	if req.System != nil {
		existing.System = *req.System
	}
	if req.Team != nil {
		existing.Team = req.Team
	}
	if req.Temperature != nil {
		existing.Temperature = req.Temperature
	}

	// Remove old agent from interpreter.
	if err := s.interp.RemoveAgent(name); err != nil {
		slog.Warn("failed to remove agent for update", "agent", name, "error", err)
	}

	// If renamed, delete old persistence entry.
	if newName != name {
		s.store.DeleteComposedAgent(name)
		existing.Name = newName
	}

	// Build DSL agent definition.
	system := existing.System
	var toolNames []string
	for _, skillName := range existing.Skills {
		names, err := s.registerSkillTools(skillName)
		if err != nil {
			slog.Warn("failed to register skill tools", "skill", skillName, "error", err)
			continue
		}
		toolNames = append(toolNames, names...)
	}

	if len(existing.Team) > 0 {
		dsl.RegisterDelegateTool(s.interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
			return s.interp.SendToAgent(ctx, agent, message)
		}, existing.Team)
		toolNames = append(toolNames, "delegate")
		system = dsl.BuildTeamPrompt(system, existing.Team, nil, false)
	}

	agentDef := &dsl.Agent{
		Name:        newName,
		Model:       existing.Model,
		System:      system,
		Tools:       toolNames,
		Temperature: existing.Temperature,
	}

	if err := s.interp.AddAgent(newName, agentDef); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to re-create agent: " + err.Error()})
		return
	}

	// Persist updated agent.
	existing.CreatedAt = time.Now()
	if err := s.store.InsertComposedAgent(*existing); err != nil {
		slog.Error("failed to persist updated agent", "agent", newName, "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "name": newName})
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if name == "mother" {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "Mother cannot be deleted"})
		return
	}

	if err := s.interp.RemoveAgent(name); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}

	// Remove from persistence.
	s.store.DeleteComposedAgent(name)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

// --- Skill Tool Parsing ---

// skillManifest is used for parsing skill YAML files that include tool definitions.
type skillManifest struct {
	Tools []skillToolDef `yaml:"tools"`
}

type skillToolDef struct {
	Name        string                       `yaml:"name"`
	Description string                       `yaml:"description"`
	Params      map[string]skillToolParamDef `yaml:"params"`
	Run         string                       `yaml:"run"`
}

type skillToolParamDef struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     any    `yaml:"default"`
}

// registerSkillTools reads an installed skill's YAML and registers its tools.
// Returns the list of registered tool names.
func (s *Server) registerSkillTools(skillName string) ([]string, error) {
	items, err := s.popClient.List(population.KindSkill)
	if err != nil {
		return nil, err
	}

	var skillPath string
	for _, item := range items {
		if item.Name == skillName {
			skillPath = item.Path
			break
		}
	}
	if skillPath == "" {
		return nil, fmt.Errorf("skill '%s' not installed", skillName)
	}

	manifestPath := filepath.Join(skillPath, "vega.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading skill manifest: %w", err)
	}

	var manifest skillManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing skill manifest: %w", err)
	}

	toolReg := s.interp.Tools()
	var names []string
	for _, t := range manifest.Tools {
		if t.Run == "" {
			continue
		}

		params := make([]tools.DynamicParamDef, 0, len(t.Params))
		for pname, pdef := range t.Params {
			params = append(params, tools.DynamicParamDef{
				Name:        pname,
				Type:        pdef.Type,
				Description: pdef.Description,
				Required:    pdef.Required,
				Default:     pdef.Default,
			})
		}

		def := tools.DynamicToolDef{
			Name:        t.Name,
			Description: t.Description,
			Params:      params,
			Implementation: tools.DynamicToolImpl{
				Type:    "exec",
				Command: t.Run,
			},
		}

		if err := toolReg.RegisterDynamicTool(def); err != nil {
			slog.Warn("failed to register tool", "tool", t.Name, "error", err)
			continue
		}
		names = append(names, t.Name)
	}

	return names, nil
}

// restoreComposedAgents loads composed agents from the database and re-creates them.
func (s *Server) restoreComposedAgents() {
	agents, err := s.store.ListComposedAgents()
	if err != nil {
		slog.Error("failed to load composed agents", "error", err)
		return
	}

	// metaTool returns true for tools that belong exclusively to Mother or Hermes
	// and must never be handed to arbitrary composed agents.
	metaTool := func(name string) bool {
		return dsl.IsMotherTool(name) || dsl.IsHermesTool(name)
	}

	ctx := context.Background()
	for _, a := range agents {
		// Start with any explicitly persisted tool restrictions.
		toolNames := append([]string(nil), a.Tools...)

		// Re-register and append skill tools.
		for _, skillName := range a.Skills {
			names, err := s.registerSkillTools(skillName)
			if err != nil {
				slog.Warn("failed to restore skill tools", "skill", skillName, "error", err)
				continue
			}
			toolNames = append(toolNames, names...)
		}

		// If persona system prompt is empty, try to reload from installed persona.
		system := a.System
		if a.Persona != "" && system == "" {
			info, err := s.popClient.Info(ctx, "@"+a.Persona)
			if err == nil && info.InstalledPath != "" {
				manifest, merr := population.LoadManifest(filepath.Join(info.InstalledPath, "vega.yaml"))
				if merr == nil {
					system = manifest.SystemPrompt
				}
			}
		}

		// If the agent has a team, register the delegate tool and enrich the prompt.
		if len(a.Team) > 0 {
			dsl.RegisterDelegateTool(s.interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
				return s.interp.SendToAgent(ctx, agent, message)
			}, a.Team)
			hasDel := false
			for _, t := range toolNames {
				if t == "delegate" {
					hasDel = true
					break
				}
			}
			if !hasDel {
				toolNames = append(toolNames, "delegate")
			}
			system = dsl.BuildTeamPrompt(system, a.Team, nil, false)
		}

		// If no explicit tool list, the agent would get every registered tool.
		// Exclude meta-tools (Mother/Hermes) which must never leak to arbitrary agents.
		if len(toolNames) == 0 {
			for _, ts := range s.interp.Tools().Schema() {
				if !metaTool(ts.Name) {
					toolNames = append(toolNames, ts.Name)
				}
			}
		}

		agentDef := &dsl.Agent{
			Name:        a.Name,
			Model:       a.Model,
			System:      system,
			Tools:       toolNames,
			Temperature: a.Temperature,
		}

		if err := s.interp.AddAgent(a.Name, agentDef); err != nil {
			slog.Warn("failed to restore composed agent", "name", a.Name, "error", err)
		} else {
			slog.Info("restored composed agent", "name", a.Name)
		}
	}
}
