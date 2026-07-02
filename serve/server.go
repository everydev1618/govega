package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/internal/envcompat"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/reactive"
	"github.com/everydev1618/govega/serve/peering"
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
	// startedAt is the wall-clock instant this stream was created.
	// handleChatStatus uses it to detect entries orphaned by a killed
	// goroutine (machine restart, OOM, panic) — anything older than
	// activeStreamMaxAge with `done` still open is treated as dead so
	// the SPA's chat input doesn't lock the user out forever.
	startedAt time.Time

	mu          sync.Mutex
	history     []vega.ChatEvent       // all events received, for replay
	subscribers []*streamSubscriber    // active SSE subscribers
	response    string                 // set after done
	err         error                  // set after done
	metrics     *vega.ChatEventMetrics // set after done
	// recalled is the per-turn ledger of memory pages that backed
	// this stream's reply (active injection + memory_read tool
	// calls). Snapshotted at stream completion and emitted as a
	// `recalled` SSE event so the FE can render "remembered from X"
	// pills (refs govega#100).
	recalled []RecallEntry
}

// activeStreamMaxAge is the upper bound for treating an entry in
// s.streams as a live in-flight run. Bounded by handleChatStream's
// detached-context timeout (60min) — beyond that, the goroutine is
// gone whether it told us or not.
const activeStreamMaxAge = 65 * time.Minute

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

// DBKind selects the persistence backend (refs govega#61). Empty / "sqlite"
// stays the zero-config default and reads/writes a single file from DBPath.
// "postgres" routes through DBURL and is the right choice for hosted,
// multi-writer deployments where SQLite's single-writer model bites.
type DBKind string

const (
	DBKindSQLite   DBKind = "sqlite"
	DBKindPostgres DBKind = "postgres"
)

// Config holds server configuration.
type Config struct {
	// Version is the govega build version (e.g. "v0.7.15"), surfaced to
	// the UI via GET /api/v1/tenant/config. Set by the CLI from the
	// ldflags-injected main.version; empty ("dev") when unset.
	Version string
	Addr    string
	DBPath  string
	// DBKind picks the persistence backend. Defaults to SQLite when empty.
	DBKind DBKind
	// DBURL is the Postgres connection URL when DBKind == "postgres"
	// (e.g. postgres://user:pass@host:5432/db?sslmode=require). Ignored
	// for SQLite.
	DBURL string
	// BlobDir, when non-empty, enables the FilesystemBlobStore at the
	// supplied path (refs govega#61 phase 5). When empty, blob-bearing
	// resources (agent brain files) keep their content inline in the
	// relational DB — preserving the zero-config default. Set this on
	// hosted Postgres deployments so brain content stays out of the
	// DB and on the filesystem (or, future, an S3-compatible store).
	BlobDir       string
	TelegramToken string       // TELEGRAM_BOT_TOKEN; leave empty to disable
	TelegramAgent string       // TELEGRAM_AGENT; defaults to first agent if empty
	DiscordToken  string       // DISCORD_BOT_TOKEN; leave empty to disable
	DiscordAgent  string       // DISCORD_AGENT; defaults to orchestrator if empty
	Company       *dsl.Company // optional company identity (env var overrides)

	// PublicURL is the externally-reachable base URL of this server (no
	// trailing slash), used to build OAuth redirect URIs that match what
	// operators register in their identity-provider consoles. When empty,
	// the redirect URI is inferred from the incoming request's Host header
	// and X-Forwarded-Proto — which works for laptops but not behind some
	// reverse proxies. Set this on any deployed instance.
	PublicURL string

	// Orchestrator/Builder identify the meta-agents used for routing,
	// scheduling, and UI affordances. Default to "iris"/"hera" — apps
	// embedding govega can override to rebrand.
	Orchestrator dsl.IrisConfig
	Builder      dsl.HeraConfig

	// FrontendFS lets a downstream consumer embed its own React build
	// instead of govega's bundled UI. When nil, the bundled frontend is
	// served from serve/builtinui.
	FrontendFS fs.FS

	// Store, when non-nil, is used as the persistence layer instead of
	// opening a SQLite database from DBPath. Useful for tests (in-memory
	// or temp-file stores) and for embedding products that want to
	// supply a different backend.
	Store Store

	// Middleware is applied between govega's authMiddleware and the mux
	// (i.e. as the innermost layer before route handlers run). Embedding
	// products use this to inject identity from non-JWT sources via
	// WithClaims, enforce shared-secret headers from trusted proxies,
	// stash request-scoped data in the context, etc.
	//
	// The slice is composed left-to-right: Middleware[0] wraps everything
	// inside it, so the first entry runs outermost relative to the rest.
	Middleware []func(http.Handler) http.Handler

	// ExtraSystemProvider, when non-nil, supplies additional system-prompt
	// content on every chat turn — appended after the standard
	// memory/project/company blocks. Use this to inject per-session
	// context that the agent's static configuration can't express,
	// e.g. looking up a per-book writing norm from an external service
	// based on the cloned agent name. Return "" for a no-op.
	//
	// The callback runs on the chat hot path: keep it fast and cache
	// where appropriate.
	ExtraSystemProvider ExtraSystemProvider
}

// ExtraSystemProvider returns additional system-prompt content for a
// given chat session. See Config.ExtraSystemProvider for details.
//
//   - agentName is the full (possibly cloned) name, e.g.
//     "guide:user_abc:my-book".
//   - baseAgent is the un-namespaced agent name, e.g. "guide".
//   - userID is the authenticated user (empty in self-hosted mode).
type ExtraSystemProvider func(ctx context.Context, agentName, baseAgent, userID string) string

// Server is the HTTP server for the Vega dashboard and REST API.
type Server struct {
	interp *dsl.Interpreter
	broker *EventBroker
	// bus is the domain-event spine (D3). The SSE broker is double-fed from
	// it (Phase 1); the reactive router subscribes to it to wake agents.
	bus            *events.Bus
	reactiveRouter *reactive.Router
	// reactiveDistiller, if set, turns a reactive wake's (event, result) into a
	// concise memory note via the LLM. Nil = use the templated fallback.
	reactiveDistiller func(ctx context.Context, e events.Event, result string) string
	store             Store
	// Peering subsystem (orchestrator-to-orchestrator federation over AIRE).
	// Nil when VEGA_PEERING_ADDR is unset. See peering_boot.go.
	peeringNode   *peering.Node
	peeringDialer *peering.Dialer
	peeringStore  peering.Store
	// blobs is the optional object-storage backend for binary content
	// that shouldn't live in the relational DB (refs govega#61 phase 5).
	// Today only agent brain files use it; when blobs is nil, the brain
	// handler keeps inline content in the agent_brain_files row instead.
	blobs     BlobStore
	popClient *population.Client

	// Telegram bots. Multi-bot — keyed by bot ID (the numeric prefix of
	// the API token). Each bot has its own polling loop and cancel func.
	// telegramCtx is the server's Start ctx, used as parent for every
	// bot's polling ctx so they all die when the server stops.
	telegramMu  sync.Mutex
	telegrams   map[string]*runningTelegramBot
	telegramCtx context.Context

	// Discord bots. Same multi-bot model as Telegram, keyed by bot ID (the
	// snowflake derived from the token). Each bot owns a gateway session;
	// discordCtx is the parent Start ctx so all sessions close on stop.
	discordMu  sync.Mutex
	discords   map[string]*runningDiscordBot
	discordCtx context.Context

	scheduler *Scheduler
	cfg       Config
	startedAt time.Time

	// callerResolver, when set, enriches the ctx for any background work
	// path (scheduler tick, Telegram inbound, peering inbound) with the
	// caller's identity + per-user credentials before dispatch. Set via
	// WithCallerResolver before Start (apexvega#24). Nil in self-hosted
	// mode — no resolution happens, and the static API key on the LLM
	// client handles authentication just like before.
	callerResolver CallerResolver

	// extractLLM is a separate LLM client used for memory extraction.
	extractLLM   llm.LLM
	extractLLMMu sync.Once

	// extractSem limits memory extraction to one at a time; extra
	// requests are dropped rather than queued.
	extractSem chan struct{}

	// company is the resolved company identity for this instance.
	company *dsl.Company

	// streams tracks active chat streams keyed by agent name, decoupled
	// from any particular SSE client connection.
	streamsMu sync.Mutex
	streams   map[string]*activeStream

	// oauthStateCache holds short-lived CSRF state tokens issued during
	// the OAuth start phase. See oauth_gmail.go.
	oauthStateCache *oauthStateCache

	// authCfg captures the JWT validation contract (set in Start from env).
	// Handlers outside the /api/v1 middleware (e.g. Gmail handoff) call
	// authCfg.Verify to validate signed tokens from the control plane.
	authCfg AuthConfig

	// controlPlaneURL is where /api/v1/integrations/gmail/start proxies init
	// requests. Empty in self-hosted mode → /api/v1/integrations/gmail/start
	// returns 503.
	controlPlaneURL string

	// consumedJTIs tracks Gmail handoff JTIs that have already been
	// consumed, defending against replay of a leaked 60-second handoff.
	// jti → unix expiry. Pruning is best-effort lazy.
	consumedJTIs sync.Map

	// replyTargets binds an agent name to a channel-of-origin handle so
	// async dispatch completions can be pushed back to the conversation
	// they came from (Telegram bot.Send, web SSE flush, etc.). See
	// reply_targets.go.
	replyTargetsMu sync.RWMutex
	replyTargets   map[string]dsl.ReplyTarget

	// routeHooks are extra HTTP handlers registered by the embedding
	// product (apexvega) via RegisterRoute. Applied after govega's own
	// routes during Start, so any conflict surfaces as a Go panic the
	// operator will see immediately.
	routeHooksMu sync.Mutex
	routeHooks   []routeHook
}

// routeHook is one (pattern, handler) pair stashed by RegisterRoute and
// mounted on the mux during Start.
type routeHook struct {
	pattern string
	handler http.HandlerFunc
}

// RegisterRoute mounts an HTTP handler on the server's mux. Must be
// called before Start. Routes outside /api/v1/* are not gated by the
// bearer-JWT middleware — useful for service-to-service webhooks that
// authenticate themselves (shared secret in a header, signed JWT in a
// query param, etc.).
//
// Conflicts with existing patterns surface as a panic from net/http when
// the mux is built — same behavior as govega's own route registrations.
func (s *Server) RegisterRoute(pattern string, handler http.HandlerFunc) {
	s.routeHooksMu.Lock()
	defer s.routeHooksMu.Unlock()
	s.routeHooks = append(s.routeHooks, routeHook{pattern: pattern, handler: handler})
}

// Interpreter returns the underlying dsl.Interpreter so integrations can
// access the tool registry, agent set, etc.
func (s *Server) Interpreter() *dsl.Interpreter { return s.interp }

// Store returns the persistence store. Integrations use this to persist
// their settings (API keys, shared secrets, per-tenant config) under the
// MCPSettingKey namespace.
func (s *Server) Store() Store { return s.store }

// New creates a new Server.
func New(interp *dsl.Interpreter, cfg Config) *Server {
	// Fill in defaults for the meta-agent identities so callers passing
	// zero-value Config still get the bundled Iris/Hera personas.
	if cfg.Orchestrator.Name == "" {
		cfg.Orchestrator = dsl.DefaultIrisConfig()
	} else {
		// Caller set Name (and possibly other fields); fill remaining defaults.
		// applyDefaults is unexported, so set the zero fields here.
		def := dsl.DefaultIrisConfig()
		if cfg.Orchestrator.DisplayName == "" {
			cfg.Orchestrator.DisplayName = def.DisplayName
		}
		if cfg.Orchestrator.BuilderName == "" {
			cfg.Orchestrator.BuilderName = def.BuilderName
		}
		if cfg.Orchestrator.BuilderDisplayName == "" {
			cfg.Orchestrator.BuilderDisplayName = def.BuilderDisplayName
		}
		if cfg.Orchestrator.ProductName == "" {
			cfg.Orchestrator.ProductName = def.ProductName
		}
	}
	if cfg.Builder.Name == "" {
		cfg.Builder = dsl.DefaultHeraConfig()
	} else {
		def := dsl.DefaultHeraConfig()
		if cfg.Builder.DisplayName == "" {
			cfg.Builder.DisplayName = def.DisplayName
		}
		if cfg.Builder.OrchestratorName == "" {
			cfg.Builder.OrchestratorName = def.OrchestratorName
		}
		if cfg.Builder.OrchestratorDisplayName == "" {
			cfg.Builder.OrchestratorDisplayName = def.OrchestratorDisplayName
		}
		if cfg.Builder.ProductName == "" {
			cfg.Builder.ProductName = def.ProductName
		}
	}
	return &Server{
		interp:          interp,
		broker:          NewEventBroker(),
		bus:             events.NewBus(),
		store:           cfg.Store, // may be nil; Start() opens SQLite when so
		cfg:             cfg,
		streams:         make(map[string]*activeStream),
		extractSem:      make(chan struct{}, 1),
		oauthStateCache: newOAuthStateCache(),
	}
}

// resolveCompany determines the company identity: Config.Company > Document.Company > nil.
func (s *Server) resolveCompany() *dsl.Company {
	if s.cfg.Company != nil {
		return s.cfg.Company
	}
	if doc := s.interp.Document(); doc != nil && doc.Company != nil {
		return doc.Company
	}
	return nil
}

// getExtractLLM returns the lazily-initialized LLM client for memory extraction.
func (s *Server) getExtractLLM() llm.LLM {
	s.extractLLMMu.Do(func() {
		s.extractLLM = llm.New()
	})
	return s.extractLLM
}

// resolveAddr binds a TCP listener on addr (or ":0" if addr is empty to
// let the OS pick a free port). It returns the listener and the resolved
// address with the actual port filled in.
// composeMiddleware wraps inner with the given middleware slice such that
// mw[0] runs outermost (first to see the request) and mw[len-1] runs
// innermost. Matches the conventional left-to-right reading order: the
// first middleware listed is the first to execute. Returns inner
// unchanged when the slice is empty.
func composeMiddleware(mw []func(http.Handler) http.Handler, inner http.Handler) http.Handler {
	h := inner
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

func resolveAddr(addr string) (net.Listener, string, error) {
	if addr == "" {
		// Security default: bind loopback only so an unconfigured self-hosted
		// instance isn't reachable (and RCE-exploitable via the MCP endpoint)
		// from the LAN. To expose it, pass an explicit address such as
		// ":8080" or "0.0.0.0:8080".
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	return ln, ln.Addr().String(), nil
}

// Start initializes the store, wires callbacks, registers routes, and
// listens for HTTP requests. It blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	s.startedAt = time.Now()

	// Initialize the persistence backend (unless one was injected via
	// Config.Store). DBKind picks between SQLite (default — zero config,
	// single file) and Postgres (refs govega#61 — for multi-writer
	// hosted deployments). We own (and Close) the store iff we opened
	// it; an injected store is the caller's responsibility.
	var weOwnStore bool
	if s.store == nil {
		store, err := openConfiguredStore(s.cfg)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		s.store = store
		if err := store.Init(); err != nil {
			return fmt.Errorf("init database: %w", err)
		}
		weOwnStore = true
	}

	// Optional blob store for binary content (brain files today; chat
	// attachments next when #48 lands). When unconfigured, brain
	// content stays inline in the DB — zero-config default preserved.
	if s.blobs == nil && s.cfg.BlobDir != "" {
		fs, err := NewFilesystemBlobStore(s.cfg.BlobDir)
		if err != nil {
			return fmt.Errorf("init blob store: %w", err)
		}
		s.blobs = fs
	}

	// Hydrate provider env vars from settings so a fresh container with a
	// restored database boots ready to talk to those providers.
	s.hydrateGmailRefreshToken()

	// Resolve company identity.
	s.company = s.resolveCompany()

	// Base URL is set later after the listener resolves the actual port.

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
		if err := s.store.InsertWorkspaceFile(WorkspaceFile{
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
	s.autoConnectHTTPServers(ctx)
	s.autoConnectPersistedServers(ctx)
	s.persistYAMLMCPServers()

	// Restore composed agents from persistence (after MCP servers are connected).
	if s.popClient != nil {
		s.restoreComposedAgents()
	}

	// Register memory tools before injecting meta-agents so they can use them.
	RegisterMemoryTools(s.interp)
	// Wiki-style memory tools (govega#71). Registered alongside the
	// legacy remember/recall/forget surface — both available during the
	// migration window.
	RegisterWikiMemoryTools(s.interp)

	// One-shot migration of legacy user_memory + memory_items into the
	// wiki (govega#71). Idempotent — gated by the
	// memory_wiki_migrated_v1 setting. Logged but non-fatal: a
	// migration failure shouldn't block startup.
	if report, err := migrateToWikiMemory(s.store); err != nil {
		slog.Error("wiki memory migration failed; continuing without migrating", "error", err)
	} else if report.JustApplied {
		slog.Info("wiki memory migration applied",
			"users", report.Users, "agents_touched", report.AgentsTouched,
			"pages_written", report.PagesWritten)
	}

	// v2 backfill — picks up memory_items rows that landed after the v1
	// stamp (e.g. anything `remember`d before Iris's tool cutover) and
	// publishes them to a user-scope legacy-items.md so they're visible
	// in /memory. Gated by memory_wiki_backfilled_v2; non-fatal.
	if report, err := backfillUserScopeLegacyItems(s.store); err != nil {
		slog.Error("wiki memory backfill v2 failed; continuing", "error", err)
	} else if report.JustApplied {
		slog.Info("wiki memory backfill v2 applied",
			"users_touched", report.UsersTouched,
			"pages_written", report.PagesWritten)
	}

	// Register channel tools BEFORE injecting meta-agents — both Hera and
	// Iris list channel tools in their `Tools` slice, and spawnAgent's
	// Filter() takes a snapshot at injection time. If channel tools register
	// later, they're silently dropped from the meta-agents' schemas and the
	// LLM never sees them (refs govega#57 — ARIA hitting "create_channel not
	// available" during onboarding). Same root cause as the MCP ordering
	// comment above.
	channelPostCb, channelReactiveCb, channelLifecycleCb := s.buildChannelCallbacks()
	dsl.RegisterChannelTools(s.interp, s.store, channelPostCb, channelReactiveCb, channelLifecycleCb)

	// Apply any persisted orchestrator identity override (refs govega#58).
	// The override lives in the settings table because the orchestrator is
	// injected programmatically from cfg.Orchestrator and doesn't have a
	// composed_agent record. Reading settings here lets a renamed
	// orchestrator survive a restart.
	//
	// ORCHESTRATOR_NAME env var (set by v39a from /company/account) gets
	// persisted to settings before we apply, so an external rename becomes
	// the authoritative identity on every boot.
	if envName := os.Getenv("ORCHESTRATOR_NAME"); envName != "" {
		// Two roles for the env value:
		//   - Name (slug) is the canonical id used in URLs / API paths /
		//     agent-map lookups. Lowercased so /chat/charlie and
		//     /chat/Charlie don't diverge into two different keys.
		//   - DisplayName preserves the user's casing for the system prompt
		//     and UI labels, so the LLM still introduces itself as "Charlie".
		slug := strings.ToLower(envName)
		display := envName
		if err := persistOrchestratorOverrides(s.store, slug, display, ""); err != nil {
			slog.Warn("ORCHESTRATOR_NAME env: failed to persist override", "name", envName, "error", err)
		}
	}
	s.cfg.Orchestrator = applyOrchestratorOverrides(s.cfg.Orchestrator, s.store)

	// Model override — env-driven only (no UI surface yet). Lets v39a pick
	// Opus / Sonnet / Haiku per-tenant.
	if envModel := os.Getenv("ORCHESTRATOR_MODEL"); envModel != "" {
		s.cfg.Orchestrator.Model = envModel
	}

	// Start peering (orchestrator-to-orchestrator federation over AIRE)
	// before meta-agent injection so Iris can pick up federation tools.
	// No-op when VEGA_PEERING_ADDR is unset; logs + skips on misconfig.
	s.startPeering(ctx)

	// Inject Hera — the built-in meta-agent for creating agents via chat.
	s.injectHera()

	// Inject Iris — the messenger goddess that routes goals across all agents.
	s.injectIris()

	// Inject Mira — the memory curator (govega#71). Fires after each
	// chat exchange to update the shared user wiki.
	if err := InjectMira(s.interp, DefaultMiraConfig()); err != nil {
		slog.Warn("failed to inject Mira", "error", err)
	}

	// Prime intro greetings for the meta-agents on first boot of a tenant
	// (refs govega#63). primeAgentIntro is idempotent on chat history, so
	// this is a no-op after the first run — re-injection on every restart
	// won't re-introduce them.
	s.primeAgentIntroAsync(s.cfg.Builder.Name)
	s.primeAgentIntroAsync(s.cfg.Orchestrator.Name)

	// Set up scheduler and restore persisted jobs.
	s.scheduler = NewScheduler(
		s.interp,
		func(job dsl.ScheduledJob) error {
			// scheduler.AddJob fires this for both first-time DSL inserts and
			// re-adds after a routine PATCH. The DSL job carries only the
			// legacy fields, so merge with the existing row to preserve
			// the routine-specific columns (title, schedule_json) that the
			// HTTP layer populated (refs govega#52).
			row := ScheduledJob{
				Name:      job.Name,
				Cron:      job.Cron,
				AgentName: job.AgentName,
				Message:   job.Message,
				Enabled:   job.Enabled,
			}
			if existing, err := s.store.GetScheduledJobByID(job.Name); err == nil && existing != nil {
				row.ID = existing.ID
				row.Title = existing.Title
				row.ScheduleJSON = existing.ScheduleJSON
			}
			return s.store.UpsertScheduledJob(row)
		},
		func(name string) error {
			return s.store.DeleteScheduledJob(name)
		},
	)
	if checker, ok := s.store.(inboxChecker); ok {
		s.scheduler.inbox = checker
	}
	if s.callerResolver != nil {
		s.scheduler.SetCallerResolver(s.callerResolver)
	}
	if rec, ok := s.store.(scheduleRunRecorder); ok {
		s.scheduler.recorder = rec
	}
	// Drop any pre-govega#101 orchestrator-heartbeat rows. The current
	// orchestrator's heartbeat is added below as an InMemoryOnly job, so
	// any persisted "<agent>-heartbeat" row is a leftover from before this
	// fix (typically from an orchestrator rename). Self-applying migration.
	if _, err := pruneStaleOrchestratorHeartbeats(s.store); err != nil {
		slog.Warn("scheduler: prune legacy heartbeats failed", "error", err)
	}
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

	// Register inbox tools — ask_orchestrator (and ask_iris alias) are available to all agents,
	// list_inbox and resolve_inbox are already in Iris's tool list.
	inboxBack := &inboxAdapter{store: s.store}
	dsl.RegisterInboxTools(s.interp, inboxBack)

	// Wire inbox backend so DispatchToAgent can post completion notifications.
	s.interp.SetInboxBackend(inboxBack)

	// Register kanban task tools. All agents get them registered; only the
	// orchestrator is taught (in irisToolNames + system prompt) to actually
	// use them for triage. Workers can claim/comment/update too — their
	// prompt decides whether they should.
	dsl.RegisterTaskTools(s.interp, &taskAdapter{store: s.store})

	// Wire memory injector so agents get their memories + project context during delegated tasks.
	// Wiki memory injection (govega#71) — "default" matches the userID
	// that delegated runs are scoped to when there's no per-user context.
	s.interp.SetMemoryInjector(func(proc *vega.Process, agentName string) {
		memText := formatWikiMemoryForInjection(s.store, "default", agentName)
		projectCtx := buildProjectContext(s.interp.Tools().ActiveProject())
		companyCtx := buildCompanyContext(s.company)
		if extra := buildExtraSystem(memText, projectCtx, companyCtx); extra != "" {
			proc.SetExtraSystem(extra)
		}
	})

	// Scope memory context to delegated agent so each module's remember/recall
	// tools use their own namespace.
	s.interp.SetDelegationCtxDecorator(func(ctx context.Context, agentName string) context.Context {
		return ContextWithMemory(ctx, s.store, "default", agentName)
	})

	// Create channels defined in the YAML document (idempotent — skips existing).
	if doc := s.interp.Document(); doc != nil && doc.Channels != nil {
		for name, chDef := range doc.Channels {
			if ch, _ := s.store.GetChannel(name); ch != nil {
				continue // channel already exists
			}
			id := "ch_" + name
			mode := chDef.Mode
			if err := s.store.CreateChannel(id, name, chDef.Description, "yaml", chDef.Team, mode); err != nil {
				slog.Warn("failed to create YAML channel", "name", name, "error", err)
			} else {
				slog.Info("created channel from YAML", "name", name, "team", chDef.Team)
			}
		}
	}

	// Wire channel backend so DispatchToAgent can post completion summaries.
	s.interp.SetChannelBackend(s.store, channelPostCb)

	// Register a synthetic active stream when an agent is dispatched so the
	// UI shows a busy spinner on the agent's avatar.
	s.interp.SetDispatchStartCallback(func(agentName string) {
		as := &activeStream{
			agentName: agentName,
			done:      make(chan struct{}),
			startedAt: time.Now(),
		}
		s.streamsMu.Lock()
		s.streams[agentName] = as
		s.streamsMu.Unlock()

		// Notify frontends so the agent list refreshes with busy state.
		s.broker.Publish(BrokerEvent{
			Type:      "process.started",
			Agent:     agentName,
			Timestamp: time.Now(),
		})
	})

	// Stream dispatched-agent ChatEvents (tool starts/ends, text deltas)
	// to the broker as "chat.event" events keyed on the agent. Frontends
	// viewing /chat/<agent> use these to render live tool-call activity
	// during dispatched runs (the same way direct chats render their
	// own stream). Without this, the chat goes silent until the final
	// response is persisted at dispatch-complete time.
	s.interp.SetDispatchEventCallback(func(agentName string, ev vega.ChatEvent) {
		s.broker.Publish(BrokerEvent{
			Type:      "chat.event",
			Agent:     agentName,
			Data:      ev,
			Timestamp: time.Now(),
		})
	})

	// When a dispatched agent finishes, immediately poke the orchestrator
	// to triage the inbox instead of waiting for the 15-minute heartbeat.
	// Routes the orchestrator's response back to whichever conversation
	// originated the dispatch (Telegram chat, web user) via the
	// ReplyTarget registered for that agent — see server.replyTargets.
	s.interp.SetDispatchCompleteCallback(func(dispatchCtx context.Context, completedAgent, callerAgent, dispatchMsg, dispatchResp string, dispatchErr error) {
		// Clear the synthetic active stream so the busy spinner stops.
		s.streamsMu.Lock()
		if as, ok := s.streams[completedAgent]; ok {
			select {
			case <-as.done:
			default:
				close(as.done)
			}
			delete(s.streams, completedAgent)
		}
		s.streamsMu.Unlock()

		// Persist the dispatched exchange to the agent's private chat
		// history so the user can watch the work in /chat/<agent>.
		// Format the inbound message as if it came from the caller so
		// it's clear who asked, and the response as the agent's reply.
		// Skip when dispatchMsg is empty (defensive — shouldn't happen).
		if dispatchMsg != "" {
			fromTag := callerAgent
			if fromTag == "" {
				fromTag = "system"
			}
			tagged := "_(from " + fromTag + ")_ " + dispatchMsg
			if err := s.store.InsertChatMessage(completedAgent, "user", tagged, nil); err != nil {
				slog.Warn("failed to persist dispatched user message", "agent", completedAgent, "error", err)
			}
			if dispatchErr != nil {
				_ = s.store.InsertChatMessage(completedAgent, "assistant", "_(dispatch failed: "+dispatchErr.Error()+")_", nil)
			} else if dispatchResp != "" {
				_ = s.store.InsertChatMessage(completedAgent, "assistant", dispatchResp, nil)
			}
			// Notify the frontend so any open /chat/<agent> view refreshes.
			s.broker.Publish(BrokerEvent{
				Type:      "chat.update",
				Agent:     completedAgent,
				Timestamp: time.Now(),
			})
		}

		// Publish onto the domain-event spine so agents can react to another
		// agent finishing a task (agent.completed). Origin is threaded from the
		// dispatch context, so a delegation made *inside* a reactive wake
		// inherits that wake's chain depth (D2) and the loop guard bounds it; a
		// normal delegation carries no Origin and is a root event.
		dispatchStatus, dispatchErrStr := "ok", ""
		if dispatchErr != nil {
			dispatchStatus, dispatchErrStr = "failed", dispatchErr.Error()
		}
		s.bus.Publish(events.Event{
			Type:   "agent.completed",
			Origin: events.OriginFromContext(dispatchCtx),
			Data: map[string]any{
				"agent":  completedAgent,
				"caller": callerAgent,
				"status": dispatchStatus,
				"result": truncate(dispatchResp, 2048),
				"error":  dispatchErrStr,
			},
		})

		orchName := s.cfg.Orchestrator.Name
		// Resolve who to poke: the originating caller if known, else the
		// base orchestrator. Per-user clones are retired — caller names
		// are now always the base agent.
		pokeAgent := callerAgent
		if pokeAgent == "" {
			pokeAgent = orchName
		}

		// Don't loop: when the orchestrator finishes its own task, skip
		// the poke.
		if completedAgent == orchName {
			slog.Debug("skipping orchestrator poke — completing agent is the orchestrator itself", "agent", completedAgent)
			return
		}

		slog.Info("dispatch complete, poking originating conversation", "completed", completedAgent, "caller", pokeAgent)
		go func() {
			// composeDispatchCompletePoke pulls URLs out of the dispatched
			// agent's response and embeds them as a "paste verbatim" block.
			// Without this, the orchestrator's LLM mangles long hex
			// suffixes when relaying the URL (e.g. acme-dashboard-8b24eb
			// → made-up nadia-dashboard-zw5gpf) and hands the user a dead
			// link.
			msg := composeDispatchCompletePoke(completedAgent, dispatchResp)
			// Detach from the dispatch goroutine's cancel/deadline but
			// preserve its values — most importantly the BYOK API key
			// from the original user request, so the orchestrator's
			// poke-LLM call can authenticate. Without this, the poke
			// 401s and the user never sees the "task triaged" update.
			ctx := carryRequestValues(dispatchCtx, context.Background())
			resp, err := s.interp.SendToAgent(ctx, pokeAgent, msg)
			if err != nil {
				slog.Error("failed to poke orchestrator after dispatch", "completed", completedAgent, "caller", pokeAgent, "error", err)
				return
			}

			// Persist the response to the caller's chat. Single agent,
			// single timeline — no clone fan-out needed.
			_ = s.store.InsertChatMessage(pokeAgent, "assistant", resp, nil)

			// Notify connected frontends to refresh the orchestrator's chat.
			s.broker.Publish(BrokerEvent{
				Type:      "chat.update",
				Agent:     pokeAgent,
				Timestamp: time.Now(),
			})

			// Push to the originating channel via its ReplyTarget if
			// registered (e.g. Telegram bot.Send back to the user's chat).
			if target := s.lookupReplyTarget(pokeAgent); target != nil {
				if err := target.Reply(ctx, resp); err != nil {
					slog.Warn("reply target push failed", "agent", pokeAgent, "error", err)
				}
			}
		}()
	})

	// Wire delegation observer so agent-to-agent messages appear in channels.
	s.interp.SetDelegationObserver(func(ctx context.Context, from, to, message, response string) {
		chID, chName, err := s.store.FindChannelForAgents(from, to)
		if err != nil || chID == "" {
			return // no shared channel, skip
		}

		// Insert delegation message as a top-level message from the delegator.
		msgID, err := s.store.InsertChannelMessage(chID, from, "assistant", message, nil, `{"type":"delegation"}`, from, nil)
		if err != nil {
			slog.Error("delegation observer: insert message", "error", err)
			return
		}

		// Insert response as a thread reply from the delegatee.
		var replyID int64
		if response != "" && msgID > 0 {
			replyID, _ = s.store.InsertChannelMessage(chID, to, "assistant", response, &msgID, `{"type":"delegation_response"}`, to, nil)
		}

		// Publish SSE events so connected clients see it in real time.
		cs := s.getOrCreateChannelStream(chName)
		cs.publish(ChannelEvent{
			Type:      "channel.message",
			Channel:   chName,
			MessageID: msgID,
			Agent:     from,
			Role:      "assistant",
			Content:   message,
		})
		if response != "" && msgID > 0 {
			cs.publish(ChannelEvent{
				Type:      "channel.thread_reply",
				Channel:   chName,
				MessageID: replyID,
				ThreadID:  &msgID,
				Agent:     to,
				Role:      "assistant",
				Content:   response,
			})
		}
	})

	// Add the orchestrator heartbeat schedule. InMemoryOnly so it's re-
	// derived from config every boot instead of strand­ing stale rows in
	// scheduled_jobs after an orchestrator rename (govega#101).
	s.scheduler.AddJob(dsl.ScheduledJob{
		Name:         s.cfg.Orchestrator.Name + "-heartbeat",
		Cron:         "*/15 * * * *",
		AgentName:    s.cfg.Orchestrator.Name,
		Message:      "Heartbeat: (1) Check list_inbox for pending agent questions and triage. (2) Check list_unassigned_tasks for the kanban routing queue and assign_task each one to the right agent. Resolve what you can; escalate only if a human decision is required.",
		Enabled:      true,
		InMemoryOnly: true,
	})

	go s.scheduler.Start(ctx)

	// Idle agent process eviction. Composed agents that haven't been
	// messaged in 30 minutes are unloaded; EnsureAgent re-spawns them
	// on demand if the user comes back. Meta-agents (orchestrator,
	// builder) and yaml-defined agents stay resident regardless.
	// Sweeps every 5 minutes — frequent enough to bound resident
	// memory, infrequent enough to be cheap.
	s.interp.StartIdleEviction(ctx, 30*time.Minute, 5*time.Minute)

	// Stash the parent context so runtime restart calls can derive a
	// child cancel-only ctx for each bot's polling loop.
	s.telegramMu.Lock()
	s.telegramCtx = ctx
	s.telegrams = make(map[string]*runningTelegramBot)
	s.telegramMu.Unlock()

	s.discordMu.Lock()
	s.discordCtx = ctx
	s.discords = make(map[string]*runningDiscordBot)
	s.discordMu.Unlock()

	// Start any persisted Telegram bots, plus the env-var-configured bot
	// (if any) for backward compatibility.
	s.startPersistedTelegramBots(ctx)
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		agent := os.Getenv("TELEGRAM_AGENT")
		if agent == "" {
			agent = s.cfg.Orchestrator.Name
		}
		if _, err := s.AddTelegramBot(ctx, token, agent, "env"); err != nil {
			slog.Warn("env-configured telegram bot init failed", "error", err)
		}
	}

	// Same for Discord: persisted bots plus an optional env-var bot.
	s.startPersistedDiscordBots(ctx)
	if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
		agent := os.Getenv("DISCORD_AGENT")
		if agent == "" {
			agent = s.cfg.Orchestrator.Name
		}
		allowed := parseAllowedUsers(os.Getenv("DISCORD_ALLOWED_USERS"))
		if _, err := s.AddDiscordBot(ctx, token, agent, "env", allowed); err != nil {
			slog.Warn("env-configured discord bot init failed", "error", err)
		}
	}

	// Wire orchestrator callbacks to broker + store.
	s.wireCallbacks()

	// Stand up reactive cognition: the event spine's trigger router + the
	// emit_event tool. Harmless when no agent declares triggers.
	s.startReactive(ctx)

	// Build router.
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	// Apply routes registered by the embedding product (RegisterRoute).
	s.routeHooksMu.Lock()
	for _, hook := range s.routeHooks {
		mux.HandleFunc(hook.pattern, hook.handler)
	}
	s.routeHooksMu.Unlock()

	ln, addr, err := resolveAddr(s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Extract the port from the resolved address and build a clean base URL.
	// Prefer s.cfg.PublicURL (set by operators / v39a) so file links the
	// agent generates point at the customer-visible hostname, not the
	// container's localhost.
	_, port, _ := net.SplitHostPort(addr)
	baseURL := strings.TrimRight(s.cfg.PublicURL, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%s", port)
	}
	s.interp.SetServerBaseURL(baseURL)

	authCfg, err := LoadAuthConfig(ctx)
	if err != nil {
		return fmt.Errorf("load auth config: %w", err)
	}
	s.authCfg = authCfg
	s.controlPlaneURL = strings.TrimRight(envcompat.Get("VEGA_CONTROL_PLANE_URL"), "/")

	// Compose the request pipeline: CORS → auth → product middleware → mux.
	// Product middleware runs innermost so any claims injected via
	// WithClaims are immediately visible to handlers' ClaimsFrom calls.
	srv := &http.Server{
		Handler: corsMiddleware(LoadCORSConfig())(authMiddleware(authCfg)(composeMiddleware(s.cfg.Middleware, mux))),
		// Bound the time to read request headers to blunt Slowloris-style
		// slow-header attacks. WriteTimeout is intentionally left unset because
		// SSE responses are long-lived.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in goroutine.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("vega serve started", "addr", addr)
		fmt.Printf("Dashboard: %s\n", baseURL)
		fmt.Printf("API:       %s/api/v1/stats\n", baseURL)
		if err := srv.Serve(ln); err != http.ErrServerClosed {
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

	// Tear down peering (close cached outbound conns + stop the AIRE
	// listener) before the broker so in-flight inbound ops fail fast
	// rather than getting stuck on a closing SSE channel.
	s.stopPeering()

	// Close broker first — this closes all SSE subscriber channels,
	// unblocking their handlers so the HTTP server can drain cleanly.
	s.broker.Close()
	// Tear down the event spine (the reactive router stops via ctx cancel).
	if s.bus != nil {
		s.bus.Close()
	}

	// Graceful shutdown with 5s timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
	if weOwnStore {
		if err := s.store.Close(); err != nil {
			slog.Error("store close error", "error", err)
		}
	}

	return nil
}

// registerRoutes adds all API and frontend routes to the mux.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// REST API
	mux.HandleFunc("GET /auth/gmail/start", s.handleGmailAuthStart)
	mux.HandleFunc("GET /auth/gmail/callback", s.handleGmailAuthCallback)
	mux.HandleFunc("GET /api/v1/company", s.handleGetCompany)
	mux.HandleFunc("GET /api/v1/processes", s.handleListProcesses)
	mux.HandleFunc("GET /api/v1/processes/{id}", s.handleGetProcess)
	mux.HandleFunc("DELETE /api/v1/processes/{id}", s.handleKillProcess)
	mux.HandleFunc("GET /api/v1/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/v1/agents/{name}", s.handleGetAgent)
	mux.HandleFunc("GET /api/v1/workflows", s.handleListWorkflows)
	mux.HandleFunc("POST /api/v1/workflows/{name}/run", s.handleRunWorkflow)
	mux.HandleFunc("GET /api/v1/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("GET /api/v1/mcp/registry", s.handleMCPRegistry)
	mux.HandleFunc("POST /api/v1/mcp/servers", s.handleConnectMCPServer)
	mux.HandleFunc("GET /api/v1/mcp/servers/{name}/config", s.handleGetMCPServerConfig)
	mux.HandleFunc("PUT /api/v1/mcp/servers/{name}", s.handleUpdateMCPServer)
	mux.HandleFunc("POST /api/v1/mcp/servers/{name}/refresh", s.handleRefreshMCPServer)
	mux.HandleFunc("POST /api/v1/mcp/servers/{name}/duplicate", s.handleDuplicateMCPServer)
	mux.HandleFunc("PUT /api/v1/mcp/servers/{name}/disable", s.handleToggleMCPServer)
	mux.HandleFunc("DELETE /api/v1/mcp/servers/{name}", s.handleDisconnectMCPServer)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/spawn-tree", s.handleSpawnTree)

	// Peering (federation over AIRE). All endpoints respond 404 with
	// {"error": "peering not enabled"} when VEGA_PEERING_ADDR is unset,
	// so the React modal naturally hides itself when federation is off.
	mux.HandleFunc("GET /api/v1/peering/status", s.handlePeeringStatus)
	mux.HandleFunc("GET /api/v1/peering/peers", s.handleListPeers)
	mux.HandleFunc("POST /api/v1/peering/peers", s.handleAddPeer)
	mux.HandleFunc("PATCH /api/v1/peering/peers/{nodeID}", s.handleUpdatePeer)
	mux.HandleFunc("DELETE /api/v1/peering/peers/{nodeID}", s.handleDeletePeer)
	mux.HandleFunc("GET /api/v1/peering/grants", s.handleListGrants)
	mux.HandleFunc("PUT /api/v1/peering/grants/{peerID}/{agent}", s.handleUpsertGrant)
	mux.HandleFunc("DELETE /api/v1/peering/grants/{peerID}/{agent}", s.handleDeleteGrant)
	mux.HandleFunc("GET /api/v1/peering/audit", s.handleAuditLog)
	mux.HandleFunc("GET /api/v1/peering/live", s.handleLiveOps)
	mux.HandleFunc("POST /api/v1/peering/invites", s.handleCreateInvite)
	mux.HandleFunc("POST /api/v1/peering/invites/return", s.handleReturnInvite)

	// Population
	mux.HandleFunc("GET /api/v1/population/search", s.handlePopulationSearch)
	mux.HandleFunc("GET /api/v1/population/info/{kind}/{name}", s.handlePopulationInfo)
	mux.HandleFunc("POST /api/v1/population/install", s.handlePopulationInstall)
	mux.HandleFunc("GET /api/v1/population/installed", s.handlePopulationInstalled)

	// Agent composition
	mux.HandleFunc("POST /api/v1/agents", s.handleCreateAgent)
	mux.HandleFunc("PUT /api/v1/agents/{name}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/v1/agents/{name}", s.handleDeleteAgent)
	mux.HandleFunc("GET /api/v1/agents/{name}/template", s.handleExportTemplate)
	mux.HandleFunc("POST /api/v1/agents/import", s.handleImportTemplate)

	// Chat
	mux.HandleFunc("GET /api/v1/agents/{name}/chat", s.handleChatHistory)
	mux.HandleFunc("POST /api/v1/agents/{name}/chat", s.handleChat)
	mux.HandleFunc("POST /api/v1/agents/{name}/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/v1/agents/{name}/chat/stream", s.handleChatStreamReconnect)
	mux.HandleFunc("GET /api/v1/agents/{name}/chat/status", s.handleChatStatus)
	mux.HandleFunc("DELETE /api/v1/agents/{name}/chat", s.handleClearChat)
	mux.HandleFunc("POST /api/v1/agents/{name}/chat/read", s.handleMarkChatRead)
	mux.HandleFunc("GET /api/v1/chat/unread", s.handleChatUnreadCounts)

	// Memory
	mux.HandleFunc("GET /api/v1/agents/{name}/memory", s.handleGetMemory)
	mux.HandleFunc("DELETE /api/v1/agents/{name}/memory", s.handleDeleteMemory)

	// Files
	mux.HandleFunc("GET /api/v1/files", s.handleListFiles)
	mux.HandleFunc("GET /api/v1/files/read", s.handleReadFile)
	mux.HandleFunc("DELETE /api/v1/files", s.handleDeleteFile)
	mux.HandleFunc("GET /api/v1/files/metadata", s.handleListFileMetadata)

	// Schedules
	mux.HandleFunc("GET /api/v1/schedules", s.handleListSchedules)
	mux.HandleFunc("DELETE /api/v1/schedules/{name}", s.handleDeleteSchedule)
	mux.HandleFunc("PUT /api/v1/schedules/{name}", s.handleToggleSchedule)

	// Per-agent Routines API (refs govega#52). Lives alongside the legacy
	// flat /schedules surface — DSL-created jobs still flow through that.
	mux.HandleFunc("GET /api/v1/agents/{agent}/schedules", s.handleListAgentRoutines)
	mux.HandleFunc("POST /api/v1/agents/{agent}/schedules", s.handleCreateAgentRoutine)
	mux.HandleFunc("GET /api/v1/agents/{agent}/schedules/{id}", s.handleGetAgentRoutine)
	mux.HandleFunc("PATCH /api/v1/agents/{agent}/schedules/{id}", s.handleUpdateAgentRoutine)
	mux.HandleFunc("DELETE /api/v1/agents/{agent}/schedules/{id}", s.handleDeleteAgentRoutine)

	// Per-agent Brain (knowledge attachments) — refs govega#43.
	mux.HandleFunc("GET /api/v1/agents/{name}/brain", s.handleListAgentBrain)
	mux.HandleFunc("POST /api/v1/agents/{name}/brain", s.handleUploadAgentBrain)
	mux.HandleFunc("GET /api/v1/agents/{name}/brain/{file_id}", s.handleGetAgentBrainFile)
	mux.HandleFunc("DELETE /api/v1/agents/{name}/brain/{file_id}", s.handleDeleteAgentBrainFile)

	// Per-agent tool toggle — refs govega#44.
	mux.HandleFunc("PATCH /api/v1/agents/{name}/tools/{tool}", s.handleToggleAgentTool)

	// Per-agent spend rollup — refs govega#47.
	mux.HandleFunc("GET /api/v1/agents/{name}/spend", s.handleGetAgentSpend)

	// Per-agent budget cap + enforcement — refs govega#47.
	mux.HandleFunc("GET /api/v1/agents/{name}/budget", s.handleGetAgentBudget)
	mux.HandleFunc("PUT /api/v1/agents/{name}/budget", s.handleUpdateAgentBudget)

	// Searchable activity log — refs govega#33. Queryable view over the
	// events table (the SSE /events stream is live-only).
	mux.HandleFunc("GET /api/v1/activity", s.handleSearchActivity)
	mux.HandleFunc("POST /api/v1/events", s.handleEmitEvent)
	mux.HandleFunc("GET /api/v1/reactive/activity", s.handleReactiveActivity)

	// Inbox
	mux.HandleFunc("GET /api/v1/inbox", s.handleListInbox)
	mux.HandleFunc("DELETE /api/v1/inbox/resolved", s.handleClearResolvedInbox)
	mux.HandleFunc("POST /api/v1/inbox/{id}/resolve", s.handleResolveInboxItem)
	mux.HandleFunc("DELETE /api/v1/inbox/{id}", s.handleDeleteInboxItem)

	// Tasks (kanban-style work tracking — independent of Process lifecycle)
	mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/comments", s.handleAddTaskComment)
	mux.HandleFunc("POST /api/v1/tasks/{id}/processes", s.handleLinkTaskProcess)

	// Settings
	mux.HandleFunc("GET /api/v1/settings", s.handleListSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.handleUpsertSetting)
	mux.HandleFunc("DELETE /api/v1/settings/{key}", s.handleDeleteSetting)

	// Channels
	mux.HandleFunc("GET /api/v1/channels", s.handleListChannels)
	mux.HandleFunc("POST /api/v1/channels", s.handleCreateChannel)
	mux.HandleFunc("GET /api/v1/channels/{name}", s.handleGetChannel)
	mux.HandleFunc("DELETE /api/v1/channels/{name}", s.handleDeleteChannel)
	mux.HandleFunc("PATCH /api/v1/channels/{name}", s.handleUpdateChannel)
	mux.HandleFunc("PUT /api/v1/channels/{name}/team", s.handleUpdateChannelTeam)
	mux.HandleFunc("GET /api/v1/channels/{name}/messages", s.handleListChannelMessages)
	mux.HandleFunc("GET /api/v1/channels/{name}/messages/{id}/thread", s.handleListThreadMessages)
	mux.HandleFunc("POST /api/v1/channels/{name}/messages", s.handleChannelPost)
	mux.HandleFunc("POST /api/v1/channels/{name}/stream", s.handleChannelStream)
	mux.HandleFunc("GET /api/v1/channels/{name}/stream", s.handleChannelStreamReconnect)
	mux.HandleFunc("POST /api/v1/channels/{name}/read", s.handleMarkChannelRead)

	// Wiki memory (govega#71): graph view + page list + per-page get.
	// Per-user, scoped via X-Auth-User. Read-only; agents own the
	// write surface through the memory_* tools.
	mux.HandleFunc("GET /api/v1/memory/graph", s.handleMemoryGraph)
	mux.HandleFunc("GET /api/v1/memory/pages", s.handleListMemoryPages)
	mux.HandleFunc("GET /api/v1/memory/page", s.handleGetMemoryPage)

	// Prompt History (survives reset)
	mux.HandleFunc("GET /api/v1/prompt-history", s.handleListPromptHistory)
	mux.HandleFunc("GET /api/v1/prompt-history/search", s.handleSearchPromptHistory)
	mux.HandleFunc("DELETE /api/v1/prompt-history/{id}", s.handleDeletePromptHistory)

	// Config
	mux.HandleFunc("GET /api/v1/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/v1/config/upload", s.handleConfigUpload)
	mux.HandleFunc("GET /api/v1/identity", s.handleGetIdentity)

	// Unified tenant config — orchestrator identity + branding (refs
	// govega#32 item B). Replaces env-var-only branding so customers
	// can rebrand at runtime without ops involvement.
	mux.HandleFunc("GET /api/v1/tenant/config", s.handleGetTenantConfig)
	mux.HandleFunc("PUT /api/v1/tenant/config", s.handleUpdateTenantConfig)
	mux.HandleFunc("GET /api/v1/integrations/telegram", s.handleTelegramStatus)
	mux.HandleFunc("POST /api/v1/integrations/telegram", s.handleTelegramConfigure)
	mux.HandleFunc("DELETE /api/v1/integrations/telegram/{id}", s.handleTelegramRemove)
	mux.HandleFunc("GET /api/v1/integrations/discord", s.handleDiscordStatus)
	mux.HandleFunc("POST /api/v1/integrations/discord", s.handleDiscordConfigure)
	mux.HandleFunc("DELETE /api/v1/integrations/discord/{id}", s.handleDiscordRemove)
	mux.HandleFunc("GET /api/v1/integrations/gmail", s.handleGmailStatus)
	mux.HandleFunc("POST /api/v1/integrations/gmail", s.handleGmailConfigure)
	mux.HandleFunc("DELETE /api/v1/integrations/gmail", s.handleGmailDisable)
	// Cloud-mode flow (Phase 2E.3): start proxies to control plane,
	// handoff consumes the signed JWT and persists tokens.
	mux.HandleFunc("POST /api/v1/integrations/gmail/start", s.handleGmailIntegrationStart)
	mux.HandleFunc("POST /api/v1/integrations/gmail/handoff", s.handleGmailIntegrationHandoff)
	// Reset
	mux.HandleFunc("POST /api/v1/reset", s.handleReset)

	// SSE
	mux.HandleFunc("GET /api/v1/events", s.handleSSE)

	// Workspace static files — serves raw files from ~/.vega/workspace/ so
	// agents can provide direct URLs (e.g. /workspace/mysite/index.html).
	mux.Handle("/workspace/", http.StripPrefix("/workspace/", http.HandlerFunc(s.handleWorkspaceStatic)))

	// Frontend SPA
	mux.Handle("/", frontendHandler(s.cfg.FrontendFS))
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

		// Note: agent.completed is emitted onto the spine from the dispatch-
		// complete callback (a single, ctx-bearing emission point), not here —
		// persistent chat processes rarely Complete(), and emitting from both
		// would double-fire for ephemeral dispatch processes.

		// Snapshot final state (via the Store interface — works for any backend).
		_ = s.store.InsertProcessSnapshot(snapshotFromResponse(processToResponse(p)))
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

		// (agent.completed with status=failed is emitted from the dispatch-
		// complete callback; see the note in OnProcessComplete.)

		// Snapshot final state (via the Store interface — works for any backend).
		_ = s.store.InsertProcessSnapshot(snapshotFromResponse(processToResponse(p)))
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

// autoConnectHTTPServers connects HTTP/SSE-transport MCP servers from the
// registry whose required env vars are already set (e.g. v39a injects
// COMPOSIO_API_KEY at launch). Without this, hosted-MCP integrations like
// Composio stay disconnected on boot — only built-in Go servers were
// auto-connected previously, and agents had no MCP tools available.
func (s *Server) autoConnectHTTPServers(ctx context.Context) {
	t := s.interp.Tools()
	for _, entry := range mcp.DefaultRegistry {
		if entry.BuiltinGo {
			continue
		}
		if entry.Transport != mcp.TransportHTTP && entry.Transport != mcp.TransportSSE {
			continue
		}
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
		cfg := entry.ToServerConfig(nil)
		n, err := t.ConnectMCPServer(ctx, cfg)
		if err != nil {
			slog.Warn("auto-connect HTTP MCP server failed", "server", entry.Name, "error", err)
			continue
		}
		slog.Info("auto-connected HTTP MCP server", "server", entry.Name, "tools", n)
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
	allSettings := make(map[string]string)
	if settings, err := s.store.ListSettings(); err == nil {
		for _, st := range settings {
			allSettings[st.Key] = st.Value
		}
	}

	for _, sc := range servers {
		// Skip disabled servers.
		if sc.Disabled {
			slog.Info("skipping disabled MCP server", "server", sc.Name)
			continue
		}
		// Skip if already connected (e.g. by autoConnectBuiltinServers).
		if t.MCPServerConnected(sc.Name) || t.BuiltinServerConnected(sc.Name) {
			continue
		}

		var req ConnectMCPRequest
		if err := json.Unmarshal([]byte(sc.ConfigJSON), &req); err != nil {
			slog.Warn("failed to parse persisted MCP server config", "name", sc.Name, "error", err)
			continue
		}

		// Fill in env values from per-server namespaced settings, falling back to bare keys.
		nsPrefix := "mcp:" + sc.Name + ":"
		for fullKey, val := range allSettings {
			if strings.HasPrefix(fullKey, nsPrefix) {
				bareKey := fullKey[len(nsPrefix):]
				req.Env[bareKey] = val
			}
		}
		for k := range req.Env {
			if req.Env[k] != "" {
				continue
			}
			nsKey := mcpSettingKey(sc.Name, k)
			if val, ok := allSettings[nsKey]; ok {
				req.Env[k] = val
			} else if val, ok := allSettings[k]; ok {
				req.Env[k] = val
			}
		}

		// Build full env map for registry/builtin servers (they may need all settings).
		envMap := make(map[string]string)
		for k, v := range req.Env {
			envMap[k] = v
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

// persistYAMLMCPServers ensures YAML-configured MCP servers are persisted to
// SQLite so the Connections page can display and edit them.
func (s *Server) persistYAMLMCPServers() {
	sqlStore, ok := s.store.(*SQLiteStore)
	if !ok {
		return
	}
	doc := s.interp.Document()
	if doc == nil || doc.Settings == nil || doc.Settings.MCP == nil {
		return
	}
	for _, serverDef := range doc.Settings.MCP.Servers {
		if serverDef.Name == "" {
			continue
		}
		// Build a ConnectMCPRequest from the YAML definition.
		req := ConnectMCPRequest{
			Name:      serverDef.Name,
			Transport: serverDef.Transport,
			Command:   serverDef.Command,
			Args:      serverDef.Args,
			URL:       serverDef.URL,
			Headers:   serverDef.Headers,
			Env:       serverDef.Env,
		}
		configJSON, err := json.Marshal(req)
		if err != nil {
			slog.Warn("failed to marshal YAML MCP server config", "server", serverDef.Name, "error", err)
			continue
		}
		if err := sqlStore.UpsertMCPServer(serverDef.Name, string(configJSON)); err != nil {
			slog.Warn("failed to persist YAML MCP server config", "server", serverDef.Name, "error", err)
			continue
		}
		slog.Info("persisted YAML MCP server config", "server", serverDef.Name)
	}
}

// publishProvisioning publishes one agent-provisioning event over the
// broker so SSE subscribers (apex-host-mgmt sidebar — refs govega#56)
// can show a placeholder row that fills in as data arrives. snapshot is
// optional; when present its user-visible fields are copied into the
// event payload. errMsg is set only when phase == "failed".
func (s *Server) publishProvisioning(phase, name string, snapshot *dsl.Agent, errMsg string) {
	data := map[string]any{"phase": phase}
	if snapshot != nil {
		if snapshot.DisplayName != "" {
			data["display_name"] = snapshot.DisplayName
		}
		if snapshot.Title != "" {
			data["title"] = snapshot.Title
		}
		if snapshot.Description != "" {
			data["description"] = snapshot.Description
		}
		if snapshot.Avatar != "" {
			data["avatar"] = snapshot.Avatar
		}
		if snapshot.Icon != "" {
			data["icon"] = snapshot.Icon
		}
		if len(snapshot.AvatarGradient) > 0 {
			data["avatar_gradient"] = snapshot.AvatarGradient
		}
		if snapshot.Model != "" {
			data["model"] = snapshot.Model
		}
		if len(snapshot.Team) > 0 {
			data["team"] = snapshot.Team
		}
	}
	if errMsg != "" {
		data["error"] = errMsg
	}
	s.broker.Publish(BrokerEvent{
		Type:      "agent.provisioning",
		Agent:     name,
		Data:      data,
		Timestamp: time.Now(),
	})
}

// injectHera adds the Hera meta-agent to the interpreter with persistence
// callbacks that keep composed agents in sync with the SQLite store.
func (s *Server) injectHera() {
	cb := &dsl.HeraCallbacks{
		OnProvisioning: func(ev dsl.ProvisioningEvent) {
			errMsg := ""
			if ev.Err != nil {
				errMsg = ev.Err.Error()
			}
			s.publishProvisioning(string(ev.Phase), ev.Name, ev.Snapshot, errMsg)
		},
		OnAgentCreated: func(agent *dsl.Agent) error {
			var skills []string
			if agent.Skills != nil {
				skills = agent.Skills.Directories
			}
			now := time.Now().UTC()
			ca := ComposedAgent{
				Name:           agent.Name,
				DisplayName:    agent.DisplayName,
				Title:          agent.Title,
				Description:    agent.Description,
				Avatar:         agent.Avatar,
				Icon:           agent.Icon,
				AvatarGradient: agent.AvatarGradient,
				Model:          agent.Model,
				System:         agent.System,
				Tools:          agent.Tools,
				Team:           agent.Team,
				Skills:         skills,
				Temperature:    agent.Temperature,
				Triggers:       agent.Triggers,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			// Retry up to 3 times on SQLITE_BUSY.
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				err = s.store.InsertComposedAgent(ca)
				if err == nil {
					break
				}
				slog.Warn("retrying agent persist", "agent", agent.Name, "attempt", attempt+1, "error", err)
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			}
			if err != nil {
				slog.Error("failed to persist composed agent", "agent", agent.Name, "error", err)
				return fmt.Errorf("persist agent: %w", err)
			}
			s.broker.Publish(BrokerEvent{
				Type:      "agent.created",
				Agent:     agent.Name,
				Timestamp: time.Now(),
			})
			// Prime the agent's first chat turn (refs govega#63). Async so
			// Hera's create_agent tool returns immediately; the worker
			// agent's intro lands in its own chat history, independent of
			// Hera's conversation.
			s.primeAgentIntroAsync(agent.Name)
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
		ChannelBackend: s.store,
	}

	if err := dsl.InjectHera(s.interp, s.cfg.Builder, cb, "create_schedule", "update_schedule", "delete_schedule", "list_schedules", "create_channel"); err != nil {
		slog.Warn("failed to inject builder agent", "error", err)
	}
}

// injectIris adds the orchestrator (default: Iris) to the interpreter.
func (s *Server) injectIris() {
	if err := dsl.InjectIris(s.interp, s.cfg.Orchestrator, s.store, irisExtras(s.peeringEnabled())...); err != nil {
		slog.Warn("failed to inject Iris agent", "error", err)
	}
}

// irisExtras returns the extra tool names piped into Iris on injection.
// Cutover (govega#71 follow-up): wiki memory tools replace the legacy
// remember/recall/forget surface so Iris's writes land in memory_pages
// (visible in /memory) instead of memory_items (invisible). Peering tools
// gated on the federation toggle.
func irisExtras(peeringEnabled bool) []string {
	extras := []string{
		"memory_read", "memory_list", "memory_search",
		"memory_write", "memory_append", "memory_edit",
		"list_inbox", "resolve_inbox",
		"list_unassigned_tasks", "list_my_tasks", "assign_task",
		"create_task", "update_task_status", "comment_on_task",
	}
	if peeringEnabled {
		extras = append(extras, peeringToolNames...)
	}
	return extras
}

// buildChannelCallbacks returns the SSE-publish and reactive-fanout
// callbacks used by RegisterChannelTools and SetChannelBackend. Extracted
// from Start so callers can register channel tools before injecting
// meta-agents (refs govega#57). channelPostCb publishes SSE events to the
// per-channel stream. channelReactiveCb is gated by channel mode and
// notifies other team members when the channel opts in via
// mode="reactive" or mode="social".
func (s *Server) buildChannelCallbacks() (dsl.ChannelPostCallback, dsl.ChannelReactiveCallback, dsl.ChannelLifecycleCallback) {
	channelPostCb := func(channelName, agent, content string, msgID int64, threadID *int64) {
		cs := s.getOrCreateChannelStream(channelName)
		if threadID != nil {
			cs.publish(ChannelEvent{
				Type:      "channel.thread_reply",
				Channel:   channelName,
				MessageID: msgID,
				ThreadID:  threadID,
				Agent:     agent,
				Role:      "assistant",
				Content:   content,
			})
		} else {
			cs.publish(ChannelEvent{
				Type:      "channel.message",
				Channel:   channelName,
				MessageID: msgID,
				Agent:     agent,
				Role:      "assistant",
				Content:   content,
			})
		}
	}
	channelReactiveCb := func(channelName string, team []string, poster string, message string, depth int, triggerMsgID int64) {
		ch, err := s.store.GetChannel(channelName)
		if err != nil || ch == nil {
			return
		}
		mode := ch.Mode
		if mode != "reactive" && mode != "social" {
			return // passive channel — log only, no agent fanout
		}
		social := mode == "social"
		go func() {
			first := true
			for _, member := range team {
				if member == poster {
					continue
				}
				if !first {
					time.Sleep(2 * time.Second)
				}
				first = false
				m := member
				go s.notifyChannelTeammate(channelName, m, poster, message, depth, social, triggerMsgID)
			}
		}()
	}
	channelLifecycleCb := func(id, name, description, createdBy string, team []string, mode string) {
		s.broker.Publish(BrokerEvent{
			Type: "channel.created",
			Data: map[string]any{
				"id":          id,
				"name":        name,
				"description": description,
				"created_by":  createdBy,
				"team":        team,
				"mode":        mode,
			},
			Timestamp: time.Now(),
		})
	}
	return channelPostCb, channelReactiveCb, channelLifecycleCb
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

// inboxAdapter bridges serve.Store to dsl.InboxBackend by converting
// between serve.InboxItem and dsl.InboxItem types.
type inboxAdapter struct {
	store Store
}

func (a *inboxAdapter) InsertInboxItem(fromAgent, subject, body, priority string) (int64, error) {
	return a.store.InsertInboxItem(fromAgent, subject, body, priority)
}

func (a *inboxAdapter) InsertResolvedInboxItem(fromAgent, subject, body, resolution string) (int64, error) {
	return a.store.InsertResolvedInboxItem(fromAgent, subject, body, resolution)
}

func (a *inboxAdapter) ListInboxItems(status string, limit int) ([]dsl.InboxItem, error) {
	items, err := a.store.ListInboxItems(status, limit)
	if err != nil {
		return nil, err
	}
	result := make([]dsl.InboxItem, len(items))
	for i, item := range items {
		result[i] = dsl.InboxItem{
			ID:            item.ID,
			FromAgent:     item.FromAgent,
			Subject:       item.Subject,
			Body:          item.Body,
			Priority:      item.Priority,
			Status:        item.Status,
			Resolution:    item.Resolution,
			CreatedAt:     item.CreatedAt,
			ResolvedAt:    item.ResolvedAt,
			TriageCount:   item.TriageCount,
			LastTriagedAt: item.LastTriagedAt,
		}
	}
	return result, nil
}

func (a *inboxAdapter) ResolveInboxItem(id int64, resolution string) error {
	return a.store.ResolveInboxItem(id, resolution)
}

func (a *inboxAdapter) DeleteInboxItem(id int64) error {
	return a.store.DeleteInboxItem(id)
}

func (a *inboxAdapter) TriageInboxItems(ids []int64, threshold int) ([]int64, error) {
	return a.store.TriageInboxItems(ids, threshold)
}

// taskAdapter bridges serve.Store to dsl.TaskBackend by translating
// between serve.Task and dsl.Task. Mirrors inboxAdapter above.
type taskAdapter struct {
	store Store
}

func toDSLTask(t Task) dsl.Task {
	return dsl.Task{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status,
		Priority:    t.Priority,
		Assignee:    t.Assignee,
		Tags:        t.Tags,
		CreatedBy:   t.CreatedBy,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		DueAt:       t.DueAt,
	}
}

func fromDSLTask(t dsl.Task) Task {
	return Task{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status,
		Priority:    t.Priority,
		Assignee:    t.Assignee,
		Tags:        t.Tags,
		CreatedBy:   t.CreatedBy,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		DueAt:       t.DueAt,
	}
}

func (a *taskAdapter) InsertTask(t dsl.Task) error {
	return a.store.InsertTask(fromDSLTask(t))
}

func (a *taskAdapter) GetTask(id string) (*dsl.Task, error) {
	t, err := a.store.GetTask(id)
	if err != nil || t == nil {
		return nil, err
	}
	dt := toDSLTask(*t)
	return &dt, nil
}

func (a *taskAdapter) ListMyTasks(assignee string, status []string, limit int) ([]dsl.Task, error) {
	tasks, err := a.store.ListMyTasks(assignee, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dsl.Task, len(tasks))
	for i, t := range tasks {
		out[i] = toDSLTask(t)
	}
	return out, nil
}

func (a *taskAdapter) ListUnassignedTasks(limit int) ([]dsl.Task, error) {
	tasks, err := a.store.ListUnassignedTasks(limit)
	if err != nil {
		return nil, err
	}
	out := make([]dsl.Task, len(tasks))
	for i, t := range tasks {
		out[i] = toDSLTask(t)
	}
	return out, nil
}

func (a *taskAdapter) UpdateTaskStatus(id, status string) error {
	return a.store.UpdateTaskStatus(id, status)
}

func (a *taskAdapter) AssignTask(id, assignee string) error {
	return a.store.AssignTask(id, assignee)
}

func (a *taskAdapter) ClaimTask(id, assignee string) error {
	return a.store.ClaimTask(id, assignee)
}

func (a *taskAdapter) AddTaskComment(taskID, author, content string) (int64, error) {
	return a.store.AddTaskComment(taskID, author, content)
}

func (a *taskAdapter) LinkTaskProcess(taskID, processID string) error {
	return a.store.LinkTaskProcess(taskID, processID)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
