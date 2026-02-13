// Package dsl provides the Vega DSL parser and interpreter.
package dsl

import "time"

// Document represents a parsed .vega.yaml file.
type Document struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Agents      map[string]*Agent   `yaml:"agents"`
	Workflows   map[string]*Workflow `yaml:"workflows"`
	Tools       map[string]*ToolDef `yaml:"tools"`
	Settings    *Settings           `yaml:"settings"`
}

// Agent represents an agent definition in the DSL.
type Agent struct {
	Name        string            `yaml:"name"`
	Extends     string            `yaml:"extends"`
	Model       string            `yaml:"model"`
	System      string            `yaml:"system"`
	Temperature *float64          `yaml:"temperature"`
	Budget      string            `yaml:"budget"` // e.g., "$0.50"
	Tools       []string          `yaml:"tools"`
	Knowledge   []string          `yaml:"knowledge"`
	Team        []string          `yaml:"team"`
	Supervision *SupervisionDef   `yaml:"supervision"`
	Retry       *RetryDef         `yaml:"retry"`
	Skills      *SkillsDef        `yaml:"skills"`
}

// SkillsDef configures skills for an agent.
type SkillsDef struct {
	Directories []string `yaml:"directories"`
	Include     []string `yaml:"include"`
	Exclude     []string `yaml:"exclude"`
	MaxActive   int      `yaml:"max_active"`
}

// SupervisionDef is DSL supervision configuration.
type SupervisionDef struct {
	Strategy    string `yaml:"strategy"` // restart, stop, escalate
	MaxRestarts int    `yaml:"max_restarts"`
	Window      string `yaml:"window"` // e.g., "10m"
}

// RetryDef is DSL retry configuration.
type RetryDef struct {
	MaxAttempts int    `yaml:"max_attempts"`
	Backoff     string `yaml:"backoff"` // linear, exponential, constant
}

// Workflow represents a workflow definition in the DSL.
type Workflow struct {
	Description string            `yaml:"description"`
	Inputs      map[string]*Input `yaml:"inputs"`
	Steps       []Step            `yaml:"steps"`
	Output      any               `yaml:"output"` // string or map
}

// Input defines a workflow input parameter.
type Input struct {
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     any      `yaml:"default"`
	Enum        []string `yaml:"enum"`
	Min         *float64 `yaml:"min"`
	Max         *float64 `yaml:"max"`
}

// Step is a workflow step (can be various types).
// This uses a flexible structure to handle the natural language format.
type Step struct {
	// Agent step fields
	Agent           string        `yaml:"-"` // Extracted from key
	Action          string        `yaml:"-"` // Extracted from key
	Send            string        `yaml:"send"`
	Save            string        `yaml:"save"`
	Timeout         string        `yaml:"timeout"`
	Budget          string        `yaml:"budget"`
	Retry           int           `yaml:"retry"`
	If              string        `yaml:"if"`
	ContinueOnError bool          `yaml:"continue_on_error"`
	Format          string        `yaml:"format"` // json, yaml, etc.

	// Control flow fields
	Condition string  `yaml:"-"` // For if steps
	Then      []Step  `yaml:"then"`
	Else      []Step  `yaml:"else"`

	// Loop fields
	ForEach   string  `yaml:"for"` // "item in items"
	Repeat    *Repeat `yaml:"repeat"`

	// Parallel fields
	Parallel []Step `yaml:"parallel"`

	// Sub-workflow fields
	Workflow    string         `yaml:"workflow"`
	With        map[string]any `yaml:"with"`

	// Special fields
	Set     map[string]any `yaml:"set"`
	Return  string         `yaml:"return"`
	Try     []Step         `yaml:"try"`
	Catch   []Step         `yaml:"catch"`

	// Raw for flexible parsing
	Raw map[string]any `yaml:"-"`
}

// Repeat defines a repeat-until loop.
type Repeat struct {
	Steps []Step `yaml:"steps"`
	Until string `yaml:"until"`
	Max   int    `yaml:"max"`
}

// ToolDef is a DSL tool definition.
type ToolDef struct {
	Name           string           `yaml:"name"`
	Description    string           `yaml:"description"`
	Params         []ToolParam      `yaml:"params"`
	Implementation *ToolImpl        `yaml:"implementation"`
	Include        []string         `yaml:"include"` // For loading from files
}

// ToolParam defines a tool parameter.
type ToolParam struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     any      `yaml:"default"`
	Enum        []string `yaml:"enum"`
}

// ToolImpl defines tool implementation.
type ToolImpl struct {
	Type    string            `yaml:"type"` // http, exec, file_read, file_write, builtin
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Query   map[string]string `yaml:"query"`
	Body    any               `yaml:"body"`
	Command string            `yaml:"command"`
	Timeout string            `yaml:"timeout"`
}

// Settings are global configuration.
type Settings struct {
	DefaultModel       string            `yaml:"default_model"`
	DefaultTemperature *float64          `yaml:"default_temperature"`
	Sandbox            string            `yaml:"sandbox"`
	Budget             string            `yaml:"budget"`
	Supervision        *SupervisionDef   `yaml:"supervision"`
	RateLimit          *RateLimitDef     `yaml:"rate_limit"`
	Logging            *LoggingDef       `yaml:"logging"`
	Tracing            *TracingDef       `yaml:"tracing"`
	MCP                *MCPDef           `yaml:"mcp"`
	Skills             *GlobalSkillsDef  `yaml:"skills"`
}

// MCPDef configures MCP servers.
type MCPDef struct {
	Servers []MCPServerDef `yaml:"servers"`
}

// MCPServerDef configures an individual MCP server.
type MCPServerDef struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	Timeout   string            `yaml:"timeout"`
}

// GlobalSkillsDef configures global skill settings.
type GlobalSkillsDef struct {
	Directories []string `yaml:"directories"`
}

// RateLimitDef is DSL rate limit configuration.
type RateLimitDef struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
	TokensPerMinute   int `yaml:"tokens_per_minute"`
}

// LoggingDef is DSL logging configuration.
type LoggingDef struct {
	Level string `yaml:"level"` // debug, info, warn, error
	File  string `yaml:"file"`
}

// TracingDef is DSL tracing configuration.
type TracingDef struct {
	Enabled  bool   `yaml:"enabled"`
	Exporter string `yaml:"exporter"` // otlp, jaeger, json
	Endpoint string `yaml:"endpoint"`
}

// ExecutionContext holds state during workflow execution.
type ExecutionContext struct {
	// Inputs are the workflow input values
	Inputs map[string]any

	// Variables holds step outputs and set values
	Variables map[string]any

	// CurrentStep is the index of the executing step
	CurrentStep int

	// LoopState for loop iterations
	LoopState *LoopState

	// StartTime is when execution began
	StartTime time.Time

	// Timeout for the entire workflow
	Timeout time.Duration
}

// LoopState tracks loop iteration state.
type LoopState struct {
	Index int
	Count int
	Item  any
	First bool
	Last  bool
}

// ValidationError provides detailed DSL validation errors.
type ValidationError struct {
	File    string
	Line    int
	Column  int
	Field   string
	Message string
	Hint    string
}

func (e *ValidationError) Error() string {
	msg := e.Message
	if e.Field != "" {
		msg = e.Field + ": " + msg
	}
	if e.Line > 0 {
		msg = msg + " (line " + string(rune(e.Line+'0')) + ")"
	}
	if e.Hint != "" {
		msg = msg + "\n  → " + e.Hint
	}
	return msg
}
