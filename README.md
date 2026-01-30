# Vega

**Fault-tolerant AI agent orchestration for Go.**

Vega makes it easy to build reliable AI agent systems with Erlang-style supervision. Use the YAML DSL for rapid prototyping or the Go library for full control.

```yaml
name: Code Review Team

agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You write clean, well-documented code.

  reviewer:
    model: claude-sonnet-4-20250514
    system: You review code for bugs and improvements.

workflows:
  review:
    steps:
      - coder:
          send: "{{task}}"
          save: code
      - reviewer:
          send: "Review this code:\n{{code}}"
          save: review
    output:
      code: "{{code}}"
      review: "{{review}}"
```

```bash
vega run team.vega.yaml --workflow review --task "Write a function to validate emails"
```

---

## Installation

### Go Library

```bash
go get github.com/vegaops/vega
```

### CLI Tool

```bash
# From source
go install github.com/vegaops/vega/cmd/vega@latest

# Or clone and build
git clone https://github.com/vegaops/vega
cd vega
go build -o vega ./cmd/vega
```

### Configuration

Set your Anthropic API key:

```bash
export ANTHROPIC_API_KEY=your-key-here
```

---

## Quick Start

### Option 1: Go Library

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/vegaops/vega"
    "github.com/vegaops/vega/llm"
)

func main() {
    // Create LLM backend
    anthropic := llm.NewAnthropic()

    // Create orchestrator
    orch := vega.NewOrchestrator(vega.WithLLM(anthropic))

    // Define an agent
    agent := vega.Agent{
        Name:   "assistant",
        Model:  "claude-sonnet-4-20250514",
        System: vega.StaticPrompt("You are a helpful coding assistant."),
    }

    // Spawn a process
    proc, err := orch.Spawn(agent)
    if err != nil {
        log.Fatal(err)
    }

    // Send a message
    ctx := context.Background()
    response, err := proc.Send(ctx, "Write a hello world function in Go")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(response)

    // Cleanup
    orch.Shutdown(ctx)
}
```

### Option 2: YAML DSL

Create `assistant.vega.yaml`:

```yaml
name: Assistant

agents:
  helper:
    model: claude-sonnet-4-20250514
    system: You are a helpful assistant.
```

Run interactively:

```bash
vega repl assistant.vega.yaml
> /ask helper
> What is the capital of France?
```

---

## Features

### Erlang-Style Supervision

Processes automatically restart on failure with configurable strategies:

```go
proc, err := orch.Spawn(agent, vega.WithSupervision(vega.Supervision{
    Strategy:    vega.Restart,  // Restart, Stop, or Escalate
    MaxRestarts: 3,
    Window:      5 * time.Minute,
    Backoff: vega.BackoffConfig{
        Initial:    100 * time.Millisecond,
        Multiplier: 2.0,
        Max:        30 * time.Second,
    },
}))
```

Or in YAML:

```yaml
agents:
  worker:
    model: claude-sonnet-4-20250514
    system: You process tasks reliably.
    supervision:
      strategy: restart
      max_restarts: 3
      window: 5m
```

### Tools

Register tools for agents to use:

```go
tools := vega.NewTools()
tools.RegisterBuiltins() // read_file, write_file, run_command

// Register custom tool
tools.Register("greet", func(name string) string {
    return "Hello, " + name + "!"
})

agent := vega.Agent{
    Name:  "greeter",
    Tools: tools,
}
```

Or define in YAML:

```yaml
tools:
  fetch_weather:
    description: Get weather for a city
    params:
      - name: city
        type: string
        required: true
    implementation:
      type: http
      method: GET
      url: https://api.weather.com/v1/current
      query:
        q: "{{city}}"
```

### Streaming Responses

```go
stream, err := proc.SendStream(ctx, "Tell me a story")
if err != nil {
    log.Fatal(err)
}

for chunk := range stream.Chunks() {
    fmt.Print(chunk)
}
fmt.Println()
```

### Async Operations

```go
// Fire and forget
future := proc.SendAsync("Process this in background")

// Do other work...

// Wait for result
response, err := future.Await(ctx)
```

### Parallel Execution (DSL)

```yaml
steps:
  - parallel:
      - agent1:
          send: "Task 1"
          save: result1
      - agent2:
          send: "Task 2"
          save: result2
  - combiner:
      send: "Combine: {{result1}} and {{result2}}"
```

### Rate Limiting & Circuit Breakers

```go
orch := vega.NewOrchestrator(
    vega.WithLLM(anthropic),
    vega.WithRateLimits(map[string]vega.RateLimitConfig{
        "claude-sonnet-4-20250514": {
            RequestsPerMinute: 60,
            TokensPerMinute:   100000,
        },
    }),
)

agent := vega.Agent{
    CircuitBreaker: &vega.CircuitBreaker{
        Threshold:  5,           // Open after 5 failures
        ResetAfter: time.Minute, // Try again after 1 minute
    },
}
```

### Budget Control

```go
agent := vega.Agent{
    Budget: &vega.Budget{
        Limit:    5.0,           // $5.00 max
        OnExceed: vega.BudgetBlock,
    },
}
```

### MCP (Model Context Protocol) Servers

Connect to MCP servers to use external tools:

```go
import "github.com/vegaops/vega/mcp"

tools := vega.NewTools(
    vega.WithMCPServer(mcp.ServerConfig{
        Name:    "filesystem",
        Command: "npx",
        Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/workspace"},
    }),
    vega.WithMCPServer(mcp.ServerConfig{
        Name:    "github",
        Command: "npx",
        Args:    []string{"-y", "@modelcontextprotocol/server-github"},
        Env:     map[string]string{"GITHUB_TOKEN": os.Getenv("GITHUB_TOKEN")},
    }),
)

// Connect to all MCP servers
ctx := context.Background()
if err := tools.ConnectMCP(ctx); err != nil {
    log.Fatal(err)
}
defer tools.DisconnectMCP()

// Tools are automatically registered with prefix: servername__toolname
// e.g., "filesystem__read_file", "github__create_issue"
```

Or in YAML:

```yaml
settings:
  mcp:
    servers:
      - name: filesystem
        transport: stdio
        command: npx
        args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
      - name: github
        command: npx
        args: ["-y", "@modelcontextprotocol/server-github"]
        env:
          GITHUB_TOKEN: "${GITHUB_TOKEN}"
        timeout: 30s

agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You are a coding assistant.
    tools:
      - filesystem__*   # All tools from filesystem server
      - github__create_issue
```

### Agent Skills

Skills provide dynamic prompt injection based on message context:

```go
import "github.com/vegaops/vega/skills"

// Load skills from directories
loader := skills.NewLoader("./skills", "~/.vega/skills")
loader.Load(ctx)

// Wrap system prompt with skills
agent := vega.Agent{
    Name:   "assistant",
    Model:  "claude-sonnet-4-20250514",
    System: vega.NewSkillsPrompt(
        vega.StaticPrompt("You are a helpful assistant."),
        loader,
        vega.WithMaxActiveSkills(3),
    ),
}
```

Create skill files (`skills/code-review.skill.md`):

```markdown
---
name: code-review
description: Expert code review guidance
tags: [code, review]
triggers:
  - type: keyword
    keywords: [review, PR, pull request, code review]
  - type: pattern
    pattern: "review (this|my) (code|changes)"
---

# Code Review Expert

When reviewing code, focus on:
1. Security vulnerabilities
2. Performance issues
3. Code clarity and maintainability
4. Test coverage
```

Or configure in YAML:

```yaml
settings:
  skills:
    directories:
      - ./skills
      - ~/.vega/skills

agents:
  reviewer:
    model: claude-sonnet-4-20250514
    system: You are a code reviewer.
    skills:
      include: [code-review, security-*]
      exclude: [deprecated-*]
      max_active: 3
```

Skills are automatically matched and injected based on:
- **keyword**: Message contains specific words
- **pattern**: Message matches a regex pattern
- **always**: Always included

---

## CLI Commands

```bash
# Run a workflow
vega run team.vega.yaml --workflow my-workflow --task "Do something"

# Validate a file
vega validate team.vega.yaml --verbose

# Interactive REPL
vega repl team.vega.yaml

# Show help
vega help
```

---

## Examples

The `examples/` directory contains complete working examples:

| Example | Description |
|---------|-------------|
| [`simple-agent.vega.yaml`](examples/simple-agent.vega.yaml) | Basic single-agent chatbot |
| [`code-review.vega.yaml`](examples/code-review.vega.yaml) | Two-agent code review workflow |
| [`dev-team.vega.yaml`](examples/dev-team.vega.yaml) | Full dev team (architect, frontend, backend, reviewer, PM) |
| [`tools-demo.vega.yaml`](examples/tools-demo.vega.yaml) | Custom HTTP and exec tools |
| [`mcp-demo.vega.yaml`](examples/mcp-demo.vega.yaml) | MCP server integration |
| [`skills-demo.vega.yaml`](examples/skills-demo.vega.yaml) | Dynamic skill injection |
| [`supervision-demo.vega.yaml`](examples/supervision-demo.vega.yaml) | Fault tolerance patterns |
| [`control-flow.vega.yaml`](examples/control-flow.vega.yaml) | Conditionals, loops, parallel execution |

Run an example:

```bash
vega run examples/code-review.vega.yaml --workflow review --task "Write a binary search function"
```

---

## DSL Reference

### Settings

```yaml
settings:
  default_model: claude-sonnet-4-20250514
  sandbox: ./workspace              # Restrict file operations
  budget: "$100.00"                 # Global budget limit

  mcp:                              # MCP server configuration
    servers:
      - name: filesystem
        transport: stdio            # stdio (default), http, sse
        command: npx
        args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
        env:
          DEBUG: "true"
        timeout: 30s

  skills:                           # Global skill directories
    directories:
      - ./skills
      - ~/.vega/skills

  rate_limit:
    requests_per_minute: 60
    tokens_per_minute: 100000
```

### Agents

```yaml
agents:
  agent-name:
    model: claude-sonnet-4-20250514    # Required
    system: |                           # Required
      Your system prompt here.
    temperature: 0.7                    # Optional (0.0-1.0)
    tools:                              # Optional
      - read_file
      - write_file
      - filesystem__*                   # MCP tools (server__pattern)
    budget: "$5.00"                     # Optional
    supervision:                        # Optional
      strategy: restart
      max_restarts: 3
    skills:                             # Optional skill configuration
      directories:                      # Agent-specific skill dirs
        - ./agent-skills
      include: [coding-*, review]       # Only these skills
      exclude: [deprecated-*]           # Never these skills
      max_active: 3                     # Max skills to inject
```

### Workflows

```yaml
workflows:
  workflow-name:
    description: What this workflow does
    inputs:
      task:
        type: string
        required: true
        default: "default value"
    steps:
      - agent-name:
          send: "Message with {{task}} interpolation"
          save: variable_name
          timeout: 30s
    output: "{{variable_name}}"
```

### Control Flow

```yaml
# Conditionals
- if: "{{approved}}"
  then:
    - agent: ...
  else:
    - agent: ...

# Loops
- for: item in items
  steps:
    - agent:
        send: "Process {{item}}"

# Parallel
- parallel:
    - agent1: ...
    - agent2: ...

# Try/Catch
- try:
    - risky-agent: ...
  catch:
    - fallback-agent: ...
```

### Expression Filters

```yaml
{{name | upper}}           # UPPERCASE
{{name | lower}}           # lowercase
{{name | trim}}            # Remove whitespace
{{name | default:anon}}    # Default value
{{text | truncate:100}}    # Limit length
{{items | join:, }}        # Join array
```

---

## Documentation

| Document | Description |
|----------|-------------|
| [Quick Start](docs/QUICKSTART.md) | Get running in 5 minutes |
| [DSL Reference](docs/DSL.md) | Complete YAML syntax |
| [Go Library Spec](docs/SPEC.md) | Full API reference |
| [Tools](docs/TOOLS.md) | Built-in and custom tools |
| [MCP Servers](docs/MCP.md) | Model Context Protocol integration |
| [Skills](docs/SKILLS.md) | Dynamic prompt injection |
| [Supervision](docs/SUPERVISION.md) | Fault tolerance patterns |
| [Architecture](docs/ARCHITECTURE.md) | Internal design |

---

## Why Vega?

| Feature | Raw SDK | Other Frameworks | Vega |
|---------|---------|------------------|------|
| Supervision trees | Manual | ❌ | ✅ Built-in |
| Automatic retries | Manual | Partial | ✅ Built-in |
| Rate limiting | Manual | Manual | ✅ Built-in |
| Cost tracking | Manual | Partial | ✅ Built-in |
| MCP server support | Manual | Partial | ✅ Built-in |
| Dynamic skills | ❌ | ❌ | ✅ Built-in |
| Non-programmer friendly | ❌ | ❌ | ✅ YAML DSL |
| Parallel execution | Complex | Complex | ✅ `parallel:` |
| Config-driven | ❌ | Limited | ✅ Full YAML |

---

## Project Structure

```
vega/
├── agent.go           # Agent definition
├── process.go         # Running process with lifecycle
├── orchestrator.go    # Process management
├── supervision.go     # Fault tolerance
├── tools.go           # Tool registration
├── mcp_tools.go       # MCP server integration
├── skills.go          # Skills prompt wrapper
├── llm.go             # LLM interface
├── errors.go          # Error types
├── llm/
│   └── anthropic.go   # Anthropic backend
├── mcp/               # Model Context Protocol client
│   ├── types.go       # MCP types and JSON-RPC
│   ├── client.go      # MCP client implementation
│   ├── transport_stdio.go  # Subprocess transport
│   └── transport_http.go   # HTTP/SSE transport
├── skills/            # Agent skills system
│   ├── types.go       # Skill and trigger types
│   ├── parser.go      # SKILL.md file parser
│   ├── loader.go      # Directory scanner
│   └── matcher.go     # Keyword/pattern matching
├── dsl/
│   ├── types.go       # AST types
│   ├── parser.go      # YAML parser
│   └── interpreter.go # Workflow execution
├── cmd/vega/
│   └── main.go        # CLI entry point
├── examples/          # Example .vega.yaml files
└── docs/              # Documentation
```

---

## Contributing

Contributions welcome! Please read the code, write tests, and submit PRs.

```bash
# Run tests
go test ./...

# Build CLI
go build -o vega ./cmd/vega
```

---

## License

MIT
