// Package envcompat reads platform-neutral VEGA_* environment variables
// with a backward-compatible fallback to the legacy APEX_* names that
// govega shipped with first. The fallback exists so existing apexvega
// deployments and CI configs keep working; new products (galleyvega and
// future tenants) should set only the VEGA_* names.
//
// The package logs a one-time deprecation warning per legacy name when
// the legacy form is the only one set, so operators see a clear
// migration path in their logs without log spam on every request.
package envcompat

import (
	"log/slog"
	"os"
	"strings"
	"sync"
)

// loggedOnce tracks which legacy names we've already warned about, so a
// process that reads the same env var twice doesn't double-log.
var (
	loggedMu sync.Mutex
	logged   = map[string]bool{}
)

// Get returns the value of name, where name is the modern platform-
// neutral env var (e.g. VEGA_TENANT_ID). It transparently falls back to
// the legacy APEX_-prefixed equivalent (e.g. APEX_TENANT_ID) when the
// new name is empty.
//
// name must begin with "VEGA_" or Get panics — this is a programmer
// error in the caller, not a runtime configuration concern.
func Get(name string) string {
	if !strings.HasPrefix(name, "VEGA_") {
		panic("envcompat.Get: name must start with VEGA_, got " + name)
	}
	if v := os.Getenv(name); v != "" {
		return v
	}
	legacy := "APEX_" + strings.TrimPrefix(name, "VEGA_")
	v := os.Getenv(legacy)
	if v != "" {
		warnDeprecated(legacy, name)
	}
	return v
}

// warnDeprecated logs the rename hint once per legacy name per process.
func warnDeprecated(legacy, replacement string) {
	loggedMu.Lock()
	defer loggedMu.Unlock()
	if logged[legacy] {
		return
	}
	logged[legacy] = true
	slog.Warn("env var renamed; please migrate", "old", legacy, "new", replacement)
}

// resetForTest clears the dedup table so tests can exercise the warning
// path repeatedly. Not exported — only the test file in this package
// can call it via the same-package access rule.
func resetForTest() {
	loggedMu.Lock()
	defer loggedMu.Unlock()
	logged = map[string]bool{}
}
