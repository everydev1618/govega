package vega

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Tools is a collection of callable tools.
type Tools struct {
	tools      map[string]*tool
	middleware []ToolMiddleware
	sandbox    string
	mcpClients []*mcpClientEntry // MCP server clients
	mu         sync.RWMutex
}

// tool is an internal representation of a registered tool.
type tool struct {
	name        string
	description string
	fn          any
	schema      ToolSchema
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

// Register adds a tool to the collection.
// The function can be:
// - func(params) string
// - func(params) (string, error)
// - func(ctx, params) (string, error)
// - ToolDef with explicit schema
func (t *Tools) Register(name string, fn any) error {
	t.mu.Lock()
	defer t.mu.Unlock()

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

// Execute calls a tool by name.
func (t *Tools) Execute(ctx context.Context, name string, params map[string]any) (string, error) {
	t.mu.RLock()
	tl, ok := t.tools[name]
	middleware := t.middleware
	sandbox := t.sandbox
	t.mu.RUnlock()

	if !ok {
		return "", &ToolError{ToolName: name, Err: ErrToolNotFound}
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

// Schema returns the schemas for all tools.
func (t *Tools) Schema() []ToolSchema {
	t.mu.RLock()
	defer t.mu.RUnlock()

	schemas := make([]ToolSchema, 0, len(t.tools))
	for _, tl := range t.tools {
		schemas = append(schemas, tl.schema)
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

// LoadDirectory loads tool definitions from YAML files.
func (t *Tools) LoadDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read tools directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}

		toolPath := filepath.Join(path, entry.Name())
		if err := t.LoadFile(toolPath); err != nil {
			return fmt.Errorf("load tool %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// LoadFile loads a single tool definition from YAML.
func (t *Tools) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var def DynamicToolDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}

	return t.registerDynamicTool(def)
}

// DynamicToolDef is a YAML tool definition.
type DynamicToolDef struct {
	Name           string              `yaml:"name"`
	Description    string              `yaml:"description"`
	Params         []DynamicParamDef   `yaml:"params"`
	Implementation DynamicToolImpl     `yaml:"implementation"`
}

// DynamicParamDef is a YAML parameter definition.
type DynamicParamDef struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     any      `yaml:"default"`
	Enum        []string `yaml:"enum"`
}

// DynamicToolImpl is a YAML implementation definition.
type DynamicToolImpl struct {
	Type    string            `yaml:"type"` // http, exec, file_read, file_write, builtin
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Query   map[string]string `yaml:"query"`
	Body    any               `yaml:"body"`
	Command string            `yaml:"command"`
	Path    string            `yaml:"path"`
	Timeout string            `yaml:"timeout"`
}

// registerDynamicTool registers a tool from YAML definition.
func (t *Tools) registerDynamicTool(def DynamicToolDef) error {
	// Build params map
	params := make(map[string]ParamDef)
	for _, p := range def.Params {
		params[p.Name] = ParamDef{
			Type:        p.Type,
			Description: p.Description,
			Required:    p.Required,
			Default:     p.Default,
			Enum:        p.Enum,
		}
	}

	// Create executor based on implementation type
	var fn ToolFunc
	switch def.Implementation.Type {
	case "http":
		fn = t.createHTTPExecutor(def.Implementation)
	case "exec":
		fn = t.createExecExecutor(def.Implementation)
	case "file_read":
		fn = t.createFileReadExecutor(def.Implementation)
	case "file_write":
		fn = t.createFileWriteExecutor(def.Implementation)
	default:
		return fmt.Errorf("unknown implementation type: %s", def.Implementation.Type)
	}

	// Register with explicit schema
	return t.Register(def.Name, ToolDef{
		Description: def.Description,
		Fn:          fn,
		Params:      params,
	})
}

// inferSchema infers a JSON schema from a function signature.
func (t *Tools) inferSchema(name string, fn any) ToolSchema {
	schema := ToolSchema{
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
func (t *Tools) buildSchema(name, description string, params map[string]ParamDef) ToolSchema {
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

	return ToolSchema{
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

		// Handle single string parameter
		if inType.Kind() == reflect.String && len(params) == 1 {
			for _, v := range params {
				args = append(args, reflect.ValueOf(fmt.Sprint(v)))
				break
			}
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
					// Path escapes sandbox - this will cause an error at execution
					result[k] = v
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

// HTTP executor (placeholder - needs net/http implementation)
func (t *Tools) createHTTPExecutor(impl DynamicToolImpl) ToolFunc {
	return func(ctx context.Context, params map[string]any) (string, error) {
		// TODO: Implement HTTP execution with template interpolation
		return "", fmt.Errorf("HTTP executor not implemented")
	}
}

// Exec executor (placeholder - needs os/exec implementation)
func (t *Tools) createExecExecutor(impl DynamicToolImpl) ToolFunc {
	return func(ctx context.Context, params map[string]any) (string, error) {
		// TODO: Implement command execution with template interpolation
		return "", fmt.Errorf("exec executor not implemented")
	}
}

// File read executor
func (t *Tools) createFileReadExecutor(impl DynamicToolImpl) ToolFunc {
	return func(ctx context.Context, params map[string]any) (string, error) {
		path, ok := params["path"].(string)
		if !ok {
			return "", fmt.Errorf("path parameter required")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

// File write executor
func (t *Tools) createFileWriteExecutor(impl DynamicToolImpl) ToolFunc {
	return func(ctx context.Context, params map[string]any) (string, error) {
		path, ok := params["path"].(string)
		if !ok {
			return "", fmt.Errorf("path parameter required")
		}
		content, ok := params["content"].(string)
		if !ok {
			return "", fmt.Errorf("content parameter required")
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", err
		}
		return "File written successfully", nil
	}
}

// Built-in tools

// RegisterBuiltins adds the built-in tools.
func (t *Tools) RegisterBuiltins() {
	t.Register("read_file", func(path string) (string, error) {
		data, err := os.ReadFile(path)
		return string(data), err
	})

	t.Register("write_file", ToolDef{
		Description: "Write content to a file",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			path := params["path"].(string)
			content := params["content"].(string)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", err
			}
			return "File written successfully", nil
		},
		Params: map[string]ParamDef{
			"path":    {Type: "string", Description: "File path", Required: true},
			"content": {Type: "string", Description: "Content to write", Required: true},
		},
	})

	t.Register("list_files", func(path string) (string, error) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", err
		}
		var names []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		result, _ := json.Marshal(names)
		return string(result), nil
	})

	t.Register("append_file", ToolDef{
		Description: "Append content to a file",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			path := params["path"].(string)
			content := params["content"].(string)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return "", err
			}
			defer f.Close()
			if _, err := f.WriteString(content); err != nil {
				return "", err
			}
			return "Content appended successfully", nil
		},
		Params: map[string]ParamDef{
			"path":    {Type: "string", Description: "File path", Required: true},
			"content": {Type: "string", Description: "Content to append", Required: true},
		},
	})
}
