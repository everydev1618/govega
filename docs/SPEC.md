# Vega Specification

**Version:** 0.1.0
**Status:** Draft

Vega is a Go library for building fault-tolerant AI agent systems with Erlang-style supervision.

## Philosophy

1. **Supervision first** — Agents fail. Vega makes recovery automatic.
2. **Composition over configuration** — Build complex systems from simple primitives.
3. **Dynamic by default** — Team, tools, and knowledge change without recompilation.
4. **Leverage the ecosystem** — Thin layer over LangChainGo/standard library, not a replacement.

## Core Abstractions

### Agent

An Agent is a definition, not a running process. It describes *what* an agent is.

```go
type Agent struct {
    Name    string
    Model   string
    System  SystemPrompt
    Tools   Tools
    Memory  Memory           // Optional
    Context ContextManager   // Optional

    // Resilience (all optional)
    Budget         *Budget         // Cost limits
    Retry          *RetryPolicy    // Transient failure handling
    RateLimit      *RateLimit      // Request throttling
    CircuitBreaker *CircuitBreaker // Failure isolation

    // Backend (optional, uses default if not set)
    LLM LLM
}

// SystemPrompt can be static or dynamic
type SystemPrompt interface {
    Prompt() string
}

// Static prompt
type StaticPrompt string
func (s StaticPrompt) Prompt() string { return string(s) }

// Dynamic prompt (refreshes each turn)
type DynamicPrompt func() string
func (d DynamicPrompt) Prompt() string { return d() }
```

### Process

A Process is a running Agent. It has state, can receive messages, and can fail.

```go
type Process struct {
    ID          string
    Agent       *Agent
    Status      Status
    Task        string           // What this process is working on
    WorkDir     string           // Isolated workspace
    StartedAt   time.Time
    Supervision *Supervision

    // Metrics
    Iterations   int
    InputTokens  int
    OutputTokens int

    // Internal
    cancel context.CancelFunc
    mu     sync.Mutex
}

type Status string
const (
    StatusPending   Status = "pending"
    StatusRunning   Status = "running"
    StatusCompleted Status = "completed"
    StatusFailed    Status = "failed"
    StatusTimeout   Status = "timeout"
)
```

### Supervision

Supervision defines how a Process recovers from failure. Inspired by Erlang/OTP.

```go
type Supervision struct {
    Strategy    Strategy
    MaxRestarts int           // -1 for unlimited
    Window      time.Duration // Time window for MaxRestarts
    Backoff     BackoffStrategy

    // Callbacks
    OnFailure func(p *Process, err error)
    OnRestart func(p *Process, attempt int)
    OnGiveUp  func(p *Process, err error)
}

type Strategy int
const (
    // Restart: Restart the failed process
    Restart Strategy = iota

    // Stop: Let it stay dead
    Stop

    // Escalate: Propagate failure to parent supervisor
    Escalate
)

type BackoffStrategy struct {
    Initial    time.Duration // First retry delay
    Max        time.Duration // Maximum delay
    Multiplier float64       // Exponential factor
}
```

**Restart Logic:**

```
Process fails
    ↓
Check restart count within Window
    ↓
If count < MaxRestarts:
    Apply Backoff delay
    Call OnRestart
    Restart process
Else:
    Call OnGiveUp
    Apply Strategy (Stop or Escalate)
```

### Orchestrator

The Orchestrator manages multiple Processes. It handles spawning, health checks, and persistence.

```go
type Orchestrator struct {
    processes map[string]*Process
    mu        sync.RWMutex

    // Configuration
    maxProcesses int
    healthCheck  time.Duration
    persistence  Persistence

    // Health monitoring
    healthMonitor *HealthMonitor
}

type OrchestratorOption func(*Orchestrator)

func NewOrchestrator(opts ...OrchestratorOption) *Orchestrator

// Options
func WithMaxProcesses(n int) OrchestratorOption
func WithHealthCheck(interval time.Duration) OrchestratorOption
func WithPersistence(p Persistence) OrchestratorOption
func WithRecovery(enabled bool) OrchestratorOption
func WithRateLimits(limits RateLimits) OrchestratorOption
func WithTracing(config TracingConfig) OrchestratorOption
func WithMetrics(config MetricsConfig) OrchestratorOption
```

## Tools

### Tool Interface

Tools are functions that agents can call. Vega auto-generates JSON schemas from function signatures.

```go
// Tools is a collection of named tool functions
type Tools map[string]any

// Tool functions can have various signatures:
// func() string
// func() (string, error)
// func(param string) string
// func(param1 string, param2 int) (string, error)
// func(params MyStruct) (string, error)
```

**Schema Generation:**

```go
tools := vega.Tools{
    "read_file": func(path string) (string, error) {
        return os.ReadFile(path)
    },
}

// Vega generates:
// {
//   "name": "read_file",
//   "description": "read_file(path string) (string, error)",
//   "input_schema": {
//     "type": "object",
//     "properties": {
//       "path": {"type": "string"}
//     },
//     "required": ["path"]
//   }
// }
```

**Explicit Schema:**

For better descriptions, use `ToolDef`:

```go
tools := vega.Tools{
    "read_file": vega.ToolDef{
        Description: "Read contents of a file from the filesystem",
        Fn: readFile,
        Params: vega.Params{
            "path": {Type: "string", Description: "Absolute or relative file path", Required: true},
        },
    },
}
```

### Dynamic Tools

Tools can be loaded from YAML definitions at runtime. No recompilation needed.

```yaml
# tools/web_search.yaml
name: web_search
description: Search the web using Brave Search API
params:
  - name: query
    type: string
    description: The search query
    required: true
  - name: count
    type: integer
    description: Number of results (default 10)
    required: false
implementation:
  type: http
  method: GET
  url: https://api.search.brave.com/res/v1/web/search
  headers:
    X-Subscription-Token: ${BRAVE_API_KEY}
  query:
    q: "{{query}}"
    count: "{{count | default:10}}"
```

**Implementation Types:**

| Type | Description |
|------|-------------|
| `http` | Make HTTP request |
| `exec` | Execute shell command |
| `file_read` | Read file (sandboxed) |
| `file_write` | Write file (sandboxed) |
| `builtin` | Reference a compiled Go function |

```go
// Load dynamic tools
tools := vega.NewTools()
tools.LoadDirectory("tools/")
```

### Tool Sandboxing

File operations are sandboxed to prevent escaping designated directories.

```go
tools := vega.NewTools(
    vega.WithSandbox("/app/workspace"),  // All file ops restricted here
)

// Attempts to read /etc/passwd will fail
// Attempts to read ../../../etc/passwd will fail
// Only paths within /app/workspace are allowed
```

## Memory

Memory provides persistent storage for agent knowledge.

```go
type Memory interface {
    // Store saves a value with a key
    Store(ctx context.Context, key string, value any, metadata map[string]any) error

    // Retrieve performs semantic search and returns top-k results
    Retrieve(ctx context.Context, query string, k int) ([]MemoryItem, error)

    // Get retrieves a specific item by key
    Get(ctx context.Context, key string) (MemoryItem, error)

    // Delete removes an item
    Delete(ctx context.Context, key string) error

    // List returns all items (paginated)
    List(ctx context.Context, opts ListOptions) ([]MemoryItem, error)
}

type MemoryItem struct {
    Key       string
    Value     any
    Metadata  map[string]any
    CreatedAt time.Time
    UpdatedAt time.Time
    Score     float64  // Relevance score (for Retrieve)
}
```

**Implementations:**

```go
// In-memory (for development/testing)
mem := vega.NewInMemoryMemory()

// File-based with rolling window
mem := vega.NewFileMemory("memory.md", 7*24*time.Hour)

// PostgreSQL with pgvector
mem := vega.NewPgVectorMemory(db, "agent_memory")

// SQLite with vec extension
mem := vega.NewSQLiteMemory("memory.db")

// Composite (short-term + long-term)
mem := vega.NewCompositeMemory(shortTerm, longTerm)
```

## Context Management

ContextManager handles conversation history and token budgets.

```go
type ContextManager interface {
    // Add appends a message to the context
    Add(msg Message)

    // Messages returns messages that fit within maxTokens
    Messages(maxTokens int) []Message

    // Clear resets the context
    Clear()

    // TokenCount returns current token usage
    TokenCount() int
}

type Message struct {
    Role    string // "user", "assistant", "system"
    Content string
}
```

**Implementations:**

```go
// Sliding window - keeps most recent messages
ctx := vega.NewSlidingWindow(100000)  // 100k token limit

// Summarizing - compresses old messages
ctx := vega.NewSummarizingContext(100000, vega.ClaudeHaiku)

// Hybrid - recent messages verbatim, older summarized
ctx := vega.NewHybridContext(
    vega.WithRecentWindow(20000),   // Last 20k tokens verbatim
    vega.WithSummaryBudget(10000),  // 10k for summaries
    vega.WithSummarizer(vega.ClaudeHaiku),
)
```

## Budgets & Cost Control

Budgets enforce spending limits at the agent and session level.

### Agent Budget

```go
agent := vega.Agent{
    Name:  "Coder",
    Model: "claude-sonnet-4-20250514",
    Budget: &vega.Budget{
        Limit:    0.50,           // $0.50 per task
        OnExceed: vega.Block,     // Block, Warn, or Allow
    },
}
```

### Session Budget (Context-Based)

```go
// Create budget context
ctx := vega.WithBudget(ctx, 10.00)  // $10 budget

// All calls within this context share the budget
response, err := proc.Send(ctx, "expensive task")
if errors.Is(err, vega.ErrBudgetExceeded) {
    // Handle budget exhaustion
}

// Query budget status
remaining := vega.BudgetRemaining(ctx)  // $7.50
used := vega.BudgetUsed(ctx)            // $2.50
```

### Budget Inheritance

Nested budgets create sub-allocations:

```go
ctx := vega.WithBudget(ctx, 10.00)

// Child context gets $5 of the parent's $10
childCtx := vega.WithBudget(ctx, 5.00)

// If child exceeds $5, it fails
// Parent still has remaining $5 for other work
```

### Cost Tracking

Every LLM call tracks costs automatically:

```go
metrics := proc.Metrics()
fmt.Printf("Input tokens:  %d\n", metrics.InputTokens)
fmt.Printf("Output tokens: %d\n", metrics.OutputTokens)
fmt.Printf("Total cost:    $%.4f\n", metrics.CostUSD)
```

---

## Retry Policies

Retry policies handle transient failures within a single operation. This is separate from supervision (which handles process crashes).

```go
agent := vega.Agent{
    Name:  "APIClient",
    Model: "claude-sonnet-4-20250514",
    Retry: &vega.RetryPolicy{
        MaxAttempts: 3,
        Backoff: vega.ExponentialBackoff{
            Initial:    100 * time.Millisecond,
            Multiplier: 2.0,
            Max:        5 * time.Second,
            Jitter:     0.1,  // ±10% randomness
        },
        RetryOn: []vega.ErrorClass{
            vega.ErrRateLimit,
            vega.ErrOverloaded,
            vega.ErrTimeout,
            vega.ErrTemporary,
        },
    },
}
```

### Error Classification

| Error Class | Description | Retryable |
|-------------|-------------|-----------|
| `ErrRateLimit` | API rate limit hit | Yes |
| `ErrOverloaded` | Service overloaded | Yes |
| `ErrTimeout` | Request timed out | Yes |
| `ErrTemporary` | Temporary failure | Yes |
| `ErrInvalidRequest` | Bad request | No |
| `ErrAuthentication` | Auth failure | No |
| `ErrBudgetExceeded` | Over budget | No |

### Retry vs Supervision

| Concern | Retry | Supervision |
|---------|-------|-------------|
| Scope | Single LLM call | Entire process |
| Trigger | Transient API errors | Process crash/failure |
| Strategy | Backoff + retry | Restart process |
| State | Preserved | Reset |

---

## Rate Limiting

Rate limiting prevents overwhelming LLM APIs.

```go
orch := vega.NewOrchestrator(
    vega.WithRateLimits(vega.RateLimits{
        "claude-sonnet-4-20250514": {
            RequestsPerMinute: 60,
            TokensPerMinute:   100000,
            Strategy:          vega.Queue,  // Queue, Reject, or Backpressure
        },
        "claude-opus-4-20250514": {
            RequestsPerMinute: 30,
            TokensPerMinute:   50000,
            Strategy:          vega.Queue,
        },
    }),
)
```

### Rate Limit Strategies

| Strategy | Behavior |
|----------|----------|
| `Queue` | Queue requests until capacity available |
| `Reject` | Immediately reject with `ErrRateLimited` |
| `Backpressure` | Slow down callers (blocking) |

### Per-Agent Rate Limits

```go
agent := vega.Agent{
    RateLimit: &vega.RateLimit{
        RequestsPerMinute: 10,  // This agent: max 10 req/min
    },
}
```

---

## Circuit Breaker

Circuit breakers prevent cascading failures when a service is unhealthy.

```go
agent := vega.Agent{
    Name: "ExternalService",
    CircuitBreaker: &vega.CircuitBreaker{
        Threshold:   5,               // Open after 5 failures
        ResetAfter:  30 * time.Second, // Try again after 30s
        HalfOpenMax: 2,               // Allow 2 requests in half-open

        OnOpen: func() {
            log.Println("Circuit opened - service unhealthy")
        },
        OnClose: func() {
            log.Println("Circuit closed - service recovered")
        },
    },
}
```

### Circuit States

```
     ┌─────────┐
     │ Closed  │◄────────────────────┐
     └────┬────┘                     │
          │ failure threshold        │ success in half-open
          ▼                          │
     ┌─────────┐                     │
     │  Open   │                     │
     └────┬────┘                     │
          │ reset timeout            │
          ▼                          │
     ┌───────────┐                   │
     │ Half-Open ├───────────────────┘
     └───────────┘
          │ failure
          ▼
     ┌─────────┐
     │  Open   │
     └─────────┘
```

---

## Streaming

Streaming enables real-time response processing.

### Stream Response

```go
stream, err := proc.SendStream(ctx, "Write a long story")
if err != nil {
    return err
}

for chunk := range stream.Chunks() {
    fmt.Print(chunk)  // Print as chunks arrive
}

if err := stream.Err(); err != nil {
    return err
}

// Get final complete response
fullResponse := stream.Response()
```

### Callback Style

```go
err := proc.SendStreamCallback(ctx, "Write a long story",
    func(chunk string) {
        fmt.Print(chunk)
    },
    func(err error) {
        if err != nil {
            log.Printf("Stream error: %v", err)
        }
    },
)
```

### Stream Cancellation

```go
ctx, cancel := context.WithCancel(ctx)
stream, _ := proc.SendStream(ctx, "Write a very long story")

go func() {
    time.Sleep(5 * time.Second)
    cancel()  // Cancel after 5 seconds
}()

for chunk := range stream.Chunks() {
    fmt.Print(chunk)
}
// Stream stops when context is cancelled
```

---

## Observability & Tracing

Vega provides built-in observability via OpenTelemetry.

### Enable Tracing

```go
orch := vega.NewOrchestrator(
    vega.WithTracing(vega.TracingConfig{
        ServiceName: "my-agent-system",
        Exporter:    vega.OTLPExporter("localhost:4317"),
        SampleRate:  1.0,  // Sample all requests
    }),
)
```

### Use Existing Tracer

```go
// If you already have OpenTelemetry set up
orch := vega.NewOrchestrator(
    vega.WithTracerProvider(otel.GetTracerProvider()),
)
```

### Automatic Span Attributes

Every agent call creates a span with:

```json
{
  "vega.agent.name": "Coder",
  "vega.agent.model": "claude-sonnet-4-20250514",
  "vega.tokens.input": 150,
  "vega.tokens.output": 892,
  "vega.cost.usd": 0.0043,
  "vega.retries": 0,
  "vega.tools.called": ["read_file", "write_file"]
}
```

### Manual Spans

```go
ctx, span := vega.StartSpan(ctx, "process-batch")
defer span.End()

span.SetAttribute("batch.size", len(items))

for _, item := range items {
    result, err := proc.Send(ctx, item)  // Child spans created automatically
    if err != nil {
        span.RecordError(err)
    }
}
```

### Export Options

```go
// OTLP (OpenTelemetry Protocol)
vega.OTLPExporter("localhost:4317")

// Jaeger
vega.JaegerExporter("http://localhost:14268/api/traces")

// JSON file (for local debugging)
vega.JSONFileExporter("traces.jsonl")

// SQLite (for local queries)
vega.SQLiteExporter("traces.db")
```

### Metrics

Vega exports Prometheus-compatible metrics:

```go
orch := vega.NewOrchestrator(
    vega.WithMetrics(vega.MetricsConfig{
        Endpoint: "/metrics",
        Port:     9090,
    }),
)
```

Available metrics:
- `vega_agent_calls_total` - Total agent calls by agent/model/status
- `vega_agent_latency_seconds` - Call latency histogram
- `vega_tokens_total` - Tokens used by direction (input/output)
- `vega_cost_usd_total` - Total cost by agent
- `vega_process_restarts_total` - Process restarts by agent
- `vega_circuit_breaker_state` - Circuit breaker state gauge

---

## Batch Execution

Execute multiple independent calls in parallel.

```go
// Execute multiple calls in parallel
results, errs := vega.Batch(ctx,
    func() (string, error) { return proc1.Send(ctx, "task1") },
    func() (string, error) { return proc2.Send(ctx, "task2") },
    func() (string, error) { return proc3.Send(ctx, "task3") },
)

// All three run concurrently
for i, result := range results {
    if errs[i] != nil {
        log.Printf("Task %d failed: %v", i, errs[i])
    } else {
        log.Printf("Task %d: %s", i, result)
    }
}
```

### Batch with Same Agent

```go
// Same agent, multiple inputs
inputs := []string{"task1", "task2", "task3", "task4"}
results, errs := proc.BatchSend(ctx, inputs)
```

### Batch with Concurrency Limit

```go
results, errs := vega.BatchWithLimit(ctx, 2,  // Max 2 concurrent
    func() (string, error) { return proc.Send(ctx, "task1") },
    func() (string, error) { return proc.Send(ctx, "task2") },
    func() (string, error) { return proc.Send(ctx, "task3") },
    func() (string, error) { return proc.Send(ctx, "task4") },
)
```

---

## Health Monitoring

The Orchestrator includes health monitoring for running processes.

```go
type HealthConfig struct {
    CheckInterval        time.Duration
    StaleProgressMinutes int      // Warn if no progress
    MaxIterationsWarning int      // Warn if iterations exceed
    ErrorLoopCount       int      // Consecutive errors before alert
    CostAlertUSD         float64  // Alert on cost threshold
}

type HealthMonitor struct {
    config    HealthConfig
    alerts    chan Alert
}

type Alert struct {
    ProcessID string
    Type      AlertType
    Message   string
    Timestamp time.Time
}

type AlertType string
const (
    AlertStaleProgress AlertType = "stale_progress"
    AlertHighCost      AlertType = "high_cost"
    AlertErrorLoop     AlertType = "error_loop"
    AlertTimeout       AlertType = "timeout"
)
```

## Persistence

Processes can be persisted and recovered across restarts.

```go
type Persistence interface {
    Save(processes []*Process) error
    Load() ([]*Process, error)
}

// JSON file persistence
persist := vega.NewJSONPersistence("processes.json")

// With orchestrator
orch := vega.NewOrchestrator(
    vega.WithPersistence(persist),
    vega.WithRecovery(true),  // Restore on startup
)
```

**Recovery Behavior:**

On startup with `WithRecovery(true)`:
1. Load persisted process state
2. For each process with `StatusRunning`:
   - Re-spawn with same Agent configuration
   - Resume from last checkpoint if available
   - Apply supervision rules

## API Reference

### Orchestrator

```go
// Create orchestrator
orch := vega.NewOrchestrator(opts...)

// Spawn a process
proc, err := orch.Spawn(agent, spawnOpts...)

// List all processes
procs := orch.List()

// Get specific process
proc := orch.Get(id)

// Kill a process
err := orch.Kill(id)

// Shutdown all processes
err := orch.Shutdown(ctx)
```

### Spawn Options

```go
vega.WithTask(task string)              // Set the task description
vega.WithWorkDir(path string)           // Set working directory
vega.WithSupervision(s Supervision)     // Set supervision config
vega.WithTimeout(d time.Duration)       // Set execution timeout
vega.WithMaxIterations(n int)           // Set iteration limit
vega.WithContext(ctx context.Context)   // Set parent context
```

### Process

```go
// Send message and wait for response
response, err := proc.Send(ctx, "message")

// Send message asynchronously
future := proc.SendAsync("message")
response, err := future.Await(ctx)

// Get current status
status := proc.Status()

// Get metrics
metrics := proc.Metrics()

// Stop the process
proc.Stop()
```

### Future

```go
type Future struct {
    // ...
}

// Wait for completion
result, err := future.Await(ctx)

// Check if done without blocking
if future.Done() {
    result, err := future.Result()
}

// Cancel the operation
future.Cancel()
```

## Orchestration Patterns

Vega provides common orchestration patterns as composable functions.

### Pipeline

Sequential execution where output of one agent feeds into the next.

```go
pipeline := vega.Pipeline(writer, factChecker, editor)
result, err := pipeline.Run(ctx, "Write about AI agents")

// Equivalent to:
// r1 := writer.Send("Write about AI agents")
// r2 := factChecker.Send(r1)
// r3 := editor.Send(r2)
```

### Parallel

Concurrent execution of the same input to multiple agents.

```go
parallel := vega.Parallel(optimist, pessimist, realist)
results, err := parallel.Run(ctx, "Evaluate this business idea")

// Returns []string with all three responses
```

### FanOut

Parallel execution with custom aggregation.

```go
fanout := vega.FanOut(
    vega.All(researcher1, researcher2, researcher3),
    vega.Aggregate(func(results []string) string {
        return summarize(results)
    }),
)
summary, err := fanout.Run(ctx, "Research quantum computing")
```

### Router

Route messages to different agents based on content.

```go
router := vega.Router(
    vega.Route("code", coder),
    vega.Route("test", tester),
    vega.Route("deploy", devops),
    vega.Default(generalist),
)
result, err := router.Run(ctx, "Write unit tests for auth module")
// Routes to tester based on "test" keyword
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ANTHROPIC_API_KEY` | Claude API key | required |
| `VEGA_MODEL` | Default model | `claude-sonnet-4-20250514` |
| `VEGA_MAX_TOKENS` | Default max tokens | `100000` |
| `VEGA_TIMEOUT` | Default process timeout | `10m` |

### Config File

```yaml
# vega.yaml
defaults:
  model: claude-sonnet-4-20250514
  max_tokens: 100000
  timeout: 10m

orchestrator:
  max_processes: 10
  health_check: 30s
  persistence: processes.json

supervision:
  strategy: restart
  max_restarts: 3
  window: 10m
  backoff:
    initial: 1s
    max: 30s
    multiplier: 2.0
```

```go
cfg, _ := vega.LoadConfig("vega.yaml")
orch := vega.NewOrchestrator(vega.WithConfig(cfg))
```

## LLM Backend

Vega uses an LLM interface that can be backed by various providers.

```go
type LLM interface {
    Generate(ctx context.Context, messages []Message, tools []Tool) (*Response, error)
    GenerateStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamEvent, error)
}

// Using LangChainGo
llm := vega.NewLangChainLLM(anthropic.New())

// Using Anthropic SDK directly
llm := vega.NewAnthropicLLM(os.Getenv("ANTHROPIC_API_KEY"))

// Set on agent
agent := vega.Agent{
    LLM: llm,
    // ...
}
```

## Example: Complete Application

```go
package main

import (
    "github.com/yourname/vega"
)

func main() {
    // Create orchestrator
    orch := vega.NewOrchestrator(
        vega.WithMaxProcesses(5),
        vega.WithPersistence(vega.NewJSONPersistence("processes.json")),
        vega.WithRecovery(true),
    )

    // Load dynamic tools
    tools := vega.NewTools(vega.WithSandbox("workspace/"))
    tools.LoadDirectory("tools/")
    tools.Register("spawn_agent", orch.Spawn)

    // Define supervisor agent
    supervisor := vega.Agent{
        Name:  "Supervisor",
        Model: "claude-sonnet-4-20250514",
        System: vega.StaticPrompt("You coordinate a team of specialists."),
        Tools: tools,
        Memory: vega.NewFileMemory("memory.md", 7*24*time.Hour),
        Context: vega.NewSlidingWindow(100000),
    }

    // Spawn supervisor (always restart)
    proc, _ := orch.Spawn(supervisor,
        vega.WithSupervision(vega.Supervision{
            Strategy:    vega.Restart,
            MaxRestarts: -1,
        }),
    )

    // Interactive loop
    for {
        fmt.Print("> ")
        input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
        response, _ := proc.Send(context.Background(), input)
        fmt.Println(response)
    }
}
```

## Versioning

Vega follows semantic versioning:

- **0.x.x** — API may change between minor versions
- **1.x.x** — Stable API, backwards compatible within major version

## License

MIT
