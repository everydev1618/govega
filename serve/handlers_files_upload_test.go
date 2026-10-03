package serve

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
)

// uploadTestServer gives each test an isolated VEGA_HOME so uploads land
// in a temp workspace rather than the developer's real ~/.vega.
func uploadTestServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("VEGA_HOME", home)
	if err := os.MkdirAll(vega.WorkspacePath(), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return &Server{
		store:   newTestStore(t),
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
	}
}

// uploadFile posts a multipart upload to the workspace upload endpoint.
// A non-empty dir is sent as the optional "dir" form field.
func uploadFile(t *testing.T, s *Server, filename, dir string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if dir != "" {
		if err := mw.WriteField("dir", dir); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	s.handleUploadFile(w, req)
	return w
}

// TestUploadFile_RoundTrip is the headline contract: a dropped file lands
// in the workspace under uploads/ and is readable back through the
// existing read endpoint, which is how the agent reaches it.
func TestUploadFile_RoundTrip(t *testing.T) {
	s := uploadTestServer(t)

	content := []byte("# Voice prompt\n\nWrite like Etienne.\n")
	w := uploadFile(t, s, "etienne-voice-prompt.md", "", content)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var got UploadedFile
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != "uploads/etienne-voice-prompt.md" {
		t.Errorf("path = %q, want uploads/etienne-voice-prompt.md", got.Path)
	}
	if got.Name != "etienne-voice-prompt.md" {
		t.Errorf("name = %q", got.Name)
	}
	if got.ContentType != "text/markdown" {
		t.Errorf("content_type = %q, want text/markdown", got.ContentType)
	}
	if got.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", got.Size, len(content))
	}

	onDisk, err := os.ReadFile(filepath.Join(vega.WorkspacePath(), "uploads", "etienne-voice-prompt.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(onDisk, content) {
		t.Errorf("disk content = %q, want %q", onDisk, content)
	}
}

// TestUploadFile_RecordsMetadata: the upload shows up in the Files page's
// metadata list, attributed to the user rather than an agent.
func TestUploadFile_RecordsMetadata(t *testing.T) {
	s := uploadTestServer(t)

	if w := uploadFile(t, s, "notes.txt", "", []byte("hello")); w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	files, err := s.store.ListWorkspaceFiles("")
	if err != nil {
		t.Fatalf("ListWorkspaceFiles: %v", err)
	}
	var found *WorkspaceFile
	for i := range files {
		if files[i].Path == "uploads/notes.txt" {
			found = &files[i]
		}
	}
	if found == nil {
		t.Fatalf("upload not recorded; got %+v", files)
	}
	if found.Operation != "upload" {
		t.Errorf("operation = %q, want upload", found.Operation)
	}
	if found.Agent != "" {
		t.Errorf("agent = %q, want empty (user upload)", found.Agent)
	}
}

// TestUploadFile_RejectsTraversal: a filename carrying path components is
// reduced to its base name, so nothing escapes the workspace.
func TestUploadFile_RejectsTraversal(t *testing.T) {
	s := uploadTestServer(t)

	w := uploadFile(t, s, "../../../../etc/passwd", "", []byte("root:x:0:0"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got UploadedFile
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != "uploads/passwd" {
		t.Errorf("path = %q, want uploads/passwd", got.Path)
	}
	if _, err := os.Stat(filepath.Join(vega.WorkspacePath(), "uploads", "passwd")); err != nil {
		t.Errorf("sanitized file missing: %v", err)
	}
}

// TestUploadFile_RejectsTraversalDir: an explicit dir that escapes the
// workspace is refused outright rather than silently clamped.
func TestUploadFile_RejectsTraversalDir(t *testing.T) {
	s := uploadTestServer(t)

	w := uploadFile(t, s, "evil.sh", "../../..", []byte("rm -rf /"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

// TestUploadFile_HonoursDir: an in-workspace dir is used as given, so the
// Files page can offer "upload here".
func TestUploadFile_HonoursDir(t *testing.T) {
	s := uploadTestServer(t)

	w := uploadFile(t, s, "spec.md", "projects/apollo", []byte("spec"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got UploadedFile
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != "projects/apollo/spec.md" {
		t.Errorf("path = %q, want projects/apollo/spec.md", got.Path)
	}
}

// TestUploadFile_NoClobber: uploading the same name twice keeps both —
// an upload must never overwrite an agent's deliverable.
func TestUploadFile_NoClobber(t *testing.T) {
	s := uploadTestServer(t)

	if w := uploadFile(t, s, "report.md", "", []byte("first")); w.Code != http.StatusCreated {
		t.Fatalf("first upload: %d %s", w.Code, w.Body.String())
	}
	w := uploadFile(t, s, "report.md", "", []byte("second"))
	if w.Code != http.StatusCreated {
		t.Fatalf("second upload: %d %s", w.Code, w.Body.String())
	}
	var got UploadedFile
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != "uploads/report-1.md" {
		t.Errorf("path = %q, want uploads/report-1.md", got.Path)
	}
	first, err := os.ReadFile(filepath.Join(vega.WorkspacePath(), "uploads", "report.md"))
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if string(first) != "first" {
		t.Errorf("first file clobbered: %q", first)
	}
}

// TestUploadFile_MissingField: no multipart file part is a client error,
// not a panic.
func TestUploadFile_MissingField(t *testing.T) {
	s := uploadTestServer(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("dir", "")
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	s.handleUploadFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "multipart field 'file'") {
		t.Errorf("unhelpful error: %s", w.Body.String())
	}
}

// TestUploadFile_TooLarge: the per-file cap is enforced, matching the
// 10 MB ceiling handleReadFile will later read it back under.
func TestUploadFile_TooLarge(t *testing.T) {
	s := uploadTestServer(t)

	big := bytes.Repeat([]byte("x"), int(uploadMaxFileBytes)+1)
	w := uploadFile(t, s, "huge.bin", "", big)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(vega.WorkspacePath(), "uploads", "huge.bin")); !os.IsNotExist(err) {
		t.Errorf("oversized file was written to disk")
	}
}
