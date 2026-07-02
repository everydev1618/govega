package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// StdioTransport implements Transport over subprocess stdin/stdout.
type StdioTransport struct {
	config  ServerConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser

	// Request tracking
	nextID   int64
	pending  map[int64]chan *JSONRPCResponse
	pendingMu sync.Mutex

	// Notification handling
	notifyHandler func(method string, params json.RawMessage)
	notifyMu      sync.RWMutex

	// Stderr capture for diagnostics
	stderrLines []string
	stderrMu    sync.Mutex

	// Lifecycle
	done     chan struct{}
	closeErr error
	mu       sync.Mutex
}

// mcpEnvAllowlist names environment variables safe to pass to MCP server
// subprocesses. Broader than the exec-tool allowlist (servers legitimately
// need HOME for caches/config, and node/python tooling paths) but still
// excludes secrets like API keys and tokens.
var mcpEnvAllowlist = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true, "TZ": true,
	"USER": true, "LOGNAME": true, "SHELL": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true,
	"NODE_PATH": true, "NVM_DIR": true, "VIRTUAL_ENV": true, "PYTHONPATH": true,
}

// mcpSubprocessEnv builds the environment for an MCP server subprocess:
// the allowlisted subset of the parent environment, operator additions from
// VEGA_MCP_ENV_PASSTHROUGH (comma-separated names), and the config-provided
// entries (which always win).
func mcpSubprocessEnv(configEnv map[string]string) []string {
	extra := make(map[string]bool)
	for _, name := range strings.Split(os.Getenv("VEGA_MCP_ENV_PASSTHROUGH"), ",") {
		if n := strings.TrimSpace(name); n != "" {
			extra[n] = true
		}
	}

	result := make([]string, 0, len(mcpEnvAllowlist)+len(configEnv))
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if _, overridden := configEnv[name]; overridden {
			continue // config value appended below
		}
		if mcpEnvAllowlist[name] || extra[name] {
			result = append(result, e)
		}
	}
	for k, v := range configEnv {
		result = append(result, k+"="+v)
	}
	return result
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(config ServerConfig) *StdioTransport {
	return &StdioTransport{
		config:  config,
		pending: make(map[int64]chan *JSONRPCResponse),
		done:    make(chan struct{}),
	}
}

// Connect starts the subprocess and establishes communication.
func (t *StdioTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Resolve binary path — auto-download from GitHub Releases if needed.
	command := t.config.Command
	if t.config.GitHubRepo != "" {
		if resolved, err := EnsureBinary(ctx, t.config.GitHubRepo, command); err == nil {
			command = resolved
		}
	}

	// Create command — use exec.Command (not CommandContext) so the subprocess
	// lifecycle is not tied to the connect context. The connect context may have
	// a short timeout for the handshake, but the process must survive beyond it.
	// The process is cleaned up by Close() when the transport is shut down.
	t.cmd = exec.Command(command, t.config.Args...)

	// Filtered environment: MCP servers are subprocesses running
	// third-party code — they must not inherit the server's secrets
	// (ANTHROPIC_API_KEY etc.), matching the exec tool's policy (P2-2).
	// Vars the server needs are passed explicitly via config.Env;
	// operators can widen the allowlist via VEGA_MCP_ENV_PASSTHROUGH.
	t.cmd.Env = mcpSubprocessEnv(t.config.Env)

	// Get pipes
	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Start process
	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// Start reading responses
	go t.readLoop()

	// Start reading stderr for debugging
	go t.readStderr()

	return nil
}

// Send sends a JSON-RPC request and waits for the response.
func (t *StdioTransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&t.nextID, 1)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	// Create response channel
	respCh := make(chan *JSONRPCResponse, 1)

	t.pendingMu.Lock()
	t.pending[id] = respCh
	t.pendingMu.Unlock()

	defer func() {
		t.pendingMu.Lock()
		ch := t.pending[id]
		delete(t.pending, id)
		t.pendingMu.Unlock()
		// Close channel to prevent goroutine leaks if response arrives after cleanup
		if ch != nil {
			// Drain channel if needed
			select {
			case <-ch:
			default:
			}
		}
	}()

	// Send request
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	t.mu.Lock()
	if t.stdin == nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport not connected")
	}
	_, err = fmt.Fprintf(t.stdin, "%s\n", data)
	t.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, fmt.Errorf("transport closed")
	case resp, ok := <-respCh:
		if !ok || resp == nil {
			if stderr := t.recentStderr(); stderr != "" {
				return nil, fmt.Errorf("transport closed before response received. Server stderr:\n%s", stderr)
			}
			return nil, fmt.Errorf("transport closed before response received")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a JSON-RPC notification: no id field, and no waiting for a
// response — servers never reply to notifications, so routing them through
// Send would block until the caller's context expired.
func (t *StdioTransport) Notify(ctx context.Context, method string, params any) error {
	notif := JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin == nil {
		return fmt.Errorf("transport not connected")
	}
	if _, err := fmt.Fprintf(t.stdin, "%s\n", data); err != nil {
		return fmt.Errorf("write notification: %w", err)
	}
	return nil
}

// stdioCloseGrace is how long Close waits for the subprocess to exit after
// stdin is closed before killing it. Var so tests can shrink it.
var stdioCloseGrace = 5 * time.Second

// Close shuts down the transport. The subprocess gets stdioCloseGrace to
// exit after stdin closes; a subprocess that ignores EOF is killed and
// reaped rather than wedging Close (and, via the transport mutex, every
// concurrent Send) forever.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	// Signal shutdown
	select {
	case <-t.done:
		// Already closed
		err := t.closeErr
		t.mu.Unlock()
		return err
	default:
		close(t.done)
	}

	// Close stdin to signal subprocess
	if t.stdin != nil {
		t.stdin.Close()
	}
	cmd := t.cmd
	// Release the mutex before waiting — holding it through a stubborn
	// subprocess's exit would deadlock concurrent Sends.
	t.mu.Unlock()

	var closeErr error
	if cmd != nil && cmd.Process != nil {
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		select {
		case closeErr = <-waited:
		case <-time.After(stdioCloseGrace):
			slog.Warn("mcp server ignored stdin close; killing",
				"server", t.config.Name, "grace", stdioCloseGrace)
			_ = cmd.Process.Kill()
			closeErr = <-waited // Wait returns once killed; reaps the process
		}
	}

	t.mu.Lock()
	t.closeErr = closeErr
	t.mu.Unlock()
	return closeErr
}

// OnNotification registers a handler for server notifications.
func (t *StdioTransport) OnNotification(handler func(method string, params json.RawMessage)) {
	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()
	t.notifyHandler = handler
}

// readLoop reads JSON-RPC messages from stdout.
func (t *StdioTransport) readLoop() {
	scanner := bufio.NewScanner(t.stdout)
	// Increase buffer size for large responses
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Try to parse as response first
		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err == nil && resp.ID != 0 {
			t.pendingMu.Lock()
			if ch, ok := t.pending[resp.ID]; ok {
				ch <- &resp
			}
			t.pendingMu.Unlock()
			continue
		}

		// Try to parse as notification
		var notif JSONRPCNotification
		if err := json.Unmarshal(line, &notif); err == nil && notif.Method != "" {
			t.notifyMu.RLock()
			handler := t.notifyHandler
			t.notifyMu.RUnlock()

			if handler != nil {
				params, _ := json.Marshal(notif.Params)
				go handler(notif.Method, params)
			}
		}
	}

	// Close all pending requests
	t.pendingMu.Lock()
	for _, ch := range t.pending {
		close(ch)
	}
	t.pending = make(map[int64]chan *JSONRPCResponse)
	t.pendingMu.Unlock()
}

// readStderr reads stderr, logs it, and buffers recent lines for diagnostics.
func (t *StdioTransport) readStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			slog.Debug("mcp server stderr",
				"server", t.config.Name,
				"line", line,
			)
			t.stderrMu.Lock()
			if len(t.stderrLines) < 20 {
				t.stderrLines = append(t.stderrLines, line)
			}
			t.stderrMu.Unlock()
		}
	}
}

// recentStderr returns buffered stderr output for error diagnostics.
func (t *StdioTransport) recentStderr() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	if len(t.stderrLines) == 0 {
		return ""
	}
	return strings.Join(t.stderrLines, "\n")
}
