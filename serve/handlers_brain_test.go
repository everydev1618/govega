package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func brainTestServer(t *testing.T, agents ...string) *Server {
	t.Helper()
	docAgents := map[string]*dsl.Agent{}
	for _, n := range agents {
		docAgents[n] = &dsl.Agent{Name: n, Model: "claude-sonnet-4-6"}
	}
	doc := &dsl.Document{
		Agents:   docAgents,
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	return &Server{
		store:   newTestStore(t),
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
		cfg: Config{
			Builder:      dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{Name: "iris"},
		},
	}
}

// brainUpload posts a multipart upload of (filename, content) to the
// brain endpoint for the given agent.
func brainUpload(t *testing.T, s *Server, agent, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent+"/brain", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("name", agent)
	w := httptest.NewRecorder()
	s.handleUploadAgentBrain(w, req)
	return w
}

// TestAgentBrain_UploadListGetDelete is the headline govega#43 contract:
// the four endpoints round-trip a brain file under one agent.
func TestAgentBrain_UploadListGetDelete(t *testing.T) {
	s := brainTestServer(t, "riley")

	// Upload.
	content := []byte("# Onboarding guide\n\nWelcome to the team.\n")
	upW := brainUpload(t, s, "riley", "guide.md", content)
	if upW.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", upW.Code, upW.Body.String())
	}
	var created AgentBrainFile
	if err := json.NewDecoder(upW.Body).Decode(&created); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if !strings.HasPrefix(created.ID, "brain_") {
		t.Errorf("id = %q, want brain_ prefix", created.ID)
	}
	if created.AgentName != "riley" {
		t.Errorf("agent_id = %q, want riley", created.AgentName)
	}
	if created.Name != "guide.md" {
		t.Errorf("name = %q, want guide.md", created.Name)
	}
	if created.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", created.SizeBytes, len(content))
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at unset")
	}

	// List.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/brain", nil)
	listReq.SetPathValue("name", "riley")
	listW := httptest.NewRecorder()
	s.handleListAgentBrain(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d", listW.Code)
	}
	var listed []AgentBrainFile
	_ = json.NewDecoder(listW.Body).Decode(&listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list = %+v, want one entry matching upload", listed)
	}

	// Get (download).
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/brain/"+created.ID, nil)
	getReq.SetPathValue("name", "riley")
	getReq.SetPathValue("file_id", created.ID)
	getW := httptest.NewRecorder()
	s.handleGetAgentBrainFile(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get status = %d", getW.Code)
	}
	gotBody, _ := io.ReadAll(getW.Body)
	if !bytes.Equal(gotBody, content) {
		t.Errorf("downloaded content mismatch:\n got %q\nwant %q", gotBody, content)
	}

	// Delete.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/riley/brain/"+created.ID, nil)
	delReq.SetPathValue("name", "riley")
	delReq.SetPathValue("file_id", created.ID)
	delW := httptest.NewRecorder()
	s.handleDeleteAgentBrainFile(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body = %s", delW.Code, delW.Body.String())
	}

	// List should be empty after delete.
	listW2 := httptest.NewRecorder()
	s.handleListAgentBrain(listW2, listReq)
	var listed2 []AgentBrainFile
	_ = json.NewDecoder(listW2.Body).Decode(&listed2)
	if len(listed2) != 0 {
		t.Errorf("post-delete list len = %d, want 0", len(listed2))
	}
}

// TestAgentBrain_PerAgentScoping confirms one agent's brain files don't
// leak into another's list/get/delete, even with the same file_id guess.
func TestAgentBrain_PerAgentScoping(t *testing.T) {
	s := brainTestServer(t, "riley", "alex")

	upW := brainUpload(t, s, "riley", "rileys.txt", []byte("hi"))
	if upW.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", upW.Code, upW.Body.String())
	}
	var rileysFile AgentBrainFile
	_ = json.NewDecoder(upW.Body).Decode(&rileysFile)

	// alex's list must not see riley's file.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/alex/brain", nil)
	listReq.SetPathValue("name", "alex")
	listW := httptest.NewRecorder()
	s.handleListAgentBrain(listW, listReq)
	var alexFiles []AgentBrainFile
	_ = json.NewDecoder(listW.Body).Decode(&alexFiles)
	if len(alexFiles) != 0 {
		t.Errorf("alex list returned riley's files: %+v", alexFiles)
	}

	// alex GET-by-id with riley's id must return 404 — no cross-agent reads.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/alex/brain/"+rileysFile.ID, nil)
	getReq.SetPathValue("name", "alex")
	getReq.SetPathValue("file_id", rileysFile.ID)
	getW := httptest.NewRecorder()
	s.handleGetAgentBrainFile(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("cross-agent GET = %d, want 404", getW.Code)
	}

	// alex DELETE with riley's id must 404 — no cross-agent deletes.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/alex/brain/"+rileysFile.ID, nil)
	delReq.SetPathValue("name", "alex")
	delReq.SetPathValue("file_id", rileysFile.ID)
	delW := httptest.NewRecorder()
	s.handleDeleteAgentBrainFile(delW, delReq)
	if delW.Code != http.StatusNotFound {
		t.Errorf("cross-agent DELETE = %d, want 404", delW.Code)
	}
}

// TestAgentBrain_RejectsUnknownAgent confirms uploads to nonexistent
// agents return 404 — same shape as hidden agents so callers can't probe.
func TestAgentBrain_RejectsUnknownAgent(t *testing.T) {
	s := brainTestServer(t, "riley")
	upW := brainUpload(t, s, "ghost", "x.txt", []byte("x"))
	if upW.Code != http.StatusNotFound {
		t.Errorf("upload to ghost = %d, want 404", upW.Code)
	}
}

// TestAgentBrain_FilenameSanitization ensures path components are
// stripped from the stored name so a malicious client can't smuggle
// path traversal into downstream consumers.
func TestAgentBrain_FilenameSanitization(t *testing.T) {
	s := brainTestServer(t, "riley")
	upW := brainUpload(t, s, "riley", "../../etc/passwd", []byte("oops"))
	if upW.Code != http.StatusCreated {
		t.Fatalf("upload status = %d", upW.Code)
	}
	var created AgentBrainFile
	_ = json.NewDecoder(upW.Body).Decode(&created)
	if strings.Contains(created.Name, "/") || strings.Contains(created.Name, "..") {
		t.Errorf("sanitized name %q still contains path components", created.Name)
	}
}

// TestAgentBrain_PerAgentSizeCap ensures the per-agent total storage cap
// fires before content is written. Verifies the boundary check, not the
// exact 100MB number.
func TestAgentBrain_PerAgentSizeCap(t *testing.T) {
	s := brainTestServer(t, "riley")
	// Seed enough rows to put us at the cap. Each row carries a SizeBytes
	// of exactly the per-file limit so the cap math is straightforward.
	for i := 0; i < 10; i++ {
		_ = s.store.InsertAgentBrainFile(AgentBrainFile{
			ID:        "brain_seed" + string(rune('a'+i)),
			AgentName: "riley",
			Name:      "seed.bin",
			SizeBytes: brainMaxFileBytes,
			Content:   []byte("x"), // content doesn't need to actually match SizeBytes for this test
		})
	}
	// Now an upload of any size should bounce.
	upW := brainUpload(t, s, "riley", "one-more.txt", []byte("hi"))
	if upW.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over-cap upload = %d, want 413; body = %s", upW.Code, upW.Body.String())
	}
}
