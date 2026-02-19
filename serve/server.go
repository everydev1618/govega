package serve

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/vega-population/population"
)

// Config holds server configuration.
type Config struct {
	Addr   string
	DBPath string
}

// Server is the HTTP server for the Vega dashboard and REST API.
type Server struct {
	interp    *dsl.Interpreter
	broker    *EventBroker
	store     Store
	popClient *population.Client
	cfg       Config
	startedAt time.Time
}

// New creates a new Server.
func New(interp *dsl.Interpreter, cfg Config) *Server {
	return &Server{
		interp: interp,
		broker: NewEventBroker(),
		cfg:    cfg,
	}
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

	// Initialize population client.
	popClient, err := population.NewClient()
	if err != nil {
		slog.Warn("population client init failed, population features disabled", "error", err)
	} else {
		s.popClient = popClient
	}

	// Restore composed agents from persistence.
	if s.popClient != nil {
		s.restoreComposedAgents()
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
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/spawn-tree", s.handleSpawnTree)

	// Population
	mux.HandleFunc("GET /api/population/search", s.handlePopulationSearch)
	mux.HandleFunc("GET /api/population/info/{kind}/{name}", s.handlePopulationInfo)
	mux.HandleFunc("POST /api/population/install", s.handlePopulationInstall)
	mux.HandleFunc("GET /api/population/installed", s.handlePopulationInstalled)

	// Agent composition
	mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	mux.HandleFunc("DELETE /api/agents/{name}", s.handleDeleteAgent)

	// Chat
	mux.HandleFunc("GET /api/agents/{name}/chat", s.handleChatHistory)
	mux.HandleFunc("POST /api/agents/{name}/chat", s.handleChat)
	mux.HandleFunc("DELETE /api/agents/{name}/chat", s.handleClearChat)

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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
