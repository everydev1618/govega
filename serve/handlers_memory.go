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

// memoryGraphNode is one wiki page rendered as a graph node. The
// `cluster` field is derived at response-build time from the page's
// path prefix; v2 swaps this for embedding-based community detection.
type memoryGraphNode struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Bytes     int    `json:"bytes"`
	UpdatedAt string `json:"updated_at"`
	Cluster   string `json:"cluster"`
}

// memoryGraphEdge is one directed link between pages. Weight is
// always 1 today — duplicate links are pre-deduped by the
// memory_links primary key. The field is kept so the frontend can
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
// shared wiki. Per-agent wikis are not graph-rendered today — they're
// private working notes, not the legible map of long-term memory.
func (s *Server) handleMemoryGraph(w http.ResponseWriter, r *http.Request) {
	userID := authUser(r)
	pages, err := s.store.ListMemoryPages(MemoryScopeUser, userID, userID, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	links, err := s.store.ListMemoryLinks(MemoryScopeUser, userID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	nodes := make([]memoryGraphNode, 0, len(pages))
	for _, p := range pages {
		nodes = append(nodes, memoryGraphNode{
			Path:      p.Path,
			Title:     titleForPath(p.Path),
			Bytes:     len(p.Content),
			UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
			Cluster:   clusterForPath(p.Path),
		})
	}
	edges := make([]memoryGraphEdge, 0, len(links))
	for _, l := range links {
		edges = append(edges, memoryGraphEdge{From: l.FromPath, To: l.ToPath, Weight: 1})
	}
	writeJSON(w, http.StatusOK, memoryGraphResponse{
		Nodes:       nodes,
		Edges:       edges,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
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

