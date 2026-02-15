package dsl

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parser parses .vega.yaml files.
type Parser struct {
	// BaseDir for resolving relative paths
	BaseDir string
}

// NewParser creates a new parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseFile parses a .vega.yaml file.
func (p *Parser) ParseFile(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return p.Parse(data)
}

// Parse parses YAML content into a Document.
func (p *Parser) Parse(data []byte) (*Document, error) {
	// First pass: parse into raw structure
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	// Second pass: parse into typed structure
	doc := &Document{
		Agents:    make(map[string]*Agent),
		Workflows: make(map[string]*Workflow),
		Tools:     make(map[string]*ToolDef),
	}

	// Parse name and description
	if v, ok := raw["name"].(string); ok {
		doc.Name = v
	}
	if v, ok := raw["description"].(string); ok {
		doc.Description = v
	}

	// Parse agents
	if agents, ok := raw["agents"].(map[string]any); ok {
		for name, agentRaw := range agents {
			agent, err := p.parseAgent(name, agentRaw)
			if err != nil {
				return nil, fmt.Errorf("parse agent %s: %w", name, err)
			}
			doc.Agents[name] = agent
		}
	}

	// Parse workflows
	if workflows, ok := raw["workflows"].(map[string]any); ok {
		for name, wfRaw := range workflows {
			wf, err := p.parseWorkflow(name, wfRaw)
			if err != nil {
				return nil, fmt.Errorf("parse workflow %s: %w", name, err)
			}
			doc.Workflows[name] = wf
		}
	}

	// Parse settings
	if settings, ok := raw["settings"].(map[string]any); ok {
		doc.Settings = p.parseSettings(settings)
	}

	// Validate
	if err := p.validate(doc); err != nil {
		return nil, err
	}

	return doc, nil
}

// parseAgent parses an agent definition.
func (p *Parser) parseAgent(name string, raw any) (*Agent, error) {
	agent := &Agent{}

	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map")
	}

	// Parse fields
	if v, ok := m["name"].(string); ok {
		agent.Name = v
	} else {
		agent.Name = name
	}

	if v, ok := m["extends"].(string); ok {
		agent.Extends = v
	}
	if v, ok := m["model"].(string); ok {
		agent.Model = v
	}
	if v, ok := m["system"].(string); ok {
		agent.System = v
	}
	if v, ok := m["temperature"].(float64); ok {
		agent.Temperature = &v
	}
	if v, ok := m["budget"].(string); ok {
		agent.Budget = v
	}

	// Parse tools list
	if tools, ok := m["tools"].([]any); ok {
		for _, t := range tools {
			if s, ok := t.(string); ok {
				agent.Tools = append(agent.Tools, s)
			}
		}
	}

	// Parse knowledge list
	if knowledge, ok := m["knowledge"].([]any); ok {
		for _, k := range knowledge {
			if s, ok := k.(string); ok {
				agent.Knowledge = append(agent.Knowledge, s)
			}
		}
	}

	// Parse team list
	if team, ok := m["team"].([]any); ok {
		for _, t := range team {
			if s, ok := t.(string); ok {
				agent.Team = append(agent.Team, s)
			}
		}
	}

	// Parse supervision
	if sup, ok := m["supervision"].(map[string]any); ok {
		agent.Supervision = &SupervisionDef{}
		if v, ok := sup["strategy"].(string); ok {
			agent.Supervision.Strategy = v
		}
		if v, ok := sup["max_restarts"].(int); ok {
			agent.Supervision.MaxRestarts = v
		}
		if v, ok := sup["window"].(string); ok {
			agent.Supervision.Window = v
		}
	}

	// Parse retry
	if retry, ok := m["retry"].(map[string]any); ok {
		agent.Retry = &RetryDef{}
		if v, ok := retry["max_attempts"].(int); ok {
			agent.Retry.MaxAttempts = v
		}
		if v, ok := retry["backoff"].(string); ok {
			agent.Retry.Backoff = v
		}
	}

	// Parse skills
	if skills, ok := m["skills"].(map[string]any); ok {
		agent.Skills = &SkillsDef{}
		if dirs, ok := skills["directories"].([]any); ok {
			for _, d := range dirs {
				if s, ok := d.(string); ok {
					agent.Skills.Directories = append(agent.Skills.Directories, s)
				}
			}
		}
		if inc, ok := skills["include"].([]any); ok {
			for _, i := range inc {
				if s, ok := i.(string); ok {
					agent.Skills.Include = append(agent.Skills.Include, s)
				}
			}
		}
		if exc, ok := skills["exclude"].([]any); ok {
			for _, e := range exc {
				if s, ok := e.(string); ok {
					agent.Skills.Exclude = append(agent.Skills.Exclude, s)
				}
			}
		}
		if v, ok := skills["max_active"].(int); ok {
			agent.Skills.MaxActive = v
		}
	}

	// Parse delegation
	if del, ok := m["delegation"].(map[string]any); ok {
		agent.Delegation = &DelegationDef{}
		if v, ok := del["context_window"].(int); ok {
			agent.Delegation.ContextWindow = v
		}
		if v, ok := del["blackboard"].(bool); ok {
			agent.Delegation.Blackboard = v
		}
		if roles, ok := del["include_roles"].([]any); ok {
			for _, r := range roles {
				if s, ok := r.(string); ok {
					agent.Delegation.IncludeRoles = append(agent.Delegation.IncludeRoles, s)
				}
			}
		}
	}

	return agent, nil
}

// parseWorkflow parses a workflow definition.
func (p *Parser) parseWorkflow(name string, raw any) (*Workflow, error) {
	wf := &Workflow{
		Inputs: make(map[string]*Input),
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map")
	}

	// Parse description
	if v, ok := m["description"].(string); ok {
		wf.Description = v
	}

	// Parse inputs
	if inputs, ok := m["inputs"].(map[string]any); ok {
		for inputName, inputRaw := range inputs {
			input, err := p.parseInput(inputName, inputRaw)
			if err != nil {
				return nil, fmt.Errorf("parse input %s: %w", inputName, err)
			}
			wf.Inputs[inputName] = input
		}
	}

	// Parse steps
	if steps, ok := m["steps"].([]any); ok {
		for i, stepRaw := range steps {
			step, err := p.parseStep(stepRaw)
			if err != nil {
				return nil, fmt.Errorf("parse step %d: %w", i, err)
			}
			wf.Steps = append(wf.Steps, *step)
		}
	}

	// Parse output
	wf.Output = m["output"]

	return wf, nil
}

// parseInput parses a workflow input definition.
func (p *Parser) parseInput(name string, raw any) (*Input, error) {
	input := &Input{
		Required: true, // Default to required
	}

	switch v := raw.(type) {
	case string:
		// Short form: just the type
		input.Type = v
	case map[string]any:
		if t, ok := v["type"].(string); ok {
			input.Type = t
		} else {
			input.Type = "string"
		}
		if d, ok := v["description"].(string); ok {
			input.Description = d
		}
		if r, ok := v["required"].(bool); ok {
			input.Required = r
		}
		if def := v["default"]; def != nil {
			input.Default = def
			input.Required = false
		}
		if enum, ok := v["enum"].([]any); ok {
			for _, e := range enum {
				if s, ok := e.(string); ok {
					input.Enum = append(input.Enum, s)
				}
			}
		}
	default:
		input.Type = "string"
	}

	return input, nil
}

// parseStep parses a workflow step.
func (p *Parser) parseStep(raw any) (*Step, error) {
	step := &Step{
		Raw: make(map[string]any),
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map")
	}

	// Store raw for reference
	step.Raw = m

	// Check for control flow first
	if cond, ok := m["if"].(string); ok {
		step.Condition = cond
		if then, ok := m["then"].([]any); ok {
			for _, s := range then {
				parsed, err := p.parseStep(s)
				if err != nil {
					return nil, err
				}
				step.Then = append(step.Then, *parsed)
			}
		}
		if els, ok := m["else"].([]any); ok {
			for _, s := range els {
				parsed, err := p.parseStep(s)
				if err != nil {
					return nil, err
				}
				step.Else = append(step.Else, *parsed)
			}
		}
		return step, nil
	}

	// Check for parallel
	if parallel, ok := m["parallel"].([]any); ok {
		for _, s := range parallel {
			parsed, err := p.parseStep(s)
			if err != nil {
				return nil, err
			}
			step.Parallel = append(step.Parallel, *parsed)
		}
		return step, nil
	}

	// Check for repeat
	if rep, ok := m["repeat"].(map[string]any); ok {
		step.Repeat = &Repeat{}
		if steps, ok := rep["steps"].([]any); ok {
			for _, s := range steps {
				parsed, err := p.parseStep(s)
				if err != nil {
					return nil, err
				}
				step.Repeat.Steps = append(step.Repeat.Steps, *parsed)
			}
		}
		if until, ok := rep["until"].(string); ok {
			step.Repeat.Until = until
		}
		if max, ok := rep["max"].(int); ok {
			step.Repeat.Max = max
		}
		return step, nil
	}

	// Check for workflow call
	if wf, ok := m["workflow"].(string); ok {
		step.Workflow = wf
		if with, ok := m["with"].(map[string]any); ok {
			step.With = with
		}
		if save, ok := m["save"].(string); ok {
			step.Save = save
		}
		return step, nil
	}

	// Check for set
	if set, ok := m["set"].(map[string]any); ok {
		step.Set = set
		return step, nil
	}

	// Check for return
	if ret, ok := m["return"].(string); ok {
		step.Return = ret
		return step, nil
	}

	// Check for try/catch
	if trySteps, ok := m["try"].([]any); ok {
		for _, s := range trySteps {
			parsed, err := p.parseStep(s)
			if err != nil {
				return nil, err
			}
			step.Try = append(step.Try, *parsed)
		}
		if catchSteps, ok := m["catch"].([]any); ok {
			for _, s := range catchSteps {
				parsed, err := p.parseStep(s)
				if err != nil {
					return nil, err
				}
				step.Catch = append(step.Catch, *parsed)
			}
		}
		return step, nil
	}

	// Parse as agent step - find the agent key
	for key, val := range m {
		// Skip known keys
		if isKnownKey(key) {
			continue
		}

		// This key is the agent step definition
		// Format: "Agent action:" or just "Agent:"
		agent, action := parseAgentKey(key)
		step.Agent = agent
		step.Action = action

		// Value can be string (send message) or map (step config)
		switch v := val.(type) {
		case string:
			step.Send = v
		case map[string]any:
			if send, ok := v["send"].(string); ok {
				step.Send = send
			}
			if save, ok := v["save"].(string); ok {
				step.Save = save
			}
			if timeout, ok := v["timeout"].(string); ok {
				step.Timeout = timeout
			}
			if budget, ok := v["budget"].(string); ok {
				step.Budget = budget
			}
			if retry, ok := v["retry"].(int); ok {
				step.Retry = retry
			}
			if cond, ok := v["if"].(string); ok {
				step.If = cond
			}
			if cont, ok := v["continue_on_error"].(bool); ok {
				step.ContinueOnError = cont
			}
			if format, ok := v["format"].(string); ok {
				step.Format = format
			}
		}
		break
	}

	return step, nil
}

// parseSettings parses global settings.
func (p *Parser) parseSettings(m map[string]any) *Settings {
	s := &Settings{}

	if v, ok := m["default_model"].(string); ok {
		s.DefaultModel = v
	}
	if v, ok := m["default_temperature"].(float64); ok {
		s.DefaultTemperature = &v
	}
	if v, ok := m["sandbox"].(string); ok {
		s.Sandbox = v
	}
	if v, ok := m["budget"].(string); ok {
		s.Budget = v
	}

	// Parse supervision
	if sup, ok := m["supervision"].(map[string]any); ok {
		s.Supervision = &SupervisionDef{}
		if v, ok := sup["strategy"].(string); ok {
			s.Supervision.Strategy = v
		}
		if v, ok := sup["max_restarts"].(int); ok {
			s.Supervision.MaxRestarts = v
		}
		if v, ok := sup["window"].(string); ok {
			s.Supervision.Window = v
		}
	}

	// Parse rate limit
	if rl, ok := m["rate_limit"].(map[string]any); ok {
		s.RateLimit = &RateLimitDef{}
		if v, ok := rl["requests_per_minute"].(int); ok {
			s.RateLimit.RequestsPerMinute = v
		}
		if v, ok := rl["tokens_per_minute"].(int); ok {
			s.RateLimit.TokensPerMinute = v
		}
	}

	// Parse logging
	if log, ok := m["logging"].(map[string]any); ok {
		s.Logging = &LoggingDef{}
		if v, ok := log["level"].(string); ok {
			s.Logging.Level = v
		}
		if v, ok := log["file"].(string); ok {
			s.Logging.File = v
		}
	}

	// Parse tracing
	if trace, ok := m["tracing"].(map[string]any); ok {
		s.Tracing = &TracingDef{}
		if v, ok := trace["enabled"].(bool); ok {
			s.Tracing.Enabled = v
		}
		if v, ok := trace["exporter"].(string); ok {
			s.Tracing.Exporter = v
		}
		if v, ok := trace["endpoint"].(string); ok {
			s.Tracing.Endpoint = v
		}
	}

	// Parse MCP
	if mcpRaw, ok := m["mcp"].(map[string]any); ok {
		s.MCP = &MCPDef{}
		if servers, ok := mcpRaw["servers"].([]any); ok {
			for _, serverRaw := range servers {
				if serverMap, ok := serverRaw.(map[string]any); ok {
					server := MCPServerDef{}
					if v, ok := serverMap["name"].(string); ok {
						server.Name = v
					}
					if v, ok := serverMap["transport"].(string); ok {
						server.Transport = v
					}
					if v, ok := serverMap["command"].(string); ok {
						server.Command = v
					}
					if args, ok := serverMap["args"].([]any); ok {
						for _, a := range args {
							if s, ok := a.(string); ok {
								server.Args = append(server.Args, s)
							}
						}
					}
					if env, ok := serverMap["env"].(map[string]any); ok {
						server.Env = make(map[string]string)
						for k, v := range env {
							if s, ok := v.(string); ok {
								server.Env[k] = s
							}
						}
					}
					if v, ok := serverMap["url"].(string); ok {
						server.URL = v
					}
					if headers, ok := serverMap["headers"].(map[string]any); ok {
						server.Headers = make(map[string]string)
						for k, v := range headers {
							if s, ok := v.(string); ok {
								server.Headers[k] = s
							}
						}
					}
					if v, ok := serverMap["timeout"].(string); ok {
						server.Timeout = v
					}
					s.MCP.Servers = append(s.MCP.Servers, server)
				}
			}
		}
	}

	// Parse global skills
	if skillsRaw, ok := m["skills"].(map[string]any); ok {
		s.Skills = &GlobalSkillsDef{}
		if dirs, ok := skillsRaw["directories"].([]any); ok {
			for _, d := range dirs {
				if str, ok := d.(string); ok {
					s.Skills.Directories = append(s.Skills.Directories, str)
				}
			}
		}
	}

	return s
}

// validate validates the parsed document.
func (p *Parser) validate(doc *Document) error {
	// Check for at least one agent
	if len(doc.Agents) == 0 {
		return &ValidationError{
			Field:   "agents",
			Message: "at least one agent must be defined",
		}
	}

	// Validate agents
	for name, agent := range doc.Agents {
		if agent.Model == "" && doc.Settings != nil && doc.Settings.DefaultModel != "" {
			agent.Model = doc.Settings.DefaultModel
		}
		if agent.Model == "" {
			return &ValidationError{
				Field:   fmt.Sprintf("agents.%s.model", name),
				Message: "model is required",
				Hint:    "Add 'model: claude-sonnet-4-20250514' or set default_model in settings",
			}
		}
		if agent.System == "" {
			return &ValidationError{
				Field:   fmt.Sprintf("agents.%s.system", name),
				Message: "system prompt is required",
			}
		}

		// Check extends reference
		if agent.Extends != "" {
			if _, ok := doc.Agents[agent.Extends]; !ok {
				return &ValidationError{
					Field:   fmt.Sprintf("agents.%s.extends", name),
					Message: fmt.Sprintf("agent '%s' not found", agent.Extends),
					Hint:    fmt.Sprintf("Did you mean one of: %s?", strings.Join(agentNames(doc), ", ")),
				}
			}
		}

		// Check team references
		for _, member := range agent.Team {
			if member == name {
				return &ValidationError{
					Field:   fmt.Sprintf("agents.%s.team", name),
					Message: "agent cannot be on its own team",
				}
			}
			if _, ok := doc.Agents[member]; !ok {
				return &ValidationError{
					Field:   fmt.Sprintf("agents.%s.team", name),
					Message: fmt.Sprintf("team member '%s' not found", member),
					Hint:    fmt.Sprintf("Did you mean one of: %s?", strings.Join(agentNames(doc), ", ")),
				}
			}
		}
	}

	// Validate workflows
	for name, wf := range doc.Workflows {
		for i, step := range wf.Steps {
			if err := p.validateStep(doc, name, i, &step); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateStep validates a workflow step.
func (p *Parser) validateStep(doc *Document, wfName string, stepIndex int, step *Step) error {
	// Validate agent reference
	if step.Agent != "" {
		if _, ok := doc.Agents[step.Agent]; !ok {
			return &ValidationError{
				Field:   fmt.Sprintf("workflows.%s.steps[%d]", wfName, stepIndex),
				Message: fmt.Sprintf("unknown agent '%s'", step.Agent),
				Hint:    fmt.Sprintf("Did you mean '%s'?", findSimilar(step.Agent, agentNames(doc))),
			}
		}
	}

	// Validate sub-workflow reference
	if step.Workflow != "" {
		if _, ok := doc.Workflows[step.Workflow]; !ok {
			return &ValidationError{
				Field:   fmt.Sprintf("workflows.%s.steps[%d].workflow", wfName, stepIndex),
				Message: fmt.Sprintf("unknown workflow '%s'", step.Workflow),
			}
		}
	}

	// Recursively validate nested steps
	for i, s := range step.Then {
		if err := p.validateStep(doc, wfName, i, &s); err != nil {
			return err
		}
	}
	for i, s := range step.Else {
		if err := p.validateStep(doc, wfName, i, &s); err != nil {
			return err
		}
	}
	for i, s := range step.Parallel {
		if err := p.validateStep(doc, wfName, i, &s); err != nil {
			return err
		}
	}
	if step.Repeat != nil {
		for i, s := range step.Repeat.Steps {
			if err := p.validateStep(doc, wfName, i, &s); err != nil {
				return err
			}
		}
	}

	return nil
}

// Helper functions

func isKnownKey(key string) bool {
	known := map[string]bool{
		"if": true, "then": true, "else": true,
		"parallel": true, "repeat": true, "for": true,
		"workflow": true, "with": true,
		"set": true, "return": true,
		"try": true, "catch": true,
		"save": true, "timeout": true, "budget": true,
		"retry": true, "continue_on_error": true, "format": true,
	}
	return known[key]
}

// parseAgentKey extracts agent name and optional action from step key.
// Examples: "Coder writes code:" -> ("Coder", "writes code")
//
//	"Coder:" -> ("Coder", "")
//	"Coder" -> ("Coder", "")
func parseAgentKey(key string) (agent, action string) {
	key = strings.TrimSuffix(key, ":")

	// Try to split on first space
	parts := strings.SplitN(key, " ", 2)
	agent = parts[0]
	if len(parts) > 1 {
		action = parts[1]
	}

	return agent, action
}

func agentNames(doc *Document) []string {
	names := make([]string, 0, len(doc.Agents))
	for name := range doc.Agents {
		names = append(names, name)
	}
	return names
}

// findSimilar finds the most similar string using simple edit distance.
func findSimilar(target string, candidates []string) string {
	target = strings.ToLower(target)
	best := ""
	bestScore := -1

	for _, c := range candidates {
		score := similarity(target, strings.ToLower(c))
		if score > bestScore {
			bestScore = score
			best = c
		}
	}

	return best
}

// similarity returns a simple similarity score.
func similarity(a, b string) int {
	if a == b {
		return 100
	}

	// Check prefix
	score := 0
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			score += 2
		} else {
			break
		}
	}

	// Check contains
	if strings.Contains(b, a) || strings.Contains(a, b) {
		score += 10
	}

	return score
}

// Expression parsing

var exprPattern = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// ExtractExpressions finds all {{...}} expressions in a string.
func ExtractExpressions(s string) []string {
	matches := exprPattern.FindAllStringSubmatch(s, -1)
	exprs := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 {
			exprs = append(exprs, strings.TrimSpace(m[1]))
		}
	}
	return exprs
}

// ContainsExpression checks if a string contains expressions.
func ContainsExpression(s string) bool {
	return exprPattern.MatchString(s)
}
