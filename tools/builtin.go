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
	"sync"
	"time"
)

// absPathRe matches absolute path tokens inside shell command strings.
// Stops at whitespace and common shell meta-characters.
var absPathRe = regexp.MustCompile(`(/[^\s"'<>|&;(){}\[\]\\]+)`)

// backgroundService tracks a long-running background process started by an agent.
type backgroundService struct {
	Name    string    `json:"name"`
	Command string    `json:"command"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	cmd     *exec.Cmd
	output  *ringBuffer
}

// ringBuffer is a simple circular buffer that keeps the last N bytes of output.
type ringBuffer struct {
	buf  []byte
	size int
	pos  int
	full bool
	mu   sync.Mutex
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size), size: size}
}

func (rb *ringBuffer) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for _, b := range p {
		rb.buf[rb.pos] = b
		rb.pos = (rb.pos + 1) % rb.size
		if rb.pos == 0 {
			rb.full = true
		}
	}
	return len(p), nil
}

func (rb *ringBuffer) String() string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if !rb.full {
		return string(rb.buf[:rb.pos])
	}
	return string(rb.buf[rb.pos:]) + string(rb.buf[:rb.pos])
}

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
			msg := "File written successfully"
			// Include accessible URL when a server base URL is configured.
			if t.baseURL != "" && t.sandbox != "" {
				relPath, err := filepath.Rel(t.sandbox, path)
				if err == nil && !strings.HasPrefix(relPath, "..") {
					msg += fmt.Sprintf("\nAccessible at: %s/workspace/%s", t.baseURL, relPath)
				}
			}
			return msg, nil
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

	// Background service management — for long-running processes like dev servers.
	t.Register("start_service", ToolDef{
		Description: "Start a long-running background process (e.g. npm dev server, python http.server). The process runs until explicitly stopped. Returns the service name and recent output.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			command, _ := params["command"].(string)
			if name == "" || command == "" {
				return "", fmt.Errorf("both name and command are required")
			}

			sandbox := t.effectiveSandbox()
			workdir := sandbox
			if workdir == "" {
				var err error
				workdir, err = os.Getwd()
				if err != nil {
					return "", err
				}
			}
			if sub, ok := params["workdir"].(string); ok && sub != "" {
				candidate := sub
				if !filepath.IsAbs(candidate) {
					candidate = filepath.Join(workdir, candidate)
				}
				candidate = filepath.Clean(candidate)
				if sandbox != "" {
					rel, err := filepath.Rel(sandbox, candidate)
					if err != nil || strings.HasPrefix(rel, "..") {
						candidate = workdir
					}
				}
				workdir = candidate
			}

			if err := os.MkdirAll(workdir, 0755); err != nil {
				return "", fmt.Errorf("cannot create workdir: %w", err)
			}

			t.servicesMu.Lock()
			if t.services == nil {
				t.services = make(map[string]*backgroundService)
			}
			if existing, ok := t.services[name]; ok {
				t.servicesMu.Unlock()
				return "", fmt.Errorf("service %q is already running (PID %d). Stop it first or use a different name", name, existing.PID)
			}
			t.servicesMu.Unlock()

			if sandbox != "" {
				command = rewriteCommandPaths(command, sandbox)
			}

			cmd := exec.Command("sh", "-c", command)
			cmd.Dir = workdir
			if sandbox != "" {
				cmd.Env = sandboxEnv(sandbox)
			}

			output := newRingBuffer(8192)
			cmd.Stdout = output
			cmd.Stderr = output

			if err := cmd.Start(); err != nil {
				return "", fmt.Errorf("failed to start service: %w", err)
			}

			svc := &backgroundService{
				Name:    name,
				Command: command,
				PID:     cmd.Process.Pid,
				Started: time.Now(),
				cmd:     cmd,
				output:  output,
			}

			t.servicesMu.Lock()
			t.services[name] = svc
			t.servicesMu.Unlock()

			// Monitor the process and clean up when it exits.
			go func() {
				cmd.Wait()
				t.servicesMu.Lock()
				delete(t.services, name)
				t.servicesMu.Unlock()
			}()

			// Wait briefly for startup output.
			time.Sleep(2 * time.Second)

			return fmt.Sprintf("Service %q started (PID %d).\nRecent output:\n%s", name, svc.PID, output.String()), nil
		},
		Params: map[string]ParamDef{
			"name":    {Type: "string", Description: "Unique name for this service (e.g. 'dev-server')", Required: true},
			"command": {Type: "string", Description: "Shell command to run (e.g. 'npm run dev -- --port 8080')", Required: true},
			"workdir": {Type: "string", Description: "Subdirectory within the workspace to run in (optional)", Required: false},
		},
	})

	t.Register("stop_service", ToolDef{
		Description: "Stop a running background service by name.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}

			t.servicesMu.Lock()
			svc, ok := t.services[name]
			if !ok {
				t.servicesMu.Unlock()
				return "", fmt.Errorf("no service named %q is running", name)
			}
			delete(t.services, name)
			t.servicesMu.Unlock()

			lastOutput := svc.output.String()
			if err := svc.cmd.Process.Kill(); err != nil {
				return fmt.Sprintf("Failed to kill service %q (PID %d): %s\nLast output:\n%s", name, svc.PID, err, lastOutput), nil
			}
			return fmt.Sprintf("Service %q stopped (PID %d).\nLast output:\n%s", name, svc.PID, lastOutput), nil
		},
		Params: map[string]ParamDef{
			"name": {Type: "string", Description: "Name of the service to stop", Required: true},
		},
	})

	t.Register("list_services", ToolDef{
		Description: "List all running background services with their status and recent output.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			t.servicesMu.Lock()
			defer t.servicesMu.Unlock()

			if len(t.services) == 0 {
				return "No background services running.", nil
			}

			var b strings.Builder
			for _, svc := range t.services {
				uptime := time.Since(svc.Started).Round(time.Second)
				b.WriteString(fmt.Sprintf("**%s** — PID %d, up %s\n  Command: %s\n  Recent output: %s\n\n",
					svc.Name, svc.PID, uptime, svc.Command, svc.output.String()))
			}
			return b.String(), nil
		},
		Params: map[string]ParamDef{},
	})

	t.Register("service_logs", ToolDef{
		Description: "Get recent output from a running background service.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}

			t.servicesMu.Lock()
			svc, ok := t.services[name]
			t.servicesMu.Unlock()

			if !ok {
				return "", fmt.Errorf("no service named %q is running", name)
			}
			return fmt.Sprintf("Service %q (PID %d) output:\n%s", name, svc.PID, svc.output.String()), nil
		},
		Params: map[string]ParamDef{
			"name": {Type: "string", Description: "Name of the service to get logs from", Required: true},
		},
	})
}
