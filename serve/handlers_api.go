package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
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

// hydrateAgent loads persisted chat history into a process that has no
// conversation history (e.g. freshly spawned after restart). This gives
// agents continuity across server restarts.
func (s *Server) hydrateAgent(proc *vega.Process, agentName string) {
	if len(proc.Messages()) > 0 {
		return // already has history
	}

	history, err := s.store.ListChatMessages(agentName)
	if err != nil || len(history) == 0 {
		return
	}

	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		role := llm.RoleUser
		if m.Role == "assistant" {
			role = llm.RoleAssistant
		}
		msgs = append(msgs, llm.Message{Role: role, Content: m.Content})
	}

	proc.HydrateMessages(msgs)
	slog.Debug("hydrated agent from chat history", "agent", agentName, "messages", len(msgs))
}

// chatAgentName returns a per-user agent name if X-Auth-User is set.
// e.g. agent "dan" + user "etienne" → "dan:etienne".
// It also ensures the per-user agent exists by cloning the base definition.
func (s *Server) chatAgentName(baseAgent string, r *http.Request) string {
	user := r.Header.Get("X-Auth-User")
	if user == "" {
		return baseAgent
	}

	name := baseAgent + ":" + user

	// Ensure per-user agent exists (clone from base on first use).
	if agents := s.interp.Agents(); agents[name] == nil {
		doc := s.interp.Document()
		if baseDef, ok := doc.Agents[baseAgent]; ok {
			clone := *baseDef
			s.interp.AddAgent(name, &clone)
		}
	}

	return name
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	baseAgent := r.PathValue("name")
	name := s.chatAgentName(baseAgent, r)
	userID := r.Header.Get("X-Auth-User")
	if userID == "" {
		userID = "default"
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "message is required"})
		return
	}

	// Ensure the agent process is spawned so we can inject memory.
	proc, err := s.interp.EnsureAgent(name)
	if err != nil {
		status, msg := classifyHTTPError(err)
		writeJSON(w, status, ErrorResponse{Error: msg})
		return
	}

	// Hydrate conversation history from SQLite if this is a fresh process.
	s.hydrateAgent(proc, name)

	// Load and inject memory into the process before sending.
	if memories, err := s.store.GetUserMemory(userID, baseAgent); err == nil && len(memories) > 0 {
		if memText := formatMemoryForInjection(memories); memText != "" {
			proc.SetExtraSystem(memText)
		}
	}

	// Persist user message.
	if err := s.store.InsertChatMessage(name, "user", req.Message); err != nil {
		slog.Error("failed to persist user chat message", "agent", name, "error", err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	ctx = ContextWithMemory(ctx, s.store, userID, baseAgent)

	response, err := s.interp.SendToAgent(ctx, name, req.Message)
	if err != nil {
		status, msg := classifyHTTPError(err)
		writeJSON(w, status, ErrorResponse{Error: msg})
		return
	}

	// Persist assistant response.
	if err := s.store.InsertChatMessage(name, "assistant", response); err != nil {
		slog.Error("failed to persist assistant chat message", "agent", name, "error", err)
	}

	// Fire async memory extraction.
	go s.extractMemory(userID, baseAgent, req.Message, response)

	writeJSON(w, http.StatusOK, map[string]string{"response": response})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	baseAgent := r.PathValue("name")
	name := s.chatAgentName(baseAgent, r)
	userID := r.Header.Get("X-Auth-User")
	if userID == "" {
		userID = "default"
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "message is required"})
		return
	}

	proc, err := s.interp.EnsureAgent(name)
	if err != nil {
		status, msg := classifyHTTPError(err)
		writeJSON(w, status, ErrorResponse{Error: msg})
		return
	}

	s.hydrateAgent(proc, name)

	if memories, err := s.store.GetUserMemory(userID, baseAgent); err == nil && len(memories) > 0 {
		if memText := formatMemoryForInjection(memories); memText != "" {
			proc.SetExtraSystem(memText)
		}
	}

	if err := s.store.InsertChatMessage(name, "user", req.Message); err != nil {
		slog.Error("failed to persist user chat message", "agent", name, "error", err)
	}

	// Use a detached context so the LLM stream survives client disconnect.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	ctx = ContextWithMemory(ctx, s.store, userID, baseAgent)

	stream, err := s.interp.StreamToAgent(ctx, name, req.Message)
	if err != nil {
		cancel()
		status, msg := classifyHTTPError(err)
		writeJSON(w, status, ErrorResponse{Error: msg})
		return
	}

	// Create a server-side active stream to decouple from the HTTP connection.
	streamID := uuid.New().String()[:8]
	as := &activeStream{
		events: make(chan vega.ChatEvent, 256),
		done:   make(chan struct{}),
	}

	s.streamsMu.Lock()
	s.streams[streamID] = as
	s.streamsMu.Unlock()

	// Detached goroutine: relay events from the LLM ChatStream into the
	// activeStream, persist the result, then clean up.
	go func() {
		defer cancel()

		for event := range stream.Events() {
			as.events <- event
		}

		response := stream.Response()
		streamErr := stream.Err()

		as.mu.Lock()
		as.response = response
		as.err = streamErr
		as.mu.Unlock()
		close(as.done)
		close(as.events)

		// Persist assistant response even if no client is listening.
		if streamErr != nil {
			slog.Error("stream completed with error, assistant response not saved",
				"agent", name, "error", streamErr, "response_len", len(response))
		} else if response == "" {
			slog.Warn("stream completed with empty response, nothing to save", "agent", name)
		} else {
			if err := s.store.InsertChatMessage(name, "assistant", response); err != nil {
				slog.Error("failed to persist assistant chat message", "agent", name, "error", err)
			}
			go s.extractMemory(userID, baseAgent, req.Message, response)
		}

		// Keep the stream in the map briefly so a reconnecting client
		// could theoretically find it, then remove it.
		time.Sleep(30 * time.Second)
		s.streamsMu.Lock()
		delete(s.streams, streamID)
		s.streamsMu.Unlock()
	}()

	// --- SSE relay: forward events to the connected client ---

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "streaming not supported"})
		return
	}

	for {
		select {
		case event, ok := <-as.events:
			if !ok {
				// Stream finished — send final events.
				as.mu.Lock()
				streamErr := as.err
				as.mu.Unlock()

				if streamErr != nil {
					_, friendlyMsg := classifyHTTPError(streamErr)
					errData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventError, Error: friendlyMsg})
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
					flusher.Flush()
				}

				doneData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventDone})
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
				flusher.Flush()
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()

		case <-r.Context().Done():
			// Client disconnected — handler returns but the goroutine
			// keeps running and will persist the response.
			return
		}
	}
}

func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	baseAgent := r.PathValue("name")
	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = r.Header.Get("X-Auth-User")
	}
	if userID == "" {
		userID = "default"
	}

	memories, err := s.store.GetUserMemory(userID, baseAgent)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if memories == nil {
		memories = []UserMemory{}
	}

	writeJSON(w, http.StatusOK, MemoryResponse{
		UserID: userID,
		Agent:  baseAgent,
		Layers: memories,
	})
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	baseAgent := r.PathValue("name")
	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = r.Header.Get("X-Auth-User")
	}
	if userID == "" {
		userID = "default"
	}

	if err := s.store.DeleteUserMemory(userID, baseAgent); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	name := s.chatAgentName(r.PathValue("name"), r)
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

func (s *Server) handleClearChat(w http.ResponseWriter, r *http.Request) {
	name := s.chatAgentName(r.PathValue("name"), r)

	// Clear DB messages.
	if err := s.store.DeleteChatMessages(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Reset in-memory agent process so it starts fresh.
	if err := s.interp.ResetAgent(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
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

// --- Settings Handlers ---

func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.ListSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if settings == nil {
		settings = []Setting{}
	}

	// Mask sensitive values in response.
	for i := range settings {
		if settings[i].Sensitive {
			settings[i].Value = "********"
		}
	}

	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpsertSetting(w http.ResponseWriter, r *http.Request) {
	var req Setting
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "key is required"})
		return
	}

	if err := s.store.UpsertSetting(req); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Refresh settings on the tools collection.
	s.refreshToolSettings()

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "key is required"})
		return
	}

	if err := s.store.DeleteSetting(key); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "setting not found"})
		return
	}

	// Refresh settings on the tools collection.
	s.refreshToolSettings()

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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

// classifyHTTPError maps an error to an HTTP status code and user-friendly message
// using vega.ClassifyError.
func classifyHTTPError(err error) (int, string) {
	class := vega.ClassifyError(err)
	switch class {
	case vega.ErrClassAuthentication:
		return http.StatusUnauthorized,
			"API key is missing or invalid. Check your ~/.vega/env file or run 'vega init'."
	case vega.ErrClassRateLimit:
		return http.StatusTooManyRequests,
			"Rate limited by the AI provider. Please wait a moment and try again."
	case vega.ErrClassOverloaded:
		return http.StatusServiceUnavailable,
			"The AI provider is currently overloaded. Please try again shortly."
	case vega.ErrClassBudgetExceeded:
		return http.StatusPaymentRequired,
			"Budget exceeded for this agent."
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
