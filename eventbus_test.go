package vega

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventTypes(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{EventStarted, "started"},
		{EventProgress, "progress"},
		{EventCompleted, "completed"},
		{EventFailed, "failed"},
		{EventHeartbeat, "heartbeat"},
	}

	for _, tt := range tests {
		if string(tt.et) != tt.want {
			t.Errorf("EventType = %q, want %q", tt.et, tt.want)
		}
	}
}

func TestPublishEventFile(t *testing.T) {
	dir := t.TempDir()

	event := Event{
		Type:      EventCompleted,
		ProcessID: "proc-123",
		AgentName: "TestAgent",
		Result:    "all done",
	}

	err := publishEventFile(event, dir)
	if err != nil {
		t.Fatalf("publishEventFile() error: %v", err)
	}

	// Verify file was written
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 event file, got %d", len(entries))
	}

	// Verify contents
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	var loaded Event
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if loaded.ProcessID != "proc-123" {
		t.Errorf("Loaded event ProcessID = %q, want %q", loaded.ProcessID, "proc-123")
	}
	if loaded.Result != "all done" {
		t.Errorf("Loaded event Result = %q, want %q", loaded.Result, "all done")
	}
}

func TestPublishEventHTTP(t *testing.T) {
	var received Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := &CallbackConfig{
		URL:        server.URL,
		httpClient: server.Client(),
	}

	event := Event{
		Type:      EventFailed,
		ProcessID: "proc-456",
		Error:     "something broke",
	}

	err := publishEventHTTP(context.Background(), event, config)
	if err != nil {
		t.Fatalf("publishEventHTTP() error: %v", err)
	}

	if received.ProcessID != "proc-456" {
		t.Errorf("Received event ProcessID = %q, want %q", received.ProcessID, "proc-456")
	}
	if received.Error != "something broke" {
		t.Errorf("Received event Error = %q, want %q", received.Error, "something broke")
	}
}

func TestPublishEventHTTPServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	config := &CallbackConfig{
		URL:        server.URL,
		httpClient: server.Client(),
	}

	err := publishEventHTTP(context.Background(), Event{}, config)
	if err == nil {
		t.Error("Expected error for server error response")
	}
}

func TestPublishEvent_NoConfig(t *testing.T) {
	err := PublishEvent(context.Background(), Event{}, nil)
	if err == nil {
		t.Error("PublishEvent with nil config should error")
	}
}

func TestPublishEvent_NoMethod(t *testing.T) {
	err := PublishEvent(context.Background(), Event{}, &CallbackConfig{})
	if err == nil {
		t.Error("PublishEvent with no dir or URL should error")
	}
}

func TestNewCallbackConfig(t *testing.T) {
	config := NewCallbackConfig("/tmp/events", "")
	if config.Dir != "/tmp/events" {
		t.Errorf("Dir = %q, want %q", config.Dir, "/tmp/events")
	}
	if config.httpClient != nil {
		t.Error("httpClient should be nil when URL is empty")
	}

	config = NewCallbackConfig("", "http://example.com/events")
	if config.URL != "http://example.com/events" {
		t.Errorf("URL = %q, want %q", config.URL, "http://example.com/events")
	}
	if config.httpClient == nil {
		t.Error("httpClient should not be nil when URL is set")
	}
}

func TestEventPollerReadEvents(t *testing.T) {
	dir := t.TempDir()

	// Write an event file
	event := Event{
		Type:      EventCompleted,
		ProcessID: "proc-1",
		Result:    "done",
	}
	data, _ := json.Marshal(event)
	os.WriteFile(filepath.Join(dir, "proc-1-completed-123.event"), data, 0644)

	poller := newEventPoller(dir)
	ch := poller.Start()

	// Should receive the event
	select {
	case ev := <-ch:
		if ev.ProcessID != "proc-1" {
			t.Errorf("Event ProcessID = %q, want %q", ev.ProcessID, "proc-1")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timed out waiting for event")
	}

	poller.Stop()
}

func TestEventPollerIgnoresNonEventFiles(t *testing.T) {
	dir := t.TempDir()

	// Write a non-event file
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not an event"), 0644)

	poller := newEventPoller(dir)

	// Directly test readEvents
	poller.readEvents()

	// Channel should be empty
	select {
	case ev := <-poller.events:
		t.Errorf("Should not receive events for non-.event files, got: %+v", ev)
	default:
		// Expected
	}
}

func TestEventPollerRemovesCorruptFiles(t *testing.T) {
	dir := t.TempDir()

	// Write a corrupt event file
	os.WriteFile(filepath.Join(dir, "corrupt-completed-123.event"), []byte("not json{{{"), 0644)

	poller := newEventPoller(dir)
	poller.readEvents()

	// Corrupt file should have been removed
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Error("Corrupt event file should have been removed")
	}
}

func TestHandleEventCallback(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "test"}
	proc, _ := o.Spawn(agent)

	handler := o.HandleEventCallback()

	// Test successful event
	event := Event{
		Type:      EventCompleted,
		ProcessID: proc.ID,
		Result:    "test result",
	}
	data, _ := json.Marshal(event)

	req := httptest.NewRequest(http.MethodPost, "/events", jsonReader(data))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Handler status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleEventCallbackMethodNotAllowed(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	handler := o.HandleEventCallback()

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Handler status for GET = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestCallbackDirAndURL(t *testing.T) {
	o := NewOrchestrator()

	if o.CallbackDir() != "" {
		t.Error("CallbackDir should be empty by default")
	}
	if o.CallbackURL() != "" {
		t.Error("CallbackURL should be empty by default")
	}
}

func TestHandleEvent_Progress(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "test"}
	proc, _ := o.Spawn(agent)

	o.handleEvent(Event{
		Type:      EventProgress,
		ProcessID: proc.ID,
		Iteration: 5,
	})

	if proc.iteration != 5 {
		t.Errorf("After progress event, iteration = %d, want 5", proc.iteration)
	}
}

func TestHandleEvent_Heartbeat(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "test"}
	proc, _ := o.Spawn(agent)

	before := proc.Metrics().LastActiveAt

	time.Sleep(5 * time.Millisecond)

	o.handleEvent(Event{
		Type:      EventHeartbeat,
		ProcessID: proc.ID,
	})

	after := proc.Metrics().LastActiveAt
	if !after.After(before) {
		t.Error("Heartbeat should update LastActiveAt")
	}
}

func TestHandleEvent_UnknownProcess(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))

	// Should not panic
	o.handleEvent(Event{
		Type:      EventCompleted,
		ProcessID: "nonexistent",
	})
}

// jsonReader is a helper to create a reader from JSON bytes.
func jsonReader(data []byte) *jsonBody {
	return &jsonBody{data: data, pos: 0}
}

type jsonBody struct {
	data []byte
	pos  int
}

func (j *jsonBody) Read(p []byte) (int, error) {
	if j.pos >= len(j.data) {
		return 0, nil
	}
	n := copy(p, j.data[j.pos:])
	j.pos += n
	if j.pos >= len(j.data) {
		return n, nil
	}
	return n, nil
}
