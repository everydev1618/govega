package dsl

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/everydev1618/govega"
	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/internal/skills"
	"github.com/everydev1618/govega/reactive"
	"github.com/everydev1618/govega/tools"
)

// InterpreterOption configures the interpreter.
type InterpreterOption func(*Interpreter)

// WithLazySpawn defers agent process creation until first use.
// Useful for serve mode where agents are only needed when workflows run.
func WithLazySpawn() InterpreterOption {
	return func(i *Interpreter) {
		i.lazySpawn = true
	}
}

// WithLLM overrides the LLM backend used by every spawned agent. Without it,
// the interpreter builds a default backend from the environment. Primarily for
// tests that need a deterministic fake, but also usable by embedders.
func WithLLM(backend llm.LLM) InterpreterOption {
	return func(i *Interpreter) {
		i.llmOverride = backend
	}
}

// DelegationObserver is called after each agent-to-agent delegation completes.
// It receives the caller agent name, target agent name, the delegation message,
// and the response. Implementations should not block.
type DelegationObserver func(ctx context.Context, fromAgent, toAgent, message, response string)

// Interpreter executes DSL workflows.
type Interpreter struct {
	doc               *Document
	orch              *vega.Orchestrator
	agents            map[string]*vega.Process
	tools             *tools.Tools
	skillsLoader      *skills.Loader
	delegationConfigs map[string]*DelegationDef
	lazySpawn         bool
	llmOverride       llm.LLM               // set by WithLLM; applied per-agent in spawnAgent
	eventPublish      func(e events.Event)  // set by SetEventPublisher; nil = no-op (spine)
	delegationObserver DelegationObserver
	inboxBackend      InboxBackend   // for async dispatch completion notifications
	channelBackend    ChannelBackend // for posting completion summaries to channels
	memoryInjector       func(proc *vega.Process, agentName string) // injects memory into agent before send
	delegationCtxDecorator func(ctx context.Context, agentName string) context.Context // rewrites ctx before delegation
	channelPostCb      func(channelName, agent, content string, msgID int64, threadID *int64)
	onDispatchStart    func(agentName string) // fires when a dispatched agent begins working
	onDispatchComplete func(ctx context.Context, agentName, callerName, message, response string, err error) // fires when a dispatched agent finishes; callerName is the agent that called send_to_agent (may be empty); ctx carries the dispatched goroutine's values including the BYOK API key so downstream pokes can authenticate as the original caller
	onDispatchEvent    func(agentName string, ev vega.ChatEvent)                        // fires for each ChatEvent from a dispatched run, so the serve layer can stream tool calls / text deltas back to the user via SSE
	serverBaseURL      string                 // set by serve package so agents know their public URL
	yamlAgents         map[string]bool        // original YAML-defined agent names (survives reset)

	// dispatchSem caps the number of simultaneously-running dispatched
	// agent goroutines. Each dispatch holds its own conversation history
	// (~30-100K tokens) plus tool call state in memory; a fan-out of 6+
	// agents from a single orchestrator turn was triggering macOS OOM
	// kills. Default 4; tunable via WithMaxConcurrentDispatches.
	dispatchSem chan struct{}

	mu                sync.RWMutex
}

// DefaultMaxConcurrentDispatches is the default cap on simultaneous
// background dispatch goroutines.
const DefaultMaxConcurrentDispatches = 4

// SetServerBaseURL sets the base URL of the Vega server so agents can construct
// workspace URLs for deliverables.
func (i *Interpreter) SetServerBaseURL(url string) {
	i.serverBaseURL = url
	i.tools.SetBaseURL(url)
}

// SetDelegationObserver registers a callback that fires after each delegation.
func (i *Interpreter) SetDelegationObserver(fn DelegationObserver) {
	i.delegationObserver = fn
}

// Norm returns the named norm definition from the loaded document. The
// boolean is false when no norm is registered under that name.
func (i *Interpreter) Norm(name string) (*Norm, bool) {
	if i.doc == nil {
		return nil, false
	}
	n, ok := i.doc.Norms[name]
	return n, ok
}

// NewInterpreter creates a new interpreter for a document.
func NewInterpreter(doc *Document, opts ...InterpreterOption) (*Interpreter, error) {
	// Create orchestrator with settings
	orchOpts := []vega.OrchestratorOption{}

	if doc.Settings != nil {
		if doc.Settings.Sandbox != "" {
			// Note: sandbox is set on tools, not orchestrator
		}
	}

	// Create default LLM (picks OpenAI-compatible or Anthropic based on env)
	defaultLLM := llm.New()
	orchOpts = append(orchOpts, vega.WithLLM(defaultLLM))

	orch := vega.NewOrchestrator(orchOpts...)

	// Create tools
	toolOpts := []tools.ToolsOption{}
	if doc.Settings != nil && doc.Settings.Sandbox != "" {
		toolOpts = append(toolOpts, tools.WithSandbox(doc.Settings.Sandbox))
	} else {
		// Default sandbox to the shared workspace directory.
		if err := vega.EnsureHome(); err == nil {
			toolOpts = append(toolOpts, tools.WithSandbox(vega.WorkspacePath()))
		}
	}

	// Add MCP servers if configured
	if doc.Settings != nil && doc.Settings.MCP != nil {
		for _, serverDef := range doc.Settings.MCP.Servers {
			var config mcp.ServerConfig

			if serverDef.FromRegistry {
				// Resolve from registry
				entry, ok := mcp.Lookup(serverDef.Name)
				if !ok {
					return nil, fmt.Errorf("MCP server %q not found in registry", serverDef.Name)
				}
				config = entry.ToServerConfig(expandEnvVars(serverDef.Env))

				// Merge overrides from DSL
				if serverDef.Transport != "" {
					config.Transport = mcp.TransportType(serverDef.Transport)
				}
				if len(serverDef.Args) > 0 {
					config.Args = append(config.Args, serverDef.Args...)
				}
				if serverDef.Timeout != "" {
					if d, err := time.ParseDuration(serverDef.Timeout); err == nil {
						config.Timeout = d
					}
				}
			} else {
				// Full config from DSL
				config = mcp.ServerConfig{
					Name:    serverDef.Name,
					Command: serverDef.Command,
					Args:    serverDef.Args,
					Env:     expandEnvVars(serverDef.Env),
					URL:     serverDef.URL,
					Headers: serverDef.Headers,
				}
				if serverDef.Transport != "" {
					config.Transport = mcp.TransportType(serverDef.Transport)
				}
				if serverDef.Timeout != "" {
					if d, err := time.ParseDuration(serverDef.Timeout); err == nil {
						config.Timeout = d
					}
				}
			}

			toolOpts = append(toolOpts, tools.WithMCPServer(config))
		}
	}

	t := tools.NewTools(toolOpts...)
	t.RegisterBuiltins()
	// Sandbox tools (spawn/run/write/destroy fly machines for user-built apps)
	// are no-ops unless FLY_SANDBOX_TOKEN is in the environment.
	tools.RegisterSandboxTools(t)

	// Register custom tools defined in the YAML tools: section.
	for name, td := range doc.Tools {
		if td.Implementation == nil {
			continue
		}
		// Expand ${ENV_VAR} references in tool implementation fields.
		headers := make(map[string]string, len(td.Implementation.Headers))
		for k, v := range td.Implementation.Headers {
			headers[k] = os.ExpandEnv(v)
		}
		query := make(map[string]string, len(td.Implementation.Query))
		for k, v := range td.Implementation.Query {
			query[k] = os.ExpandEnv(v)
		}
		dynDef := tools.DynamicToolDef{
			Name:        name,
			Description: td.Description,
			Implementation: tools.DynamicToolImpl{
				Type:    td.Implementation.Type,
				Method:  td.Implementation.Method,
				URL:     os.ExpandEnv(td.Implementation.URL),
				Headers: headers,
				Query:   query,
				Body:    td.Implementation.Body,
				Command: td.Implementation.Command,
				Timeout: td.Implementation.Timeout,
			},
		}
		for _, p := range td.Params {
			dynDef.Params = append(dynDef.Params, tools.DynamicParamDef{
				Name:        p.Name,
				Type:        p.Type,
				Description: p.Description,
				Required:    p.Required,
				Default:     p.Default,
				Enum:        p.Enum,
			})
		}
		if err := t.RegisterDynamicTool(dynDef); err != nil {
			return nil, fmt.Errorf("register tool %s: %w", name, err)
		}
	}

	// Connect MCP servers
	if doc.Settings != nil && doc.Settings.MCP != nil && len(doc.Settings.MCP.Servers) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := t.ConnectMCP(ctx); err != nil {
			slog.Warn("mcp: connection phase completed with errors", "error", err)
		}
	}

	// Initialize skills loader
	var skillsLoader *skills.Loader
	if doc.Settings != nil && doc.Settings.Skills != nil && len(doc.Settings.Skills.Directories) > 0 {
		skillsLoader = skills.NewLoader(doc.Settings.Skills.Directories...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		skillsLoader.Load(ctx)
	}

	// Record which agents came from the YAML file — these survive reset.
	yamlAgents := make(map[string]bool, len(doc.Agents))
	for name := range doc.Agents {
		yamlAgents[name] = true
	}

	interp := &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             t,
		skillsLoader:      skillsLoader,
		delegationConfigs: make(map[string]*DelegationDef),
		yamlAgents:        yamlAgents,
		dispatchSem:       make(chan struct{}, DefaultMaxConcurrentDispatches),
	}

	for _, opt := range opts {
		opt(interp)
	}

	// Spawn agents upfront unless lazy spawn is enabled.
	if !interp.lazySpawn {
		for name, agentDef := range doc.Agents {
			if err := interp.spawnAgent(name, agentDef); err != nil {
				return nil, fmt.Errorf("spawn agent %s: %w", name, err)
			}
		}
	}

	return interp, nil
}

// spawnAgent creates a Vega process for a DSL agent.
func (i *Interpreter) spawnAgent(name string, def *Agent) error {
	// Build the base system string, enriching with team section if needed.
	systemStr := def.System

	// Compose referenced norm (writing/style guidance) into the base
	// prompt. Appended after the agent's own system block so the agent's
	// role definition leads and the norm refines how it writes. The
	// parser already validates that `norm` resolves to a defined norm.
	if def.Norm != "" {
		if n, ok := i.doc.Norms[def.Norm]; ok && strings.TrimSpace(n.System) != "" {
			systemStr += "\n\n## Writing norm: " + def.Norm + "\n\n" + strings.TrimSpace(n.System)
		}
	}

	if len(def.Team) > 0 {
		// Store delegation config for this agent.
		if def.Delegation != nil {
			i.mu.Lock()
			i.delegationConfigs[name] = def.Delegation
			i.mu.Unlock()
		}

		RegisterDelegateTool(i.tools, func(ctx context.Context, agentName string, message string) (string, error) {
			// Enrich the message with caller context if configured.
			callerProc := vega.ProcessFromContext(ctx)
			if callerProc != nil && callerProc.Agent != nil {
				i.mu.RLock()
				delConfig := i.delegationConfigs[callerProc.Agent.Name]
				i.mu.RUnlock()
				if delConfig != nil && delConfig.ContextWindow > 0 {
					dc := ExtractCallerContext(callerProc, delConfig)
					message = FormatDelegationContext(dc, message)
				}
			}
			return i.SendToAgent(ctx, agentName, message)
		}, func(ctx context.Context) []string {
			proc := vega.ProcessFromContext(ctx)
			if proc != nil && proc.Agent != nil {
				i.mu.RLock()
				defer i.mu.RUnlock()
				if def, ok := i.doc.Agents[proc.Agent.Name]; ok {
					return def.Team
				}
			}
			return nil
		})

		bbEnabled := def.Delegation != nil && def.Delegation.Blackboard
		descs := make(map[string]string, len(def.Team))
		for _, member := range def.Team {
			if memberDef, ok := i.doc.Agents[member]; ok {
				if first, _, ok := strings.Cut(strings.TrimSpace(memberDef.System), "\n"); ok {
					descs[member] = first
				} else {
					descs[member] = strings.TrimSpace(memberDef.System)
				}
			}
		}
		systemStr = BuildTeamPrompt(systemStr, def.Team, descs, bbEnabled)
	}

	// Resolve knowledge and prepend to system prompt if configured.
	if len(def.Knowledge) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		knowledgeSection := i.resolveKnowledge(ctx, def.Knowledge)
		cancel()
		if knowledgeSection != "" {
			systemStr = knowledgeSection + "\n\n" + systemStr
		}
	}

	// Inject current date so agents know what day it is.
	systemStr += "\nToday's date is " + time.Now().Format("January 2, 2006") + "."

	// Universal brevity directive — applies to ALL agents.
	systemStr += "\n\n## Communication style\nBe direct and concise. Lead with the answer, not the reasoning. 1-3 sentences for simple responses. Use bullet points only when listing concrete items — never for padding. No filler phrases, no restating the question, no sign-offs. The user's time is sacred."

	// Inject workspace path and deliverable URL so agents know where files go and how to serve them.
	systemStr += "\nYour working directory is " + vega.WorkspacePath()
	if i.serverBaseURL != "" {
		systemStr += fmt.Sprintf("\n\n## Delivering work product\nFiles you write to your working directory are served at %s/workspace/. For example, if you write a website to `%s/mysite/index.html`, it will be accessible at `%s/workspace/mysite/index.html`. When you produce deliverables (websites, documents, images), ALWAYS report the full URL so the user can view them immediately.", i.serverBaseURL, vega.WorkspacePath(), i.serverBaseURL)
		systemStr += "\n\nFor dynamic applications (Node.js, Python, etc.), use `start_service` to run dev servers in the background. The service keeps running until stopped with `stop_service`. Use `service_logs` to check output and `list_services` to see what's running. Always report the URL where the service is accessible."
	}

	// Inject connected MCP tool summary so agents know what external data
	// sources are available. Group by server with descriptions.
	type mcpTool struct {
		name string
		desc string
	}
	mcpServers := make(map[string][]mcpTool)
	for _, schema := range i.tools.Schema() {
		if parts := strings.SplitN(schema.Name, "__", 2); len(parts) == 2 {
			desc := schema.Description
			if len(desc) > 80 {
				desc = desc[:80] + "..."
			}
			mcpServers[parts[0]] = append(mcpServers[parts[0]], mcpTool{parts[1], desc})
		}
	}
	if len(mcpServers) > 0 {
		systemStr += "\n\n## Connected data sources\nYou have live access to external systems. When asked about real data, you MUST call these tools — do not say you lack access or tell the user to check manually.\n"
		for server, tools := range mcpServers {
			systemStr += fmt.Sprintf("\n**%s** (%d tools):\n", server, len(tools))
			for _, t := range tools {
				if t.desc != "" {
					systemStr += fmt.Sprintf("  - %s — %s\n", t.name, t.desc)
				} else {
					systemStr += fmt.Sprintf("  - %s\n", t.name)
				}
			}
		}
	}

	// Build base system prompt
	var systemPrompt vega.SystemPrompt = vega.StaticPrompt(systemStr)

	// Wrap with skills if configured
	if def.Skills != nil {
		var loader *skills.Loader

		// Use agent-specific directories if provided, otherwise use global
		if len(def.Skills.Directories) > 0 {
			loader = skills.NewLoader(def.Skills.Directories...)
		} else if i.skillsLoader != nil {
			loader = i.skillsLoader
		}

		if loader != nil {
			// Apply include/exclude filters
			if len(def.Skills.Include) > 0 || len(def.Skills.Exclude) > 0 {
				loader.SetFilters(def.Skills.Include, def.Skills.Exclude)
			}

			// Load skills if not already loaded
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			loader.Load(ctx)
			cancel()

			// Create skills prompt
			opts := []vega.SkillsPromptOption{}
			if def.Skills.MaxActive > 0 {
				opts = append(opts, vega.WithMaxActiveSkills(def.Skills.MaxActive))
			}
			systemPrompt = vega.NewSkillsPrompt(vega.StaticPrompt(systemStr), loader, opts...)
		}
	}

	// Build agent tools — filter if agent has explicit tools, then wire skill-tools.
	// Always include connected MCP/builtin server tools (prefixed with "server__")
	// so agents can use any connected external service.
	agentTools := i.tools
	if len(def.Tools) > 0 {
		toolNames := append([]string{}, def.Tools...)
		// Always-available bucket — tools the host opted into that every
		// agent gets regardless of its persisted allow-list:
		//   - "__"-prefixed MCP/builtin server tools
		//   - sandbox tools (spawn_app etc.), registered only when
		//     FLY_SANDBOX_TOKEN is set. Without this, agents created before
		//     the sandbox surface shipped silently fall back to
		//     `start_service` + `python -m http.server`, handing the user a
		//     localhost URL instead of a public *.fly.dev URL.
		//   - wiki memory tools (memory_read/list/search/write/append/edit).
		//     Every agent shares the user wiki — gating these behind a
		//     per-agent allow-list left custom personas read-only-by-prompt:
		//     they'd see the injected MEMORY.md but couldn't drill into
		//     linked pages or write back. Memory is first-class for all.
		always := make(map[string]bool, 16)
		for _, n := range tools.SandboxToolNames() {
			always[n] = true
		}
		for _, n := range tools.WikiMemoryToolNames() {
			always[n] = true
		}
		for _, schema := range i.tools.Schema() {
			if strings.Contains(schema.Name, "__") || always[schema.Name] {
				toolNames = append(toolNames, schema.Name)
			}
		}
		agentTools = i.tools.Filter(toolNames...)
	}

	// If agent has skills, set skillsRef so skill-declared tools augment the schema dynamically.
	if sp, ok := systemPrompt.(*vega.SkillsPrompt); ok {
		agentTools = agentTools.WithSkillsRef(sp)
	}

	// Build agent config
	agent := vega.Agent{
		Name:          name,
		Model:         def.Model,
		FallbackModel: def.FallbackModel,
		Models:        def.Models,
		System:        systemPrompt,
		Tools:         agentTools,
		LLM:           i.llmOverride, // nil unless WithLLM was set; orch default otherwise
	}

	if def.Temperature != nil {
		agent.Temperature = def.Temperature
	}
	if def.MaxTokens > 0 {
		agent.MaxTokens = def.MaxTokens
	}
	if def.Effort != "" {
		agent.Effort = def.Effort
	}

	// Reactive triggers: events this agent wakes to (§4.2 / D1).
	for _, td := range def.Triggers {
		agent.Triggers = append(agent.Triggers, reactive.Trigger{
			On:     td.On,
			Where:  td.Where,
			Gate:   td.Gate,
			Prompt: td.Prompt,
		})
	}

	// Map DSL retry config to core retry policy
	if def.Retry != nil {
		bp := vega.BackoffExponential
		switch def.Retry.Backoff {
		case "linear":
			bp = vega.BackoffLinear
		case "constant":
			bp = vega.BackoffConstant
		}
		agent.Retry = &vega.RetryPolicy{
			MaxAttempts: def.Retry.MaxAttempts,
			Backoff: vega.BackoffConfig{
				Initial:    time.Second,
				Multiplier: 2.0,
				Type:       bp,
			},
		}
	}

	// Map DSL rate limit to core
	if def.RateLimit != nil {
		agent.RateLimit = &vega.RateLimit{
			RequestsPerMinute: def.RateLimit.RequestsPerMinute,
			TokensPerMinute:   def.RateLimit.TokensPerMinute,
		}
	}

	// Map DSL circuit breaker to core
	if def.CircuitBreaker != nil {
		resetAfter := 30 * time.Second
		if def.CircuitBreaker.ResetAfter != "" {
			if d, err := time.ParseDuration(def.CircuitBreaker.ResetAfter); err == nil {
				resetAfter = d
			}
		}
		halfOpenMax := def.CircuitBreaker.HalfOpenMax
		if halfOpenMax <= 0 {
			halfOpenMax = 1
		}
		agent.CircuitBreaker = &vega.CircuitBreaker{
			Threshold:   def.CircuitBreaker.Threshold,
			ResetAfter:  resetAfter,
			HalfOpenMax: halfOpenMax,
		}
	}

	// Handle extends (merge parent config)
	if def.Extends != "" {
		parent, ok := i.doc.Agents[def.Extends]
		if ok {
			if agent.Model == "" {
				agent.Model = parent.Model
			}
			// Could merge other fields too
		}
	}

	// Apply defaults from settings
	if agent.Model == "" && i.doc.Settings != nil {
		agent.Model = i.doc.Settings.DefaultModel
	}

	// Build spawn options
	opts := []vega.SpawnOption{}

	if def.Supervision != nil {
		sup := vega.Supervision{
			MaxRestarts: def.Supervision.MaxRestarts,
		}
		switch def.Supervision.Strategy {
		case "restart":
			sup.Strategy = vega.Restart
		case "stop":
			sup.Strategy = vega.Stop
		case "escalate":
			sup.Strategy = vega.Escalate
		}
		opts = append(opts, vega.WithSupervision(sup))
	}

	// Spawn the process
	proc, err := i.orch.Spawn(agent, opts...)
	if err != nil {
		return err
	}

	i.mu.Lock()
	i.agents[name] = proc
	i.mu.Unlock()

	// Auto-create team group and join leader process.
	if len(def.Team) > 0 {
		groupName := "team:" + name
		group := i.orch.GetOrCreateGroup(groupName)
		group.Join(proc)

		// Register blackboard tools for every team. The blackboard is the
		// canonical "what's in flight, what's done" store that prevents
		// duplicate work across team members. Tool registration is
		// idempotent. The resolver picks the correct team blackboard
		// based on the calling process's group membership.
		resolver := i.teamGroupResolver(groupName)
		i.registerToolIfAbsent("bb_read", NewBlackboardReadTool(resolver))
		i.registerToolIfAbsent("bb_write", NewBlackboardWriteTool(resolver))
		i.registerToolIfAbsent("bb_list", NewBlackboardListTool(resolver))
	}

	// Check if this agent is a team member of another agent and join that group.
	for leaderName, leaderDef := range i.doc.Agents {
		for _, member := range leaderDef.Team {
			if member == name {
				groupName := "team:" + leaderName
				group := i.orch.GetOrCreateGroup(groupName)
				group.Join(proc)
			}
		}
	}

	return nil
}

// teamGroupResolver returns a GroupResolver that finds the team group for the calling process.
func (i *Interpreter) teamGroupResolver(defaultGroup string) GroupResolver {
	return func(ctx context.Context) *vega.ProcessGroup {
		proc := vega.ProcessFromContext(ctx)
		if proc != nil {
			// Check if the process belongs to any team group.
			for _, gName := range proc.Groups() {
				if len(gName) > 5 && gName[:5] == "team:" {
					group, ok := i.orch.GetGroup(gName)
					if ok {
						return group
					}
				}
			}
		}
		// Fallback to the default group.
		group, _ := i.orch.GetGroup(defaultGroup)
		return group
	}
}

// registerToolIfAbsent registers a tool only if no tool with that name exists.
func (i *Interpreter) registerToolIfAbsent(name string, def tools.ToolDef) {
	for _, ts := range i.tools.Schema() {
		if ts.Name == name {
			return
		}
	}
	i.tools.Register(name, def)
}

// RunWorkflow executes a workflow by name.
func (i *Interpreter) RunWorkflow(ctx context.Context, name string, inputs map[string]any) (any, error) {
	wf, ok := i.doc.Workflows[name]
	if !ok {
		return nil, vega.ErrWorkflowNotFound
	}

	// Validate inputs
	for inputName, inputDef := range wf.Inputs {
		if inputDef.Required {
			if _, ok := inputs[inputName]; !ok {
				if inputDef.Default != nil {
					inputs[inputName] = inputDef.Default
				} else {
					return nil, &ValidationError{
						Field:   inputName,
						Message: "required input missing",
					}
				}
			}
		}
	}

	// Create execution context
	execCtx := &ExecutionContext{
		Inputs:    inputs,
		Variables: make(map[string]any),
		StartTime: time.Now(),
	}

	// Copy inputs to variables
	for k, v := range inputs {
		execCtx.Variables[k] = v
	}

	// Execute steps
	for idx, step := range wf.Steps {
		execCtx.CurrentStep = idx

		result, err := i.executeStep(ctx, &step, execCtx)
		if err != nil {
			if step.ContinueOnError {
				execCtx.Variables["error"] = err.Error()
				continue
			}
			return nil, fmt.Errorf("step %d: %w", idx, err)
		}

		// Handle early return
		if step.Return != "" {
			return i.evaluateExpression(step.Return, execCtx)
		}

		// Save result if step has save
		if step.Save != "" && result != nil {
			execCtx.Variables[step.Save] = result
		}
	}

	// Evaluate output
	if wf.Output != nil {
		return i.evaluateOutput(wf.Output, execCtx)
	}

	// Return last saved variable or nil
	return execCtx.Variables["result"], nil
}

// executeStep executes a single workflow step.
func (i *Interpreter) executeStep(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	// Check condition
	if step.If != "" {
		result, err := i.evaluateCondition(step.If, execCtx)
		if err != nil {
			return nil, fmt.Errorf("evaluate condition: %w", err)
		}
		if !result {
			return nil, nil // Skip step
		}
	}

	// Handle different step types
	switch {
	case step.Condition != "": // if/then/else
		return i.executeConditional(ctx, step, execCtx)

	case len(step.Parallel) > 0:
		return i.executeParallel(ctx, step, execCtx)

	case step.Repeat != nil:
		return i.executeRepeat(ctx, step, execCtx)

	case step.ForEach != "":
		return i.executeForEach(ctx, step, execCtx)

	case step.Workflow != "":
		return i.executeSubWorkflow(ctx, step, execCtx)

	case step.Set != nil:
		return i.executeSet(step, execCtx)

	case step.Return != "":
		return i.evaluateExpression(step.Return, execCtx)

	case len(step.Try) > 0:
		return i.executeTryCatch(ctx, step, execCtx)

	case step.Agent != "":
		return i.executeAgentStep(ctx, step, execCtx)

	default:
		return nil, nil
	}
}

// ensureAgent spawns an agent process on demand if it doesn't exist yet.
// If the existing process has failed (e.g. due to context cancellation), it is
// removed and a fresh process is spawned so callers don't get stuck.
func (i *Interpreter) ensureAgent(name string) (*vega.Process, error) {
	i.mu.RLock()
	proc, ok := i.agents[name]
	i.mu.RUnlock()
	if ok && proc.Status() != vega.StatusFailed {
		return proc, nil
	}

	// Remove the failed process from the map before respawning.
	if ok {
		i.mu.Lock()
		delete(i.agents, name)
		i.mu.Unlock()
	}

	agentDef, exists := i.doc.Agents[name]
	if !exists {
		return nil, fmt.Errorf("agent '%s' not found", name)
	}

	if err := i.spawnAgent(name, agentDef); err != nil {
		return nil, fmt.Errorf("spawn agent %s: %w", name, err)
	}

	i.mu.RLock()
	proc = i.agents[name]
	i.mu.RUnlock()
	return proc, nil
}

// executeAgentStep sends a message to an agent.
func (i *Interpreter) executeAgentStep(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	proc, err := i.ensureAgent(step.Agent)
	if err != nil {
		return nil, err
	}

	// Interpolate the message
	message, err := i.interpolate(step.Send, execCtx)
	if err != nil {
		return nil, fmt.Errorf("interpolate message: %w", err)
	}

	// Apply timeout if specified
	if step.Timeout != "" {
		dur, err := time.ParseDuration(step.Timeout)
		if err == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, dur)
			defer cancel()
		}
	}

	// Send message
	response, err := proc.Send(ctx, message)
	if err != nil {
		return nil, err
	}

	// Parse response if format specified
	if step.Format == "json" {
		// TODO: Parse JSON response
	}

	return response, nil
}

// executeConditional handles if/then/else.
func (i *Interpreter) executeConditional(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	result, err := i.evaluateCondition(step.Condition, execCtx)
	if err != nil {
		return nil, err
	}

	var steps []Step
	if result {
		steps = step.Then
	} else {
		steps = step.Else
	}

	var lastResult any
	for _, s := range steps {
		lastResult, err = i.executeStep(ctx, &s, execCtx)
		if err != nil {
			return nil, err
		}
		if s.Save != "" && lastResult != nil {
			execCtx.Variables[s.Save] = lastResult
		}
	}

	return lastResult, nil
}

// executeParallel runs steps in parallel.
func (i *Interpreter) executeParallel(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	var wg sync.WaitGroup
	results := make([]any, len(step.Parallel))
	errors := make([]error, len(step.Parallel))

	for idx, s := range step.Parallel {
		wg.Add(1)
		go func(idx int, s Step) {
			defer wg.Done()

			// Create a copy of execCtx for this goroutine
			localCtx := &ExecutionContext{
				Inputs:    execCtx.Inputs,
				Variables: copyMap(execCtx.Variables),
			}

			result, err := i.executeStep(ctx, &s, localCtx)
			results[idx] = result
			errors[idx] = err

			// Save result to shared context
			if s.Save != "" && result != nil {
				i.mu.Lock()
				execCtx.Variables[s.Save] = result
				i.mu.Unlock()
			}
		}(idx, s)
	}

	wg.Wait()

	// Check for errors
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// executeRepeat handles repeat-until loops.
func (i *Interpreter) executeRepeat(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	maxIterations := step.Repeat.Max
	if maxIterations == 0 {
		maxIterations = 100 // Safety limit
	}

	var lastResult any
	for iteration := 0; iteration < maxIterations; iteration++ {
		execCtx.LoopState = &LoopState{
			Index: iteration,
			Count: iteration + 1,
			First: iteration == 0,
		}

		// Execute steps
		for _, s := range step.Repeat.Steps {
			var err error
			lastResult, err = i.executeStep(ctx, &s, execCtx)
			if err != nil {
				return nil, err
			}
			if s.Save != "" && lastResult != nil {
				execCtx.Variables[s.Save] = lastResult
			}
		}

		// Check until condition
		if step.Repeat.Until != "" {
			done, err := i.evaluateCondition(step.Repeat.Until, execCtx)
			if err != nil {
				return nil, err
			}
			if done {
				break
			}
		}
	}

	execCtx.LoopState = nil
	return lastResult, nil
}

// executeForEach handles for-each loops.
func (i *Interpreter) executeForEach(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	// Parse "item in items"
	parts := strings.SplitN(step.ForEach, " in ", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid for syntax: %s", step.ForEach)
	}

	itemVar := strings.TrimSpace(parts[0])
	collectionExpr := strings.TrimSpace(parts[1])

	// Get collection
	collection, err := i.evaluateExpression(collectionExpr, execCtx)
	if err != nil {
		return nil, err
	}

	items, ok := collection.([]any)
	if !ok {
		return nil, fmt.Errorf("for-each requires array, got %T", collection)
	}

	var results []any
	for idx, item := range items {
		// Honor cancellation between iterations.
		select {
		case <-ctx.Done():
			execCtx.LoopState = nil
			return results, ctx.Err()
		default:
		}

		execCtx.LoopState = &LoopState{
			Index: idx,
			Count: idx + 1,
			Item:  item,
			First: idx == 0,
			Last:  idx == len(items)-1,
		}
		execCtx.Variables[itemVar] = item

		// Execute the loop body once per item.
		var iterResult any
		for bi := range step.Steps {
			s := &step.Steps[bi]
			res, err := i.executeStep(ctx, s, execCtx)
			if err != nil {
				execCtx.LoopState = nil
				return results, fmt.Errorf("for-each item %d: %w", idx, err)
			}
			if s.Save != "" && res != nil {
				execCtx.Variables[s.Save] = res
			}
			iterResult = res
		}
		results = append(results, iterResult)
	}

	execCtx.LoopState = nil
	return results, nil
}

// executeSubWorkflow calls another workflow.
func (i *Interpreter) executeSubWorkflow(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	// Interpolate inputs
	inputs := make(map[string]any)
	for k, v := range step.With {
		if s, ok := v.(string); ok && ContainsExpression(s) {
			interpolated, err := i.interpolate(s, execCtx)
			if err != nil {
				return nil, err
			}
			inputs[k] = interpolated
		} else {
			inputs[k] = v
		}
	}

	return i.RunWorkflow(ctx, step.Workflow, inputs)
}

// executeSet handles variable assignment.
func (i *Interpreter) executeSet(step *Step, execCtx *ExecutionContext) (any, error) {
	for k, v := range step.Set {
		if s, ok := v.(string); ok && ContainsExpression(s) {
			interpolated, err := i.interpolate(s, execCtx)
			if err != nil {
				return nil, err
			}
			execCtx.Variables[k] = interpolated
		} else {
			execCtx.Variables[k] = v
		}
	}
	return nil, nil
}

// executeTryCatch handles try/catch blocks.
func (i *Interpreter) executeTryCatch(ctx context.Context, step *Step, execCtx *ExecutionContext) (any, error) {
	var lastResult any
	var tryErr error

	// Execute try steps
	for _, s := range step.Try {
		var err error
		lastResult, err = i.executeStep(ctx, &s, execCtx)
		if err != nil {
			tryErr = err
			break
		}
		if s.Save != "" && lastResult != nil {
			execCtx.Variables[s.Save] = lastResult
		}
	}

	// If error, execute catch
	if tryErr != nil {
		execCtx.Variables["error"] = tryErr.Error()
		for _, s := range step.Catch {
			var err error
			lastResult, err = i.executeStep(ctx, &s, execCtx)
			if err != nil {
				return nil, err // Error in catch
			}
			if s.Save != "" && lastResult != nil {
				execCtx.Variables[s.Save] = lastResult
			}
		}
	}

	return lastResult, nil
}

// interpolate replaces {{...}} expressions in a string.
func (i *Interpreter) interpolate(template string, execCtx *ExecutionContext) (string, error) {
	result := exprPattern.ReplaceAllStringFunc(template, func(match string) string {
		// Extract expression
		expr := strings.TrimPrefix(match, "{{")
		expr = strings.TrimSuffix(expr, "}}")
		expr = strings.TrimSpace(expr)

		// Evaluate
		val, err := i.evaluateExpression(expr, execCtx)
		if err != nil {
			return match // Keep original on error
		}

		return fmt.Sprint(val)
	})

	return result, nil
}

// evaluateExpression evaluates a simple expression.
func (i *Interpreter) evaluateExpression(expr string, execCtx *ExecutionContext) (any, error) {
	expr = strings.TrimSpace(expr)

	// Handle pipe operators
	if strings.Contains(expr, "|") {
		parts := strings.SplitN(expr, "|", 2)
		baseExpr := strings.TrimSpace(parts[0])
		filter := strings.TrimSpace(parts[1])

		baseVal, err := i.evaluateExpression(baseExpr, execCtx)
		if err != nil {
			return nil, err
		}

		return i.applyFilter(baseVal, filter, execCtx)
	}

	// Handle simple variable lookup
	if val, ok := execCtx.Variables[expr]; ok {
		return val, nil
	}

	// Handle input lookup
	if val, ok := execCtx.Inputs[expr]; ok {
		return val, nil
	}

	// Handle loop state
	if execCtx.LoopState != nil {
		switch expr {
		case "loop.index":
			return execCtx.LoopState.Index, nil
		case "loop.count":
			return execCtx.LoopState.Count, nil
		case "loop.first":
			return execCtx.LoopState.First, nil
		case "loop.last":
			return execCtx.LoopState.Last, nil
		case "item":
			return execCtx.LoopState.Item, nil
		}
	}

	// Handle built-in variables
	switch expr {
	case "date":
		return time.Now().Format("2006-01-02"), nil
	case "time":
		return time.Now().Format("15:04:05"), nil
	}

	// Handle dotted paths (e.g., "step1.output")
	if strings.Contains(expr, ".") {
		parts := strings.Split(expr, ".")
		val, ok := execCtx.Variables[parts[0]]
		if !ok {
			return nil, fmt.Errorf("undefined variable: %s", parts[0])
		}

		// Navigate path
		for _, part := range parts[1:] {
			if m, ok := val.(map[string]any); ok {
				val = m[part]
			} else {
				return nil, fmt.Errorf("cannot access %s on %T", part, val)
			}
		}
		return val, nil
	}

	// Return as literal string if not found
	return expr, nil
}

// evaluateCondition evaluates a boolean condition.
func (i *Interpreter) evaluateCondition(expr string, execCtx *ExecutionContext) (bool, error) {
	expr = strings.TrimSpace(expr)

	// Handle 'in' operator
	if strings.Contains(expr, " in ") {
		parts := strings.SplitN(expr, " in ", 2)
		needle := strings.Trim(strings.TrimSpace(parts[0]), "'\"")
		haystackExpr := strings.TrimSpace(parts[1])

		haystack, err := i.evaluateExpression(haystackExpr, execCtx)
		if err != nil {
			return false, err
		}

		if s, ok := haystack.(string); ok {
			return strings.Contains(s, needle), nil
		}

		return false, nil
	}

	// Handle 'not in' operator
	if strings.Contains(expr, " not in ") {
		parts := strings.SplitN(expr, " not in ", 2)
		needle := strings.Trim(strings.TrimSpace(parts[0]), "'\"")
		haystackExpr := strings.TrimSpace(parts[1])

		haystack, err := i.evaluateExpression(haystackExpr, execCtx)
		if err != nil {
			return false, err
		}

		if s, ok := haystack.(string); ok {
			return !strings.Contains(s, needle), nil
		}

		return true, nil
	}

	// Handle simple boolean variable
	val, err := i.evaluateExpression(expr, execCtx)
	if err != nil {
		return false, err
	}

	switch v := val.(type) {
	case bool:
		return v, nil
	case string:
		return v != "", nil
	case int:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return val != nil, nil
	}
}

// applyFilter applies a filter function to a value.
func (i *Interpreter) applyFilter(val any, filter string, execCtx *ExecutionContext) (any, error) {
	// Parse filter name and args
	filterName := filter
	var filterArg string

	if idx := strings.Index(filter, ":"); idx != -1 {
		filterName = filter[:idx]
		filterArg = filter[idx+1:]
	}

	s := fmt.Sprint(val)

	switch filterName {
	case "upper":
		return strings.ToUpper(s), nil
	case "lower":
		return strings.ToLower(s), nil
	case "trim":
		return strings.TrimSpace(s), nil
	case "default":
		if s == "" {
			return filterArg, nil
		}
		return s, nil
	case "lines":
		return len(strings.Split(s, "\n")), nil
	case "words":
		return len(strings.Fields(s)), nil
	case "truncate":
		// Parse max length from arg
		var maxLen int
		fmt.Sscanf(filterArg, "%d", &maxLen)
		if maxLen > 0 && len(s) > maxLen {
			return s[:maxLen] + "...", nil
		}
		return s, nil
	case "join":
		if arr, ok := val.([]any); ok {
			strs := make([]string, len(arr))
			for i, v := range arr {
				strs[i] = fmt.Sprint(v)
			}
			sep := filterArg
			if sep == "" {
				sep = ", "
			}
			return strings.Join(strs, sep), nil
		}
		return s, nil
	default:
		return val, nil
	}
}

// evaluateOutput evaluates the workflow output.
func (i *Interpreter) evaluateOutput(output any, execCtx *ExecutionContext) (any, error) {
	switch v := output.(type) {
	case string:
		return i.interpolate(v, execCtx)
	case map[string]any:
		result := make(map[string]any)
		for k, val := range v {
			if s, ok := val.(string); ok {
				interpolated, err := i.interpolate(s, execCtx)
				if err != nil {
					return nil, err
				}
				result[k] = interpolated
			} else {
				result[k] = val
			}
		}
		return result, nil
	default:
		return output, nil
	}
}

// Shutdown stops all agents and disconnects MCP servers.
func (i *Interpreter) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Disconnect MCP servers
	if i.tools != nil {
		i.tools.DisconnectMCP()
	}

	i.orch.Shutdown(ctx)
}

// StartIdleEviction launches a background sweep that periodically
// removes agent processes from the registry whose last_active_at is
// older than idleTTL. Composed agents (those NOT in yamlAgents) and
// non-meta processes are eligible; meta-agents (orchestrator, builder)
// and YAML-defined agents stay resident. Evicted processes are gracefully
// stopped — a subsequent EnsureAgent call respawns them on demand from
// the document definition.
//
// Cancel via the supplied context. Safe to call once during server
// startup; subsequent calls would spawn duplicate sweeps.
func (i *Interpreter) StartIdleEviction(ctx context.Context, idleTTL, sweepInterval time.Duration) {
	if idleTTL <= 0 || sweepInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				i.evictIdle(idleTTL)
			}
		}
	}()
}

// evictIdle does one sweep, removing eligible idle processes.
func (i *Interpreter) evictIdle(idleTTL time.Duration) {
	now := time.Now()
	type victim struct {
		name string
		proc *vega.Process
	}
	var victims []victim

	i.mu.RLock()
	for name, proc := range i.agents {
		// Keep YAML-defined agents resident — they're the user's
		// explicit team. Their cost of staying loaded is a feature.
		if i.yamlAgents[name] {
			continue
		}
		// Keep meta-agents (orchestrator, builder) — flagged at
		// registration via Agent.IsMeta.
		if def, ok := i.doc.Agents[name]; ok && def.IsMeta {
			continue
		}
		m := proc.Metrics()
		if m.LastActiveAt.IsZero() {
			continue // never been active; can't tell — leave alone
		}
		if now.Sub(m.LastActiveAt) > idleTTL {
			victims = append(victims, victim{name: name, proc: proc})
		}
	}
	i.mu.RUnlock()

	for _, v := range victims {
		// ResetAgent stops the process and removes it from i.agents but
		// leaves the definition in i.doc.Agents, so EnsureAgent respawns
		// on demand the next time someone messages the agent. RemoveAgent
		// would also wipe the doc entry — that's for explicit deletes
		// (Hera's delete_agent), not idle eviction (refs govega#76).
		if err := i.ResetAgent(v.name); err != nil {
			slog.Debug("idle-evict: reset failed", "agent", v.name, "error", err)
			continue
		}
		slog.Info("idle-evict: removed inactive agent process", "agent", v.name, "idle_for", now.Sub(v.proc.Metrics().LastActiveAt).Truncate(time.Second).String())
	}
}

// Execute runs a workflow by name (alias for RunWorkflow).
func (i *Interpreter) Execute(ctx context.Context, name string, inputs map[string]any) (any, error) {
	return i.RunWorkflow(ctx, name, inputs)
}

// Orchestrator returns the underlying orchestrator.
func (i *Interpreter) Orchestrator() *vega.Orchestrator {
	return i.orch
}

// Document returns the parsed DSL document.
func (i *Interpreter) Document() *Document {
	return i.doc
}

// Tools returns the tool registry.
func (i *Interpreter) Tools() *tools.Tools {
	return i.tools
}

// SkillsLoader returns the global skills loader, or nil if none is configured.
func (i *Interpreter) SkillsLoader() *skills.Loader {
	return i.skillsLoader
}

// Agents returns a copy of the active agent processes map.
func (i *Interpreter) Agents() map[string]*vega.Process {
	i.mu.RLock()
	defer i.mu.RUnlock()
	copy := make(map[string]*vega.Process, len(i.agents))
	for k, v := range i.agents {
		copy[k] = v
	}
	return copy
}

// ReactiveTriggers returns each agent's reactive triggers, keyed by the name
// that SendToAgent accepts. Satisfies reactive.TriggerRegistry, so the router
// reads triggers off agent definitions — the single source of truth (D1).
//
// It reads *definitions* (i.doc.Agents), not spawned processes, on purpose:
// under lazy-spawn a purely-reactive agent may never have been messaged, and
// idle agents get evicted — but their triggers must still be discoverable so
// the agent can wake. SendToAgent spawns the agent on the wake itself.
func (i *Interpreter) ReactiveTriggers() map[string][]reactive.Trigger {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make(map[string][]reactive.Trigger)
	for name, def := range i.doc.Agents {
		if def == nil || len(def.Triggers) == 0 {
			continue
		}
		trigs := make([]reactive.Trigger, 0, len(def.Triggers))
		for _, td := range def.Triggers {
			trigs = append(trigs, reactive.Trigger{
				On:     td.On,
				Where:  td.Where,
				Gate:   td.Gate,
				Prompt: td.Prompt,
			})
		}
		out[name] = trigs
	}
	return out
}

// HasAgent reports whether an agent with this name is defined (whether or
// not it is currently spawned). Useful for routing decisions where a caller
// wants to validate a target name without forcing a spawn.
func (i *Interpreter) HasAgent(name string) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	_, ok := i.doc.Agents[name]
	return ok
}

// augmentReadChannel ensures agents that can post to channels can also read
// them in full. If def.Tools contains post_to_channel or list_my_channels but
// not read_channel, read_channel is appended. No-op for agents without
// channel tools and for agents that already have read_channel.
func augmentReadChannel(def *Agent) {
	if def == nil || len(def.Tools) == 0 {
		return
	}
	hasPost, hasList, hasRead := false, false, false
	for _, t := range def.Tools {
		switch t {
		case "post_to_channel":
			hasPost = true
		case "list_my_channels":
			hasList = true
		case "read_channel":
			hasRead = true
		}
	}
	if (hasPost || hasList) && !hasRead {
		def.Tools = append(def.Tools, "read_channel")
	}
}

// AddAgent adds and spawns a new agent at runtime.
func (i *Interpreter) AddAgent(name string, def *Agent) error {
	i.mu.RLock()
	_, exists := i.agents[name]
	i.mu.RUnlock()
	if exists {
		return fmt.Errorf("agent '%s' already exists", name)
	}

	// Channel-aware augmentation: read_channel was introduced after many
	// agents were already persisted with just post_to_channel /
	// list_my_channels. Without this, existing agents can only see the
	// truncated check_status preview of teammate posts and have no way to
	// fetch the full body. Idempotent — safe for new agents too.
	augmentReadChannel(def)

	// Register in document so it's visible to list APIs.
	i.mu.Lock()
	if i.doc.Agents == nil {
		i.doc.Agents = make(map[string]*Agent)
	}
	i.doc.Agents[name] = def
	i.mu.Unlock()

	if err := i.spawnAgent(name, def); err != nil {
		// Roll back document entry on failure.
		i.mu.Lock()
		delete(i.doc.Agents, name)
		i.mu.Unlock()
		return err
	}
	return nil
}

// RemoveAgent stops and removes an agent at runtime.
func (i *Interpreter) RemoveAgent(name string) error {
	i.mu.Lock()
	// System (meta) agents like Hera and Iris must never be removed or
	// rewritten via the agent tools — that would be a privilege-escalation
	// path for a prompt-injected composed agent.
	if def, ok := i.doc.Agents[name]; ok && def.IsMeta {
		i.mu.Unlock()
		return fmt.Errorf("agent '%s' is a system agent and cannot be removed or modified", name)
	}
	proc, ok := i.agents[name]
	if !ok {
		i.mu.Unlock()
		return fmt.Errorf("agent '%s' not found", name)
	}
	delete(i.agents, name)
	delete(i.doc.Agents, name)
	i.mu.Unlock()

	// Kill the process via orchestrator.
	return i.orch.Kill(proc.ID)
}

// ResetAgent kills the agent process and removes it from the active map,
// but preserves the agent definition so it respawns fresh on next use.
func (i *Interpreter) ResetAgent(name string) error {
	i.mu.Lock()
	proc, ok := i.agents[name]
	if !ok {
		i.mu.Unlock()
		// Agent not spawned yet — nothing to reset.
		return nil
	}
	delete(i.agents, name)
	i.mu.Unlock()

	return i.orch.Kill(proc.ID)
}

// RemoveComposedAgents kills and removes all agents that were NOT defined in
// the original YAML file and are not meta-agents (iris, hera). This
// restores the interpreter to its YAML-defined state after a reset.
func (i *Interpreter) RemoveComposedAgents() {
	i.mu.Lock()
	var toRemove []string
	for name, def := range i.doc.Agents {
		if i.yamlAgents[name] {
			continue // YAML-defined, keep it
		}
		if def.IsMeta {
			continue // meta-agents, keep them
		}
		toRemove = append(toRemove, name)
	}
	i.mu.Unlock()

	for _, name := range toRemove {
		if err := i.RemoveAgent(name); err != nil {
			slog.Warn("reset: failed to remove composed agent", "agent", name, "error", err)
		} else {
			slog.Info("reset: removed composed agent", "agent", name)
		}
	}
}

// EnsureAgent ensures the named agent process is spawned and returns it.
// If the process already exists it is returned immediately; otherwise the
// agent is lazily spawned from its definition.
func (i *Interpreter) EnsureAgent(name string) (*vega.Process, error) {
	return i.ensureAgent(name)
}

// ephemeralDelegationEnvVar toggles per-call ephemeral subagent processes.
const ephemeralDelegationEnvVar = "VEGA_EPHEMERAL_DELEGATION"

// ephemeralDelegationEnabled reports whether SendToAgent should use a fresh,
// discardable process per delegation rather than reusing the long-lived
// agent process. Default true.
func ephemeralDelegationEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(ephemeralDelegationEnvVar)))
	return v != "false" && v != "0" && v != "no"
}

// spawnEphemeralProcess returns a fresh, unregistered process for the named
// agent. The caller is responsible for killing it (via orch.Kill) when done.
//
// The process is built from the same Agent config the long-lived process
// uses — so the system prompt, tools, MCP enrichments and skills wrapper
// are all preserved — but its message buffer starts empty and it is not
// registered in i.agents.
func (i *Interpreter) spawnEphemeralProcess(name string) (*vega.Process, error) {
	base, err := i.ensureAgent(name)
	if err != nil {
		return nil, err
	}
	if base.Agent == nil {
		return nil, fmt.Errorf("agent '%s' has no config", name)
	}
	proc, err := i.orch.Spawn(*base.Agent)
	if err != nil {
		return nil, fmt.Errorf("spawn ephemeral subagent '%s': %w", name, err)
	}
	return proc, nil
}

// SendToAgent sends a message to a specific agent and returns the response.
// If the calling context carries an event sink (from a streaming parent),
// SendToAgent uses streaming and forwards nested tool_start/tool_end events
// to the parent sink so the UI can display sub-agent activity in real time.
//
// When invoked from inside another agent's tool loop (i.e. as a delegated
// subagent call), the target runs in a fresh, ephemeral process spawned
// from its definition. Each delegation thus starts with an empty message
// buffer, preventing accumulated cross-conversation history from one
// caller showing up in unrelated calls from another. Direct user-driven
// chat sessions continue to use the long-lived process so multi-turn
// conversations behave as users expect. Set
// VEGA_EPHEMERAL_DELEGATION=false to opt back into the legacy
// shared-process behavior.
func (i *Interpreter) SendToAgent(ctx context.Context, agentName string, message string) (string, error) {
	// Block privilege escalation: a non-meta agent must not be able to invoke a
	// system (meta) agent like Hera/Iris via delegation. Top-level calls (no
	// caller process in context) are unaffected, so the user can still talk to
	// the orchestrator directly.
	if caller := vega.ProcessFromContext(ctx); caller != nil && caller.Agent != nil {
		i.mu.RLock()
		callerDef, callerOK := i.doc.Agents[caller.Agent.Name]
		targetDef, targetOK := i.doc.Agents[agentName]
		i.mu.RUnlock()
		callerIsMeta := callerOK && callerDef.IsMeta
		targetIsMeta := targetOK && targetDef.IsMeta
		if targetIsMeta && !callerIsMeta {
			return "", fmt.Errorf("agent %q cannot delegate to system agent %q", caller.Agent.Name, agentName)
		}
	}

	useEphemeral := ephemeralDelegationEnabled() && vega.ProcessFromContext(ctx) != nil

	var (
		proc *vega.Process
		err  error
	)
	if useEphemeral {
		proc, err = i.spawnEphemeralProcess(agentName)
		if err != nil {
			return "", err
		}
		defer func() {
			if killErr := i.orch.Kill(proc.ID); killErr != nil {
				slog.Warn("failed to clean up ephemeral subagent process", "agent", agentName, "process_id", proc.ID, "error", killErr)
			}
		}()
	} else {
		proc, err = i.ensureAgent(agentName)
		if err != nil {
			return "", err
		}
	}

	// Inject memory so the agent has context from prior conversations.
	if i.memoryInjector != nil {
		i.memoryInjector(proc, agentName)
	}

	// Scope memory context to the delegated agent so its remember/recall
	// tools use their own namespace instead of the parent's.
	if i.delegationCtxDecorator != nil {
		ctx = i.delegationCtxDecorator(ctx, agentName)
	}

	// If the parent is streaming, use rich streaming so we can forward
	// nested tool activity back to the parent's event channel.
	parentSink := vega.EventSinkFromContext(ctx)
	if parentSink != nil {
		stream, err := proc.SendStreamRich(ctx, message)
		if err != nil {
			return "", err
		}

		for event := range stream.Events() {
			// Only forward tool lifecycle events — skip text_delta and done
			// to avoid corrupting the parent's response text.
			if event.Type == vega.ChatEventToolStart || event.Type == vega.ChatEventToolEnd {
				// Build the nested agent chain.
				if event.NestedAgent == "" {
					event.NestedAgent = agentName
				} else {
					event.NestedAgent = agentName + "/" + event.NestedAgent
				}
				parentSink <- event
			}
		}

		if err := stream.Err(); err != nil {
			return "", err
		}
		resp := stream.Response()

		if i.delegationObserver != nil {
			callerName := ""
			if callerProc := vega.ProcessFromContext(ctx); callerProc != nil && callerProc.Agent != nil {
				callerName = callerProc.Agent.Name
			}
			if callerName != "" {
				go i.delegationObserver(context.Background(), callerName, agentName, message, resp)
			}
		}

		return resp, nil
	}

	response, err := proc.Send(ctx, message)
	if err != nil {
		return "", err
	}

	if i.delegationObserver != nil {
		callerName := ""
		if callerProc := vega.ProcessFromContext(ctx); callerProc != nil && callerProc.Agent != nil {
			callerName = callerProc.Agent.Name
		}
		if callerName != "" {
			go i.delegationObserver(context.Background(), callerName, agentName, message, response)
		}
	}

	return response, nil
}

// SetMemoryInjector sets a callback that injects memory into an agent process
// before sending messages. This gives agents access to their stored memories
// during delegated tasks, not just during direct chat.
func (i *Interpreter) SetMemoryInjector(fn func(proc *vega.Process, agentName string)) {
	i.memoryInjector = fn
}

// SetDelegationCtxDecorator sets a callback that rewrites the context before
// each delegation. The serve layer uses this to scope memory context to the
// delegated agent so each agent's remember/recall tools use their own namespace.
func (i *Interpreter) SetDelegationCtxDecorator(fn func(ctx context.Context, agentName string) context.Context) {
	i.delegationCtxDecorator = fn
}

// SetInboxBackend sets the inbox backend used by DispatchToAgent for
// posting completion notifications.
func (i *Interpreter) SetInboxBackend(b InboxBackend) {
	i.inboxBackend = b
}

// SetChannelBackend sets the channel backend used by DispatchToAgent to post
// completion summaries to the agent's team channel.
func (i *Interpreter) SetChannelBackend(b ChannelBackend, onPost func(channelName, agent, content string, msgID int64, threadID *int64)) {
	i.channelBackend = b
	i.channelPostCb = onPost
}

// DispatchToAgent is a non-blocking variant of SendToAgent. It validates the
// agent exists, then spawns a goroutine that calls SendToAgent. On completion
// (or error), it posts an inbox item so the orchestrator knows the work
// finished. Returns immediately with a confirmation message.
//
// The caller's agent name (read from the parent ctx's process) is captured
// before the goroutine detaches and forwarded to onDispatchComplete so
// downstream layers (e.g. serve.Server) can route the result back to the
// originating conversation — not just to the base orchestrator.
func (i *Interpreter) DispatchToAgent(ctx context.Context, agentName string, message string) (string, error) {
	// Validate agent exists synchronously so callers get immediate errors.
	if _, err := i.ensureAgent(agentName); err != nil {
		return "", err
	}

	// Capture caller name BEFORE detaching the context, since the parent's
	// process binding may not survive the detach.
	callerName := ""
	if proc := vega.ProcessFromContext(ctx); proc != nil && proc.Agent != nil {
		callerName = proc.Agent.Name
	}

	go func() {
		// Bound concurrent in-flight dispatches. Each dispatched agent
		// run holds its own conversation history + tool call state in
		// memory; a fan-out of 6+ agents from a single orchestrator
		// turn was producing macOS OOM kills. Acquire here (not at
		// the caller) so DispatchToAgent still returns immediately;
		// excess work waits in the goroutine queue instead of blocking
		// the orchestrator.
		i.dispatchSem <- struct{}{}
		defer func() { <-i.dispatchSem }()

		if i.onDispatchStart != nil {
			i.onDispatchStart(agentName)
		}

		// Detach from the caller's deadline/cancel (it will be closed)
		// but preserve context values so domain-store, memory, etc.
		// propagate.
		detached := context.WithoutCancel(ctx)

		// Two paths depending on whether the serve layer wants live
		// events: if onDispatchEvent is registered, we route through
		// StreamToAgent so each tool call / text delta can be relayed
		// to the broker for live UI streaming. Otherwise we use the
		// non-streaming SendToAgent — saves the streaming overhead
		// when nobody's listening.
		var resp string
		var err error
		if i.onDispatchEvent != nil {
			stream, sErr := i.StreamToAgent(detached, agentName, message)
			if sErr != nil {
				err = sErr
			} else {
				eventCb := i.onDispatchEvent
				for ev := range stream.Events() {
					eventCb(agentName, ev)
				}
				resp = stream.Response()
				err = stream.Err()
			}
		} else {
			resp, err = i.SendToAgent(detached, agentName, message)
		}

		// File the outcome via recordDispatchOutcome, which splits the
		// success path (resolved-on-insert, never seen by the
		// orchestrator) from the pending path (deduped against existing
		// (from_agent, subject) so re-dispatches don't pile up).
		// See dsl/dispatch_outcome.go for the routing logic.
		if i.inboxBackend != nil {
			subject, body, priority := classifyDispatchOutcome(agentName, message, resp, err)
			recordDispatchOutcome(i.inboxBackend, agentName, subject, body, priority)
		}

		// Post a summary to the agent's team channel for user visibility.
		if err == nil && i.channelBackend != nil {
			channels, chErr := i.channelBackend.ListChannelsForAgent(agentName)
			if chErr == nil {
				summary := truncateStr(resp, 500)
				for _, ch := range channels {
					// Skip general/random — post to team channels only.
					if ch.Name == "general" || ch.Name == "random" {
						continue
					}
					msgID, postErr := i.channelBackend.InsertChannelMessage(ch.ID, agentName, "assistant", summary, nil, "", agentName, nil)
					if postErr == nil && i.channelPostCb != nil {
						i.channelPostCb(ch.Name, agentName, summary, msgID, nil)
					}
					break // post to first team channel only
				}
			}
		}

		// Immediately poke the orchestrator to triage the inbox — don't
		// wait for the 15-minute heartbeat. This closes the loop so work
		// keeps flowing. callerName lets the serve layer route the
		// orchestrator's response back to the originating conversation
		// (e.g. the specific Telegram chat) rather than always routing
		// to the base orchestrator's web chat.
		if i.onDispatchComplete != nil {
			// Pass the goroutine's ctx (detached via WithoutCancel from
			// the original caller's request) so the serve layer can
			// carry BYOK + claims forward when it pokes the orchestrator.
			i.onDispatchComplete(detached, agentName, callerName, message, resp, err)
		}
	}()

	return fmt.Sprintf("Dispatched to **%s**. Watch their channel for progress.", agentName), nil
}

// SetDispatchStartCallback registers a callback that fires when a dispatched
// agent begins working. The serve layer uses this to show a busy indicator.
func (i *Interpreter) SetDispatchStartCallback(fn func(agentName string)) {
	i.onDispatchStart = fn
}

// SetDispatchCompleteCallback registers a callback that fires when a
// dispatched agent finishes. Args:
//   - agentName: the agent that just completed (e.g. "riley")
//   - callerName: the agent that called send_to_agent (e.g. "apex"), or
//     empty when the dispatch isn't attributable to an agent (e.g. a
//     scheduler-triggered turn)
//   - message: the original task message sent to the agent
//   - response: the agent's final response
//   - err: any error from the dispatch
//
// The serve layer uses these to (a) route the orchestrator's response
// back to the originating conversation and (b) persist the dispatched
// exchange to the agent's private chat history so the user can watch
// their work in /chat/<agent>.
func (i *Interpreter) SetDispatchCompleteCallback(fn func(ctx context.Context, agentName, callerName, message, response string, err error)) {
	i.onDispatchComplete = fn
}

// SetEventPublisher wires the interpreter (and the tools it hosts) to the
// reactive event spine. serve sets this to the bus's Publish so, e.g., the
// remember tool can emit memory.wrote. Nil is a safe no-op.
func (i *Interpreter) SetEventPublisher(fn func(e events.Event)) {
	i.eventPublish = fn
}

// PublishEvent emits an event onto the spine if a publisher is wired.
func (i *Interpreter) PublishEvent(e events.Event) {
	if i.eventPublish != nil {
		i.eventPublish(e)
	}
}

// SetDispatchEventCallback registers a callback that fires for every
// ChatEvent (text delta, tool start/end, etc.) emitted during a
// dispatched agent's run. The serve layer uses this to broadcast live
// progress to anyone watching the dispatched agent's private chat.
func (i *Interpreter) SetDispatchEventCallback(fn func(agentName string, ev vega.ChatEvent)) {
	i.onDispatchEvent = fn
}

// truncateStr truncates a string to max characters, appending "..." if truncated.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// StreamToAgent sends a message to a specific agent and returns a ChatStream
// with structured events for real-time streaming and tool call visibility.
func (i *Interpreter) StreamToAgent(ctx context.Context, agentName string, message string) (*vega.ChatStream, error) {
	proc, err := i.ensureAgent(agentName)
	if err != nil {
		return nil, err
	}
	return proc.SendStreamRich(ctx, message)
}

// resolveKnowledge fetches all knowledge URIs and returns a formatted section.
func (i *Interpreter) resolveKnowledge(ctx context.Context, uris []string) string {
	var builder strings.Builder
	builder.WriteString("# Knowledge\n")
	any := false

	for _, uri := range uris {
		content, err := i.fetchKnowledgeItem(ctx, uri)
		if err != nil {
			continue
		}
		any = true
		builder.WriteString("\n## ")
		builder.WriteString(uri)
		builder.WriteString("\n```\n")
		builder.WriteString(content)
		builder.WriteString("\n```\n")
	}

	if !any {
		return ""
	}
	return builder.String()
}

// fetchKnowledgeItem fetches a single knowledge resource.
// Routes file:// URIs to os.ReadFile. Other schemes are treated as MCP resource
// URIs where the scheme identifies the MCP server name.
func (i *Interpreter) fetchKnowledgeItem(ctx context.Context, uri string) (string, error) {
	if strings.HasPrefix(uri, "file://") {
		path := strings.TrimPrefix(uri, "file://")
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read knowledge file %s: %w", path, err)
		}
		return string(data), nil
	}

	// Parse scheme as MCP server name: "postgres://public/users" -> server=postgres, uri=public/users
	if idx := strings.Index(uri, "://"); idx > 0 {
		serverName := uri[:idx]
		return i.tools.ReadMCPResource(ctx, serverName, uri)
	}

	return "", fmt.Errorf("unsupported knowledge URI scheme: %s", uri)
}

// expandEnvVars expands $VAR and ${VAR} references in environment variable values.
func expandEnvVars(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	result := make(map[string]string, len(env))
	for k, v := range env {
		result[k] = os.ExpandEnv(v)
	}
	return result
}

// Helper functions

func copyMap(m map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		result[k] = v
	}
	return result
}

// exprPattern is defined in parser.go
var _ = regexp.MustCompile(`\{\{([^}]+)\}\}`)
