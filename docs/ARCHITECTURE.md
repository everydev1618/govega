# Vega Architecture

## Overview

Vega is a supervision layer for AI agents. It doesn't replace LLM libraries—it adds fault tolerance on top of them.

```
┌─────────────────────────────────────────────────────────────┐
│                      Your Application                        │
├─────────────────────────────────────────────────────────────┤
│                          Vega                                │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ Orchestrator │  │ Supervision │  │ Health Monitoring   │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │   Process   │  │    Tools    │  │ Memory / Context    │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                       LLM Backend                            │
│         (LangChainGo / Anthropic SDK / OpenAI SDK)          │
└─────────────────────────────────────────────────────────────┘
```

## Package Structure

```
vega/
├── agent.go          # Agent definition
├── process.go        # Running process lifecycle
├── orchestrator.go   # Process management
├── supervision.go    # Restart strategies, backoff
├── health.go         # Health monitoring
├── tools.go          # Tool registration and execution
├── tools_dynamic.go  # YAML tool loading
├── memory.go         # Memory interface
├── context.go        # Context management
├── patterns.go       # Pipeline, Parallel, FanOut, Router
├── persistence.go    # Process state persistence
├── config.go         # Configuration loading
├── llm.go            # LLM backend interface
│
├── llm/              # LLM backend implementations
│   ├── anthropic.go  # Direct Anthropic SDK
│   └── langchain.go  # LangChainGo adapter
│
├── memory/           # Memory implementations
│   ├── inmemory.go
│   ├── file.go
│   ├── pgvector.go
│   └── sqlite.go
│
├── context/          # Context implementations
│   ├── sliding.go
│   ├── summarizing.go
│   └── hybrid.go
│
└── middleware/       # HTTP/framework integrations
    ├── gin.go
    └── http.go
```

## Core Components

### Agent

An Agent is a blueprint. It's immutable and can be used to spawn multiple Processes.

```go
type Agent struct {
    Name    string           // Human-readable identifier
    Model   string           // LLM model ID
    System  SystemPrompt     // System prompt (static or dynamic)
    Tools   *Tools           // Available tools
    Memory  Memory           // Optional: persistent memory
    Context ContextManager   // Optional: conversation context
    LLM     LLM              // Optional: custom LLM backend
}
```

**Design decisions:**
- `System` is an interface to support both static strings and dynamic functions
- `Tools` is a pointer so multiple agents can share tool sets
- `LLM` defaults to Anthropic if not specified

### Process

A Process is a running Agent with state and lifecycle.

```go
type Process struct {
    ID          string
    Agent       *Agent
    Status      Status
    Task        string
    WorkDir     string
    StartedAt   time.Time
    Supervision *Supervision

    // Internal state
    ctx         context.Context
    cancel      context.CancelFunc
    messages    []Message
    iteration   int
    mu          sync.Mutex

    // Metrics
    inputTokens  int
    outputTokens int
}
```

**Lifecycle:**

```
    Spawn()
       │
       ▼
   ┌────────┐
   │Pending │
   └────┬───┘
        │ start()
        ▼
   ┌────────┐     fail      ┌────────┐
   │Running │──────────────▶│ Failed │
   └────┬───┘               └────┬───┘
        │                        │
        │ complete               │ restart (if supervised)
        ▼                        │
   ┌──────────┐                  │
   │Completed │◀─────────────────┘
   └──────────┘
```

### Orchestrator

The Orchestrator manages the process registry and provides the public API.

```go
type Orchestrator struct {
    processes   map[string]*Process
    mu          sync.RWMutex

    maxProcesses int
    persistence  Persistence
    healthConfig HealthConfig
    healthMon    *HealthMonitor

    llm         LLM  // Default LLM for all agents
}
```

**Responsibilities:**
1. Spawn and track processes
2. Enforce max process limits
3. Persist and recover state
4. Health monitoring
5. Shutdown coordination

### Supervision

Supervision is configured per-process and handles failure recovery.

```go
type Supervision struct {
    Strategy    Strategy
    MaxRestarts int
    Window      time.Duration
    Backoff     BackoffStrategy

    // Runtime state
    restartCount int
    failures     []time.Time
}
```

**Restart decision flow:**

```go
func (s *Supervision) shouldRestart() bool {
    if s.Strategy == Stop {
        return false
    }

    // Count failures within window
    s.pruneOldFailures()

    if s.MaxRestarts < 0 {
        return true // Unlimited
    }

    return len(s.failures) < s.MaxRestarts
}
```

### Tools

The tool system has two layers: compiled (Go functions) and dynamic (YAML).

```
┌─────────────────────────────────────┐
│              Tools                   │
├─────────────────────────────────────┤
│  ┌─────────────┐  ┌──────────────┐  │
│  │  Compiled   │  │   Dynamic    │  │
│  │ (Go funcs)  │  │   (YAML)     │  │
│  └─────────────┘  └──────────────┘  │
├─────────────────────────────────────┤
│            Middleware               │
│  (logging, rate limiting, timeout)  │
├─────────────────────────────────────┤
│            Sandbox                  │
│    (file operation restrictions)    │
└─────────────────────────────────────┘
```

**Tool execution:**

```go
func (t *Tools) Execute(ctx context.Context, name string, params map[string]any) (string, error) {
    // 1. Find tool
    tool, ok := t.tools[name]
    if !ok {
        return "", ErrToolNotFound
    }

    // 2. Apply middleware chain
    exec := tool.Fn
    for _, mw := range t.middleware {
        exec = mw(exec)
    }

    // 3. Execute with sandbox
    if t.sandbox != nil {
        params = t.sandbox.Rewrite(params)
    }

    return exec(ctx, params)
}
```

## Data Flow

### Message Flow

```
User Input
    │
    ▼
Process.Send()
    │
    ├─▶ Add to context
    │
    ├─▶ Build messages (system + context + input)
    │
    ├─▶ LLM.Generate(messages, tools)
    │         │
    │         ▼
    │   ┌─────────────────┐
    │   │  LLM Response   │
    │   │  - text         │
    │   │  - tool_calls   │
    │   └────────┬────────┘
    │            │
    │   ┌────────┴────────┐
    │   │                 │
    │   ▼                 ▼
    │ Text            Tool Calls
    │   │                 │
    │   │     ┌───────────┴───────────┐
    │   │     │                       │
    │   │     ▼                       ▼
    │   │  Execute tool 1         Execute tool 2
    │   │     │                       │
    │   │     └───────────┬───────────┘
    │   │                 │
    │   │                 ▼
    │   │         Tool results
    │   │                 │
    │   │                 ▼
    │   │     LLM.Generate(messages + results)
    │   │                 │
    │   │    (loop until no tool calls)
    │   │                 │
    │   └────────┬────────┘
    │            │
    │            ▼
    └──▶ Final response
```

### Supervision Flow

```
Process Running
       │
       │ Error occurs
       ▼
┌──────────────────┐
│ Supervision.     │
│ HandleFailure()  │
└────────┬─────────┘
         │
         ├─▶ Call OnFailure callback
         │
         ├─▶ Record failure time
         │
         ├─▶ Check restart eligibility
         │         │
         │    ┌────┴────┐
         │    │         │
         │    ▼         ▼
         │  Restart   Give Up
         │    │         │
         │    │         ├─▶ Call OnGiveUp
         │    │         │
         │    │         ├─▶ Apply Strategy
         │    │         │      (Stop/Escalate)
         │    │         │
         │    ▼         ▼
         │  Backoff   Process Dead
         │  delay
         │    │
         │    ▼
         └─▶ Call OnRestart
              │
              ▼
         Spawn new Process
         (same Agent, fresh state)
```

## Concurrency Model

### Process Isolation

Each Process runs in its own goroutine. State is protected by mutex.

```go
func (p *Process) run() {
    defer p.cleanup()

    for {
        select {
        case <-p.ctx.Done():
            return
        case msg := <-p.inbox:
            p.mu.Lock()
            response, err := p.handleMessage(msg)
            p.mu.Unlock()

            msg.respond(response, err)
        }
    }
}
```

### Orchestrator Locking

The Orchestrator uses RWMutex for the process registry:

```go
// Read operations (frequent)
func (o *Orchestrator) Get(id string) *Process {
    o.mu.RLock()
    defer o.mu.RUnlock()
    return o.processes[id]
}

// Write operations (infrequent)
func (o *Orchestrator) Spawn(...) (*Process, error) {
    o.mu.Lock()
    defer o.mu.Unlock()
    // create and register process
}
```

### Tool Execution

Tools execute synchronously within the Process goroutine. For long-running tools, use timeout middleware:

```go
tools.Use(vega.TimeoutMiddleware(30 * time.Second))
```

## Extension Points

### Custom LLM Backend

```go
type LLM interface {
    Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*Response, error)
    GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error)
}

// Implement for any provider
type MyLLM struct { ... }
func (m *MyLLM) Generate(...) (*Response, error) { ... }

agent := vega.Agent{
    LLM: &MyLLM{},
    // ...
}
```

### Custom Memory Backend

```go
type Memory interface {
    Store(ctx context.Context, key string, value any, metadata map[string]any) error
    Retrieve(ctx context.Context, query string, k int) ([]MemoryItem, error)
    Get(ctx context.Context, key string) (MemoryItem, error)
    Delete(ctx context.Context, key string) error
}

// Implement for any storage
type RedisMemory struct { ... }

agent := vega.Agent{
    Memory: &RedisMemory{},
    // ...
}
```

### Custom Persistence

```go
type Persistence interface {
    Save(processes []*ProcessState) error
    Load() ([]*ProcessState, error)
}

// Implement for any storage
type S3Persistence struct { ... }

orch := vega.NewOrchestrator(
    vega.WithPersistence(&S3Persistence{}),
)
```

### Tool Middleware

```go
type ToolMiddleware func(ToolFunc) ToolFunc

func MyMiddleware() ToolMiddleware {
    return func(next ToolFunc) ToolFunc {
        return func(ctx context.Context, params map[string]any) (string, error) {
            // Before
            result, err := next(ctx, params)
            // After
            return result, err
        }
    }
}

tools.Use(MyMiddleware())
```

## Performance Considerations

### Memory Usage

- Each Process maintains its own message history
- Use `ContextManager` to limit context size
- Use `Memory` for long-term storage, not in-process state

### Token Costs

- Monitor via `Process.Metrics()`
- Set cost alerts via `HealthConfig.CostAlertUSD`
- Use cheaper models for summarization

### Concurrency

- Processes are independent—scale horizontally
- Tool execution is the bottleneck—use timeouts
- LLM calls are I/O bound—parallelism helps

## Testing

### Unit Testing Agents

```go
func TestAgent(t *testing.T) {
    mockLLM := vega.NewMockLLM()
    mockLLM.OnGenerate(func(msgs []Message, tools []ToolSchema) *Response {
        return &Response{Content: "Hello!"}
    })

    agent := vega.Agent{
        LLM:   mockLLM,
        Tools: vega.NewMockTools(),
    }

    orch := vega.NewOrchestrator()
    proc, _ := orch.Spawn(agent)

    resp, _ := proc.Send(context.Background(), "Hi")
    assert.Equal(t, "Hello!", resp)
}
```

### Integration Testing

```go
func TestSupervision(t *testing.T) {
    failCount := 0
    tools := vega.NewTools()
    tools.Register("fail_twice", func() (string, error) {
        failCount++
        if failCount < 3 {
            return "", errors.New("intentional failure")
        }
        return "success", nil
    })

    agent := vega.Agent{Tools: tools, ...}

    restarts := 0
    proc, _ := orch.Spawn(agent,
        vega.WithSupervision(vega.Supervision{
            Strategy:    vega.Restart,
            MaxRestarts: 5,
            OnRestart: func(p *Process, attempt int) {
                restarts++
            },
        }),
    )

    resp, _ := proc.Send(ctx, "Call fail_twice")
    assert.Equal(t, 2, restarts)
    assert.Equal(t, "success", resp)
}
```
