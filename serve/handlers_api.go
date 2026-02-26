package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/mcp"
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

	// Resolve the effective default model for agents with no explicit model.
	defaultModel := ""
	if doc.Settings != nil {
		defaultModel = doc.Settings.DefaultModel
	}

	resp := make([]AgentResponse, 0, len(doc.Agents))
	for name, def := range doc.Agents {
		model := def.Model
		if model == "" {
			model = defaultModel
		}
		ar := AgentResponse{
			Name:   name,
			Model:  model,
			System: def.System,
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

	// Snapshot baseline metrics before the stream so we can compute per-response delta.
	baseMetrics := proc.Metrics()
	streamStart := time.Now()

	stream, err := s.interp.StreamToAgent(ctx, name, req.Message)
	if err != nil {
		cancel()
		status, msg := classifyHTTPError(err)
		writeJSON(w, status, ErrorResponse{Error: msg})
		return
	}

	// Create a server-side active stream keyed by agent name.
	as := &activeStream{
		agentName: name,
		done:      make(chan struct{}),
	}

	s.streamsMu.Lock()
	s.streams[name] = as
	s.streamsMu.Unlock()

	// Detached goroutine: relay events from the LLM ChatStream into the
	// activeStream via publish (which buffers and broadcasts to subscribers),
	// persist the result, then clean up.
	go func() {
		defer cancel()

		for event := range stream.Events() {
			as.publish(event)
		}

		response := stream.Response()
		streamErr := stream.Err()

		// Compute per-response metrics delta.
		finalMetrics := proc.Metrics()
		delta := &vega.ChatEventMetrics{
			InputTokens:  finalMetrics.InputTokens - baseMetrics.InputTokens,
			OutputTokens: finalMetrics.OutputTokens - baseMetrics.OutputTokens,
			CostUSD:      finalMetrics.CostUSD - baseMetrics.CostUSD,
			DurationMs:   time.Since(streamStart).Milliseconds(),
		}

		as.mu.Lock()
		as.response = response
		as.err = streamErr
		as.metrics = delta
		as.mu.Unlock()
		close(as.done)
		as.finish() // close all subscriber channels

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

		// Keep the stream in the map briefly so late reconnects can see
		// the final state, then remove it.
		time.Sleep(30 * time.Second)
		s.streamsMu.Lock()
		delete(s.streams, name)
		s.streamsMu.Unlock()
	}()

	// --- SSE relay: subscribe and forward events to the connected client ---
	s.relayStreamSSE(w, r, as)
}

// handleChatStatus returns whether an agent has an active (in-progress) stream.
func (s *Server) handleChatStatus(w http.ResponseWriter, r *http.Request) {
	name := s.chatAgentName(r.PathValue("name"), r)

	s.streamsMu.Lock()
	as := s.streams[name]
	s.streamsMu.Unlock()

	streaming := false
	if as != nil {
		select {
		case <-as.done:
			// Stream already finished but hasn't been cleaned up yet.
		default:
			streaming = true
		}
	}

	writeJSON(w, http.StatusOK, ChatStatusResponse{Streaming: streaming})
}

// handleChatStreamReconnect allows a client to reconnect to an in-progress
// chat stream. It replays all buffered events, then continues with live
// events via SSE. If the stream is already done, it replays everything and
// sends a done event.
func (s *Server) handleChatStreamReconnect(w http.ResponseWriter, r *http.Request) {
	name := s.chatAgentName(r.PathValue("name"), r)

	s.streamsMu.Lock()
	as := s.streams[name]
	s.streamsMu.Unlock()

	if as == nil {
		writeJSON(w, http.StatusOK, ChatStatusResponse{Streaming: false})
		return
	}

	s.relayStreamSSE(w, r, as)
}

// relayStreamSSE subscribes to an active stream and relays events as SSE.
// It replays buffered history first, then continues with live events.
func (s *Server) relayStreamSSE(w http.ResponseWriter, r *http.Request, as *activeStream) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "streaming not supported"})
		return
	}

	// Subscribe — get all past events plus a channel for future ones.
	history, ch := as.subscribe()
	defer as.unsubscribe(ch)

	// Replay buffered history.
	for _, event := range history {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
	}
	flusher.Flush()

	// If stream already finished, send final events and return.
	select {
	case <-as.done:
		as.mu.Lock()
		streamErr := as.err
		doneMetrics := as.metrics
		as.mu.Unlock()

		if streamErr != nil {
			_, friendlyMsg := classifyHTTPError(streamErr)
			errData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventError, Error: friendlyMsg})
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
		}
		doneData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventDone, Metrics: doneMetrics})
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
		flusher.Flush()
		return
	default:
	}

	// Stream live events.
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				// Stream finished — send final events.
				as.mu.Lock()
				streamErr := as.err
				doneMetrics := as.metrics
				as.mu.Unlock()

				if streamErr != nil {
					_, friendlyMsg := classifyHTTPError(streamErr)
					errData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventError, Error: friendlyMsg})
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
					flusher.Flush()
				}

				doneData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventDone, Metrics: doneMetrics})
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
			// Client disconnected — stream keeps running.
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
	t := s.interp.Tools()
	statuses := t.MCPServerStatuses()

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

	// Include connected built-in Go servers that aren't already listed.
	listed := make(map[string]bool, len(resp))
	for _, r := range resp {
		listed[r.Name] = true
	}
	for _, entry := range mcp.DefaultRegistry {
		if entry.BuiltinGo && !listed[entry.Name] && t.BuiltinServerConnected(entry.Name) {
			var toolNames []string
			for _, schema := range t.Schema() {
				if strings.HasPrefix(schema.Name, entry.Name+"__") {
					toolNames = append(toolNames, schema.Name)
				}
			}
			resp = append(resp, MCPServerResponse{
				Name:      entry.Name,
				Connected: true,
				Transport: "builtin",
				Tools:     toolNames,
			})
			listed[entry.Name] = true
		}
	}

	// Include disabled servers from persistence (not connected, but should be visible).
	if sqlStore, ok := s.store.(*SQLiteStore); ok {
		if servers, err := sqlStore.ListMCPServers(); err == nil {
			for _, sc := range servers {
				if sc.Disabled && !listed[sc.Name] {
					resp = append(resp, MCPServerResponse{
						Name:     sc.Name,
						Disabled: true,
					})
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- MCP Connection Handlers ---

func (s *Server) handleMCPRegistry(w http.ResponseWriter, r *http.Request) {
	tools := s.interp.Tools()

	// Load existing settings for pre-filling env fields.
	settingsMap := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			settingsMap[st.Key] = st.Key // just indicate existence, don't leak values
		}
	}

	resp := make([]MCPRegistryEntryResponse, 0)
	for _, entry := range mcp.DefaultRegistry {
		existing := make(map[string]string)
		allEnv := append(entry.RequiredEnv, entry.OptionalEnv...)
		for _, key := range allEnv {
			if _, ok := settingsMap[key]; ok {
				existing[key] = "configured"
			} else if os.Getenv(key) != "" {
				existing[key] = "configured"
			}
		}

		resp = append(resp, MCPRegistryEntryResponse{
			Name:             entry.Name,
			Description:      entry.Description,
			RequiredEnv:      entry.RequiredEnv,
			OptionalEnv:      entry.OptionalEnv,
			BuiltinGo:        entry.BuiltinGo,
			Connected:        tools.MCPServerConnected(entry.Name) || tools.BuiltinServerConnected(entry.Name),
			ExistingSettings: existing,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConnectMCPServer(w http.ResponseWriter, r *http.Request) {
	var req ConnectMCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "name is required"})
		return
	}

	tools := s.interp.Tools()

	// Check if already connected (MCP subprocess or built-in Go server).
	if tools.MCPServerConnected(req.Name) || tools.BuiltinServerConnected(req.Name) {
		writeJSON(w, http.StatusConflict, ErrorResponse{Error: fmt.Sprintf("server %q is already connected", req.Name)})
		return
	}

	var cfg mcp.ServerConfig

	// Check registry first.
	if entry, ok := mcp.Lookup(req.Name); ok {
		// Save env values as sensitive settings.
		for key, val := range req.Env {
			if val != "" {
				if err := s.store.UpsertSetting(Setting{Key: key, Value: val, Sensitive: true}); err != nil {
					slog.Error("failed to save MCP env setting", "key", key, "error", err)
				}
			}
		}
		s.refreshToolSettings()

		// Build env map: merge stored settings + request env.
		envMap := make(map[string]string)
		if settings, err := s.store.ListSettings(); err == nil {
			for _, st := range settings {
				envMap[st.Key] = st.Value
			}
		}
		for k, v := range req.Env {
			if v != "" {
				envMap[k] = v
			}
		}

		// If this registry entry has a native Go implementation, use it
		// instead of spawning an external process.
		if entry.BuiltinGo && tools.HasBuiltinServer(req.Name) {
			for k, v := range envMap {
				os.Setenv(k, v)
			}
			numTools, err := tools.ConnectBuiltinServer(r.Context(), req.Name)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
					Name:      req.Name,
					Connected: false,
					Error:     err.Error(),
				})
				return
			}

			// Collect tool names from the builtin server schema.
			var toolNames []string
			for _, schema := range tools.Schema() {
				if strings.HasPrefix(schema.Name, req.Name+"__") {
					toolNames = append(toolNames, schema.Name)
				}
			}

			_ = numTools
			s.persistMCPServer(req)
			writeJSON(w, http.StatusOK, ConnectMCPResponse{
				Name:      req.Name,
				Connected: true,
				Tools:     toolNames,
			})
			return
		}

		cfg = entry.ToServerConfig(envMap)
	} else {
		// Custom server.
		cfg = mcp.ServerConfig{
			Name:    req.Name,
			Command: req.Command,
			Args:    req.Args,
			URL:     req.URL,
			Headers: req.Headers,
			Env:     req.Env,
		}
		switch req.Transport {
		case "http":
			cfg.Transport = mcp.TransportHTTP
		case "sse":
			cfg.Transport = mcp.TransportSSE
		default:
			cfg.Transport = mcp.TransportStdio
		}
		if req.Timeout > 0 {
			cfg.Timeout = time.Duration(req.Timeout) * time.Second
		}

		// Save any env values as sensitive settings.
		for key, val := range req.Env {
			if val != "" {
				if err := s.store.UpsertSetting(Setting{Key: key, Value: val, Sensitive: true}); err != nil {
					slog.Error("failed to save MCP env setting", "key", key, "error", err)
				}
			}
		}
		s.refreshToolSettings()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	numTools, err := tools.ConnectMCPServer(ctx, cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
			Name:      req.Name,
			Connected: false,
			Error:     err.Error(),
		})
		return
	}

	// Get tool names for response.
	statuses := tools.MCPServerStatuses()
	var toolNames []string
	for _, st := range statuses {
		if st.Name == req.Name {
			toolNames = st.Tools
			break
		}
	}

	_ = numTools
	s.persistMCPServer(req)
	writeJSON(w, http.StatusOK, ConnectMCPResponse{
		Name:      req.Name,
		Connected: true,
		Tools:     toolNames,
	})
}

func (s *Server) handleDisconnectMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "server name is required"})
		return
	}

	t := s.interp.Tools()

	// Try builtin server first, then MCP subprocess.
	if t.BuiltinServerConnected(name) {
		if err := t.DisconnectBuiltinServer(name); err != nil {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
			return
		}
	} else if err := t.DisconnectMCPServer(name); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}

	// Remove from persistence so it won't auto-reconnect on restart.
	if sqlStore, ok := s.store.(*SQLiteStore); ok {
		sqlStore.DeleteMCPServer(name)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

func (s *Server) handleRefreshMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "server name is required"})
		return
	}

	t := s.interp.Tools()

	// Load persisted config so we can reconnect after disconnect.
	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "persistence not available"})
		return
	}

	servers, err := sqlStore.ListMCPServers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to load server configs"})
		return
	}

	var req ConnectMCPRequest
	var found bool
	for _, sc := range servers {
		if sc.Name == name {
			if err := json.Unmarshal([]byte(sc.ConfigJSON), &req); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to parse server config"})
				return
			}
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("server %q not found in persisted configs", name)})
		return
	}

	// Load settings for env resolution.
	envMap := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			envMap[st.Key] = st.Value
		}
	}
	for k := range req.Env {
		if val, ok := envMap[k]; ok {
			req.Env[k] = val
		}
	}

	// Disconnect the existing server.
	if t.BuiltinServerConnected(name) {
		if err := t.DisconnectBuiltinServer(name); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("disconnect failed: %s", err)})
			return
		}
	} else if t.MCPServerConnected(name) {
		if err := t.DisconnectMCPServer(name); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("disconnect failed: %s", err)})
			return
		}
	}

	// Reconnect.
	var toolNames []string
	if entry, ok := mcp.Lookup(req.Name); ok {
		if entry.BuiltinGo && t.HasBuiltinServer(req.Name) {
			for k, v := range envMap {
				os.Setenv(k, v)
			}
			if _, err := t.ConnectBuiltinServer(r.Context(), req.Name); err != nil {
				writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
					Name: req.Name, Connected: false, Error: err.Error(),
				})
				return
			}
			for _, schema := range t.Schema() {
				if strings.HasPrefix(schema.Name, req.Name+"__") {
					toolNames = append(toolNames, schema.Name)
				}
			}
		} else {
			cfg := entry.ToServerConfig(envMap)
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			if _, err := t.ConnectMCPServer(ctx, cfg); err != nil {
				writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
					Name: req.Name, Connected: false, Error: err.Error(),
				})
				return
			}
			statuses := t.MCPServerStatuses()
			for _, st := range statuses {
				if st.Name == req.Name {
					toolNames = st.Tools
					break
				}
			}
		}
	} else {
		// Custom server.
		cfg := mcp.ServerConfig{
			Name:    req.Name,
			Command: req.Command,
			Args:    req.Args,
			URL:     req.URL,
			Headers: req.Headers,
			Env:     req.Env,
		}
		switch req.Transport {
		case "http":
			cfg.Transport = mcp.TransportHTTP
		case "sse":
			cfg.Transport = mcp.TransportSSE
		default:
			cfg.Transport = mcp.TransportStdio
		}
		if req.Timeout > 0 {
			cfg.Timeout = time.Duration(req.Timeout) * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if _, err := t.ConnectMCPServer(ctx, cfg); err != nil {
			writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
				Name: req.Name, Connected: false, Error: err.Error(),
			})
			return
		}
		statuses := t.MCPServerStatuses()
		for _, st := range statuses {
			if st.Name == req.Name {
				toolNames = st.Tools
				break
			}
		}
	}

	slog.Info("refreshed MCP server", "server", name, "tools", len(toolNames))
	writeJSON(w, http.StatusOK, ConnectMCPResponse{
		Name:      name,
		Connected: true,
		Tools:     toolNames,
	})
}

func (s *Server) handleGetMCPServerConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "server name is required"})
		return
	}

	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "persistence not available"})
		return
	}

	servers, err := sqlStore.ListMCPServers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to load server configs"})
		return
	}

	var req ConnectMCPRequest
	var found bool
	for _, sc := range servers {
		if sc.Name == name {
			if err := json.Unmarshal([]byte(sc.ConfigJSON), &req); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to parse server config"})
				return
			}
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("server %q not found", name)})
		return
	}

	// Collect env keys.
	var envKeys []string
	for k := range req.Env {
		envKeys = append(envKeys, k)
	}

	// Also include registry env keys if this is a registry server.
	_, isRegistry := mcp.Lookup(name)
	if isRegistry {
		if entry, ok := mcp.Lookup(name); ok {
			seen := make(map[string]bool)
			for _, k := range envKeys {
				seen[k] = true
			}
			for _, k := range entry.RequiredEnv {
				if !seen[k] {
					envKeys = append(envKeys, k)
					seen[k] = true
				}
			}
			for _, k := range entry.OptionalEnv {
				if !seen[k] {
					envKeys = append(envKeys, k)
				}
			}
		}
	}

	// Check which env keys have saved settings and return masked values.
	existing := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		settingsMap := make(map[string]Setting)
		for _, st := range settings {
			settingsMap[st.Key] = st
		}
		for _, key := range envKeys {
			if st, ok := settingsMap[key]; ok {
				existing[key] = st.Value
			} else if val := os.Getenv(key); val != "" {
				existing[key] = val
			}
		}
	}

	writeJSON(w, http.StatusOK, MCPServerConfigResponse{
		Name:             name,
		Transport:        req.Transport,
		Command:          req.Command,
		Args:             req.Args,
		URL:              req.URL,
		Headers:          req.Headers,
		Timeout:          req.Timeout,
		EnvKeys:          envKeys,
		ExistingSettings: existing,
		IsRegistry:       isRegistry,
	})
}

func (s *Server) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "server name is required"})
		return
	}

	var req ConnectMCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	req.Name = name // ensure name matches path

	tools := s.interp.Tools()

	// Disconnect existing server.
	if tools.BuiltinServerConnected(name) {
		if err := tools.DisconnectBuiltinServer(name); err != nil {
			slog.Error("update: disconnect builtin failed", "server", name, "error", err)
		}
	} else if tools.MCPServerConnected(name) {
		if err := tools.DisconnectMCPServer(name); err != nil {
			slog.Error("update: disconnect mcp failed", "server", name, "error", err)
		}
	}

	// Save env values as sensitive settings (same as connect).
	for key, val := range req.Env {
		if val != "" {
			if err := s.store.UpsertSetting(Setting{Key: key, Value: val, Sensitive: true}); err != nil {
				slog.Error("failed to save MCP env setting", "key", key, "error", err)
			}
		}
	}
	s.refreshToolSettings()

	// Build env map from stored settings + request env.
	envMap := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			envMap[st.Key] = st.Value
		}
	}
	for k, v := range req.Env {
		if v != "" {
			envMap[k] = v
		}
	}

	// Reconnect.
	var toolNames []string
	if entry, ok := mcp.Lookup(req.Name); ok {
		if entry.BuiltinGo && tools.HasBuiltinServer(req.Name) {
			for k, v := range envMap {
				os.Setenv(k, v)
			}
			if _, err := tools.ConnectBuiltinServer(r.Context(), req.Name); err != nil {
				writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
					Name: req.Name, Connected: false, Error: err.Error(),
				})
				return
			}
			for _, schema := range tools.Schema() {
				if strings.HasPrefix(schema.Name, req.Name+"__") {
					toolNames = append(toolNames, schema.Name)
				}
			}
		} else {
			cfg := entry.ToServerConfig(envMap)
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			if _, err := tools.ConnectMCPServer(ctx, cfg); err != nil {
				writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
					Name: req.Name, Connected: false, Error: err.Error(),
				})
				return
			}
			for _, st := range tools.MCPServerStatuses() {
				if st.Name == req.Name {
					toolNames = st.Tools
					break
				}
			}
		}
	} else {
		// Custom server.
		cfg := mcp.ServerConfig{
			Name:    req.Name,
			Command: req.Command,
			Args:    req.Args,
			URL:     req.URL,
			Headers: req.Headers,
			Env:     req.Env,
		}
		switch req.Transport {
		case "http":
			cfg.Transport = mcp.TransportHTTP
		case "sse":
			cfg.Transport = mcp.TransportSSE
		default:
			cfg.Transport = mcp.TransportStdio
		}
		if req.Timeout > 0 {
			cfg.Timeout = time.Duration(req.Timeout) * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if _, err := tools.ConnectMCPServer(ctx, cfg); err != nil {
			writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
				Name: req.Name, Connected: false, Error: err.Error(),
			})
			return
		}
		for _, st := range tools.MCPServerStatuses() {
			if st.Name == req.Name {
				toolNames = st.Tools
				break
			}
		}
	}

	// Persist updated config.
	s.persistMCPServer(req)

	slog.Info("updated MCP server", "server", name, "tools", len(toolNames))
	writeJSON(w, http.StatusOK, ConnectMCPResponse{
		Name:      name,
		Connected: true,
		Tools:     toolNames,
	})
}

func (s *Server) handleDuplicateMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "server name is required"})
		return
	}

	var body struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NewName == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "new_name is required"})
		return
	}

	tools := s.interp.Tools()

	// Check the new name isn't already in use.
	if tools.MCPServerConnected(body.NewName) || tools.BuiltinServerConnected(body.NewName) {
		writeJSON(w, http.StatusConflict, ErrorResponse{Error: fmt.Sprintf("server %q already exists", body.NewName)})
		return
	}

	// Load persisted config of the source server.
	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "persistence not available"})
		return
	}

	servers, err := sqlStore.ListMCPServers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to load server configs"})
		return
	}

	var srcReq ConnectMCPRequest
	var found bool
	for _, sc := range servers {
		if sc.Name == name {
			if err := json.Unmarshal([]byte(sc.ConfigJSON), &srcReq); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to parse source config"})
				return
			}
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("server %q not found", name)})
		return
	}

	// For registry servers, resolve the actual command/args so the duplicate
	// works as a standalone custom server (registry lookup uses the name).
	if entry, ok := mcp.Lookup(name); ok {
		srcReq.Transport = "stdio"
		srcReq.Command = entry.Command
		srcReq.Args = append([]string{}, entry.Args...)
	}

	// Create the duplicate with the new name.
	dupReq := srcReq
	dupReq.Name = body.NewName

	// Build env map from stored settings (env values aren't stored in the config).
	envMap := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			envMap[st.Key] = st.Value
		}
	}

	cfg := mcp.ServerConfig{
		Name:    dupReq.Name,
		Command: dupReq.Command,
		Args:    dupReq.Args,
		URL:     dupReq.URL,
		Headers: dupReq.Headers,
		Env:     envMap,
	}
	switch dupReq.Transport {
	case "http":
		cfg.Transport = mcp.TransportHTTP
	case "sse":
		cfg.Transport = mcp.TransportSSE
	default:
		cfg.Transport = mcp.TransportStdio
	}
	if dupReq.Timeout > 0 {
		cfg.Timeout = time.Duration(dupReq.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if _, err := tools.ConnectMCPServer(ctx, cfg); err != nil {
		writeJSON(w, http.StatusBadGateway, ConnectMCPResponse{
			Name: dupReq.Name, Connected: false, Error: err.Error(),
		})
		return
	}

	var toolNames []string
	for _, st := range tools.MCPServerStatuses() {
		if st.Name == dupReq.Name {
			toolNames = st.Tools
			break
		}
	}

	s.persistMCPServer(dupReq)

	slog.Info("duplicated MCP server", "source", name, "new", dupReq.Name, "tools", len(toolNames))
	writeJSON(w, http.StatusOK, ConnectMCPResponse{
		Name:      dupReq.Name,
		Connected: true,
		Tools:     toolNames,
	})
}

func (s *Server) handleToggleMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "server name is required"})
		return
	}

	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "persistence not available"})
		return
	}

	if err := sqlStore.SetMCPServerDisabled(name, body.Disabled); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}

	t := s.interp.Tools()

	if body.Disabled {
		// Disconnect the running server but keep it in persistence.
		if t.BuiltinServerConnected(name) {
			t.DisconnectBuiltinServer(name)
		} else if t.MCPServerConnected(name) {
			t.DisconnectMCPServer(name)
		}
		slog.Info("disabled MCP server", "server", name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}

	// Re-enable: reconnect the server.
	servers, err := sqlStore.ListMCPServers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to load server configs"})
		return
	}

	var req ConnectMCPRequest
	for _, sc := range servers {
		if sc.Name == name {
			if err := json.Unmarshal([]byte(sc.ConfigJSON), &req); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to parse server config"})
				return
			}
			break
		}
	}

	// Build env map from stored settings.
	envMap := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			envMap[st.Key] = st.Value
		}
	}
	for k := range req.Env {
		if val, ok := envMap[k]; ok {
			req.Env[k] = val
		}
	}

	// Reconnect.
	var toolNames []string
	if entry, ok := mcp.Lookup(req.Name); ok {
		if entry.BuiltinGo && t.HasBuiltinServer(req.Name) {
			for k, v := range envMap {
				os.Setenv(k, v)
			}
			if _, err := t.ConnectBuiltinServer(r.Context(), req.Name); err != nil {
				writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: err.Error()})
				return
			}
			for _, schema := range t.Schema() {
				if strings.HasPrefix(schema.Name, req.Name+"__") {
					toolNames = append(toolNames, schema.Name)
				}
			}
		} else {
			cfg := entry.ToServerConfig(envMap)
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			if _, err := t.ConnectMCPServer(ctx, cfg); err != nil {
				writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: err.Error()})
				return
			}
			for _, st := range t.MCPServerStatuses() {
				if st.Name == req.Name {
					toolNames = st.Tools
					break
				}
			}
		}
	} else {
		// Custom server.
		cfg := mcp.ServerConfig{
			Name:    req.Name,
			Command: req.Command,
			Args:    req.Args,
			URL:     req.URL,
			Headers: req.Headers,
			Env:     req.Env,
		}
		switch req.Transport {
		case "http":
			cfg.Transport = mcp.TransportHTTP
		case "sse":
			cfg.Transport = mcp.TransportSSE
		default:
			cfg.Transport = mcp.TransportStdio
		}
		if req.Timeout > 0 {
			cfg.Timeout = time.Duration(req.Timeout) * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if _, err := t.ConnectMCPServer(ctx, cfg); err != nil {
			writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: err.Error()})
			return
		}
		for _, st := range t.MCPServerStatuses() {
			if st.Name == req.Name {
				toolNames = st.Tools
				break
			}
		}
	}

	slog.Info("enabled MCP server", "server", name, "tools", len(toolNames))
	writeJSON(w, http.StatusOK, ConnectMCPResponse{
		Name:      name,
		Connected: true,
		Tools:     toolNames,
	})
}

// persistMCPServer saves the MCP server connect request so it auto-reconnects on restart.
func (s *Server) persistMCPServer(req ConnectMCPRequest) {
	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		return
	}
	// Strip env values — they're already saved as sensitive settings.
	// We keep the keys so we know which settings to load on reconnect.
	stripped := req
	stripped.Env = make(map[string]string, len(req.Env))
	for k := range req.Env {
		stripped.Env[k] = ""
	}
	configJSON, err := json.Marshal(stripped)
	if err != nil {
		slog.Error("failed to marshal MCP server config", "name", req.Name, "error", err)
		return
	}
	if err := sqlStore.UpsertMCPServer(req.Name, string(configJSON)); err != nil {
		slog.Error("failed to persist MCP server", "name", req.Name, "error", err)
	}
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
		stats.TotalCacheCreationTokens += m.CacheCreationInputTokens
		stats.TotalCacheReadTokens += m.CacheReadInputTokens
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

// --- Schedule Handlers ---

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	jobs := s.scheduler.ListJobs()
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "schedule name is required"})
		return
	}
	if err := s.scheduler.RemoveJob(name); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleToggleSchedule(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "schedule name is required"})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	// Find the existing job, update enabled state, and re-add it.
	jobs := s.scheduler.ListJobs()
	var found bool
	for _, job := range jobs {
		if job.Name == name {
			found = true
			job.Enabled = req.Enabled
			if err := s.scheduler.RemoveJob(name); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
				return
			}
			if err := s.scheduler.AddJob(job); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
				return
			}
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("schedule %q not found", name)})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
