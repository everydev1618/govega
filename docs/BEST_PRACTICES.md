# Vega Best Practices

This guide covers common pitfalls and best practices for building reliable applications with Vega.

---

## Process Lifecycle Management

### Always Complete Your Processes

**This is the most important rule.** Every spawned process must eventually be marked as completed or failed. If you don't do this, processes will appear as "running" forever in your orchestrator, consuming memory and making it impossible to track actual work.

#### ❌ Wrong: Fire and Forget

```go
// DON'T DO THIS - process never completes
proc, _ := orch.Spawn(agent, vega.WithTask("Do something"))
proc.SendAsync(task)
// Process stays "running" forever
```

#### ✅ Correct: Complete After Work

```go
// Synchronous - complete after Send returns
proc, _ := orch.Spawn(agent, vega.WithTask("Do something"))
response, err := proc.Send(ctx, task)
if err != nil {
    proc.Fail(err)
    return err
}
proc.Complete(response)
```

```go
// Asynchronous - complete when future resolves
proc, _ := orch.Spawn(agent, vega.WithTask("Do something"))
future := proc.SendAsync(task)

go func() {
    result, err := future.Await(context.Background())
    if err != nil {
        proc.Fail(err)
    } else {
        proc.Complete(result)
    }
}()
```

### Process Status Flow

```
Pending → Running → Completed
                 ↘ Failed
```

- `Pending`: Process created but no work started
- `Running`: Process is executing (Send/SendAsync called)
- `Completed`: Work finished successfully (you called `Complete()`)
- `Failed`: Work failed (you called `Fail()` or process crashed)

### Use OnProcessComplete for Coordination

If you need to know when spawned processes finish:

```go
orch.OnProcessComplete(func(p *vega.Process, result string) {
    log.Printf("Process %s completed: %s", p.ID, result)
    // Notify parent, update state, etc.
})
```

---

## Spawning Agents

### Don't Spawn Duplicates

If you're handling events (webhooks, messages, etc.), prevent duplicate spawns:

```go
type Handler struct {
    processing   map[string]bool
    processingMu sync.Mutex
}

func (h *Handler) handleEvent(channelID string, event Event) {
    // Check if already processing this channel
    h.processingMu.Lock()
    if h.processing[channelID] {
        h.processingMu.Unlock()
        log.Printf("Already processing %s, skipping", channelID)
        return
    }
    h.processing[channelID] = true
    h.processingMu.Unlock()

    // Ensure cleanup
    defer func() {
        h.processingMu.Lock()
        delete(h.processing, channelID)
        h.processingMu.Unlock()
    }()

    // Now safe to spawn
    proc, _ := orch.Spawn(agent)
    // ...
}
```

### Track Parent-Child Relationships

Use `WithParent` to build spawn trees for debugging:

```go
func (t *Tools) spawnHelper(ctx context.Context, params map[string]any) (string, error) {
    parentProc := vega.ProcessFromContext(ctx)

    proc, err := t.orch.Spawn(agent,
        vega.WithTask(task),
        vega.WithParent(parentProc),  // Track lineage
    )
    // ...
}
```

---

## Message Handling

### Validate Message Content

Empty messages cause API errors. Always validate before adding to conversation:

```go
// Filter empty messages before sending to LLM
func filterMessages(messages []Message) []Message {
    filtered := make([]Message, 0, len(messages))
    for _, msg := range messages {
        if strings.TrimSpace(msg.Content) != "" {
            filtered = append(filtered, msg)
        }
    }
    return filtered
}
```

### Don't Assume LLM Response Has Text

When the LLM uses tools, the text response may be empty:

```go
// The LLM might return only tool calls with no text
response, err := proc.Send(ctx, message)
if err != nil {
    return err
}

// response might be "" if LLM only used tools
if response != "" {
    saveToHistory(response)
}
```

---

## Tool Implementation

### Handle Missing Parameters Gracefully

Tools should validate their inputs:

```go
tools.Register("search", vega.ToolDef{
    Description: "Search for something",
    Fn: func(ctx context.Context, params map[string]any) (string, error) {
        query, ok := params["query"].(string)
        if !ok || strings.TrimSpace(query) == "" {
            return "", fmt.Errorf("query parameter is required")
        }
        // ...
    },
    Params: map[string]vega.ParamDef{
        "query": {Type: "string", Required: true},
    },
})
```

### Long-Running Tools Should Be Cancellable

Respect context cancellation:

```go
func (t *Tools) longTask(ctx context.Context, params map[string]any) (string, error) {
    for i := 0; i < 100; i++ {
        select {
        case <-ctx.Done():
            return "", ctx.Err()
        default:
            // Do work
        }
    }
    return "done", nil
}
```

---

## Error Handling

### Always Handle Spawn Errors

```go
proc, err := orch.Spawn(agent)
if err != nil {
    // Don't ignore this - spawn can fail due to:
    // - Rate limiting
    // - Resource exhaustion
    // - Invalid agent configuration
    return fmt.Errorf("failed to spawn agent: %w", err)
}
```

### Use Fail() for Error Cases

```go
response, err := proc.Send(ctx, message)
if err != nil {
    proc.Fail(err)  // Mark as failed, not just abandoned
    return err
}
proc.Complete(response)
```

### Supervision Doesn't Replace Error Handling

Supervision handles crashes, not logic errors:

```go
// Supervision will restart if this crashes
// But it won't fix your bug - the restart will just crash again
proc, _ := orch.Spawn(agent, vega.WithSupervision(vega.Supervision{
    Strategy:    vega.Restart,
    MaxRestarts: 3,
}))
```

---

## Resource Management

### Shutdown Gracefully

```go
func main() {
    orch := vega.NewOrchestrator(vega.WithLLM(anthropic))

    // Handle shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigCh
        log.Println("Shutting down...")
        orch.Shutdown(ctx)
        cancel()
    }()

    // Your app logic...
}
```

### Clean Up Stale Processes

If processes can get stuck, implement cleanup:

```go
func cleanupStale(orch *vega.Orchestrator, maxAge time.Duration) {
    for _, proc := range orch.List() {
        if proc.Status() == vega.StatusRunning {
            if time.Since(proc.Metrics().LastActiveAt) > maxAge {
                log.Printf("Killing stale process %s", proc.ID)
                proc.Fail(fmt.Errorf("stale process cleanup"))
            }
        }
    }
}
```

---

## Common Anti-Patterns

### ❌ Ignoring Process Completion

```go
// Bad: Process leaks, appears "running" forever
proc.SendAsync(task)
return "Started!"
```

### ❌ Not Validating User Input

```go
// Bad: Empty message causes API error
messages = append(messages, Message{Content: userInput})
proc.Send(ctx, buildPrompt(messages))
```

### ❌ Spawning in Hot Loops

```go
// Bad: Can spawn thousands of processes
for _, item := range items {
    orch.Spawn(agent)  // No rate limiting!
}
```

### ❌ Ignoring Context Cancellation

```go
// Bad: Continues after cancellation
func myTool(ctx context.Context, p map[string]any) (string, error) {
    for i := 0; i < 1000000; i++ {
        doWork()  // Never checks ctx.Done()
    }
    return "done", nil
}
```

---

## Checklist

Before deploying a Vega application:

- [ ] Every `Spawn()` has a corresponding `Complete()` or `Fail()`
- [ ] `SendAsync()` futures are awaited and completed
- [ ] Empty messages are filtered before LLM calls
- [ ] Duplicate spawns are prevented for event handlers
- [ ] Graceful shutdown is implemented
- [ ] Context cancellation is respected in tools
- [ ] Errors are handled, not ignored
- [ ] Stale process cleanup exists (if applicable)

---

## See Also

- [QUICKSTART.md](QUICKSTART.md) - Getting started guide
- [SPEC.md](SPEC.md) - Full API specification
- [SUPERVISION.md](SUPERVISION.md) - Fault tolerance patterns
- [TOOLS.md](TOOLS.md) - Tool implementation guide
