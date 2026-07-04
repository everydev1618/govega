package serve

import (
	"net/http"
	"strings"
	"time"
)

// HTTP handlers for the wiki memory system (govega#71). All read-only
// for now; agents still own the write surface via the memory_* tools.
//
// Auth: like the channel handlers, scope is keyed off X-Auth-User
// (default "default"). Memory is always per-user — there's no
// cross-user read access.

// memoryGraphNode is one wiki page rendered as a graph node.
//
// ID is the canonical node identifier in this response. For single-scope
// requests (the default and ?scope=agent) ID equals Path so existing
// callers keep working. For ?scope=all, ID is "{scope}:{scope_id}:{path}"
// so the same logical path coming from multiple wikis stays unique.
//
// Scope is "user" or "agent:<name>" — the frontend uses it for coloring
// and for the "show only agent X" filter.
//
// Ghost is true when the page doesn't exist as a row in memory_pages but
// is referenced by some edge — the server emits a placeholder so the
// frontend doesn't have to synthesize one.
type memoryGraphNode struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	Bytes     int    `json:"bytes"`
	UpdatedAt string `json:"updated_at"`
	Cluster   string `json:"cluster"`
	Scope     string `json:"scope"`
	Ghost     bool   `json:"ghost,omitempty"`
}

// memoryGraphEdge is one directed link between pages. From/To are node
// IDs — not raw paths — so they remain unambiguous under scope=all.
// Weight is always 1 today; the field is kept so the frontend can
// scale edge thickness once we start carrying co-occurrence counts.
type memoryGraphEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"`
}

// memoryGraphResponse is the payload for GET /api/v1/memory/graph.
// `generated_at` lets the frontend show "as of" and decide whether
// to refetch.
type memoryGraphResponse struct {
	Nodes       []memoryGraphNode `json:"nodes"`
	Edges       []memoryGraphEdge `json:"edges"`
	GeneratedAt string            `json:"generated_at"`
}

// memoryPageMetadata is one row of GET /api/v1/memory/pages. Content
// is omitted so the list payload stays small; clients fetch full
// content via GET /api/v1/memory/page?path=...
type memoryPageMetadata struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Bytes     int    `json:"bytes"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
	Cluster   string `json:"cluster"`
}

// handleMemoryGraph returns the node/edge graph for the calling user's
// wikis. Query params:
//
//	scope=user|agent|all  (default "user")
//	agent=<name>          (required when scope=agent)
//
// scope=user returns just the shared user wiki (back-compat default).
// scope=agent returns one agent's private wiki.
// scope=all unions the user wiki with every agent wiki the user owns;
// node IDs are prefixed with "{scope}:{scope_id}:" to keep paths that
// repeat across wikis unique.
//
// Edges that reference paths with no corresponding page are emitted as
// ghost nodes — the server resolves dangling links so the frontend can
// just render what it receives.
func (s *Server) handleMemoryGraph(w http.ResponseWriter, r *http.Request) {
	userID := authUser(r)
	scopeParam := r.URL.Query().Get("scope")
	if scopeParam == "" {
		scopeParam = "user"
	}
	agentParam := r.URL.Query().Get("agent")

	var sources []memoryGraphSource
	switch scopeParam {
	case "user":
		sources = append(sources, memoryGraphSource{scope: MemoryScopeUser, scopeID: userID, label: "user"})
	case "agent":
		if agentParam == "" {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "scope=agent requires the agent query param"})
			return
		}
		sources = append(sources, memoryGraphSource{scope: MemoryScopeAgent, scopeID: agentParam, label: "agent:" + agentParam})
	case "all":
		sources = append(sources, memoryGraphSource{scope: MemoryScopeUser, scopeID: userID, label: "user"})
		agentIDs, err := s.store.ListMemoryScopeIDs(MemoryScopeAgent, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		for _, id := range agentIDs {
			sources = append(sources, memoryGraphSource{scope: MemoryScopeAgent, scopeID: id, label: "agent:" + id})
		}
	default:
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "scope must be one of user, agent, all"})
		return
	}

	// In single-scope mode keep node IDs equal to the raw path so existing
	// callers (the list-view Memory page) don't break. In all-scope mode we
	// always namespace IDs with "{scope}:{scope_id}:" to avoid collisions.
	multiScope := scopeParam == "all"

	nodes := []memoryGraphNode{}
	edges := []memoryGraphEdge{}
	known := map[string]int{} // node ID → index in nodes

	for _, src := range sources {
		pages, err := s.store.ListMemoryPages(src.scope, src.scopeID, userID, "")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		for _, p := range pages {
			id := graphNodeID(multiScope, src.label, p.Path)
			n := memoryGraphNode{
				ID:        id,
				Path:      p.Path,
				Title:     titleForPath(p.Path),
				Bytes:     len(p.Content),
				UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
				Cluster:   clusterForPath(p.Path),
				Scope:     src.label,
			}
			known[id] = len(nodes)
			nodes = append(nodes, n)
		}

		links, err := s.store.ListMemoryLinks(src.scope, src.scopeID, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		for _, l := range links {
			fromID := graphNodeID(multiScope, src.label, l.FromPath)
			toID := graphNodeID(multiScope, src.label, l.ToPath)
			// Ghost-fill any endpoint we haven't seen yet. The page may
			// not exist in this scope (yet) but the edge already does.
			if _, ok := known[fromID]; !ok {
				known[fromID] = len(nodes)
				nodes = append(nodes, ghostNode(fromID, l.FromPath, src.label))
			}
			if _, ok := known[toID]; !ok {
				known[toID] = len(nodes)
				nodes = append(nodes, ghostNode(toID, l.ToPath, src.label))
			}
			edges = append(edges, memoryGraphEdge{From: fromID, To: toID, Weight: 1})
		}
	}

	writeJSON(w, http.StatusOK, memoryGraphResponse{
		Nodes:       nodes,
		Edges:       edges,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// memoryGraphSource is one wiki being included in the graph response.
type memoryGraphSource struct {
	scope   MemoryScope
	scopeID string
	label   string // "user" or "agent:<name>"
}

// graphNodeID stitches a scope label and path into a unique node ID.
// In single-scope mode it returns the raw path (back-compat).
func graphNodeID(multiScope bool, scopeLabel, path string) string {
	if !multiScope {
		return path
	}
	return scopeLabel + ":" + path
}

// ghostNode builds a placeholder node for a page referenced by an edge
// but missing from memory_pages.
func ghostNode(id, path, scopeLabel string) memoryGraphNode {
	return memoryGraphNode{
		ID:      id,
		Path:    path,
		Title:   titleForPath(path),
		Bytes:   0,
		Cluster: "ghost",
		Scope:   scopeLabel,
		Ghost:   true,
	}
}

// handleListMemoryPages returns every page in the calling user's
// shared wiki with metadata-only fields. No content — clients call
// handleGetMemoryPage per page when they need the body. Ordered by
// updated_at DESC.
func (s *Server) handleListMemoryPages(w http.ResponseWriter, r *http.Request) {
	userID := authUser(r)
	pages, err := s.store.ListMemoryPages(MemoryScopeUser, userID, userID, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]memoryPageMetadata, 0, len(pages))
	for _, p := range pages {
		out = append(out, memoryPageMetadata{
			Path:      p.Path,
			Title:     titleForPath(p.Path),
			Bytes:     len(p.Content),
			UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
			CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
			Cluster:   clusterForPath(p.Path),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetMemoryPage returns one full page (content + frontmatter).
// 400 on missing `path` query param, 404 when the page doesn't exist.
func (s *Server) handleGetMemoryPage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "path query param is required"})
		return
	}
	userID := authUser(r)
	page, err := s.store.GetMemoryPage(MemoryScopeUser, userID, userID, path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if page == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "memory page not found"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// authUser pulls the calling user out of X-Auth-User (set by the
// auth middleware), defaulting to "default" to match every other
// handler in this file's neighbours.
func authUser(r *http.Request) string {
	u := r.Header.Get("X-Auth-User")
	if u == "" {
		return "default"
	}
	return u
}

// titleForPath derives a human-ish title for a memory page from its
// path. "topics/sushi.md" → "sushi". "MEMORY.md" stays MEMORY.
func titleForPath(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

// clusterForPath buckets a page by path prefix so the graph viewer
// can color/group related pages. Stable, cheap, and good enough until
// we have enough data to motivate embedding-based clustering.
func clusterForPath(path string) string {
	switch {
	case path == "MEMORY.md":
		return "index"
	case strings.HasPrefix(path, "topics/"):
		return "topics"
	case strings.HasPrefix(path, "people/"):
		return "people"
	case strings.HasPrefix(path, "legacy-") || strings.HasPrefix(path, "legacy/"):
		return "legacy"
	}
	stem := strings.TrimSuffix(path, ".md")
	switch stem {
	case "profile", "decisions", "notes":
		return stem
	}
	return "other"
}
