package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStdioNotifyIsFireAndForget verifies notifications are sent without an
// id and without waiting for a response. The subprocess is `cat`, which
// echoes the line back: because the payload has no id, the read loop routes
// the echo to the notification handler — if an id were present it would be
// treated as an (unmatched) response instead.
func TestStdioNotifyIsFireAndForget(t *testing.T) {
	tr := NewStdioTransport(ServerConfig{Name: "cat", Command: "cat"})
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer tr.Close()

	var mu sync.Mutex
	var got []string
	tr.OnNotification(func(method string, _ json.RawMessage) {
		mu.Lock()
		got = append(got, method)
		mu.Unlock()
	})

	start := time.Now()
	if err := tr.Notify(context.Background(), "notifications/initialized", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Notify blocked for %v — notifications must not wait for a response", elapsed)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("echoed notification never reached the handler — payload likely carried an id")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if got[0] != "notifications/initialized" {
		t.Errorf("handler saw method %q", got[0])
	}
}

// TestStdioCloseKillsStubbornProcess verifies Close doesn't hang forever on
// a subprocess that ignores stdin EOF: after the grace period the process is
// killed and reaped.
func TestStdioCloseKillsStubbornProcess(t *testing.T) {
	prev := stdioCloseGrace
	stdioCloseGrace = 200 * time.Millisecond
	defer func() { stdioCloseGrace = prev }()

	tr := NewStdioTransport(ServerConfig{Name: "stubborn", Command: "sleep", Args: []string{"30"}})
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	done := make(chan struct{})
	go func() {
		tr.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on a subprocess that ignores stdin EOF")
	}

	if tr.cmd.ProcessState == nil {
		t.Error("subprocess was not reaped (zombie)")
	}
}

// TestStdioCloseDoesNotBlockSend verifies a concurrent Send fails fast while
// Close is waiting out the grace period, instead of deadlocking on the
// transport mutex.
func TestStdioCloseDoesNotBlockSend(t *testing.T) {
	prev := stdioCloseGrace
	stdioCloseGrace = time.Second
	defer func() { stdioCloseGrace = prev }()

	tr := NewStdioTransport(ServerConfig{Name: "stubborn", Command: "sleep", Args: []string{"30"}})
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	closed := make(chan struct{})
	go func() {
		tr.Close()
		close(closed)
	}()
	time.Sleep(50 * time.Millisecond) // let Close signal shutdown

	errCh := make(chan error, 1)
	go func() {
		_, err := tr.Send(context.Background(), "tools/list", nil)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("Send during Close should fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send deadlocked against Close")
	}

	// Wait out Close before the deferred stdioCloseGrace restore — the
	// grace timer reads the global.
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close never finished")
	}
}

// TestStdioEnvFiltered verifies the subprocess environment is stripped to
// the allowlist: server secrets (API keys) must not leak into MCP server
// subprocesses, matching the exec tool's env policy (P2-2).
func TestStdioEnvFiltered(t *testing.T) {
	t.Setenv("VEGA_TEST_SECRET_CANARY", "leaked")

	tr := NewStdioTransport(ServerConfig{
		Name:    "envdump",
		Command: "sh",
		Args:    []string{"-c", "env 1>&2; sleep 1"},
		Env:     map[string]string{"MCP_CONFIG_VAR": "explicit"},
	})
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer tr.Close()

	deadline := time.Now().Add(3 * time.Second)
	var dump string
	for {
		dump = tr.recentStderr()
		if strings.Contains(dump, "PATH=") || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !strings.Contains(dump, "PATH=") {
		t.Fatalf("env dump missing PATH — did the subprocess run? stderr: %q", dump)
	}
	if strings.Contains(dump, "VEGA_TEST_SECRET_CANARY") {
		t.Error("parent environment leaked into the MCP subprocess")
	}
	if !strings.Contains(dump, "MCP_CONFIG_VAR=explicit") {
		t.Error("config-provided env var missing from the subprocess")
	}
}
