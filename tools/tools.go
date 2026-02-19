package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/everydev1618/govega/internal/container"
	"github.com/everydev1618/govega/internal/skills"
	"github.com/everydev1618/govega/llm"
)

// Standard errors
var (
	// ErrToolNotFound is returned when a tool is not registered
	ErrToolNotFound = errors.New("tool not found")

	// ErrToolAlreadyRegistered is returned when trying to register a duplicate tool name.
	ErrToolAlreadyRegistered = errors.New("tool already registered")
)

// ToolError wraps errors with tool context.
type ToolError struct {
	ToolName string
	Err      error
}

func (e *ToolError) Error() string {
	return "tool " + e.ToolName + ": " + e.Err.Error()
}

func (e *ToolError) Unwrap() error {
	return e.Err
}

// SkillsRef is a narrow interface for skill-based tool augmentation.
// *vega.SkillsPrompt satisfies this interface.
type SkillsRef interface {
	GetMatchedSkills() []skills.SkillMatch
}

// Tools is a collection of callable tools.
type Tools struct {
	tools      map[string]*tool
	middleware []ToolMiddleware
	sandbox    string
	mcpClients []*mcpClientEntry // MCP server clients
	container  *containerState   // Container routing state
	parent     *Tools            // parent for skill-tool lookups (set by Filter)
	skillsRef  SkillsRef         // skills prompt for dynamic tool augmentation
	mu         sync.RWMutex
}

// containerState holds container routing configuration.
type containerState struct {
	manager     *container.Manager
	project     string
	routedTools map[string]bool
}

// tool is an internal representation of a registered tool.
type tool struct {
	name        string
	description string
	fn          any
	schema      llm.ToolSchema
	params      map[string]ParamDef
}

// ParamDef defines a tool parameter.
type ParamDef struct {
	Type        string   `json:"type" yaml:"type"`
	Description string   `json:"description" yaml:"description"`
	Required    bool     `json:"required" yaml:"required"`
	Default     any      `json:"default,omitempty" yaml:"default,omitempty"`
	Enum        []string `json:"enum,omitempty" yaml:"enum,omitempty"`
}

// ToolDef allows explicit tool definition with schema.
type ToolDef struct {
	Description string
	Fn          any
	Params      map[string]ParamDef
}

// ToolMiddleware wraps tool execution.
type ToolMiddleware func(ToolFunc) ToolFunc

// ToolFunc is the signature for tool execution.
type ToolFunc func(ctx context.Context, params map[string]any) (string, error)

// ToolsOption configures Tools.
type ToolsOption func(*Tools)

// NewTools creates a new Tools collection.
func NewTools(opts ...ToolsOption) *Tools {
	t := &Tools{
		tools: make(map[string]*tool),
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// WithSandbox restricts file operations to a directory.
func WithSandbox(path string) ToolsOption {
	return func(t *Tools) {
		t.sandbox = path
	}
}

// WithContainer enables container-based tool execution.
func WithContainer(cm *container.Manager) ToolsOption {
	return func(t *Tools) {
		if t.container == nil {
			t.container = &containerState{
				routedTools: make(map[string]bool),
			}
		}
		t.container.manager = cm
	}
}

// WithContainerRouting specifies which tools should be routed to containers.
func WithContainerRouting(toolNames ...string) ToolsOption {
	return func(t *Tools) {
		if t.container == nil {
			t.container = &containerState{
				routedTools: make(map[string]bool),
			}
		}
		for _, name := range toolNames {
			t.container.routedTools[name] = true
		}
	}
}

// Register adds a tool to the collection.
// The function can be:
// - func(params) string
// - func(params) (string, error)
// - func(ctx, params) (string, error)
// - ToolDef with explicit schema
func (t *Tools) Register(name string, fn any) error {
	if name == "" {
		return errors.New("tool name is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Check for duplicate registration
	if _, exists := t.tools[name]; exists {
		return fmt.Errorf("%w: %s", ErrToolAlreadyRegistered, name)
	}

	tl := &tool{
		name: name,
	}

	// Handle ToolDef
	if def, ok := fn.(ToolDef); ok {
		tl.description = def.Description
		tl.fn = def.Fn
		tl.params = def.Params
		tl.schema = t.buildSchema(name, def.Description, def.Params)
	} else {
		tl.fn = fn
		tl.schema = t.inferSchema(name, fn)
		tl.description = tl.schema.Description
	}

	t.tools[name] = tl
	return nil
}

// Use adds middleware to the tool chain.
func (t *Tools) Use(mw ToolMiddleware) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.middleware = append(t.middleware, mw)
}

// SetProject sets the active project for container routing.
func (t *Tools) SetProject(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.container == nil {
		t.container = &containerState{
			routedTools: make(map[string]bool),
		}
	}
	t.container.project = name
}

// ContainerAvailable returns whether container execution is available.
func (t *Tools) ContainerAvailable() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.container != nil && t.container.manager != nil && t.container.manager.IsAvailable()
}

// Execute calls a tool by name.
func (t *Tools) Execute(ctx context.Context, name string, params map[string]any) (string, error) {
	t.mu.RLock()
	tl, ok := t.tools[name]
	middleware := t.middleware
	sandbox := t.sandbox
	cs := t.container
	parent := t.parent
	t.mu.RUnlock()

	// Fallback to parent for tools provided by skills.
	if !ok && parent != nil {
		parent.mu.RLock()
		tl, ok = parent.tools[name]
		parent.mu.RUnlock()
	}

	if !ok {
		return "", &ToolError{ToolName: name, Err: ErrToolNotFound}
	}

	// Check if this tool should be routed to container
	if cs != nil && cs.manager != nil &&
		cs.manager.IsAvailable() && cs.project != "" &&
		cs.routedTools[name] {
		return t.executeInContainer(ctx, name, params, cs)
	}

	// Apply sandbox rewriting if needed
	if sandbox != "" {
		params = t.rewritePathsForSandbox(params, sandbox)
	}

	// Build execution function
	exec := func(ctx context.Context, params map[string]any) (string, error) {
		return t.callFunction(tl.fn, ctx, params)
	}

	// Apply middleware (in reverse order)
	for i := len(middleware) - 1; i >= 0; i-- {
		exec = middleware[i](exec)
	}

	result, err := exec(ctx, params)
	if err != nil {
		return "", &ToolError{ToolName: name, Err: err}
	}

	return result, nil
}

// executeInContainer runs a tool in the project container.
func (t *Tools) executeInContainer(ctx context.Context, name string, params map[string]any, cs *containerState) (string, error) {
	// Build command from tool name and params
	// For now, support exec-style tools by converting params to command args
	command, ok := params["command"].(string)
	if !ok {
		// Try to get a command array
		if cmdArr, ok := params["command"].([]any); ok {
			cmdParts := make([]string, len(cmdArr))
			for i, c := range cmdArr {
				cmdParts[i] = fmt.Sprint(c)
			}
			command = strings.Join(cmdParts, " ")
		}
	}

	if command == "" {
		return "", fmt.Errorf("container routing requires 'command' parameter")
	}

	// Parse command into parts
	cmdParts := strings.Fields(command)
	if len(cmdParts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	workDir, _ := params["work_dir"].(string)

	result, err := cs.manager.Exec(ctx, cs.project, cmdParts, workDir)
	if err != nil {
		return "", err
	}

	// Combine stdout and stderr
	output := result.Stdout
	if result.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += result.Stderr
	}

	if result.ExitCode != 0 {
		return output, fmt.Errorf("command exited with code %d", result.ExitCode)
	}

	return output, nil
}

// Schema returns the schemas for all tools.
// If a skillsRef is set, tools declared by matched skills are also included.
func (t *Tools) Schema() []llm.ToolSchema {
	t.mu.RLock()
	localTools := t.tools
	sp := t.skillsRef
	p := t.parent
	t.mu.RUnlock()

	seen := make(map[string]bool, len(localTools))
	schemas := make([]llm.ToolSchema, 0, len(localTools))
	for _, tl := range localTools {
		schemas = append(schemas, tl.schema)
		seen[tl.name] = true
	}

	// Augment with skill-declared tools from the parent.
	if sp != nil && p != nil {
		matches := sp.GetMatchedSkills()
		for _, m := range matches {
			for _, toolName := range m.Skill.Tools {
				if seen[toolName] {
					continue
				}
				p.mu.RLock()
				tl, ok := p.tools[toolName]
				p.mu.RUnlock()
				if ok {
					schemas = append(schemas, tl.schema)
					seen[toolName] = true
				}
			}
		}
	}

	return schemas
}

// Filter returns a new Tools with only the specified tools.
func (t *Tools) Filter(names ...string) *Tools {
	t.mu.RLock()
	defer t.mu.RUnlock()

	filtered := &Tools{
		tools:      make(map[string]*tool),
		middleware: t.middleware,
		sandbox:    t.sandbox,
		container:  t.container,
		parent:     t,
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	for name, tl := range t.tools {
		if nameSet[name] {
			filtered.tools[name] = tl
		}
	}

	return filtered
}

// WithSkillsRef returns a shallow copy with a skills prompt reference set.
// When Schema() is called, tools declared by matched skills are included.
func (t *Tools) WithSkillsRef(sp SkillsRef) *Tools {
	return &Tools{
		tools:      t.tools,
		middleware: t.middleware,
		sandbox:    t.sandbox,
		container:  t.container,
		mcpClients: t.mcpClients,
		parent:     t.parent,
		skillsRef:  sp,
	}
}

// inferSchema infers a JSON schema from a function signature.
func (t *Tools) inferSchema(name string, fn any) llm.ToolSchema {
	schema := llm.ToolSchema{
		Name:        name,
		Description: name,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	}

	fnType := reflect.TypeOf(fn)
	if fnType.Kind() != reflect.Func {
		return schema
	}

	// Build description from signature
	var paramNames []string
	for i := 0; i < fnType.NumIn(); i++ {
		inType := fnType.In(i)
		// Skip context parameter
		if inType.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
			continue
		}
		paramNames = append(paramNames, inType.Name())
	}
	schema.Description = fmt.Sprintf("%s(%s)", name, strings.Join(paramNames, ", "))

	// Infer parameters from struct if applicable
	if fnType.NumIn() > 0 {
		lastParam := fnType.In(fnType.NumIn() - 1)
		if lastParam.Kind() == reflect.Struct {
			props := make(map[string]any)
			required := []string{}

			for i := 0; i < lastParam.NumField(); i++ {
				field := lastParam.Field(i)
				jsonTag := field.Tag.Get("json")
				if jsonTag == "" || jsonTag == "-" {
					jsonTag = strings.ToLower(field.Name)
				}
				jsonTag = strings.Split(jsonTag, ",")[0]

				prop := map[string]any{
					"type": goTypeToJSONType(field.Type),
				}
				if desc := field.Tag.Get("desc"); desc != "" {
					prop["description"] = desc
				}

				props[jsonTag] = prop

				if field.Tag.Get("required") == "true" {
					required = append(required, jsonTag)
				}
			}

			schema.InputSchema["properties"] = props
			schema.InputSchema["required"] = required
		}
	}

	return schema
}

// buildSchema builds a schema from explicit definitions.
func (t *Tools) buildSchema(name, description string, params map[string]ParamDef) llm.ToolSchema {
	props := make(map[string]any)
	required := []string{}

	for pname, pdef := range params {
		prop := map[string]any{
			"type": pdef.Type,
		}
		if pdef.Description != "" {
			prop["description"] = pdef.Description
		}
		if len(pdef.Enum) > 0 {
			prop["enum"] = pdef.Enum
		}
		props[pname] = prop

		if pdef.Required {
			required = append(required, pname)
		}
	}

	return llm.ToolSchema{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
	}
}

// callFunction calls a tool function with parameters.
func (t *Tools) callFunction(fn any, ctx context.Context, params map[string]any) (string, error) {
	// Handle ToolFunc directly
	if tf, ok := fn.(ToolFunc); ok {
		return tf(ctx, params)
	}

	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	// Build arguments
	var args []reflect.Value

	for i := 0; i < fnType.NumIn(); i++ {
		inType := fnType.In(i)

		// Handle context
		if inType.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
			args = append(args, reflect.ValueOf(ctx))
			continue
		}

		// Handle string parameter
		if inType.Kind() == reflect.String {
			// Try common param names first
			commonNames := []string{"path", "query", "name", "content", "text", "input", "value"}
			var strVal string
			found := false

			// First try common names
			for _, name := range commonNames {
				if v, ok := params[name]; ok {
					strVal = fmt.Sprint(v)
					found = true
					break
				}
			}

			// If not found and there's exactly one param, use it
			if !found && len(params) == 1 {
				for _, v := range params {
					strVal = fmt.Sprint(v)
					found = true
					break
				}
			}

			// If still not found, use empty string (function should handle validation)
			args = append(args, reflect.ValueOf(strVal))
			continue
		}

		// Handle struct parameter (unmarshal params into it)
		if inType.Kind() == reflect.Struct {
			structVal := reflect.New(inType).Elem()
			for j := 0; j < inType.NumField(); j++ {
				field := inType.Field(j)
				jsonTag := field.Tag.Get("json")
				if jsonTag == "" {
					jsonTag = strings.ToLower(field.Name)
				}
				jsonTag = strings.Split(jsonTag, ",")[0]

				if v, ok := params[jsonTag]; ok {
					fieldVal := structVal.Field(j)
					if fieldVal.CanSet() {
						fieldVal.Set(reflect.ValueOf(v).Convert(field.Type))
					}
				}
			}
			args = append(args, structVal)
			continue
		}

		// Handle map parameter
		if inType.Kind() == reflect.Map {
			args = append(args, reflect.ValueOf(params))
			continue
		}
	}

	// Call function
	results := fnVal.Call(args)

	// Parse results
	if len(results) == 0 {
		return "", nil
	}

	if len(results) == 1 {
		return fmt.Sprint(results[0].Interface()), nil
	}

	// Assume (string, error)
	result := fmt.Sprint(results[0].Interface())
	if !results[1].IsNil() {
		return result, results[1].Interface().(error)
	}
	return result, nil
}

// rewritePathsForSandbox rewrites path parameters to be within sandbox.
func (t *Tools) rewritePathsForSandbox(params map[string]any, sandbox string) map[string]any {
	result := make(map[string]any)
	for k, v := range params {
		if k == "path" || strings.HasSuffix(k, "_path") || strings.HasSuffix(k, "Path") {
			if s, ok := v.(string); ok {
				// Validate and rewrite path
				clean := filepath.Clean(s)
				if !filepath.IsAbs(clean) {
					clean = filepath.Join(sandbox, clean)
				}
				// Check it's within sandbox
				rel, err := filepath.Rel(sandbox, clean)
				if err != nil || strings.HasPrefix(rel, "..") {
					// Path escapes sandbox — redirect to sandbox/basename to keep files contained.
					result[k] = filepath.Join(sandbox, filepath.Base(clean))
				} else {
					result[k] = clean
				}
				continue
			}
		}
		result[k] = v
	}
	return result
}

// goTypeToJSONType converts Go types to JSON schema types.
func goTypeToJSONType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "string"
	}
}
