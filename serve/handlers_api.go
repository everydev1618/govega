package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/google/uuid"
)

// --- Process Handlers ---

func (s *Server) handleListProcesses(w http.ResponseWriter, r *http.Request) {
	procs := s.interp.Orchestrator().List()

	resp := make([]ProcessResponse, 0, len(procs))
	for _, p := range procs {
		resp = append(resp, processToResponse(p))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetProcess(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := s.interp.Orchestrator().Get(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "process not found"})
		return
	}

	msgs := p.Messages()
	msgResp := make([]MessageResponse, 0, len(msgs))
	for _, m := range msgs {
		msgResp = append(msgResp, MessageResponse{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	detail := ProcessDetailResponse{
		ProcessResponse: processToResponse(p),
		Messages:        msgResp,
	}

	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleKillProcess(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.interp.Orchestrator().Kill(id); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}

// --- Agent Handlers ---

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	doc := s.interp.Document()
	agents := s.interp.Agents()

	// Build maps of composed agent metadata for source tagging and team info.
	composedMap := make(map[string]ComposedAgent)
	if composed, err := s.store.ListComposedAgents(); err == nil {
		for _, a := range composed {
			composedMap[a.Name] = a
		}
	}

	resp := make([]AgentResponse, 0, len(doc.Agents))
	for name, def := range doc.Agents {
		ar := AgentResponse{
			Name:   name,
			Model:  def.Model,
			System: truncate(def.System, 500),
			Tools:  def.Tools,
		}
		if proc, ok := agents[name]; ok {
			ar.ProcessID = proc.ID
			ar.ProcessStatus = string(proc.Status())
		}
		if ca, ok := composedMap[name]; ok {
			ar.Source = "composed"
			ar.Team = ca.Team
		}
		resp = append(resp, ar)
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Chat Handlers ---

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "message is required"})
		return
	}

	// Persist user message.
	s.store.InsertChatMessage(name, "user", req.Message)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	response, err := s.interp.SendToAgent(ctx, name, req.Message)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Persist assistant response.
	s.store.InsertChatMessage(name, "assistant", response)

	writeJSON(w, http.StatusOK, map[string]string{"response": response})
}

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	msgs, err := s.store.ListChatMessages(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if msgs == nil {
		msgs = []ChatMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// --- Workflow Handlers ---

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	doc := s.interp.Document()

	resp := make([]WorkflowResponse, 0, len(doc.Workflows))
	for name, wf := range doc.Workflows {
		wr := WorkflowResponse{
			Name:        name,
			Description: wf.Description,
			Steps:       len(wf.Steps),
		}
		if len(wf.Inputs) > 0 {
			wr.Inputs = make(map[string]InputResponse, len(wf.Inputs))
			for iname, input := range wf.Inputs {
				wr.Inputs[iname] = InputResponse{
					Type:        input.Type,
					Description: input.Description,
					Required:    input.Required,
					Default:     input.Default,
					Enum:        input.Enum,
				}
			}
		}
		resp = append(resp, wr)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	doc := s.interp.Document()
	if _, ok := doc.Workflows[name]; !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("workflow '%s' not found", name)})
		return
	}

	var req WorkflowRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	runID := uuid.New().String()[:8]

	// Persist the run.
	inputsJSON, _ := json.Marshal(req.Inputs)
	s.store.InsertWorkflowRun(WorkflowRun{
		RunID:     runID,
		Workflow:  name,
		Inputs:    string(inputsJSON),
		Status:    "running",
		StartedAt: time.Now(),
	})

	// Execute async.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		result, err := s.interp.Execute(ctx, name, req.Inputs)

		status := "completed"
		resultStr := fmt.Sprintf("%v", result)
		if err != nil {
			status = "failed"
			resultStr = err.Error()
		}

		s.store.UpdateWorkflowRun(runID, status, resultStr)

		s.broker.Publish(BrokerEvent{
			Type:      "workflow." + status,
			Timestamp: time.Now(),
			Data: map[string]string{
				"run_id":   runID,
				"workflow": name,
				"status":   status,
			},
		})
	}()

	writeJSON(w, http.StatusAccepted, WorkflowRunResponse{
		RunID:  runID,
		Status: "running",
	})
}

// --- MCP Handlers ---

func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	tools := s.interp.Tools()
	statuses := tools.MCPServerStatuses()

	resp := make([]MCPServerResponse, 0, len(statuses))
	for _, st := range statuses {
		resp = append(resp, MCPServerResponse{
			Name:      st.Name,
			Connected: st.Connected,
			Transport: st.Transport,
			URL:       st.URL,
			Command:   st.Command,
			Tools:     st.Tools,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Stats Handler ---

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	procs := s.interp.Orchestrator().List()

	stats := StatsResponse{
		TotalProcesses: len(procs),
		Uptime:         time.Since(s.startedAt).Truncate(time.Second).String(),
	}

	for _, p := range procs {
		switch p.Status() {
		case vega.StatusRunning, vega.StatusPending:
			stats.RunningProcesses++
		case vega.StatusCompleted:
			stats.CompletedProcesses++
		case vega.StatusFailed, vega.StatusTimeout:
			stats.FailedProcesses++
		}

		m := p.Metrics()
		stats.TotalInputTokens += m.InputTokens
		stats.TotalOutputTokens += m.OutputTokens
		stats.TotalCostUSD += m.CostUSD
		stats.TotalToolCalls += m.ToolCalls
		stats.TotalErrors += m.Errors
	}

	writeJSON(w, http.StatusOK, stats)
}

// --- Spawn Tree Handler ---

func (s *Server) handleSpawnTree(w http.ResponseWriter, r *http.Request) {
	tree := s.interp.Orchestrator().GetSpawnTree()

	resp := make([]SpawnTreeNodeResponse, 0, len(tree))
	for _, node := range tree {
		resp = append(resp, treeNodeToResponse(node))
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Helpers ---

func processToResponse(p *vega.Process) ProcessResponse {
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}
	m := p.Metrics()

	resp := ProcessResponse{
		ID:          p.ID,
		Agent:       agentName,
		Task:        p.Task,
		Status:      string(p.Status()),
		StartedAt:   p.StartedAt,
		ParentID:    p.ParentID,
		SpawnDepth:  p.SpawnDepth,
		SpawnReason: p.SpawnReason,
		Metrics: MetricsResponse{
			Iterations:   m.Iterations,
			InputTokens:  m.InputTokens,
			OutputTokens: m.OutputTokens,
			CostUSD:      m.CostUSD,
			ToolCalls:    m.ToolCalls,
			Errors:       m.Errors,
			LastActiveAt: m.LastActiveAt,
		},
	}
	if !m.CompletedAt.IsZero() {
		resp.CompletedAt = &m.CompletedAt
	}
	return resp
}

func treeNodeToResponse(node *vega.SpawnTreeNode) SpawnTreeNodeResponse {
	children := make([]SpawnTreeNodeResponse, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, treeNodeToResponse(child))
	}
	return SpawnTreeNodeResponse{
		ProcessID:   node.ProcessID,
		AgentName:   node.AgentName,
		Task:        node.Task,
		Status:      string(node.Status),
		SpawnDepth:  node.SpawnDepth,
		SpawnReason: node.SpawnReason,
		StartedAt:   node.StartedAt,
		Children:    children,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
