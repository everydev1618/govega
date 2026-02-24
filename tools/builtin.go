package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// absPathRe matches absolute path tokens inside shell command strings.
// Stops at whitespace and common shell meta-characters.
var absPathRe = regexp.MustCompile(`(/[^\s"'<>|&;(){}\[\]\\]+)`)

// rewriteCommandPaths rewrites absolute paths in a shell command that escape
// the sandbox, redirecting them to sandbox/basename.
func rewriteCommandPaths(command, sandbox string) string {
	return absPathRe.ReplaceAllStringFunc(command, func(match string) string {
		clean := filepath.Clean(match)
		rel, err := filepath.Rel(sandbox, clean)
		if err != nil || strings.HasPrefix(rel, "..") {
			return filepath.Join(sandbox, filepath.Base(clean))
		}
		return match
	})
}

// sandboxEnv returns the current environment with HOME and TMPDIR pointed at
// the sandbox, preventing shell expansions like ~ from escaping.
func sandboxEnv(sandbox string) []string {
	env := os.Environ()
	result := make([]string, 0, len(env)+2)
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") || strings.HasPrefix(e, "TMPDIR=") {
			continue
		}
		result = append(result, e)
	}
	return append(result, "HOME="+sandbox, "TMPDIR="+sandbox)
}

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
			desc, _ := params["description"].(string)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", err
			}
			if t.OnFileWrite != nil {
				t.OnFileWrite(ctx, path, "write", desc)
			}
			return "File written successfully", nil
		},
		Params: map[string]ParamDef{
			"path":        {Type: "string", Description: "File path", Required: true},
			"content":     {Type: "string", Description: "Content to write", Required: true},
			"description": {Type: "string", Description: "Optional description of why this file is being written", Required: false},
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
			desc, _ := params["description"].(string)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return "", err
			}
			defer f.Close()
			if _, err := f.WriteString(content); err != nil {
				return "", err
			}
			if t.OnFileWrite != nil {
				t.OnFileWrite(ctx, path, "append", desc)
			}
			return "Content appended successfully", nil
		},
		Params: map[string]ParamDef{
			"path":        {Type: "string", Description: "File path", Required: true},
			"content":     {Type: "string", Description: "Content to append", Required: true},
			"description": {Type: "string", Description: "Optional description of why this file is being written", Required: false},
		},
	})

	RegisterEmailTool(t)

	t.Register("exec", ToolDef{
		Description: "Execute a shell command inside the workspace sandbox. The working directory is always the sandbox. Use this to run build tools, start servers, install dependencies, etc.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			command := params["command"].(string)

			// Determine working directory: effective sandbox (includes project subdir) if set, else cwd.
			sandbox := t.effectiveSandbox()
			workdir := sandbox
			if workdir == "" {
				var err error
				workdir, err = os.Getwd()
				if err != nil {
					return "", err
				}
			}

			// Optional subdirectory within the sandbox.
			if sub, ok := params["workdir"].(string); ok && sub != "" {
				candidate := sub
				if !filepath.IsAbs(candidate) {
					candidate = filepath.Join(workdir, candidate)
				}
				candidate = filepath.Clean(candidate)
				// Must stay within sandbox (if sandbox is set).
				if sandbox != "" {
					rel, err := filepath.Rel(sandbox, candidate)
					if err != nil || strings.HasPrefix(rel, "..") {
						candidate = workdir // silently fall back to sandbox root
					}
				}
				workdir = candidate
			}

			// Ensure workdir exists.
			if err := os.MkdirAll(workdir, 0755); err != nil {
				return "", fmt.Errorf("cannot create workdir %s: %w", workdir, err)
			}

			// Timeout: default 60 s, honour optional "timeout_seconds" param.
			timeout := 60 * time.Second
			if ts, ok := params["timeout_seconds"].(float64); ok && ts > 0 {
				timeout = time.Duration(ts) * time.Second
			}

			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			// Rewrite any absolute paths in the command that escape the sandbox,
			// and isolate HOME/TMPDIR so ~ doesn't point outside.
			if sandbox != "" {
				command = rewriteCommandPaths(command, sandbox)
			}

			cmd := exec.CommandContext(execCtx, "sh", "-c", command)
			cmd.Dir = workdir
			if sandbox != "" {
				cmd.Env = sandboxEnv(sandbox)
			}

			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf

			err := cmd.Run()
			output := buf.String()
			if len(output) > 8000 {
				output = output[:8000] + "\n... (truncated)"
			}
			if err != nil {
				return output, fmt.Errorf("command failed: %w\n%s", err, output)
			}
			return output, nil
		},
		Params: map[string]ParamDef{
			"command":         {Type: "string", Description: "Shell command to run (executed via sh -c)", Required: true},
			"workdir":         {Type: "string", Description: "Subdirectory within the workspace to run the command in (optional, must stay within sandbox)", Required: false},
			"timeout_seconds": {Type: "number", Description: "Max seconds to wait before killing the command (default 60)", Required: false},
		},
	})
}
