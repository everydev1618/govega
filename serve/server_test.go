package serve

import (
	"net"
	"testing"
)

func TestAutoPortAllocation(t *testing.T) {
	// When Addr is empty, resolveAddr should bind to a random free port.
	addr := ""
	ln, resolvedAddr, err := resolveAddr(addr)
	if err != nil {
		t.Fatalf("resolveAddr(%q) error: %v", addr, err)
	}
	defer ln.Close()

	if resolvedAddr == "" || resolvedAddr == ":0" {
		t.Fatalf("expected a resolved address with a port, got %q", resolvedAddr)
	}

	// Verify we got a valid port.
	_, port, err := net.SplitHostPort(resolvedAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error: %v", resolvedAddr, err)
	}
	if port == "0" || port == "" {
		t.Fatalf("expected a non-zero port, got %q", port)
	}
}

func TestExplicitAddr(t *testing.T) {
	// When Addr is provided, resolveAddr should bind to that exact address.
	ln, resolvedAddr, err := resolveAddr(":0") // use :0 so test doesn't clash
	if err != nil {
		t.Fatalf("resolveAddr(%q) error: %v", ":0", err)
	}
	defer ln.Close()

	_, port, err := net.SplitHostPort(resolvedAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error: %v", resolvedAddr, err)
	}
	if port == "0" || port == "" {
		t.Fatalf("expected a real port, got %q", port)
	}
}
