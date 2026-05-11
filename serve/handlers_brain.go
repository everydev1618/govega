package serve

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// --- Agent Brain (per-agent knowledge attachments) — refs govega#43 ---
//
// The Brain tab on apex-host-mgmt's agent detail page lets users attach
// reference files (guides, policies, specs) to one agent. This is the
// MVP: pure attachment list, no RAG indexing, content stored inline in
// SQLite. If/when retrieval-at-chat-time lands, the storage layer can
// swap to an object store without changing this API shape.

const (
	// brainMaxFileBytes caps each upload at 10 MB. Comfortably above the
	// "a doc-sized PDF" case the FE expects without bloating SQLite.
	brainMaxFileBytes int64 = 10 << 20
	// brainMaxAgentTotalBytes caps total brain storage per agent at
	// 100 MB. Sanity bound; can be relaxed once the storage backend
	// changes.
	brainMaxAgentTotalBytes int64 = 100 << 20
)

// handleListAgentBrain returns the BrainFile metadata for an agent.
// GET /api/v1/agents/{name}/brain
func (s *Server) handleListAgentBrain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}
	files, err := s.store.ListAgentBrainFiles(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if files == nil {
		files = []AgentBrainFile{}
	}
	writeJSON(w, http.StatusOK, files)
}

// handleUploadAgentBrain accepts a multipart upload and persists the file
// under the agent.
// POST /api/v1/agents/{name}/brain
func (s *Server) handleUploadAgentBrain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}
	// Cap the request body so a malicious caller can't OOM us before
	// FormFile has a chance to validate per-file size.
	r.Body = http.MaxBytesReader(w, r.Body, brainMaxFileBytes+1<<20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "file upload required (multipart field 'file')"})
		return
	}
	defer file.Close()

	if header.Size > brainMaxFileBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, ErrorResponse{Error: "file exceeds 10 MB per-file limit"})
		return
	}

	// Enforce the per-agent total cap before reading the body — cheaper
	// than reading then rejecting.
	existing, err := s.store.ListAgentBrainFiles(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	var total int64
	for _, f := range existing {
		total += f.SizeBytes
	}
	if total+header.Size > brainMaxAgentTotalBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, ErrorResponse{Error: "agent brain storage limit reached (100 MB)"})
		return
	}

	body, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "failed to read upload: " + err.Error()})
		return
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(body)
	}

	stored := AgentBrainFile{
		ID:        newBrainFileID(),
		AgentName: name,
		Name:      sanitizeBrainFilename(header.Filename),
		MimeType:  mime,
		SizeBytes: int64(len(body)),
		Content:   body,
	}
	// When a blob store is configured, content lives there and the DB
	// row carries only metadata (refs govega#61 phase 5). When not,
	// content stays inline in the row (preserves the zero-config
	// default).
	if s.blobs != nil {
		if err := s.blobs.Put(stored.ID, body); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "blob write: " + err.Error()})
			return
		}
		// Send an empty-but-non-nil byte slice so the BLOB NOT NULL
		// constraint on agent_brain_files.content holds while the
		// real bytes live in the blob store.
		stored.Content = []byte{}
	}
	if err := s.store.InsertAgentBrainFile(stored); err != nil {
		// Roll back the blob write so the FE can retry without orphaning data.
		if s.blobs != nil {
			_ = s.blobs.Delete(stored.ID)
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	// Re-read so created_at reflects the server clock.
	written, _ := s.store.GetAgentBrainFile(name, stored.ID)
	if written != nil {
		stored = *written
		stored.Content = nil // don't echo the bytes back in the JSON response
	}
	writeJSON(w, http.StatusCreated, stored)
}

// handleGetAgentBrainFile streams a single brain file back to the caller.
// GET /api/v1/agents/{name}/brain/{file_id}
//
// The list endpoint returns metadata only; this is the download path so a
// brain-file detail / preview UI can fetch contents on demand.
func (s *Server) handleGetAgentBrainFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id := r.PathValue("file_id")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}
	f, err := s.store.GetAgentBrainFile(name, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if f == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "brain file not found"})
		return
	}
	// When the row was written through the blob store, the DB carries
	// only metadata — pull bytes from the configured BlobStore.
	body := f.Content
	if s.blobs != nil && len(body) == 0 {
		got, err := s.blobs.Get(f.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "blob read: " + err.Error()})
			return
		}
		body = got
	}
	mime := f.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+url.PathEscape(f.Name)+`"`)
	_, _ = w.Write(body)
}

// handleDeleteAgentBrainFile removes a brain file.
// DELETE /api/v1/agents/{name}/brain/{file_id}
func (s *Server) handleDeleteAgentBrainFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id := r.PathValue("file_id")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}
	if err := s.store.DeleteAgentBrainFile(name, id); err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "brain file not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	// Best-effort blob cleanup. A failure here leaves an orphan file
	// on disk; the DB row is gone so the file isn't addressable, which
	// is acceptable for a follow-up cleanup pass.
	if s.blobs != nil {
		_ = s.blobs.Delete(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// brainAgentExists reports whether the named agent is addressable for
// brain operations. Hidden agents (builder, per-user clones) are not
// — same 404 shape as the rest of the agents API so callers can't probe
// for them.
func (s *Server) brainAgentExists(name string) bool {
	if name == "" || s.isHiddenAgent(name) {
		return false
	}
	_, ok := s.interp.Document().Agents[name]
	return ok
}

// newBrainFileID returns a server-generated opaque id for a brain file.
// 72 random bits — collision probability is negligible at the volumes a
// per-agent attachment list ever reaches.
func newBrainFileID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return "brain_" + hex.EncodeToString(b[:])
}

// sanitizeBrainFilename keeps the original filename for display but
// strips path components so a malicious client can't smuggle a path
// traversal into the stored row (we never use the name for filesystem
// access today, but downstream consumers like apex-host-mgmt may).
func sanitizeBrainFilename(name string) string {
	// Strip any path components.
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return "untitled"
	}
	return name
}
