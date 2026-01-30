# Vega Supervision

Vega's supervision system is inspired by Erlang/OTP's battle-tested approach to fault tolerance. The core philosophy: **let it crash, then recover gracefully**.

## Why Supervision?

AI agents fail. They hit rate limits, make invalid tool calls, get stuck in loops, or produce unexpected outputs. Without supervision:

```
Agent fails → Entire system breaks → Manual intervention required
```

With supervision:

```
Agent fails → Supervisor detects → Applies restart strategy → Agent recovers
```

## Core Concepts

### Process

A Process is a running agent with lifecycle management.

```go
type Process struct {
    ID          string
    Agent       *Agent
    Status      Status      // pending, running, completed, failed, timeout
    Task        string
    StartedAt   time.Time
    Supervision *Supervision
}
```

### Supervision

Supervision configuration defines how failures are handled.

```go
type Supervision struct {
    Strategy    Strategy      // What to do on failure
    MaxRestarts int           // How many times to restart
    Window      time.Duration // Time window for restart counting
    Backoff     BackoffStrategy

    // Callbacks
    OnFailure func(p *Process, err error)
    OnRestart func(p *Process, attempt int)
    OnGiveUp  func(p *Process, err error)
}
```

### Strategies

| Strategy | Behavior |
|----------|----------|
| `Restart` | Restart the failed process (default) |
| `Stop` | Let the process stay dead |
| `Escalate` | Propagate failure to parent supervisor |

## Basic Usage

### Simple Restart

```go
proc, _ := orch.Spawn(agent,
    vega.WithSupervision(vega.Supervision{
        Strategy:    vega.Restart,
        MaxRestarts: 3,
        Window:      10 * time.Minute,
    }),
)
```

Behavior:
- If the agent fails, restart it
- If it fails 3 times within 10 minutes, give up
- After 10 minutes, the restart counter resets

### Always Restart

For critical agents that must stay alive:

```go
vega.WithSupervision(vega.Supervision{
    Strategy:    vega.Restart,
    MaxRestarts: -1,  // Unlimited
})
```

### Stop on Failure

For one-shot tasks where restart doesn't make sense:

```go
vega.WithSupervision(vega.Supervision{
    Strategy: vega.Stop,
})
```

## Backoff Strategies

Prevent thundering herd with exponential backoff.

```go
vega.WithSupervision(vega.Supervision{
    Strategy:    vega.Restart,
    MaxRestarts: 5,
    Backoff: vega.BackoffStrategy{
        Initial:    1 * time.Second,   // First retry: wait 1s
        Max:        30 * time.Second,  // Cap at 30s
        Multiplier: 2.0,               // Double each time
    },
})

// Retry timeline:
// Failure 1 → wait 1s → restart
// Failure 2 → wait 2s → restart
// Failure 3 → wait 4s → restart
// Failure 4 → wait 8s → restart
// Failure 5 → wait 16s → restart
// Failure 6 → give up (exceeded MaxRestarts)
```

### Jitter

Add randomness to prevent synchronized retries:

```go
Backoff: vega.BackoffStrategy{
    Initial:    1 * time.Second,
    Max:        30 * time.Second,
    Multiplier: 2.0,
    Jitter:     0.1,  // ±10% randomness
}
```

## Callbacks

React to supervision events.

```go
vega.WithSupervision(vega.Supervision{
    Strategy:    vega.Restart,
    MaxRestarts: 3,

    OnFailure: func(p *Process, err error) {
        log.Printf("Process %s failed: %v", p.ID, err)
        metrics.IncrementCounter("agent_failures")
        alerting.Notify(fmt.Sprintf("Agent %s failed: %v", p.Agent.Name, err))
    },

    OnRestart: func(p *Process, attempt int) {
        log.Printf("Restarting %s (attempt %d)", p.ID, attempt)
        metrics.IncrementCounter("agent_restarts")
    },

    OnGiveUp: func(p *Process, err error) {
        log.Printf("Giving up on %s after max restarts: %v", p.ID, err)
        alerting.Page(fmt.Sprintf("CRITICAL: Agent %s permanently failed", p.Agent.Name))
    },
})
```

## Supervision Trees

Processes can supervise other processes, forming a tree.

```
                    Orchestrator
                         │
           ┌─────────────┼─────────────┐
           │             │             │
       Supervisor    Supervisor    Supervisor
       (Tony)        (Research)   (Content)
           │             │             │
      ┌────┴────┐    ┌───┴───┐    ┌───┴───┐
      │         │    │       │    │       │
    Gary     Sarah  Bot1   Bot2  Marcus  Vera
```

### Parent-Child Relationships

```go
// Tony supervises his team
tonyProc, _ := orch.Spawn(tony,
    vega.WithSupervision(vega.Supervision{
        Strategy:    vega.Restart,
        MaxRestarts: -1,
    }),
)

// Gary is spawned by Tony, supervised by Tony's process
garyProc, _ := tonyProc.SpawnChild(gary,
    vega.WithSupervision(vega.Supervision{
        Strategy:    vega.Restart,
        MaxRestarts: 3,
        Window:      10 * time.Minute,
    }),
)
```

### Escalation

When a child exhausts its restarts, it can escalate to the parent:

```go
// Gary escalates after 3 failures
vega.WithSupervision(vega.Supervision{
    Strategy:    vega.Escalate,  // After MaxRestarts, escalate
    MaxRestarts: 3,
})

// Tony's handler receives escalation
tonySupervision := vega.Supervision{
    OnChildEscalation: func(child *Process, err error) {
        log.Printf("Child %s escalated: %v", child.ID, err)
        // Tony could: restart Gary with different config,
        // notify user, or escalate further
    },
}
```

## Health Monitoring

Beyond crash recovery, Vega monitors for unhealthy behavior.

```go
orch := vega.NewOrchestrator(
    vega.WithHealthCheck(30 * time.Second),
    vega.WithHealthConfig(vega.HealthConfig{
        StaleProgressMinutes: 5,   // No progress in 5 min
        MaxIterationsWarning: 50,  // Warn at 50 iterations
        ErrorLoopCount:       3,   // 3 consecutive errors
        CostAlertUSD:         1.0, // $1 cost threshold
    }),
)

// Subscribe to health alerts
orch.OnHealthAlert(func(alert vega.Alert) {
    switch alert.Type {
    case vega.AlertStaleProgress:
        log.Printf("Agent %s appears stuck", alert.ProcessID)
    case vega.AlertErrorLoop:
        log.Printf("Agent %s in error loop", alert.ProcessID)
    case vega.AlertHighCost:
        log.Printf("Agent %s exceeded cost threshold", alert.ProcessID)
    }
})
```

### Alert Types

| Alert | Trigger | Typical Response |
|-------|---------|------------------|
| `AlertStaleProgress` | No tool calls or output for N minutes | Check if stuck, possibly restart |
| `AlertErrorLoop` | N consecutive failed tool calls | Investigate, possibly change approach |
| `AlertHighCost` | Token cost exceeds threshold | Review efficiency, possibly terminate |
| `AlertTimeout` | Process exceeded time limit | Kill process |
| `AlertHighIterations` | Iteration count exceeds warning threshold | Review for infinite loops |

## Persistence and Recovery

Supervision state survives restarts.

```go
orch := vega.NewOrchestrator(
    vega.WithPersistence(vega.NewJSONPersistence("processes.json")),
    vega.WithRecovery(true),
)
```

### What's Persisted

```json
{
  "processes": [
    {
      "id": "gary-abc123",
      "agent_name": "Gary",
      "status": "running",
      "task": "Build landing page",
      "started_at": "2024-01-15T10:30:00Z",
      "supervision": {
        "strategy": "restart",
        "max_restarts": 3,
        "restart_count": 1,
        "last_failure": "2024-01-15T10:35:00Z"
      },
      "checkpoint": {
        "iteration": 15,
        "last_message": "Installing dependencies...",
        "context_hash": "abc123"
      }
    }
  ]
}
```

### Recovery Behavior

On startup:
1. Load persisted state
2. For each `running` process:
   - Re-create Agent from config
   - Resume from checkpoint if available
   - Apply supervision rules
3. For each `failed` process within restart window:
   - Apply restart strategy

```go
// Custom recovery logic
orch := vega.NewOrchestrator(
    vega.WithRecovery(true),
    vega.WithRecoveryFilter(func(p *Process) bool {
        // Only recover processes that were running less than 1 hour
        return time.Since(p.StartedAt) < time.Hour
    }),
)
```

## Common Patterns

### The Immortal Supervisor

A top-level agent that must never die:

```go
supervisor := vega.Agent{
    Name:   "Supervisor",
    System: supervisorPrompt,
    Tools:  supervisorTools,
}

proc, _ := orch.Spawn(supervisor,
    vega.WithSupervision(vega.Supervision{
        Strategy:    vega.Restart,
        MaxRestarts: -1,  // Always restart
        Backoff: vega.BackoffStrategy{
            Initial:    100 * time.Millisecond,
            Max:        10 * time.Second,
            Multiplier: 2.0,
        },
        OnRestart: func(p *Process, attempt int) {
            if attempt > 10 {
                alerting.Page("Supervisor unstable - many restarts")
            }
        },
    }),
)
```

### The Expendable Worker

One-shot tasks that shouldn't retry:

```go
worker := vega.Agent{
    Name:   "DataProcessor",
    System: workerPrompt,
    Tools:  workerTools,
}

proc, _ := orch.Spawn(worker,
    vega.WithTask("Process batch 12345"),
    vega.WithSupervision(vega.Supervision{
        Strategy: vega.Stop,  // Don't restart
        OnFailure: func(p *Process, err error) {
            // Log failure, move to dead letter queue
            deadLetter.Add(p.Task, err)
        },
    }),
    vega.WithTimeout(5 * time.Minute),
)
```

### The Careful Worker

Retry with increasing caution:

```go
proc, _ := orch.Spawn(agent,
    vega.WithSupervision(vega.Supervision{
        Strategy:    vega.Restart,
        MaxRestarts: 3,
        Window:      30 * time.Minute,
        Backoff: vega.BackoffStrategy{
            Initial:    5 * time.Second,
            Max:        5 * time.Minute,
            Multiplier: 3.0,
        },
        OnRestart: func(p *Process, attempt int) {
            // Adjust agent behavior based on failure count
            switch attempt {
            case 1:
                // First retry: same approach
            case 2:
                // Second retry: reduce scope
                p.Agent.System = simplerPrompt
            case 3:
                // Third retry: minimal approach
                p.Agent.Tools = minimalTools
            }
        },
    }),
)
```

### Circuit Breaker

Stop all agents if too many failures:

```go
var failureCount atomic.Int32

supervision := vega.Supervision{
    Strategy:    vega.Restart,
    MaxRestarts: 3,
    OnFailure: func(p *Process, err error) {
        count := failureCount.Add(1)
        if count > 10 {
            log.Println("Circuit breaker tripped - stopping all agents")
            orch.Shutdown(context.Background())
            alerting.Page("System circuit breaker tripped")
        }
    },
}

// Reset counter periodically
go func() {
    for range time.Tick(time.Minute) {
        failureCount.Store(0)
    }
}()
```

## Debugging Supervision

### Supervision Events Log

```go
orch := vega.NewOrchestrator(
    vega.WithSupervisionLogger(log.Default()),
)

// Output:
// [SUPERVISION] Process gary-abc123 started
// [SUPERVISION] Process gary-abc123 failed: tool error: file not found
// [SUPERVISION] Process gary-abc123 restarting (attempt 1/3, backoff 1s)
// [SUPERVISION] Process gary-abc123 started
// [SUPERVISION] Process gary-abc123 completed
```

### Supervision Metrics

```go
orch.Metrics() // Returns SupervisionMetrics

type SupervisionMetrics struct {
    TotalSpawns      int64
    TotalRestarts    int64
    TotalFailures    int64
    TotalCompletions int64
    ActiveProcesses  int
    ByAgent          map[string]AgentMetrics
}
```

## Best Practices

1. **Always use supervision** — Even for "simple" agents
2. **Set reasonable limits** — MaxRestarts prevents infinite loops
3. **Use backoff** — Prevents hammering failed services
4. **Monitor alerts** — Health alerts often catch issues before crashes
5. **Log supervision events** — Essential for post-mortems
6. **Test failure scenarios** — Simulate failures in development
7. **Escalate appropriately** — Don't let failures cascade silently
