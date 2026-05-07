# Gap Analysis: Original Vega (C) vs Go Package

This document compares features from the original Vega language specification to what's captured in the Go package spec.

## Summary

| Feature | Original Vega | Go Package | Status |
|---------|---------------|------------|--------|
| Supervision trees | ✅ | ✅ | ✅ Complete |
| Restart strategies | ✅ | ✅ | ✅ Complete |
| Backoff | ✅ | ✅ | ✅ Complete |
| Health monitoring | ✅ | ✅ | ✅ Complete |
| Process recovery | ✅ | ✅ | ✅ Complete |
| Tools (compiled) | ✅ | ✅ | ✅ Complete |
| Tools (dynamic) | ❌ | ✅ | ✅ Enhanced |
| Memory interface | ✅ | ✅ | ✅ Complete |
| Context management | ✅ | ✅ | ✅ Complete |
| Orchestration patterns | ✅ | ✅ | ✅ Complete |
| **Automatic parallelization** | ✅ | ❌ | 🔴 **Gap** |
| **Budget enforcement** | ✅ | ⚠️ | 🟡 **Partial** |
| **Retry policies** | ✅ | ⚠️ | 🟡 **Partial** |
| **Rate limiting** | ✅ | ❌ | 🔴 **Gap** |
| **Circuit breaker** | ✅ | ❌ | 🔴 **Gap** |
| **Streaming** | ✅ | ❌ | 🔴 **Gap** |
| **Observability/Tracing** | ✅ | ⚠️ | 🟡 **Partial** |
| **Process linking** | ✅ | ❌ | 🟡 **Maybe N/A** |
| **Capability-based security** | ✅ | ⚠️ | 🟡 **Partial** |

---

## Detailed Gap Analysis

### 🔴 Gap: Automatic Parallelization

**Original Vega:**
```vega
// Compiler detects these are independent and parallelizes automatically
let review1 = reviewer <- code1;
let review2 = reviewer <- code2;
let review3 = reviewer <- code3;

// This depends on all three - waits for all to complete
let summary = summarizer <- review1 + review2 + review3;

// Explicit parallel block
parallel {
    let a = agent1 <- msg1;
    let b = agent2 <- msg2;
}
```

**Go Package (current):**
- Has `vega.Parallel()` pattern for explicit parallelism
- Does NOT have automatic dataflow analysis
- User must explicitly use patterns

**Recommendation:**
This is a compile-time feature that doesn't translate to a library. However, we could add:
1. **Runtime dataflow tracking** - Track which agent calls are independent at runtime
2. **Async-by-default** - All `Send()` calls return futures, explicit `Await()` to join
3. **AutoParallel helper** - Analyzes a function and parallelizes calls

```go
// Option 1: Async by default
f1 := proc1.SendAsync("msg1")
f2 := proc2.SendAsync("msg2")
f3 := proc3.SendAsync("msg3")
// All three running in parallel
r1, r2, r3 := f1.Await(), f2.Await(), f3.Await()

// Option 2: Batch helper
results := vega.Batch(
    func() string { return proc1.Send(ctx, "msg1") },
    func() string { return proc2.Send(ctx, "msg2") },
    func() string { return proc3.Send(ctx, "msg3") },
)
```

**Add to spec:** Yes - add `Batch()` helper and document async patterns.

---

### 🔴 Gap: Rate Limiting

**Original Vega:**
```vega
// Per-model rate limit tracking
// Token bucket / leaky bucket
// Request queuing when at limit
// Backpressure signal to callers
```

**Go Package (current):**
- Mentioned briefly in tool middleware
- No first-class rate limiting for LLM calls

**Recommendation:** Add rate limiting as orchestrator-level feature.

```go
orch := vega.NewOrchestrator(
    vega.WithRateLimits(vega.RateLimits{
        "claude-sonnet-4-6": vega.TokenBucket{
            Rate:     60,           // requests per minute
            Burst:    10,           // burst capacity
            Strategy: vega.Queue,   // Queue, Reject, or Backpressure
        },
    }),
)
```

**Add to spec:** Yes - this is critical for production.

---

### 🔴 Gap: Circuit Breaker

**Original Vega:**
```vega
agent FlakeyService {
    circuit_breaker {
        threshold 5          // failures before opening
        reset_after 30s      // time before half-open
    }
}
```

**Go Package (current):**
- Not mentioned

**Recommendation:** Add circuit breaker as agent option.

```go
agent := vega.Agent{
    CircuitBreaker: &vega.CircuitBreaker{
        Threshold:  5,              // failures before opening
        ResetAfter: 30 * time.Second,
        OnOpen:     func() { log.Println("Circuit opened") },
        OnClose:    func() { log.Println("Circuit closed") },
    },
}
```

**Add to spec:** Yes - important for resilience.

---

### 🔴 Gap: Streaming

**Original Vega:**
```vega
// Streaming response
for chunk in coder <~ "Write a long story" {
    print(chunk);
}
```

**Go Package (current):**
- LLM interface has `GenerateStream()` but not exposed in Process API

**Recommendation:** Add streaming to Process.

```go
// Streaming send
stream := proc.SendStream(ctx, "Write a long story")
for chunk := range stream.Chunks() {
    fmt.Print(chunk)
}
if err := stream.Err(); err != nil {
    // handle error
}

// Or callback style
proc.SendStream(ctx, "Write a long story", func(chunk string) {
    fmt.Print(chunk)
})
```

**Add to spec:** Yes - essential for UX.

---

### 🟡 Partial: Budget Enforcement

**Original Vega:**
```vega
// Per-agent budget
agent Coder {
    budget $0.50
}

// Session budget block
session budget $10.00 {
    let code = coder <- task;
}

// Budget API
let remaining = budget::remaining();
```

**Go Package (current):**
- Health monitoring has `CostAlertUSD` - alerts only
- No enforcement (blocking when exceeded)
- No budget blocks/scopes

**Recommendation:** Add budget as first-class feature.

```go
// Per-agent budget
agent := vega.Agent{
    Budget: vega.Budget{
        Limit:    0.50,
        OnExceed: vega.Block,  // Block, Warn, or Allow
    },
}

// Session budget (context-based)
ctx := vega.WithBudget(ctx, 10.00)
proc.Send(ctx, "expensive task")  // Fails if would exceed

// Query budget
remaining := vega.BudgetRemaining(ctx)
used := vega.BudgetUsed(ctx)
```

**Add to spec:** Yes - this was a key differentiator.

---

### 🟡 Partial: Retry Policies

**Original Vega:**
```vega
agent Coder {
    retry {
        max_attempts 3
        backoff exponential(100ms, 2.0, 5s)
        on [rate_limit, overloaded, timeout]
    }
}
```

**Go Package (current):**
- Has backoff in Supervision
- Supervision restarts on ANY failure
- No per-error-type retry classification

**Recommendation:** Add retry policy separate from supervision.

```go
agent := vega.Agent{
    Retry: &vega.RetryPolicy{
        MaxAttempts: 3,
        Backoff: vega.ExponentialBackoff{
            Initial:    100 * time.Millisecond,
            Multiplier: 2.0,
            Max:        5 * time.Second,
        },
        RetryOn: []vega.ErrorType{
            vega.ErrRateLimit,
            vega.ErrOverloaded,
            vega.ErrTimeout,
        },
    },
}
```

**Note:** Retry is for transient failures within a single operation. Supervision is for process crashes. They're different concerns.

**Add to spec:** Yes - clarify distinction from supervision.

---

### 🟡 Partial: Observability/Tracing

**Original Vega:**
```vega
// Automatic per-call tracing
{
  "trace_id": "abc123",
  "span_id": "def456",
  "agent": "Coder",
  "model": "claude-sonnet-4-6",
  "input_tokens": 150,
  "output_tokens": 892,
  "cost_usd": 0.0043,
  "latency_ms": 2341,
  "status": "ok",
  "retries": 0
}

// Trace API
trace::span(name: str) -> Span
trace::event(msg: str)

// Export: OTLP, JSON lines, SQLite
```

**Go Package (current):**
- Health monitoring tracks some metrics
- No OpenTelemetry integration
- No trace API

**Recommendation:** Add first-class tracing with OpenTelemetry.

```go
orch := vega.NewOrchestrator(
    vega.WithTracing(vega.TracingConfig{
        Exporter:   vega.OTLPExporter("localhost:4317"),
        SampleRate: 1.0,
    }),
)

// Or use existing OTel setup
orch := vega.NewOrchestrator(
    vega.WithTracerProvider(otel.GetTracerProvider()),
)

// Every agent call automatically creates spans:
// - trace_id, span_id
// - agent name, model
// - input/output tokens
// - cost
// - latency
// - status, retries
```

**Add to spec:** Yes - observability is critical.

---

### 🟡 Partial: Capability-Based Security

**Original Vega:**
```vega
tool read_file(path: str) -> str
    capabilities [fs::read]
    allowed_paths ["/project/**"]
{
    return file::read(path);
}
```

**Go Package (current):**
- Has sandboxing (path restrictions)
- No explicit capability declarations
- No glob pattern matching for allowed paths

**Recommendation:** Enhance tool security model.

```go
tools := vega.NewTools(
    vega.WithCapabilities(vega.Capabilities{
        "read_file": {
            Capabilities: []string{"fs:read"},
            AllowedPaths: []string{"/project/**", "/data/*.json"},
        },
        "run_tests": {
            Capabilities: []string{"process:exec"},
            AllowedCommands: []string{"npm test", "pytest", "go test"},
        },
    }),
)
```

**Add to spec:** Yes - strengthen security story.

---

### 🟡 Maybe N/A: Process Linking

**Original Vega:**
```vega
OP_LINK    // Link two processes (bidirectional failure notification)
OP_MONITOR // Monitor a process (unidirectional)
```

**Go Package:**
- Uses Go's goroutines, not custom processes
- Supervision tree handles failure propagation
- Linking might not be needed

**Recommendation:** Consider if needed. Could add:

```go
// Link processes - if either fails, both fail
vega.Link(proc1, proc2)

// Monitor - get notified when proc fails
vega.Monitor(proc, func(reason error) {
    log.Printf("Process died: %v", reason)
})
```

**Add to spec:** Maybe - evaluate if supervision tree is sufficient.

---

## New Features in Go Package (Not in Original)

These are enhancements in the Go package that weren't in the original:

| Feature | Description |
|---------|-------------|
| **Dynamic tools (YAML)** | Load tools from YAML without recompile |
| **Tool middleware** | Logging, rate limiting, timeout as middleware |
| **Persistence interface** | Pluggable process state persistence |
| **LLM backend interface** | Swap LLM providers (Anthropic, LangChain, etc.) |
| **Gin/HTTP middleware** | Easy web framework integration |
| **Process checkpoints** | Resume from last state on recovery |

---

## Recommended Additions to Spec

### Priority 1 (Must Have)

1. **Budget enforcement** - Not just alerts, actual blocking
2. **Rate limiting** - Token bucket for LLM calls
3. **Streaming** - `SendStream()` on Process
4. **Retry policies** - Separate from supervision, per-error-type

### Priority 2 (Should Have)

5. **Circuit breaker** - Per-agent circuit breaker config
6. **OpenTelemetry tracing** - Automatic spans for agent calls
7. **Batch/parallel helper** - Easy parallelization of independent calls

### Priority 3 (Nice to Have)

8. **Capability-based security** - Explicit tool capabilities
9. **Process linking** - If supervision tree isn't sufficient
10. **Budget scopes** - Nested budget contexts

---

## Updated Feature Comparison

After addressing gaps, feature parity would look like:

| Feature | Original Vega | Go Package |
|---------|---------------|------------|
| Supervision | ✅ Custom VM | ✅ Goroutines + library |
| Parallelization | ✅ Compiler analysis | ✅ Explicit patterns + Batch() |
| Budgets | ✅ Language primitive | ✅ Context + middleware |
| Rate limiting | ✅ VM-level | ✅ Orchestrator-level |
| Circuit breaker | ✅ Agent config | ✅ Agent config |
| Streaming | ✅ `<~` operator | ✅ SendStream() |
| Tracing | ✅ Automatic | ✅ OpenTelemetry |
| Tools | ✅ Compiled only | ✅ Compiled + dynamic |
| Memory | ✅ Basic | ✅ Pluggable backends |

The Go package would have **feature parity** with the original while being:
- Easier to maintain (Go vs C)
- More extensible (interfaces vs fixed implementation)
- Better integrated (Go ecosystem)
