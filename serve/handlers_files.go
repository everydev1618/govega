package serve

import (
	"encoding/base64"
	"io"
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
