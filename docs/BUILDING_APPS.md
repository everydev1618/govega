# Building Apps with Vega

Vega is an **orchestration kernel** for AI agents. It handles spawning, supervision, delegation, and lifecycle management. It does NOT include tools, knowledge bases, or domain logic — those belong to your app.

```
┌─────────────────────────────────────┐
│           Your App                  │
│                                     │
│  - Knowledge base / embeddings      │
│  - Vector DB (RAG)                  │
│  - Domain-specific tools            │
│  - Custom personas & workflows      │
│  - Secrets management               │
│  - UI / API                         │
│                                     │
│  Registers capabilities as:         │
│    → Tool functions                 │
│    → MCP servers                    │
│    → Dynamic tool definitions       │
├─────────────────────────────────────┤
│           Vega (library)            │
│                                     │
│  - Spawn / kill agents              │
│  - Delegate between agents          │
│  - Supervision & restarts           │
│  - Workflow execution               │
│  - Tool registry (plugin system)    │
│  - Cost tracking & budgets          │
│  - Process tree & groups            │
└─────────────────────────────────────┘
```

## What Vega Does

- **Spawns agents** as processes with lifecycle management
- **Routes messages** between agents (delegation, workflows, pipelines)
- **Supervises** processes with restart strategies (Erlang-style)
- **Manages tools** via a plugin registry — your app registers tools, vega calls them
- **Tracks costs** per process (tokens, USD)
- **Links processes** for failure propagation (parent/child, monitors)

## What Vega Does NOT Do

- File I/O, Docker, Kubernetes — those are tools your app provides
- Secrets management — your app handles API keys and credentials
- Vector databases or embeddings — your app brings its own knowledge layer
- UI — vega is a library, not a framework (the dashboard is a separate convenience tool)

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "os"

    vega "github.com/everydev1618/govega"
    "github.com/everydev1618/govega/llm"
)

func main() {
    // 1. Create LLM backend (your app manages the API key)
    backend := llm.NewAnthropic(
        llm.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
    )

    // 2. Create orchestrator
    orch := vega.NewOrchestrator(vega.WithLLM(backend))
    defer orch.Shutdown(context.Background())

    // 3. Register your app's tools
    tools := vega.NewTools()
    tools.Register("search_docs", vega.ToolDef{
        Description: "Search the knowledge base",
        Fn: func(ctx context.Context, params map[string]any) (string, error) {
            query := params["query"].(string)
            // Your app's vector DB search here
            return myVectorDB.Search(query), nil
        },
        Params: map[string]vega.ParamDef{
            "query": {Type: "string", Description: "Search query", Required: true},
        },
    })

    // 4. Spawn an agent
    agent := vega.Agent{
        Name:   "assistant",
        Model:  "claude-sonnet-4-20250514",
        System: vega.StaticPrompt("You are a helpful assistant."),
        Tools:  tools,
    }

    proc, _ := orch.Spawn(agent)

    // 5. Chat
    response, _ := proc.Send(context.Background(), "Hello!")
    fmt.Println(response)
}
```

## Core Concepts

### Agent

An agent definition — the blueprint, not the running instance.

```go
agent := vega.Agent{
    Name:        "researcher",
    Model:       "claude-sonnet-4-20250514",
    System:      vega.StaticPrompt("You are a research agent."),
    Tools:       tools,          // What tools this agent can use
    Temperature: float64Ptr(0.7), // Optional
    MaxTokens:   4096,           // Optional
    Budget:      &vega.Budget{Limit: 1.00, OnExceed: vega.BudgetBlock},
}
```

### Process

A running instance of an agent. Created by `Spawn()`.

```go
proc, err := orch.Spawn(agent,
    vega.WithTask("research quantum computing"),
    vega.WithTimeout(10 * time.Minute),
)

// Send messages
response, err := proc.Send(ctx, "What are the latest breakthroughs?")

// Async
future := proc.SendAsync("Summarize your findings")
result, err := future.Await(ctx)

// Streaming
stream, err := proc.SendStream(ctx, "Explain step by step")
for chunk := range stream.Chunks() {
    fmt.Print(chunk)
}

// Lifecycle
proc.Status()     // StatusRunning, StatusCompleted, etc.
proc.Messages()   // Conversation history
proc.Metrics()    // Tokens, cost, iterations
proc.Stop()       // Terminate
```

### Orchestrator

Manages all processes. One per app.

```go
orch := vega.NewOrchestrator(
    vega.WithLLM(backend),
    vega.WithMaxProcesses(50),
)

// Process management
proc := orch.Get("process-id")
procs := orch.List()
orch.Kill("process-id")

// Named registry
orch.Register("cto", proc)
cto := orch.GetByName("cto")

// Callbacks
orch.OnProcessComplete(func(p *vega.Process, result string) {
    log.Printf("Agent %s completed: %s", p.Agent.Name, result)
})
orch.OnProcessFailed(func(p *vega.Process, err error) {
    log.Printf("Agent %s failed: %v", p.Agent.Name, err)
})
```

### Tools

The plugin system. Your app registers capabilities here.

```go
tools := vega.NewTools()

// Simple function — schema auto-inferred
tools.Register("read_file", func(path string) (string, error) {
    return os.ReadFile(path)
})

// Explicit schema with ToolDef
tools.Register("deploy", vega.ToolDef{
    Description: "Deploy to production",
    Fn: func(ctx context.Context, params map[string]any) (string, error) {
        env := params["environment"].(string)
        return myDeployer.Deploy(env)
    },
    Params: map[string]vega.ParamDef{
        "environment": {Type: "string", Required: true, Enum: []string{"staging", "production"}},
    },
})

// Middleware (runs around every tool call)
tools.Use(func(next vega.ToolFunc) vega.ToolFunc {
    return func(ctx context.Context, params map[string]any) (string, error) {
        log.Printf("tool called with %v", params)
        return next(ctx, params)
    }
})

// Filter tools per agent
devTools := tools.Filter("read_file", "write_file", "run_tests")
opsTools := tools.Filter("deploy", "rollback", "check_health")
```

## Patterns

### Multi-Agent Delegation

Give a lead agent a tool that delegates to other agents.

```go
// Spawn team members
architect, _ := orch.Spawn(vega.Agent{Name: "architect", System: architectPrompt, Tools: tools})
developer, _ := orch.Spawn(vega.Agent{Name: "developer", System: devPrompt, Tools: tools})

// Register in named registry
orch.Register("architect", architect)
orch.Register("developer", developer)

// Create a delegation tool
tools.Register("delegate", vega.ToolDef{
    Description: "Send a task to a team member and get their response",
    Fn: func(ctx context.Context, params map[string]any) (string, error) {
        name := params["agent"].(string)
        message := params["message"].(string)
        target := orch.GetByName(name)
        if target == nil {
            return "", fmt.Errorf("agent %q not found", name)
        }
        return target.Send(ctx, message)
    },
    Params: map[string]vega.ParamDef{
        "agent":   {Type: "string", Description: "Team member name", Required: true},
        "message": {Type: "string", Description: "Task or question", Required: true},
    },
})

// CTO agent with the delegation tool
cto, _ := orch.Spawn(vega.Agent{
    Name:   "cto",
    System: vega.StaticPrompt("You are a CTO. Delegate to: architect, developer."),
    Tools:  tools, // includes "delegate"
})

cto.Send(ctx, "Design and build a REST API for user management")
// CTO will call delegate("architect", "Design the API...")
// then delegate("developer", "Implement this design...")
```

### Supervision (Fault Tolerance)

Agents that automatically restart on failure.

```go
// Simple: restart on failure
proc, _ := orch.Spawn(agent,
    vega.WithSupervision(vega.Supervision{
        Strategy:    vega.Restart,
        MaxRestarts: 3,
        Window:      5 * time.Minute,
        OnRestart: func(p *vega.Process, attempt int) {
            log.Printf("Restarting %s (attempt %d)", p.Agent.Name, attempt)
        },
    }),
)

// Supervision tree (Erlang-style)
supervisor := orch.NewSupervisor(vega.SupervisorSpec{
    Strategy:    vega.OneForOne, // restart only the failed child
    MaxRestarts: 5,
    Window:      10 * time.Minute,
    Children: []vega.ChildSpec{
        {Name: "researcher", Agent: researchAgent, Restart: vega.Permanent},
        {Name: "writer", Agent: writerAgent, Restart: vega.Transient},
    },
})
supervisor.Start()
```

### Process Linking

Erlang-style process linking for failure propagation.

```go
// If child dies, parent gets notified
parent.Link(child)

// Trap exits instead of dying
parent.SetTrapExit(true)
go func() {
    for signal := range parent.ExitSignals() {
        log.Printf("Child %s exited: %s", signal.AgentName, signal.Reason)
        // Decide: restart, ignore, escalate
    }
}()
```

### Process Groups

Broadcast to a group of agents.

```go
orch.JoinGroup("reviewers", reviewer1)
orch.JoinGroup("reviewers", reviewer2)
orch.JoinGroup("reviewers", reviewer3)

// Fan-out: ask all reviewers in parallel
errors, _ := orch.BroadcastToGroup(ctx, "reviewers", "Review this PR: ...")
```

### Spawn Tree (Parent-Child)

Track who spawned whom.

```go
parent, _ := orch.Spawn(managerAgent)
child, _ := orch.Spawn(workerAgent, vega.WithParent(parent), vega.WithSpawnReason("handle subtask"))

tree := orch.GetSpawnTree()
// Returns tree of SpawnTreeNode with Children
```

### MCP Servers (External Tools)

Connect to MCP-compatible tool servers.

```go
import "github.com/everydev1618/govega/mcp"

client, _ := mcp.NewClient(mcp.ServerConfig{
    Name:      "github",
    Transport: mcp.TransportStdio,
    Command:   "npx",
    Args:      []string{"-y", "github-mcp"},
    Timeout:   30 * time.Second,
})
client.Connect(ctx)

// Discover and register MCP tools into vega's tool registry
mcpTools, _ := client.DiscoverTools(ctx)
for _, t := range mcpTools {
    tools.Register(t.Name, vega.ToolDef{
        Description: t.Description,
        Fn: func(ctx context.Context, params map[string]any) (string, error) {
            result, err := client.CallTool(ctx, t.Name, params)
            if err != nil { return "", err }
            return result.Content[0].Text, nil
        },
        // Convert MCP schema to vega ParamDef...
    })
}
```

### DSL (YAML Workflows)

For declarative multi-agent pipelines without Go code.

```go
import "github.com/everydev1618/govega/dsl"

parser := dsl.NewParser()
doc, _ := parser.ParseFile("team.vega.yaml")
interp, _ := dsl.NewInterpreter(doc)
defer interp.Shutdown()

result, _ := interp.Execute(ctx, "review-code", map[string]any{
    "repo": "github.com/myorg/myapp",
})
```

Corresponding YAML:

```yaml
name: Code Review Team
settings:
  default_model: claude-sonnet-4-20250514

agents:
  reviewer:
    system: "You review code for bugs and security issues."
  architect:
    system: "You review code for architectural concerns."

workflows:
  review-code:
    inputs:
      repo: { type: string, required: true }
    steps:
      - reviewer:
          send: "Review this repo: {{repo}}"
          save: review
      - architect:
          send: "Review the architecture of: {{repo}}\nCode review notes: {{review}}"
          save: arch_review
      - parallel:
          - reviewer:
              send: "Address the architectural feedback: {{arch_review}}"
              save: final_review
    output: "{{final_review}}"
```

### Cost Control

```go
// Per-agent budget
agent := vega.Agent{
    Budget: &vega.Budget{
        Limit:    5.00, // $5 max
        OnExceed: vega.BudgetBlock, // stop the agent
    },
}

// Track costs across all processes
for _, p := range orch.List() {
    m := p.Metrics()
    fmt.Printf("%s: $%.4f (%d tokens)\n", p.Agent.Name, m.CostUSD, m.InputTokens+m.OutputTokens)
}
```

## Architecture for a Real App

Here's how a production app (like "Tony CTO") would be structured:

```
tony-cto/
├── main.go              # App entry point — creates orchestrator, spawns agents
├── agents/
│   ├── cto.go           # CTO agent definition + system prompt
│   ├── architect.go     # Architect agent
│   └── developer.go     # Developer agent
├── tools/
│   ├── codebase.go      # read_file, write_file, search_code (app-specific)
│   ├── deploy.go        # Docker/K8s deployment tools
│   └── knowledge.go     # Vector DB search, embeddings
├── knowledge/
│   ├── embeddings.go    # Embedding generation + storage
│   └── retrieval.go     # RAG retrieval pipeline
├── api/
│   └── server.go        # REST/WebSocket API for the frontend
├── go.mod               # depends on github.com/everydev1618/govega
└── .env                 # ANTHROPIC_API_KEY, DB credentials, etc.
```

```go
// main.go
func main() {
    // App manages its own config, secrets, databases
    cfg := loadConfig()
    vectorDB := knowledge.NewPineconeDB(cfg.PineconeKey)

    // Vega is just the orchestration layer
    backend := llm.NewAnthropic(llm.WithAPIKey(cfg.AnthropicKey))
    orch := vega.NewOrchestrator(vega.WithLLM(backend), vega.WithMaxProcesses(20))
    defer orch.Shutdown(context.Background())

    // App registers its tools
    tools := vega.NewTools()
    tools.Register("search_knowledge", knowledgeTool(vectorDB))
    tools.Register("read_file", fileTools.ReadFile)
    tools.Register("write_file", fileTools.WriteFile)
    tools.Register("deploy", deployTool(cfg.K8sConfig))
    tools.Register("delegate", delegationTool(orch))

    // App spawns its agents with app-specific prompts and tools
    spawnTeam(orch, tools)

    // App serves its own API
    api.Serve(orch, cfg.Port)
}
```

## Key Principle

**Vega is the kernel. Your app is the operating system.**

Vega manages processes (agents). Your app decides what those agents can do (tools), what they know (knowledge), and how they're exposed to users (UI/API). Keep vega thin — it should never need to know about your domain.
