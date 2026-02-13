package dsl

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/internal/skills"
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

// Interpreter executes DSL workflows.
type Interpreter struct {
	doc          *Document
	orch         *vega.Orchestrator
	agents       map[string]*vega.Process
	tools        *vega.Tools
	skillsLoader *skills.Loader
	lazySpawn    bool
	mu           sync.RWMutex
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

	// Create default LLM
	anthropicLLM := llm.NewAnthropic()
	orchOpts = append(orchOpts, vega.WithLLM(anthropicLLM))

	orch := vega.NewOrchestrator(orchOpts...)

	// Create tools
	toolOpts := []vega.ToolsOption{}
	if doc.Settings != nil && doc.Settings.Sandbox != "" {
		toolOpts = append(toolOpts, vega.WithSandbox(doc.Settings.Sandbox))
	}

	// Add MCP servers if configured
	if doc.Settings != nil && doc.Settings.MCP != nil {
		for _, serverDef := range doc.Settings.MCP.Servers {
			config := mcp.ServerConfig{
				Name:    serverDef.Name,
				Command: serverDef.Command,
				Args:    serverDef.Args,
				Env:     serverDef.Env,
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
			toolOpts = append(toolOpts, vega.WithMCPServer(config))
		}
	}

	tools := vega.NewTools(toolOpts...)
	tools.RegisterBuiltins()

	// Connect MCP servers
	if doc.Settings != nil && doc.Settings.MCP != nil && len(doc.Settings.MCP.Servers) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := tools.ConnectMCP(ctx); err != nil {
			// Log warning but continue
			_ = err
		}
	}

	// Initialize skills loader
	var skillsLoader *skills.Loader
	if doc.Settings != nil && doc.Settings.Skills != nil && len(doc.Settings.Skills.Directories) > 0 {
		skillsLoader = skills.NewLoader(doc.Settings.Skills.Directories...)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		skillsLoader.Load(ctx)
	}

	interp := &Interpreter{
		doc:          doc,
		orch:         orch,
		agents:       make(map[string]*vega.Process),
		tools:        tools,
		skillsLoader: skillsLoader,
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

	if len(def.Team) > 0 {
		RegisterDelegateTool(i.tools, func(ctx context.Context, agentName string, message string) (string, error) {
			return i.SendToAgent(ctx, agentName, message)
		})

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
		systemStr = BuildTeamPrompt(systemStr, def.Team, descs)
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	// Build agent config
	agent := vega.Agent{
		Name:   name,
		Model:  def.Model,
		System: systemPrompt,
		Tools:  i.tools,
	}

	if def.Temperature != nil {
		agent.Temperature = def.Temperature
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

	return nil
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
func (i *Interpreter) ensureAgent(name string) (*vega.Process, error) {
	i.mu.RLock()
	proc, ok := i.agents[name]
	i.mu.RUnlock()
	if ok {
		return proc, nil
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
		execCtx.LoopState = &LoopState{
			Index: idx,
			Count: idx + 1,
			Item:  item,
			First: idx == 0,
			Last:  idx == len(items)-1,
		}
		execCtx.Variables[itemVar] = item

		// Execute nested steps (from Raw)
		// TODO: Parse nested steps from Raw
		results = append(results, item)
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
func (i *Interpreter) Tools() *vega.Tools {
	return i.tools
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

// AddAgent adds and spawns a new agent at runtime.
func (i *Interpreter) AddAgent(name string, def *Agent) error {
	i.mu.RLock()
	_, exists := i.agents[name]
	i.mu.RUnlock()
	if exists {
		return fmt.Errorf("agent '%s' already exists", name)
	}

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

// EnsureAgent ensures the named agent process is spawned and returns it.
// If the process already exists it is returned immediately; otherwise the
// agent is lazily spawned from its definition.
func (i *Interpreter) EnsureAgent(name string) (*vega.Process, error) {
	return i.ensureAgent(name)
}

// SendToAgent sends a message to a specific agent and returns the response.
func (i *Interpreter) SendToAgent(ctx context.Context, agentName string, message string) (string, error) {
	proc, err := i.ensureAgent(agentName)
	if err != nil {
		return "", err
	}

	response, err := proc.Send(ctx, message)
	if err != nil {
		return "", err
	}

	return response, nil
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
