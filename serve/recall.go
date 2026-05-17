package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	vega "github.com/everydev1618/govega"
)

// RecallSource indicates how a memory page made it into the agent's
// context for the current turn. "read" means the agent explicitly
// called memory_read; "active" means the page's `active: true` flag
// caused its body to be injected into the system prompt. Both flow
// to the same UI surface — the user sees "remembered from X"
// regardless of mechanism, which is the point: the experience is
// "Vega remembered," not "Vega read." Refs govega#100.
type RecallSource string

const (
	RecallSourceRead   RecallSource = "read"
	RecallSourceActive RecallSource = "active"
)

// RecallEntry is a single recall event in a turn's ledger. The
// fields are JSON-friendly so the chat handler can serialize the
// list directly into the response payload.
type RecallEntry struct {
	Scope  MemoryScope  `json:"scope"`
	Path   string       `json:"path"`
	Source RecallSource `json:"source"`
	At     time.Time    `json:"at"`
}

// RecallLedger collects RecallEntry values during a single turn so
// the chat handler can surface them in the response payload. Safe
// for concurrent appends — the active-injection collector runs on
// the request goroutine but memory_read can fire from a tool-loop
// goroutine. Refs govega#100.
type RecallLedger struct {
	mu      sync.Mutex
	entries []RecallEntry
}

// NewRecallLedger returns a fresh ledger ready to accept entries.
func NewRecallLedger() *RecallLedger {
	return &RecallLedger{}
}

// Add appends an entry. Stamps At with time.Now if the caller
// didn't fill it — the ledger is the authoritative timeline so
// callers shouldn't have to plumb a clock through every call site.
func (l *RecallLedger) Add(e RecallEntry) {
	if l == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

// Entries returns a copy of the recorded entries in append order.
func (l *RecallLedger) Entries() []RecallEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]RecallEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

type recallCtxKey struct{}

// ContextWithRecall attaches a RecallLedger to ctx so tools and
// helpers running under it can record what they used.
func ContextWithRecall(ctx context.Context, ledger *RecallLedger) context.Context {
	return context.WithValue(ctx, recallCtxKey{}, ledger)
}

// RecallFromContext returns the ledger attached to ctx, or nil when
// none is present. Callers must nil-check before appending; both
// RecallLedger.Add and Entries tolerate a nil receiver so callers
// can be tidy.
func RecallFromContext(ctx context.Context) *RecallLedger {
	l, _ := ctx.Value(recallCtxKey{}).(*RecallLedger)
	return l
}

// readMemoryPageForRecall is the recall-aware variant of the
// memory_read tool body. Extracted so unit tests can exercise the
// ledger plumbing without spinning up the interpreter. Returns the
// formatted page content (frontmatter + body) — same shape the tool
// returns to the LLM — and appends a RecallEntry to any ledger
// attached to ctx.
func readMemoryPageForRecall(ctx context.Context, path, rawScope string) (string, error) {
	store, userID, agent, err := memoryFromContext(ctx)
	if err != nil {
		return "", err
	}
	scope, scopeID, err := resolveScope(rawScope, userID, agent)
	if err != nil {
		return "", err
	}
	p, err := store.GetMemoryPage(scope, scopeID, userID, path)
	if err != nil {
		return "", fmt.Errorf("read memory page: %w", err)
	}
	if p == nil {
		return fmt.Sprintf("(no page at %q in %s wiki)", path, scope), nil
	}
	RecallFromContext(ctx).Add(RecallEntry{
		Scope:  scope,
		Path:   path,
		Source: RecallSourceRead,
	})
	out := p.Content
	if p.Frontmatter != "" {
		out = "---\n" + p.Frontmatter + "---\n" + out
	}
	return out, nil
}

// writeRecalledSSE emits a chat event of type "recalled" listing
// the memory pages that backed the just-finished stream, when any
// are present. No-op when the list is empty so clients that don't
// render recall don't see noise events. The payload is a normal
// ChatEvent so the FE's existing SSE parser routes it by `type`
// without needing a custom event-line code path.
func writeRecalledSSE(w io.Writer, as *activeStream) {
	as.mu.Lock()
	entries := append([]RecallEntry(nil), as.recalled...)
	as.mu.Unlock()
	if len(entries) == 0 {
		return
	}
	data, err := json.Marshal(vega.ChatEvent{
		Type:     vega.ChatEventRecalled,
		Recalled: toWireRecall(entries),
	})
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: recalled\ndata: %s\n\n", data)
}

// toWireRecall converts an internal ledger slice to the
// dependency-free wire shape carried on ChatEvent. Keeps the typed
// MemoryScope / RecallSource values on this side of the boundary
// while the root package stays free of serve-layer types.
func toWireRecall(entries []RecallEntry) []vega.ChatRecallEntry {
	out := make([]vega.ChatRecallEntry, len(entries))
	for i, e := range entries {
		out[i] = vega.ChatRecallEntry{
			Scope:  string(e.Scope),
			Path:   e.Path,
			Source: vega.ChatRecallSource(e.Source),
			At:     e.At,
		}
	}
	return out
}

// formatWikiMemoryForInjectionWithCtx is the recall-aware variant
// of formatWikiMemoryForInjection. It runs the same logic but also
// records any `active: true` pages whose bodies were injected, so
// the chat UI can surface "remembered from X" pills for them.
//
// The unparametered variant calls this with a background ctx and
// discards the ledger — both forms exist so call sites that don't
// care about recall don't have to plumb context through.
func formatWikiMemoryForInjectionWithCtx(ctx context.Context, store Store, userID, agent string) string {
	out := formatWikiMemoryForInjection(store, userID, agent)
	if out == "" {
		return out
	}
	if l := RecallFromContext(ctx); l != nil {
		for _, p := range collectActivePages(store, userID, agent, activeBodyInjectionCap) {
			l.Add(RecallEntry{
				Scope:  p.Scope,
				Path:   p.Path,
				Source: RecallSourceActive,
			})
		}
	}
	return out
}
