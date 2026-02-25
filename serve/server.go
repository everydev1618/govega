package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/vega-population/population"
)

// streamSubscriber is a single SSE client subscribed to an active stream.
type streamSubscriber struct {
	ch     chan vega.ChatEvent
	closed bool
}

// activeStream tracks a server-side chat stream that runs independently of
// any connected SSE client. Events are buffered in history so reconnecting
// clients can replay them. Multiple subscribers can listen concurrently.
type activeStream struct {
	agentName string
	done      chan struct{} // closed when stream completes

	mu          sync.Mutex
	history     []vega.ChatEvent    // all events received, for replay
	subscribers []*streamSubscriber // active SSE subscribers
	response    string              // set after done
	err         error               // set after done
}

// publish sends an event to all active subscribers and appends it to history.
func (as *activeStream) publish(event vega.ChatEvent) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.history = append(as.history, event)
	for _, sub := range as.subscribers {
		if !sub.closed {
			select {
			case sub.ch <- event:
			default: // subscriber too slow, skip
			}
		}
	}
}

// subscribe returns a snapshot of all past events plus a channel for future
// events. The caller must call unsubscribe when done.
func (as *activeStream) subscribe() ([]vega.ChatEvent, chan vega.ChatEvent) {
	as.mu.Lock()
	defer as.mu.Unlock()
	snapshot := make([]vega.ChatEvent, len(as.history))
	copy(snapshot, as.history)
	ch := make(chan vega.ChatEvent, 256)
	as.subscribers = append(as.subscribers, &streamSubscriber{ch: ch})
	return snapshot, ch
}

// unsubscribe removes a subscriber channel.
func (as *activeStream) unsubscribe(ch chan vega.ChatEvent) {
	as.mu.Lock()
	defer as.mu.Unlock()
	for _, sub := range as.subscribers {
		if sub.ch == ch {
			sub.closed = true
			// Don't close — the finish() method handles closing all channels.
			return
		}
	}
}

// finish closes all subscriber channels. Called when the stream completes.
func (as *activeStream) finish() {
	as.mu.Lock()
	defer as.mu.Unlock()
	for _, sub := range as.subscribers {
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
	}
}

// Config holds server configuration.
type Config struct {
	Addr          string
	DBPath        string
	TelegramToken string // TELEGRAM_BOT_TOKEN; leave empty to disable
	TelegramAgent string // TELEGRAM_AGENT; defaults to first agent if empty
}

// Server is the HTTP server for the Vega dashboard and REST API.
type Server struct {
	interp    *dsl.Interpreter
	broker    *EventBroker
	store     Store
	popClient *population.Client
	telegram  *TelegramBot
	scheduler *Scheduler
	cfg       Config
	startedAt time.Time

	// extractLLM is a separate LLM client used for memory extraction.
	extractLLM   llm.LLM
	extractLLMMu sync.Once

	// streams tracks active chat streams keyed by agent name, decoupled
	// from any particular SSE client connection.
	streamsMu sync.Mutex
	streams   map[string]*activeStream
}

// New creates a new Server.
func New(interp *dsl.Interpreter, cfg Config) *Server {
	return &Server{
		interp:  interp,
		broker:  NewEventBroker(),
		cfg:     cfg,
		streams: make(map[string]*activeStream),
	}
}

// getExtractLLM returns the lazily-initialized LLM client for memory extraction.
func (s *Server) getExtractLLM() llm.LLM {
	s.extractLLMMu.Do(func() {
		s.extractLLM = llm.NewAnthropic(llm.WithModel("claude-haiku-4-5-20251001"))
	})
	return s.extractLLM
}

// Start initializes the store, wires callbacks, registers routes, and
// listens for HTTP requests. It blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	s.startedAt = time.Now()

	// Initialize SQLite store.
	store, err := NewSQLiteStore(s.cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	s.store = store
	if err := store.Init(); err != nil {
		return fmt.Errorf("init database: %w", err)
	}

	// Load settings into tools collection.
	s.refreshToolSettings()

	// Wire file-write tracking callback.
	s.interp.Tools().OnFileWrite = func(ctx context.Context, path, operation, description string) {
		agentName := ""
		processID := ""
		if proc := vega.ProcessFromContext(ctx); proc != nil {
			processID = proc.ID
			if proc.Agent != nil {
				agentName = proc.Agent.Name
			}
		}
		if err := store.InsertWorkspaceFile(WorkspaceFile{
			Path:        path,
			Agent:       agentName,
			ProcessID:   processID,
			Operation:   operation,
			Description: description,
		}); err != nil {
			slog.Error("failed to record workspace file", "path", path, "error", err)
		}
	}

	// Initialize population client.
	popClient, err := population.NewClient()
	if err != nil {
		slog.Warn("population client init failed, population features disabled", "error", err)
	} else {
		s.popClient = popClient
	}

	// Auto-connect MCP servers BEFORE restoring agents so that MCP tools
	// are registered in the global tool collection when agents spawn.
	// Without this ordering, spawnAgent's Filter() silently drops MCP tool
	// names that don't yet exist, leaving agents without their MCP tools.
	s.autoConnectBuiltinServers(ctx)
	s.autoConnectPersistedServers(ctx)

	// Restore composed agents from persistence (after MCP servers are connected).
	if s.popClient != nil {
		s.restoreComposedAgents()
	}

	// Register memory tools before injecting meta-agents so they can use them.
	RegisterMemoryTools(s.interp)

	// Inject Mother — the built-in meta-agent for creating agents via chat.
	s.injectMother()

	// Inject Hermes — the cosmic orchestrator that routes goals across all agents.
	s.injectHermes()

	// Set up scheduler and restore persisted jobs.
	s.scheduler = NewScheduler(
		s.interp,
		func(job dsl.ScheduledJob) error {
			return s.store.UpsertScheduledJob(ScheduledJob{
				Name:      job.Name,
				Cron:      job.Cron,
				AgentName: job.AgentName,
				Message:   job.Message,
				Enabled:   job.Enabled,
			})
		},
		func(name string) error {
			return s.store.DeleteScheduledJob(name)
		},
	)
	if storedJobs, err := s.store.ListScheduledJobs(); err != nil {
		slog.Warn("scheduler: failed to load persisted jobs", "error", err)
	} else {
		for _, sj := range storedJobs {
			job := dsl.ScheduledJob{
				Name:      sj.Name,
				Cron:      sj.Cron,
				AgentName: sj.AgentName,
				Message:   sj.Message,
				Enabled:   sj.Enabled,
			}
			if err := s.scheduler.AddJob(job); err != nil {
				slog.Warn("scheduler: failed to restore job", "name", sj.Name, "error", err)
			}
		}
	}
	dsl.RegisterSchedulerTools(s.interp, s.scheduler)
	go s.scheduler.Start(ctx)

	// Start Telegram bot if configured (after meta-agents are injected).
	if s.cfg.TelegramToken != "" {
		agentName := s.cfg.TelegramAgent
		if agentName == "" {
			agentName = dsl.HermesAgentName // default to Hermes
		}
		tb, err := NewTelegramBot(s.cfg.TelegramToken, agentName, s.interp, s.store, func(userID, agent, userMsg, response string) {
			s.extractMemory(userID, agent, userMsg, response)
		})
		if err != nil {
			slog.Warn("telegram bot init failed", "error", err)
		} else {
			s.telegram = tb
			go tb.Start(ctx)
			slog.Info("telegram bot started", "agent", agentName)
		}
	}

	// Wire orchestrator callbacks to broker + store.
	s.wireCallbacks()

	// Build router.
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := &http.Server{
		Addr:    s.cfg.Addr,
		Handler: corsMiddleware(mux),
	}

	// Start server in goroutine.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("vega serve started", "addr", s.cfg.Addr)
		fmt.Printf("Dashboard: http://localhost%s\n", s.cfg.Addr)
		fmt.Printf("API:       http://localhost%s/api/stats\n", s.cfg.Addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or error.
	select {
	case <-ctx.Done():
		slog.Info("shutting down server")
	case err := <-errCh:
		return err
	}

	// Close broker first — this closes all SSE subscriber channels,
	// unblocking their handlers so the HTTP server can drain cleanly.
	s.broker.Close()

	// Graceful shutdown with 5s timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
	if err := store.Close(); err != nil {
		slog.Error("store close error", "error", err)
	}

	return nil
}

// registerRoutes adds all API and frontend routes to the mux.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// REST API
	mux.HandleFunc("GET /api/processes", s.handleListProcesses)
	mux.HandleFunc("GET /api/processes/{id}", s.handleGetProcess)
	mux.HandleFunc("DELETE /api/processes/{id}", s.handleKillProcess)
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/workflows", s.handleListWorkflows)
	mux.HandleFunc("POST /api/workflows/{name}/run", s.handleRunWorkflow)
	mux.HandleFunc("GET /api/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("GET /api/mcp/registry", s.handleMCPRegistry)
	mux.HandleFunc("POST /api/mcp/servers", s.handleConnectMCPServer)
	mux.HandleFunc("POST /api/mcp/servers/{name}/refresh", s.handleRefreshMCPServer)
	mux.HandleFunc("DELETE /api/mcp/servers/{name}", s.handleDisconnectMCPServer)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/spawn-tree", s.handleSpawnTree)

	// Population
	mux.HandleFunc("GET /api/population/search", s.handlePopulationSearch)
	mux.HandleFunc("GET /api/population/info/{kind}/{name}", s.handlePopulationInfo)
	mux.HandleFunc("POST /api/population/install", s.handlePopulationInstall)
	mux.HandleFunc("GET /api/population/installed", s.handlePopulationInstalled)

	// Agent composition
	mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	mux.HandleFunc("PUT /api/agents/{name}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{name}", s.handleDeleteAgent)

	// Chat
	mux.HandleFunc("GET /api/agents/{name}/chat", s.handleChatHistory)
	mux.HandleFunc("POST /api/agents/{name}/chat", s.handleChat)
	mux.HandleFunc("POST /api/agents/{name}/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/agents/{name}/chat/stream", s.handleChatStreamReconnect)
	mux.HandleFunc("GET /api/agents/{name}/chat/status", s.handleChatStatus)
	mux.HandleFunc("DELETE /api/agents/{name}/chat", s.handleClearChat)

	// Memory
	mux.HandleFunc("GET /api/agents/{name}/memory", s.handleGetMemory)
	mux.HandleFunc("DELETE /api/agents/{name}/memory", s.handleDeleteMemory)

	// Files
	mux.HandleFunc("GET /api/files", s.handleListFiles)
	mux.HandleFunc("GET /api/files/read", s.handleReadFile)
	mux.HandleFunc("DELETE /api/files", s.handleDeleteFile)
	mux.HandleFunc("GET /api/files/metadata", s.handleListFileMetadata)

	// Settings
	mux.HandleFunc("GET /api/settings", s.handleListSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpsertSetting)
	mux.HandleFunc("DELETE /api/settings/{key}", s.handleDeleteSetting)

	// SSE
	mux.HandleFunc("GET /api/events", s.handleSSE)

	// Frontend SPA
	mux.Handle("/", frontendHandler())
}

// wireCallbacks hooks the orchestrator's lifecycle events into the broker and store.
func (s *Server) wireCallbacks() {
	orch := s.interp.Orchestrator()

	orch.OnProcessStarted(func(p *vega.Process) {
		agentName := ""
		if p.Agent != nil {
			agentName = p.Agent.Name
		}

		event := BrokerEvent{
			Type:      "process.started",
			ProcessID: p.ID,
			Agent:     agentName,
			Timestamp: time.Now(),
		}
		s.broker.Publish(event)

		s.store.InsertEvent(StoreEvent{
			Type:      "process.started",
			ProcessID: p.ID,
			AgentName: agentName,
			Timestamp: time.Now(),
		})
	})

	orch.OnProcessComplete(func(p *vega.Process, result string) {
		agentName := ""
		if p.Agent != nil {
			agentName = p.Agent.Name
		}

		event := BrokerEvent{
			Type:      "process.completed",
			ProcessID: p.ID,
			Agent:     agentName,
			Timestamp: time.Now(),
		}
		s.broker.Publish(event)

		s.store.InsertEvent(StoreEvent{
			Type:      "process.completed",
			ProcessID: p.ID,
			AgentName: agentName,
			Timestamp: time.Now(),
			Result:    truncate(result, 4096),
		})

		// Snapshot final state.
		s.store.(*SQLiteStore).snapshotProcess(processToResponse(p))
	})

	orch.OnProcessFailed(func(p *vega.Process, err error) {
		agentName := ""
		if p.Agent != nil {
			agentName = p.Agent.Name
		}

		event := BrokerEvent{
			Type:      "process.failed",
			ProcessID: p.ID,
			Agent:     agentName,
			Timestamp: time.Now(),
		}
		s.broker.Publish(event)

		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}

		s.store.InsertEvent(StoreEvent{
			Type:      "process.failed",
			ProcessID: p.ID,
			AgentName: agentName,
			Timestamp: time.Now(),
			Error:     errMsg,
		})

		// Snapshot final state.
		s.store.(*SQLiteStore).snapshotProcess(processToResponse(p))
	})
}

// corsMiddleware adds permissive CORS headers for development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Auth-User")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// autoConnectBuiltinServers connects any built-in Go MCP servers whose
// required environment variables are already set (e.g. from ~/.vega/env).
func (s *Server) autoConnectBuiltinServers(ctx context.Context) {
	t := s.interp.Tools()
	for _, entry := range mcp.DefaultRegistry {
		if !entry.BuiltinGo || !t.HasBuiltinServer(entry.Name) {
			continue
		}
		// Check all required env vars are present.
		allSet := true
		for _, key := range entry.RequiredEnv {
			if os.Getenv(key) == "" {
				allSet = false
				break
			}
		}
		if !allSet {
			continue
		}
		n, err := t.ConnectBuiltinServer(ctx, entry.Name)
		if err != nil {
			slog.Warn("auto-connect builtin server failed", "server", entry.Name, "error", err)
			continue
		}
		slog.Info("auto-connected builtin MCP server", "server", entry.Name, "tools", n)
	}
}

// autoConnectPersistedServers reconnects MCP servers that were previously
// connected and persisted in the mcp_servers table.
func (s *Server) autoConnectPersistedServers(ctx context.Context) {
	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		return
	}
	servers, err := sqlStore.ListMCPServers()
	if err != nil {
		slog.Warn("failed to load persisted MCP servers", "error", err)
		return
	}

	t := s.interp.Tools()

	// Load all settings for env resolution.
	envMap := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			envMap[st.Key] = st.Value
		}
	}

	for _, sc := range servers {
		// Skip if already connected (e.g. by autoConnectBuiltinServers).
		if t.MCPServerConnected(sc.Name) || t.BuiltinServerConnected(sc.Name) {
			continue
		}

		var req ConnectMCPRequest
		if err := json.Unmarshal([]byte(sc.ConfigJSON), &req); err != nil {
			slog.Warn("failed to parse persisted MCP server config", "name", sc.Name, "error", err)
			continue
		}

		// Fill in env values from stored settings.
		for k := range req.Env {
			if val, ok := envMap[k]; ok {
				req.Env[k] = val
			}
		}

		// Check registry for this server.
		if entry, ok := mcp.Lookup(req.Name); ok {
			// Builtin Go server — set env and connect.
			if entry.BuiltinGo && t.HasBuiltinServer(req.Name) {
				for k, v := range envMap {
					os.Setenv(k, v)
				}
				n, err := t.ConnectBuiltinServer(ctx, req.Name)
				if err != nil {
					slog.Warn("auto-connect persisted builtin server failed", "server", req.Name, "error", err)
					continue
				}
				slog.Info("auto-connected persisted builtin MCP server", "server", req.Name, "tools", n)
				continue
			}

			// Registry subprocess server — build config from registry entry.
			cfg := entry.ToServerConfig(envMap)
			connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			n, err := t.ConnectMCPServer(connectCtx, cfg)
			cancel()
			if err != nil {
				slog.Warn("auto-connect persisted registry server failed", "server", req.Name, "error", err)
				continue
			}
			slog.Info("auto-connected persisted MCP server", "server", req.Name, "tools", n)
		} else {
			// Custom server — build config from persisted request.
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
			connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			n, err := t.ConnectMCPServer(connectCtx, cfg)
			cancel()
			if err != nil {
				slog.Warn("auto-connect persisted custom server failed", "server", req.Name, "error", err)
				continue
			}
			slog.Info("auto-connected persisted custom MCP server", "server", req.Name, "tools", n)
		}
	}
}

// injectMother adds the Mother meta-agent to the interpreter with persistence
// callbacks that keep composed agents in sync with the SQLite store.
func (s *Server) injectMother() {
	cb := &dsl.MotherCallbacks{
		OnAgentCreated: func(agent *dsl.Agent) error {
			var skills []string
			if agent.Skills != nil {
				skills = agent.Skills.Directories
			}
			if err := s.store.InsertComposedAgent(ComposedAgent{
				Name:        agent.Name,
				Model:       agent.Model,
				System:      agent.System,
				Tools:       agent.Tools,
				Team:        agent.Team,
				Skills:      skills,
				Temperature: agent.Temperature,
				CreatedAt:   time.Now(),
			}); err != nil {
				slog.Error("failed to persist composed agent", "agent", agent.Name, "error", err)
				return fmt.Errorf("persist agent: %w", err)
			}
			s.broker.Publish(BrokerEvent{
				Type:      "agent.created",
				Agent:     agent.Name,
				Timestamp: time.Now(),
			})
			return nil
		},
		OnAgentDeleted: func(name string) {
			if err := s.store.DeleteComposedAgent(name); err != nil {
				slog.Error("failed to delete composed agent from store", "agent", name, "error", err)
			}
			s.broker.Publish(BrokerEvent{
				Type:      "agent.deleted",
				Agent:     name,
				Timestamp: time.Now(),
			})
		},
	}

	if err := dsl.InjectMother(s.interp, cb, "create_schedule", "update_schedule", "delete_schedule", "list_schedules"); err != nil {
		slog.Warn("failed to inject Mother agent", "error", err)
	}
}

// injectHermes adds Hermes, the cosmic orchestrator, to the interpreter.
func (s *Server) injectHermes() {
	if err := dsl.InjectHermes(s.interp, "remember", "recall", "forget"); err != nil {
		slog.Warn("failed to inject Hermes agent", "error", err)
	}
}

// refreshToolSettings loads all settings from the store and sets them on the
// interpreter's tools collection so dynamic tools can reference them.
func (s *Server) refreshToolSettings() {
	settings, err := s.store.ListSettings()
	if err != nil {
		slog.Error("failed to load settings for tools", "error", err)
		return
	}
	m := make(map[string]string, len(settings))
	for _, st := range settings {
		m[st.Key] = st.Value
	}
	s.interp.Tools().SetSettings(m)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
