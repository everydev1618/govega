package serve

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	vega "github.com/everydev1618/govega"
)

// handleListFiles returns directory contents for the given path under the workspace.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	absPath, err := safePath(relPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "path not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if !info.IsDir() {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "path is not a directory"})
		return
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	root := vega.WorkspacePath()
	var files []FileEntry
	for _, e := range entries {
		// Skip hidden files.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Skip blueprints directory at root level — internal to Hera.
		if relPath == "" && e.Name() == "blueprints" {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		entryRel, _ := filepath.Rel(root, filepath.Join(absPath, e.Name()))
		fe := FileEntry{
			Name:    e.Name(),
			Path:    entryRel,
			IsDir:   e.IsDir(),
			Size:    fi.Size(),
			ModTime: fi.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}
		if !e.IsDir() {
			fe.ContentType = detectContentType(e.Name())
		}
		files = append(files, fe)
	}

	if files == nil {
		files = []FileEntry{}
	}
	writeJSON(w, http.StatusOK, files)
}

// handleReadFile returns the content of a file under the workspace.
func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "path parameter required"})
		return
	}

	absPath, err := safePath(relPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "file not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "path is a directory"})
		return
	}

	// Limit read size to 10MB.
	const maxSize = 10 * 1024 * 1024
	if info.Size() > maxSize {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "file too large (max 10MB)"})
		return
	}

	f, err := os.Open(absPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	ct := detectContentType(info.Name())
	resp := FileContentResponse{
		Path:        relPath,
		ContentType: ct,
		Size:        info.Size(),
	}

	if isTextContentType(ct) {
		resp.Content = string(data)
		resp.Encoding = "utf-8"
	} else {
		resp.Content = base64.StdEncoding.EncodeToString(data)
		resp.Encoding = "base64"
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleDeleteFile removes a file or empty directory under the workspace.
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "path parameter required"})
		return
	}

	absPath, err := safePath(relPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "file not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if info.IsDir() {
		err = os.Remove(absPath) // only removes empty directories
	} else {
		err = os.Remove(absPath)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "path": relPath})
}

// handleListFileMetadata returns file metadata records, optionally filtered by agent.
func (s *Server) handleListFileMetadata(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	files, err := s.store.ListWorkspaceFiles(agent)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if files == nil {
		files = []WorkspaceFile{}
	}

	// Also fetch distinct agents for the frontend grouping.
	agents, _ := s.store.ListWorkspaceFileAgents()
	if agents == nil {
		agents = []string{}
	}

	writeJSON(w, http.StatusOK, FileMetadataResponse{
		Files:  files,
		Agents: agents,
	})
}

// handleWorkspaceStatic serves raw files from the workspace directory.
// This allows agents to produce deliverables (HTML sites, images, etc.) that
// are accessible via direct URLs like /workspace/project/index.html.
func (s *Server) handleWorkspaceStatic(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Path
	absPath, err := safePath(relPath)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Serve index.html for directories.
	if info.IsDir() {
		indexPath := filepath.Join(absPath, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			absPath = indexPath
			info, _ = os.Stat(indexPath)
		} else {
			http.NotFound(w, r)
			return
		}
	}

	http.ServeFile(w, r, absPath)
}

// safePath resolves the given relative path within the workspace directory,
// rejecting any traversal attempts.
func safePath(relPath string) (string, error) {
	root := vega.WorkspacePath()
	if relPath == "" {
		return root, nil
	}

	// Clean the path and resolve within root.
	cleaned := filepath.Clean(filepath.Join(root, relPath))

	// Ensure the resolved path is within the workspace.
	if !strings.HasPrefix(cleaned, root) {
		return "", &pathError{"path escapes workspace directory"}
	}

	// Resolve symlinks and check again.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// If the path doesn't exist yet, just use cleaned.
		if os.IsNotExist(err) {
			return cleaned, nil
		}
		return "", err
	}

	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(resolved, rootResolved) {
		return "", &pathError{"path escapes workspace directory"}
	}

	return resolved, nil
}

type pathError struct {
	msg string
}

func (e *pathError) Error() string { return e.msg }

// detectContentType returns a MIME type for the given filename.
func detectContentType(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return "application/octet-stream"
	}

	// Common types that mime package may not know.
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		return "text/html"
	case ".md", ".markdown":
		return "text/markdown"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".json":
		return "application/json"
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".js":
		return "text/javascript"
	case ".ts", ".tsx":
		return "text/typescript"
	case ".jsx":
		return "text/jsx"
	case ".sh", ".bash":
		return "text/x-shellscript"
	case ".toml":
		return "text/toml"
	case ".csv":
		return "text/csv"
	case ".svg":
		return "image/svg+xml"
	}

	if ct := mime.TypeByExtension(ext); ct != "" {
		// Strip parameters like charset (e.g. "text/html; charset=utf-8" → "text/html").
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = strings.TrimSpace(ct[:i])
		}
		return ct
	}
	return "application/octet-stream"
}

// isTextContentType returns true if the content type is text-based.
func isTextContentType(ct string) bool {
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/xml", "application/javascript",
		"application/x-yaml", "application/toml":
		return true
	}
	return false
}

// uploadMaxFileBytes caps a single user upload at 10 MB — the same ceiling
// handleReadFile will later read it back under, so an accepted upload is
// always one the UI and the agents can actually open again.
const uploadMaxFileBytes int64 = 10 << 20

// defaultUploadDir is where a dropped file lands when the caller doesn't
// name a directory. Keeping uploads out of the workspace root means an
// agent's deliverables stay the only thing at the top level.
const defaultUploadDir = "uploads"

// handleUploadFile accepts a multipart upload and writes it into the
// workspace, returning the workspace-relative path.
//
// This is the write half of the Files API: the chat composer uses it so a
// file dragged onto a conversation becomes something the agent can reach
// with read_file, rather than bytes that only ever existed in the browser.
// POST /api/v1/files/upload
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	// Cap the body before parsing so a large upload can't exhaust memory
	// ahead of the per-file check below. The slack covers multipart
	// framing so a file at exactly the limit still reaches FormFile.
	r.Body = http.MaxBytesReader(w, r.Body, uploadMaxFileBytes+1<<20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "file upload required (multipart field 'file')"})
		return
	}
	defer file.Close()

	if header.Size > uploadMaxFileBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, ErrorResponse{Error: "file exceeds 10 MB limit"})
		return
	}

	dir := strings.TrimSpace(r.FormValue("dir"))
	if dir == "" {
		dir = defaultUploadDir
	}
	// A traversing dir is refused rather than clamped: the caller asked
	// for somewhere specific, and quietly writing elsewhere is worse
	// than an error.
	absDir, err := safePath(dir)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	name := sanitizeUploadFilename(header.Filename)
	absPath, relPath, err := uniqueUploadPath(absDir, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	body, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "failed to read upload: " + err.Error()})
		return
	}
	if int64(len(body)) > uploadMaxFileBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, ErrorResponse{Error: "file exceeds 10 MB limit"})
		return
	}
	if err := os.WriteFile(absPath, body, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Record it the same way an agent's write_file is recorded, so the
	// Files page's metadata view shows uploads alongside agent output.
	// An empty Agent marks it as the user's own.
	if s.store != nil {
		if err := s.store.InsertWorkspaceFile(WorkspaceFile{
			Path:        relPath,
			Operation:   "upload",
			Description: "uploaded by user",
		}); err != nil {
			slog.Error("failed to record uploaded file", "path", relPath, "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, UploadedFile{
		Name:        filepath.Base(relPath),
		Path:        relPath,
		ContentType: detectContentType(name),
		Size:        int64(len(body)),
	})
}

// sanitizeUploadFilename reduces a client-supplied filename to a safe base
// name. Leading dots are stripped as well as path components: the file
// listing hides dotfiles, so a name like ".env" would otherwise upload
// successfully and then be invisible in the UI.
func sanitizeUploadFilename(name string) string {
	// Windows clients send backslash-separated paths; filepath.Base on a
	// unix build wouldn't split those.
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
		return "upload"
	}
	return name
}

// uniqueUploadPath returns a non-colliding absolute path inside dir, plus
// its workspace-relative form. An upload never overwrites an existing
// file — agents put deliverables in the same tree, and a silent clobber
// would destroy work no one asked to replace.
func uniqueUploadPath(absDir, name string) (string, string, error) {
	// safePath resolves symlinks when the target exists and doesn't when
	// it doesn't, so one of these two can be the resolved form while the
	// other isn't (on macOS a VEGA_HOME under /var resolves to
	// /private/var). Resolving both makes filepath.Rel meaningful.
	root := resolvedPath(vega.WorkspacePath())
	absDir = resolvedPath(absDir)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	for i := 0; i < 1000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		abs := filepath.Join(absDir, candidate)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return "", "", err
			}
			return abs, filepath.ToSlash(rel), nil
		} else if err != nil {
			return "", "", err
		}
	}
	return "", "", &pathError{"too many files with that name"}
}

// resolvedPath returns p with symlinks resolved, or p unchanged when it
// can't be resolved (most often because it doesn't exist yet).
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
