# Hellotron + Vega Integration

This document describes how Hellotron would integrate with Vega as its agent orchestration layer.

## Current Hellotron Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Hellotron                                │
├─────────────────────────────────────────────────────────────┤
│  internal/agent/                                            │
│    orchestrator.go  (~800 LOC) - Process management         │
│    worker.go        (~300 LOC) - Agent execution loop       │
│    tools.go         (~400 LOC) - Tool definitions           │
├─────────────────────────────────────────────────────────────┤
│  internal/claude/                                           │
│    client.go        (~500 LOC) - API client                 │
├─────────────────────────────────────────────────────────────┤
│  internal/persona/                                          │
│    persona.go       - Tony's configuration                  │
├─────────────────────────────────────────────────────────────┤
│  internal/team/                                             │
│    team.go          - Team member loading                   │
├─────────────────────────────────────────────────────────────┤
│  tron.persona/      - Tony's YAML config                    │
│  tron.team/         - Team YAML + Markdown prompts          │
│  tron.work/         - Sandboxed workspace                   │
└─────────────────────────────────────────────────────────────┘
```

## With Vega

```
┌─────────────────────────────────────────────────────────────┐
│                     Hellotron                                │
├─────────────────────────────────────────────────────────────┤
│  internal/tron/                                             │
│    tron.go          (~100 LOC) - Thin Vega wrapper          │
│    persona.go       (~80 LOC)  - Config loading             │
│    team.go          (~60 LOC)  - Team loading               │
│    tools.go         (~150 LOC) - Domain-specific tools      │
├─────────────────────────────────────────────────────────────┤
│  github.com/yourname/vega                                   │
│    (Orchestrator, Process, Supervision, Tools, etc.)        │
├─────────────────────────────────────────────────────────────┤
│  tron.persona/      - Tony's YAML config (unchanged)        │
│  tron.team/         - Team YAML + Markdown (unchanged)      │
│  tron.tools/        - Dynamic tool definitions (NEW)        │
│  tron.work/         - Sandboxed workspace (unchanged)       │
└─────────────────────────────────────────────────────────────┘
```

**Reduction: ~2000 LOC → ~400 LOC** (excluding Vega library)

## File Structure

```
hellotron/
├── cmd/
│   └── tron/
│       └── main.go
├── internal/
│   ├── tron/
│   │   ├── tron.go           # Orchestrator wrapper
│   │   ├── persona.go        # PersonaConfig loading
│   │   ├── team.go           # Team loading
│   │   └── tools.go          # Hellotron-specific tools
│   ├── voice/                # VAPI/ElevenLabs (unchanged)
│   └── container/            # Docker management (unchanged)
├── tron.persona/
│   ├── persona.yaml          # Tony's config
│   └── memory.md             # Rolling memory
├── tron.team/
│   ├── team.yaml             # Team roster
│   └── specialists/
│       ├── gary.md
│       ├── sarah.md
│       ├── marcus.md
│       └── ...
├── tron.tools/
│   └── definitions/          # Dynamic tools (NEW)
│       ├── web_search.yaml
│       ├── send_email.yaml
│       └── ...
├── tron.knowledge/           # Playbooks, guides (unchanged)
└── tron.work/                # Sandbox (unchanged)
```

## Implementation

### tron.go - Main Orchestrator

```go
package tron

import (
    "github.com/yourname/vega"
)

type Tron struct {
    *vega.Orchestrator
    persona *PersonaConfig
    team    *Team
    tools   *vega.Tools
}

func New(personaPath, teamPath, toolsPath string) (*Tron, error) {
    // Load configuration
    persona, err := LoadPersona(personaPath)
    if err != nil {
        return nil, fmt.Errorf("load persona: %w", err)
    }

    team, err := LoadTeam(teamPath)
    if err != nil {
        return nil, fmt.Errorf("load team: %w", err)
    }

    // Create Vega orchestrator with Hellotron's settings
    orch := vega.NewOrchestrator(
        vega.WithMaxProcesses(persona.Boundaries.MaxAgents),
        vega.WithPersistence(vega.NewJSONPersistence("tron.work/agents.json")),
        vega.WithRecovery(true),
        vega.WithHealthConfig(vega.HealthConfig{
            CheckInterval:        persona.HealthMonitoring.CheckInterval,
            StaleProgressMinutes: persona.HealthMonitoring.Thresholds.StaleProgressMinutes,
            MaxIterationsWarning: persona.HealthMonitoring.Thresholds.MaxIterationsWarning,
            ErrorLoopCount:       persona.HealthMonitoring.Thresholds.ErrorLoopCount,
            CostAlertUSD:         persona.HealthMonitoring.Thresholds.CostAlertUSD,
        }),
    )

    t := &Tron{
        Orchestrator: orch,
        persona:      persona,
        team:         team,
    }

    // Initialize tools with sandbox
    t.tools = t.initTools(toolsPath)

    return t, nil
}

// Tony returns the persona as a running Vega process
func (t *Tron) Tony() (*vega.Process, error) {
    agent := t.persona.ToAgent(t)

    return t.Spawn(agent,
        vega.WithSupervision(vega.Supervision{
            Strategy:    vega.Restart,
            MaxRestarts: -1, // Always restart Tony
            Backoff: vega.BackoffStrategy{
                Initial:    100 * time.Millisecond,
                Max:        10 * time.Second,
                Multiplier: 2.0,
            },
            OnRestart: func(p *vega.Process, attempt int) {
                log.Printf("Tony restarted (attempt %d)", attempt)
            },
        }),
    )
}

// SpawnTeamMember spawns a team member as an agent
// This is called by Tony via the spawn_agent tool
func (t *Tron) SpawnTeamMember(memberName, task string, project string) (string, error) {
    agent, err := t.team.ToAgent(memberName, t.tools)
    if err != nil {
        return "", err
    }

    workDir := fmt.Sprintf("tron.work/agents/%s-%s",
        strings.ToLower(memberName),
        uuid.New().String()[:8])

    proc, err := t.Spawn(agent,
        vega.WithTask(task),
        vega.WithWorkDir(workDir),
        vega.WithSupervision(vega.Supervision{
            Strategy:    vega.Restart,
            MaxRestarts: 3,
            Window:      t.persona.Boundaries.AgentTimeout,
            Backoff: vega.BackoffStrategy{
                Initial:    1 * time.Second,
                Max:        30 * time.Second,
                Multiplier: 2.0,
            },
            OnFailure: func(p *vega.Process, err error) {
                log.Printf("Agent %s failed: %v", p.ID, err)
            },
        }),
        vega.WithTimeout(t.persona.Boundaries.AgentTimeout),
        vega.WithMaxIterations(100),
    )
    if err != nil {
        return "", err
    }

    return proc.ID, nil
}

// ListAgents returns status of all running agents
func (t *Tron) ListAgents() []AgentStatus {
    procs := t.List()
    result := make([]AgentStatus, 0, len(procs))

    for _, p := range procs {
        if p.Agent.Name == t.persona.Name {
            continue // Skip Tony himself
        }
        result = append(result, AgentStatus{
            ID:     p.ID,
            Name:   p.Agent.Name,
            Task:   p.Task,
            Status: string(p.Status()),
        })
    }

    return result
}
```

### persona.go - Tony's Configuration

```go
package tron

import (
    "github.com/yourname/vega"
    "gopkg.in/yaml.v3"
)

type PersonaConfig struct {
    Name         string `yaml:"name"`
    Role         string `yaml:"role"`
    Email        string `yaml:"email"`
    SystemPrompt string `yaml:"system_prompt"`

    Boundaries struct {
        Sandbox      string        `yaml:"sandbox"`
        MaxAgents    int           `yaml:"max_agents"`
        AgentTimeout time.Duration `yaml:"agent_timeout"`
    } `yaml:"boundaries"`

    HealthMonitoring struct {
        Enabled       bool          `yaml:"enabled"`
        CheckInterval time.Duration `yaml:"check_interval"`
        Thresholds    struct {
            StaleProgressMinutes int     `yaml:"stale_progress_minutes"`
            MaxIterationsWarning int     `yaml:"max_iterations_warning"`
            ErrorLoopCount       int     `yaml:"error_loop_count"`
            CostAlertUSD         float64 `yaml:"cost_alert_usd"`
        } `yaml:"thresholds"`
    } `yaml:"health_monitoring"`
}

func LoadPersona(path string) (*PersonaConfig, error) {
    data, err := os.ReadFile(filepath.Join(path, "persona.yaml"))
    if err != nil {
        return nil, err
    }

    var cfg PersonaConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }

    return &cfg, nil
}

// ToAgent converts PersonaConfig to a Vega Agent
func (p *PersonaConfig) ToAgent(t *Tron) vega.Agent {
    return vega.Agent{
        Name:  p.Name,
        Model: "claude-sonnet-4-20250514",

        // Dynamic system prompt - refreshes each turn with current state
        System: vega.DynamicPrompt(func() string {
            return p.buildSystemPrompt(t)
        }),

        // Tony's tools
        Tools: t.personaTools(),

        // 7-day rolling memory
        Memory: vega.NewFileMemory(
            filepath.Join("tron.persona", "memory.md"),
            7*24*time.Hour,
        ),

        // 100k token context window
        Context: vega.NewSlidingWindow(100000),
    }
}

func (p *PersonaConfig) buildSystemPrompt(t *Tron) string {
    var b strings.Builder

    // Base prompt from YAML
    b.WriteString(p.SystemPrompt)

    // Current team roster
    b.WriteString("\n\n## Your Team\n")
    for _, m := range t.team.Members {
        if m.Available {
            fmt.Fprintf(&b, "- **%s** (%s): %s\n", m.Name, m.Role, m.Specialty)
        }
    }
    for key, s := range t.team.Specialists {
        fmt.Fprintf(&b, "- **%s** (%s): %s\n", s.Name, s.Role, s.Description)
    }

    // Active agents
    agents := t.ListAgents()
    if len(agents) > 0 {
        b.WriteString("\n## Active Agents\n")
        for _, a := range agents {
            fmt.Fprintf(&b, "- %s (%s): %s - %s\n", a.ID, a.Name, a.Task, a.Status)
        }
    }

    // Could add: recent memory, alerts, active projects, etc.

    return b.String()
}
```

### team.go - Team Loading

```go
package tron

import (
    "github.com/yourname/vega"
)

type TeamMember struct {
    Name       string   `yaml:"name"`
    Role       string   `yaml:"role"`
    Specialty  string   `yaml:"specialty"`
    PromptFile string   `yaml:"prompt_file"`
    Skills     []string `yaml:"skills"`
    Knowledge  []string `yaml:"knowledge"`
    Available  bool     `yaml:"available"`
}

type Team struct {
    Members     []TeamMember          `yaml:"members"`
    Specialists map[string]TeamMember `yaml:"specialists"`

    prompts map[string]string // Loaded from .md files
}

func LoadTeam(path string) (*Team, error) {
    // Load team.yaml
    data, err := os.ReadFile(filepath.Join(path, "team.yaml"))
    if err != nil {
        return nil, err
    }

    var team Team
    if err := yaml.Unmarshal(data, &team); err != nil {
        return nil, err
    }

    // Load all prompt files
    team.prompts = make(map[string]string)

    for _, m := range team.Members {
        prompt, _ := os.ReadFile(filepath.Join(path, m.PromptFile))
        team.prompts[m.Name] = string(prompt)
    }

    for _, s := range team.Specialists {
        prompt, _ := os.ReadFile(filepath.Join(path, s.PromptFile))
        team.prompts[s.Name] = string(prompt)
    }

    return &team, nil
}

// Get returns a team member by name
func (t *Team) Get(name string) (TeamMember, bool) {
    for _, m := range t.Members {
        if m.Name == name {
            return m, true
        }
    }
    for _, s := range t.Specialists {
        if s.Name == name {
            return s, true
        }
    }
    return TeamMember{}, false
}

// ToAgent converts a team member to a Vega Agent
func (t *Team) ToAgent(name string, allTools *vega.Tools) (vega.Agent, error) {
    member, ok := t.Get(name)
    if !ok {
        return vega.Agent{}, fmt.Errorf("unknown team member: %s", name)
    }

    // Filter tools to member's allowed skills
    var tools *vega.Tools
    if len(member.Skills) > 0 {
        tools = allTools.Filter(member.Skills...)
    } else {
        // Default agent tools
        tools = allTools.Filter(
            "read_file", "write_file", "list_files",
            "project_exec", "web_search",
        )
    }

    // Build system prompt with knowledge injection
    prompt := t.prompts[name]
    if len(member.Knowledge) > 0 {
        prompt += "\n\n## Knowledge\n"
        for _, k := range member.Knowledge {
            content, _ := os.ReadFile(filepath.Join("tron.knowledge", k))
            prompt += string(content) + "\n"
        }
    }

    return vega.Agent{
        Name:    member.Name,
        Model:   "claude-sonnet-4-20250514",
        System:  vega.StaticPrompt(prompt),
        Tools:   tools,
        Context: vega.NewSlidingWindow(50000),
    }, nil
}

// Names returns all team member names
func (t *Team) Names() []string {
    names := make([]string, 0, len(t.Members)+len(t.Specialists))
    for _, m := range t.Members {
        names = append(names, m.Name)
    }
    for _, s := range t.Specialists {
        names = append(names, s.Name)
    }
    return names
}
```

### tools.go - Tool Initialization

```go
package tron

import (
    "github.com/yourname/vega"
)

func (t *Tron) initTools(toolsPath string) *vega.Tools {
    tools := vega.NewTools(
        vega.WithSandbox(t.persona.Boundaries.Sandbox),
    )

    // Core file tools (compiled)
    tools.Register("read_file", t.readFile)
    tools.Register("write_file", t.writeFile)
    tools.Register("list_files", t.listFiles)
    tools.Register("append_file", t.appendFile)

    // Project execution (compiled - needs Docker access)
    tools.Register("project_exec", t.projectExec)
    tools.Register("start_server", t.startServer)
    tools.Register("get_server_logs", t.getServerLogs)

    // Load dynamic tools from YAML
    if toolsPath != "" {
        tools.LoadDirectory(filepath.Join(toolsPath, "definitions"))
    }

    return tools
}

// personaTools returns tools available to Tony (the persona)
func (t *Tron) personaTools() *vega.Tools {
    tools := vega.NewTools()

    // Agent management
    tools.Register("spawn_agent", vega.ToolDef{
        Description: "Delegate work to a team member",
        Fn:          t.SpawnTeamMember,
        Params: vega.Params{
            "team_member": {
                Type:        "string",
                Description: "Team member name",
                Required:    true,
                Enum:        t.team.Names(),
            },
            "task": {
                Type:        "string",
                Description: "Task description",
                Required:    true,
            },
            "project": {
                Type:        "string",
                Description: "Project name (optional)",
            },
        },
    })

    tools.Register("list_agents", t.ListAgents)
    tools.Register("get_agent_status", t.GetAgentStatus)

    // Callbacks
    tools.Register("schedule_callback", t.scheduleCallback)
    tools.Register("schedule_batch_callback", t.scheduleBatchCallback)

    // Communication
    tools.Register("send_email", t.sendEmail)
    tools.Register("web_search", t.webSearch)

    // Memory
    tools.Register("save_directive", t.saveDirective)
    tools.Register("save_person_memory", t.savePersonMemory)
    tools.Register("identify_caller", t.identifyCaller)
    tools.Register("read_journal", t.readJournal)

    // Projects
    tools.Register("list_projects", t.listProjects)
    tools.Register("get_project_url", t.getProjectURL)
    tools.Register("start_server", t.startServer)
    tools.Register("get_server_logs", t.getServerLogs)

    return tools
}

// Compiled tools that need Go code

func (t *Tron) readFile(path string) (string, error) {
    // Sandbox validation handled by vega.Tools
    data, err := os.ReadFile(path)
    return string(data), err
}

func (t *Tron) writeFile(path, content string) error {
    return os.WriteFile(path, []byte(content), 0644)
}

func (t *Tron) projectExec(project, command string) (string, error) {
    // Delegate to container manager
    return t.containers.Exec(project, command)
}

// ... other compiled tools
```

### main.go - Entry Point

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/gin-gonic/gin"
    "github.com/yourname/vega/middleware"
    "hellotron/internal/tron"
    "hellotron/internal/voice"
)

func main() {
    // Initialize Tron with Vega
    t, err := tron.New(
        "tron.persona/",
        "tron.team/",
        "tron.tools/",
    )
    if err != nil {
        log.Fatalf("Failed to initialize: %v", err)
    }

    // Start Tony (supervised, always restarts)
    tony, err := t.Tony()
    if err != nil {
        log.Fatalf("Failed to start Tony: %v", err)
    }

    // HTTP server
    r := gin.Default()

    // OpenAI-compatible chat endpoint
    r.POST("/chat/completions", middleware.GinChatHandler(tony))

    // VAPI webhook
    r.POST("/vapi/webhook", voice.VAPIHandler(tony))

    // Admin endpoints
    r.GET("/agents", func(c *gin.Context) {
        c.JSON(200, t.ListAgents())
    })

    r.GET("/health", func(c *gin.Context) {
        c.JSON(200, gin.H{
            "status": "ok",
            "tony":   tony.Status(),
            "agents": len(t.List()),
        })
    })

    // Graceful shutdown
    srv := &http.Server{Addr: ":8080", Handler: r}

    go func() {
        if err := srv.ListenAndServe(); err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    // Wait for interrupt
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    log.Println("Shutting down...")

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Shutdown orchestrator (persists state)
    t.Shutdown(ctx)

    // Shutdown HTTP server
    srv.Shutdown(ctx)

    log.Println("Goodbye")
}
```

## Dynamic Tools Example

```yaml
# tron.tools/definitions/web_search.yaml
name: web_search
description: Search the web using Brave Search API
params:
  - name: query
    type: string
    description: The search query
    required: true
  - name: count
    type: integer
    description: Number of results
    default: 10
implementation:
  type: http
  method: GET
  url: https://api.search.brave.com/res/v1/web/search
  headers:
    Accept: application/json
    X-Subscription-Token: ${BRAVE_API_KEY}
  query:
    q: "{{query}}"
    count: "{{count}}"
  timeout: 30s
```

```yaml
# tron.tools/definitions/send_email.yaml
name: send_email
description: Send an email via SendGrid
params:
  - name: to
    type: string
    description: Recipient email address
    required: true
  - name: subject
    type: string
    description: Email subject
    required: true
  - name: body
    type: string
    description: Email body (plain text)
    required: true
implementation:
  type: http
  method: POST
  url: https://api.sendgrid.com/v3/mail/send
  headers:
    Authorization: Bearer ${SENDGRID_API_KEY}
    Content-Type: application/json
  body:
    personalizations:
      - to:
          - email: "{{to}}"
    from:
      email: "tony@tonycto.com"
      name: "Tony"
    subject: "{{subject}}"
    content:
      - type: text/plain
        value: "{{body}}"
```

## What Changes, What Stays

### Changes Without Recompilation

| Change | File | Recompile? |
|--------|------|------------|
| Tony's personality | `tron.persona/persona.yaml` | No |
| Add team member | `tron.team/team.yaml` + `.md` | No |
| Change member prompt | `tron.team/specialists/*.md` | No |
| Change member skills | `tron.team/team.yaml` | No |
| Add knowledge | `tron.knowledge/*` | No |
| Add API-based tool | `tron.tools/definitions/*.yaml` | No |
| Change supervision settings | `tron.persona/persona.yaml` | No |

### Changes Requiring Recompilation

| Change | Why |
|--------|-----|
| New tool type (not HTTP/exec/file) | Needs Go implementation |
| Docker integration changes | Go code |
| VAPI/voice handling changes | Go code |
| New orchestration patterns | Go code |

## Migration Path

1. **Add Vega dependency**
   ```bash
   go get github.com/yourname/vega
   ```

2. **Create tron.tools/definitions/**
   - Move web_search, send_email, etc. to YAML

3. **Rewrite internal/agent/ → internal/tron/**
   - Replace orchestrator.go with thin wrapper
   - Replace worker.go with Vega Process
   - Keep domain-specific tool implementations

4. **Update main.go**
   - Use Vega middleware for HTTP handlers

5. **Test**
   - Supervision behavior
   - Tool execution
   - Recovery on restart

6. **Delete old code**
   - internal/agent/orchestrator.go
   - internal/agent/worker.go
   - internal/claude/client.go (use Vega's LLM backend)
