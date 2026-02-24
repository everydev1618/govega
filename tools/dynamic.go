package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// DynamicToolDef is a YAML tool definition.
type DynamicToolDef struct {
	Name           string            `yaml:"name"`
	Description    string            `yaml:"description"`
	Params         []DynamicParamDef `yaml:"params"`
	Implementation DynamicToolImpl   `yaml:"implementation"`
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

// RegisterDynamicTool registers a tool from a DynamicToolDef.
func (t *Tools) RegisterDynamicTool(def DynamicToolDef) error {
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

	return t.RegisterDynamicTool(def)
}

// mergeSettings returns a new params map with settings as defaults, user params taking precedence.
func (t *Tools) mergeSettings(params map[string]any) map[string]any {
	t.mu.RLock()
	settings := t.settings
	t.mu.RUnlock()

	if len(settings) == 0 {
		return params
	}

	merged := make(map[string]any, len(settings)+len(params))
	for k, v := range settings {
		merged[k] = v
	}
	for k, v := range params {
		merged[k] = v // user params take precedence
	}
	return merged
}

// HTTP executor with template interpolation support.
func (t *Tools) createHTTPExecutor(impl DynamicToolImpl) ToolFunc {
	return func(ctx context.Context, params map[string]any) (string, error) {
		// Merge settings into params for template interpolation.
		params = t.mergeSettings(params)

		// Parse timeout
		timeout := 30 * time.Second
		if impl.Timeout != "" {
			if d, err := time.ParseDuration(impl.Timeout); err == nil {
				timeout = d
			}
		}

		// Create context with timeout
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Interpolate URL with params
		url, err := interpolateTemplate(impl.URL, params)
		if err != nil {
			return "", fmt.Errorf("interpolate URL: %w", err)
		}

		// Determine method (default GET)
		method := impl.Method
		if method == "" {
			method = "GET"
		}
		method = strings.ToUpper(method)

		// Build request body if present
		var bodyReader io.Reader
		if impl.Body != nil {
			// Handle body as template string or map
			switch body := impl.Body.(type) {
			case string:
				interpolated, err := interpolateTemplate(body, params)
				if err != nil {
					return "", fmt.Errorf("interpolate body: %w", err)
				}
				bodyReader = strings.NewReader(interpolated)
			case map[string]any:
				// Interpolate map values
				interpolatedMap := make(map[string]any)
				for k, v := range body {
					if s, ok := v.(string); ok {
						interpolated, err := interpolateTemplate(s, params)
						if err != nil {
							return "", fmt.Errorf("interpolate body field %s: %w", k, err)
						}
						interpolatedMap[k] = interpolated
					} else {
						interpolatedMap[k] = v
					}
				}
				jsonBody, err := json.Marshal(interpolatedMap)
				if err != nil {
					return "", fmt.Errorf("marshal body: %w", err)
				}
				bodyReader = bytes.NewReader(jsonBody)
			default:
				jsonBody, err := json.Marshal(body)
				if err != nil {
					return "", fmt.Errorf("marshal body: %w", err)
				}
				bodyReader = bytes.NewReader(jsonBody)
			}
		}

		// Create request
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}

		// Set headers
		for k, v := range impl.Headers {
			interpolated, err := interpolateTemplate(v, params)
			if err != nil {
				return "", fmt.Errorf("interpolate header %s: %w", k, err)
			}
			req.Header.Set(k, interpolated)
		}

		// Set default content type for JSON body
		if bodyReader != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		// Add query parameters
		if len(impl.Query) > 0 {
			q := req.URL.Query()
			for k, v := range impl.Query {
				interpolated, err := interpolateTemplate(v, params)
				if err != nil {
					return "", fmt.Errorf("interpolate query %s: %w", k, err)
				}
				q.Set(k, interpolated)
			}
			req.URL.RawQuery = q.Encode()
		}

		// Execute request
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("http request: %w", err)
		}
		defer resp.Body.Close()

		// Read response
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}

		// Check status
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("http error %d: %s", resp.StatusCode, string(body))
		}

		return string(body), nil
	}
}

// Exec executor with template interpolation support.
func (t *Tools) createExecExecutor(impl DynamicToolImpl) ToolFunc {
	return func(ctx context.Context, params map[string]any) (string, error) {
		// Merge settings into params for template interpolation.
		params = t.mergeSettings(params)

		// Parse timeout
		timeout := 30 * time.Second
		if impl.Timeout != "" {
			if d, err := time.ParseDuration(impl.Timeout); err == nil {
				timeout = d
			}
		}

		// Create context with timeout
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Interpolate command with params
		command, err := interpolateTemplate(impl.Command, params)
		if err != nil {
			return "", fmt.Errorf("interpolate command: %w", err)
		}

		// Parse command into parts (simple shell-like parsing)
		cmdParts := parseCommand(command)
		if len(cmdParts) == 0 {
			return "", fmt.Errorf("empty command")
		}

		// Create command
		cmd := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...)

		// Capture output
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		// Set working directory if specified in params
		if workDir, ok := params["work_dir"].(string); ok && workDir != "" {
			cmd.Dir = workDir
		}

		// Run command
		err = cmd.Run()
		output := stdout.String()
		if stderr.Len() > 0 {
			if output != "" {
				output += "\n"
			}
			output += stderr.String()
		}

		if err != nil {
			return output, fmt.Errorf("command failed: %w", err)
		}

		return output, nil
	}
}

// interpolateTemplate replaces {{.field}} placeholders with values from params.
func interpolateTemplate(tmplStr string, params map[string]any) (string, error) {
	// Quick check if interpolation is needed
	if !strings.Contains(tmplStr, "{{") {
		return tmplStr, nil
	}

	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// parseCommand splits a command string into parts, respecting quotes.
func parseCommand(cmd string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range cmd {
		switch {
		case r == '"' || r == '\'':
			if !inQuote {
				inQuote = true
				quoteChar = r
			} else if r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				current.WriteRune(r)
			}
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
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
