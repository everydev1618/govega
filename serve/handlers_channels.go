package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	vega "github.com/everydev1618/govega"
)

// channelStreams tracks active channel streams keyed by channel name.
var (
	channelStreamsMu sync.Mutex
	channelStreams    = make(map[string]*channelStream)
)

// --- Channel CRUD Handlers ---

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.store.ListChannels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if channels == nil {
		channels = []Channel{}
	}
	writeJSON(w, http.StatusOK, channels)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "name is required"})
		return
	}

	userID := r.Header.Get("X-Auth-User")
	if userID == "" {
		userID = "default"
	}

	id := fmt.Sprintf("ch_%d", time.Now().UnixNano())
	if err := s.store.CreateChannel(id, req.Name, req.Description, userID, req.Team, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	ch, _ := s.store.GetChannel(req.Name)
	if ch == nil {
		writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "id": id})
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ch, err := s.store.GetChannel(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if ch == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.DeleteChannel(name); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleUpdateChannelTeam(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Team []string `json:"team"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request"})
		return
	}
	if err := s.store.UpdateChannelTeam(name, req.Team); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- Channel Message Handlers ---

func (s *Server) handleListChannelMessages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ch, err := s.store.GetChannel(name)
	if err != nil || ch == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	messages, err := s.store.ListChannelMessages(ch.ID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if messages == nil {
		messages = []ChannelMessage{}
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleListThreadMessages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ch, err := s.store.GetChannel(name)
	if err != nil || ch == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}

	idStr := r.PathValue("id")
	threadID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid message id"})
		return
	}

	messages, err := s.store.ListThreadMessages(ch.ID, threadID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if messages == nil {
		messages = []ChannelMessage{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// --- Channel Post (non-streaming) ---

func (s *Server) handleChannelPost(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ch, err := s.store.GetChannel(name)
	if err != nil || ch == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}

	var req ChannelPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "message is required"})
		return
	}

	// Insert user message (top-level or into thread).
	msgID, err := s.store.InsertChannelMessage(ch.ID, "", "user", req.Message, req.ThreadID, "{}")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Determine which agent(s) to activate.
	targetAgent := req.Agent
	if targetAgent == "" && len(ch.Team) > 0 {
		targetAgent = ch.Team[0] // team lead
	}

	if targetAgent == "" {
		writeJSON(w, http.StatusOK, map[string]any{"message_id": msgID})
		return
	}

	// Start agent response in a thread under the user message.
	threadID := msgID
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}

	go s.runChannelAgent(ch, targetAgent, req.Message, threadID)

	// In social mode, notify all OTHER team members so they respond too.
	if ch.Mode == "social" {
		s.notifyChannelTeammates(ch, targetAgent, req.Message, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{"message_id": msgID, "thread_id": threadID})
}

// --- Channel Stream Handler ---

func (s *Server) handleChannelStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ch, err := s.store.GetChannel(name)
	if err != nil || ch == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "channel not found"})
		return
	}

	if r.Method == "GET" {
		// Reconnect to existing stream.
		s.relayChannelStreamSSE(w, r, name)
		return
	}

	// POST — new message + stream.
	var req ChannelPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "message is required"})
		return
	}

	// Insert user message.
	msgID, err := s.store.InsertChannelMessage(ch.ID, "", "user", req.Message, req.ThreadID, "{}")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Determine thread: if posting to existing thread, use that; else thread under user message.
	threadID := msgID
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}

	// Determine target agent.
	targetAgent := req.Agent
	if targetAgent == "" && len(ch.Team) > 0 {
		targetAgent = ch.Team[0]
	}

	// Get or create channel stream.
	cs := s.getOrCreateChannelStream(name)

	// Publish user message event — if posting into an existing thread,
	// emit it as a thread_reply so the client places it in the thread panel.
	if req.ThreadID != nil {
		cs.publish(ChannelEvent{
			Type:      "channel.thread_reply",
			Channel:   name,
			MessageID: msgID,
			ThreadID:  req.ThreadID,
			Role:      "user",
			Content:   req.Message,
		})
	} else {
		cs.publish(ChannelEvent{
			Type:      "channel.message",
			Channel:   name,
			MessageID: msgID,
			Role:      "user",
			Content:   req.Message,
		})
	}

	if targetAgent != "" {
		go s.runChannelAgentStreamed(ch, cs, targetAgent, req.Message, threadID)
	}

	// In social mode, notify all OTHER team members so they respond too.
	if ch.Mode == "social" {
		s.notifyChannelTeammates(ch, targetAgent, req.Message, 0)
	}

	// Relay SSE to this client.
	s.relayChannelStreamSSE(w, r, name)
}

func (s *Server) handleChannelStreamReconnect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.relayChannelStreamSSE(w, r, name)
}

// --- Internal ---

func (s *Server) getOrCreateChannelStream(name string) *channelStream {
	channelStreamsMu.Lock()
	defer channelStreamsMu.Unlock()
	if cs, ok := channelStreams[name]; ok && !cs.finished {
		return cs
	}
	cs := &channelStream{
		channelName: name,
		done:        make(chan struct{}),
	}
	channelStreams[name] = cs
	return cs
}

// runChannelAgent runs an agent in a channel thread (non-streaming, fire-and-forget).
func (s *Server) runChannelAgent(ch *Channel, agentName, message string, threadID int64) {
	proc, err := s.interp.EnsureAgent(agentName)
	if err != nil {
		slog.Error("channel: failed to ensure agent", "agent", agentName, "error", err)
		return
	}
	s.hydrateAgent(proc, agentName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	stream, err := s.interp.StreamToAgent(ctx, agentName, message)
	if err != nil {
		slog.Error("channel: failed to stream to agent", "agent", agentName, "error", err)
		return
	}

	// Consume all events to get the response.
	for range stream.Events() {
	}

	response := stream.Response()
	if response != "" {
		tid := threadID
		s.store.InsertChannelMessage(ch.ID, agentName, "assistant", response, &tid, "{}")
	}
}

// runChannelAgentStreamed runs an agent and publishes events to the channel stream.
func (s *Server) runChannelAgentStreamed(ch *Channel, cs *channelStream, agentName, message string, threadID int64) {
	proc, err := s.interp.EnsureAgent(agentName)
	if err != nil {
		slog.Error("channel: failed to ensure agent", "agent", agentName, "error", err)
		cs.publish(ChannelEvent{
			Type:    "channel.done",
			Channel: ch.Name,
		})
		return
	}
	s.hydrateAgent(proc, agentName)

	userID := "default"
	if memories, err := s.store.GetUserMemory(userID, agentName); err == nil && len(memories) > 0 {
		if memText := formatMemoryForInjection(memories); memText != "" {
			proc.SetExtraSystem(memText)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	baseMetrics := proc.Metrics()
	streamStart := time.Now()

	stream, err := s.interp.StreamToAgent(ctx, agentName, message)
	if err != nil {
		slog.Error("channel: failed to stream to agent", "agent", agentName, "error", err)
		cs.publish(ChannelEvent{
			Type:    "channel.done",
			Channel: ch.Name,
		})
		return
	}

	// Publish typing indicator.
	tid := threadID
	cs.publish(ChannelEvent{
		Type:     "channel.typing",
		Channel:  ch.Name,
		Agent:    agentName,
		ThreadID: &tid,
	})

	// Relay LLM events as channel events.
	for event := range stream.Events() {
		switch event.Type {
		case vega.ChatEventTextDelta:
			cs.publish(ChannelEvent{
				Type:     "channel.text_delta",
				Channel:  ch.Name,
				Agent:    agentName,
				ThreadID: &tid,
				Delta:    event.Delta,
			})
		case vega.ChatEventToolStart:
			meta, _ := json.Marshal(map[string]any{
				"tool_call_id": event.ToolCallID,
				"tool_name":    event.ToolName,
				"arguments":    event.Arguments,
			})
			cs.publish(ChannelEvent{
				Type:     "channel.tool_start",
				Channel:  ch.Name,
				Agent:    agentName,
				ThreadID: &tid,
				Content:  string(meta),
			})
		case vega.ChatEventToolEnd:
			meta, _ := json.Marshal(map[string]any{
				"tool_call_id": event.ToolCallID,
				"tool_name":    event.ToolName,
				"result":       truncate(event.Result, 2048),
				"duration_ms":  event.DurationMs,
				"error":        event.Error,
			})
			cs.publish(ChannelEvent{
				Type:     "channel.tool_end",
				Channel:  ch.Name,
				Agent:    agentName,
				ThreadID: &tid,
				Content:  string(meta),
			})
		case vega.ChatEventError:
			cs.publish(ChannelEvent{
				Type:     "channel.error",
				Channel:  ch.Name,
				Agent:    agentName,
				ThreadID: &tid,
				Content:  event.Error,
			})
		}
	}

	response := stream.Response()

	// Compute metrics delta.
	finalMetrics := proc.Metrics()
	delta := map[string]any{
		"input_tokens":  finalMetrics.InputTokens - baseMetrics.InputTokens,
		"output_tokens": finalMetrics.OutputTokens - baseMetrics.OutputTokens,
		"cost_usd":      finalMetrics.CostUSD - baseMetrics.CostUSD,
		"duration_ms":   time.Since(streamStart).Milliseconds(),
	}

	// Persist the agent response.
	var replyMsgID int64
	if response != "" {
		replyMsgID, _ = s.store.InsertChannelMessage(ch.ID, agentName, "assistant", response, &tid, "{}")
	}

	// Publish the complete thread reply event.
	cs.publish(ChannelEvent{
		Type:      "channel.thread_reply",
		Channel:   ch.Name,
		MessageID: replyMsgID,
		ThreadID:  &tid,
		Agent:     agentName,
		Role:      "assistant",
		Content:   response,
	})

	// Publish done event.
	cs.publish(ChannelEvent{
		Type:     "channel.done",
		Channel:  ch.Name,
		ThreadID: &tid,
		Agent:    agentName,
		Metrics:  delta,
	})

	// Close all subscriber channels so SSE connections terminate.
	close(cs.done)
	cs.finish()

	// Clean up the channel stream after a delay so late reconnects
	// can still see the final state via history replay.
	go func() {
		time.Sleep(30 * time.Second)
		channelStreamsMu.Lock()
		if current, ok := channelStreams[ch.Name]; ok && current == cs {
			delete(channelStreams, ch.Name)
		}
		channelStreamsMu.Unlock()
	}()
}

// relayChannelStreamSSE subscribes to a channel stream and relays events as SSE.
func (s *Server) relayChannelStreamSSE(w http.ResponseWriter, r *http.Request, channelName string) {
	channelStreamsMu.Lock()
	cs, ok := channelStreams[channelName]
	channelStreamsMu.Unlock()

	if !ok {
		// No active stream — just send a connected comment and wait.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "streaming not supported"})
			return
		}
		fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		// Wait for context cancellation.
		<-r.Context().Done()
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "streaming not supported"})
		return
	}

	history, ch := cs.subscribe()
	defer cs.unsubscribe(ch)

	// Replay buffered history.
	for _, event := range history {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
	}
	flusher.Flush()

	// Check if already finished.
	cs.mu.Lock()
	finished := cs.finished
	cs.mu.Unlock()
	if finished {
		return
	}

	// Stream live events.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()

		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}
