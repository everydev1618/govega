package vega

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event represents a worker lifecycle event.
type Event struct {
	Type      EventType         `json:"type"`
	ProcessID string            `json:"process_id"`
	AgentName string            `json:"agent_name"`
	Timestamp time.Time         `json:"timestamp"`
	Data      map[string]string `json:"data,omitempty"`

	// For completion events
	Result string `json:"result,omitempty"`

	// For failure events
	Error string `json:"error,omitempty"`

	// For progress events
	Progress  float64 `json:"progress,omitempty"`
	Message   string  `json:"message,omitempty"`
	Iteration int     `json:"iteration,omitempty"`
}

// EventType identifies the kind of event.
type EventType string

const (
	EventStarted   EventType = "started"
	EventProgress  EventType = "progress"
	EventCompleted EventType = "completed"
	EventFailed    EventType = "failed"
	EventHeartbeat EventType = "heartbeat"
)

// CallbackConfig holds callback configuration for an orchestrator.
type CallbackConfig struct {
	// Dir is the directory for file-based callbacks (local mode)
	Dir string

	// URL is the endpoint for HTTP-based callbacks (distributed mode)
	URL string

	// httpClient for HTTP callbacks
	httpClient *http.Client
}

// WithCallbackDir configures file-based callbacks for local workers.
// Events are written as JSON files to the specified directory.
// The orchestrator polls this directory for events from workers.
//
// Example:
//
//	orch := vega.NewOrchestrator(
//	    vega.WithCallbackDir("~/.vega/events"),
//	)
func WithCallbackDir(dir string) OrchestratorOption {
	return func(o *Orchestrator) {
		// Expand ~ to home directory
		if len(dir) > 0 && dir[0] == '~' {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, dir[1:])
			}
		}

		// Ensure directory exists
		os.MkdirAll(dir, 0755)

		o.callbackConfig = &CallbackConfig{Dir: dir}

		// Start polling for events
		o.startEventPoller()
	}
}

// WithCallbackURL configures HTTP-based callbacks for distributed workers.
// Workers POST events to this URL. The orchestrator must expose this endpoint.
//
// Example:
//
//	orch := vega.NewOrchestrator(
//	    vega.WithCallbackURL("http://orchestrator:3001/events"),
//	)
func WithCallbackURL(url string) OrchestratorOption {
	return func(o *Orchestrator) {
		o.callbackConfig = &CallbackConfig{
			URL:        url,
			httpClient: &http.Client{Timeout: 10 * time.Second},
		}
	}
}

// PublishEvent sends an event to the orchestrator.
// Used by workers to report their status.
func PublishEvent(ctx context.Context, event Event, config *CallbackConfig) error {
	if config == nil {
		return fmt.Errorf("no callback configuration")
	}

	event.Timestamp = time.Now()

	if config.Dir != "" {
		return publishEventFile(event, config.Dir)
	}

	if config.URL != "" {
		return publishEventHTTP(ctx, event, config)
	}

	return fmt.Errorf("no callback method configured")
}

// publishEventFile writes an event to a file.
func publishEventFile(event Event, dir string) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("%s-%s-%d.event", event.ProcessID, event.Type, time.Now().UnixNano())
	path := filepath.Join(dir, filename)

	return os.WriteFile(path, data, 0644)
}

// publishEventHTTP sends an event via HTTP POST.
func publishEventHTTP(ctx context.Context, event Event, config *CallbackConfig) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.URL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := config.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("callback failed: %s", resp.Status)
	}

	return nil
}

// EventPoller polls a directory for event files.
type EventPoller struct {
	dir      string
	events   chan Event
	stopCh   chan struct{}
	wg       sync.WaitGroup
	mu       sync.RWMutex
	stopped  bool
}

// newEventPoller creates a new event poller.
func newEventPoller(dir string) *EventPoller {
	return &EventPoller{
		dir:    dir,
		events: make(chan Event, 100),
		stopCh: make(chan struct{}),
	}
}

// Start begins polling for events.
func (p *EventPoller) Start() <-chan Event {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.poll()
	}()
	return p.events
}

// Stop stops the poller.
func (p *EventPoller) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	close(p.stopCh)
	p.wg.Wait()
	close(p.events)
}

func (p *EventPoller) poll() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.readEvents()
		}
	}
}

func (p *EventPoller) readEvents() {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".event" {
			continue
		}

		path := filepath.Join(p.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			os.Remove(path) // Remove corrupt file
			continue
		}

		select {
		case p.events <- event:
			os.Remove(path) // Remove processed file
		default:
			// Queue full, leave file for later
		}
	}
}

// CallbackDir returns the callback directory if configured.
func (o *Orchestrator) CallbackDir() string {
	if o.callbackConfig != nil {
		return o.callbackConfig.Dir
	}
	return ""
}

// CallbackURL returns the callback URL if configured.
func (o *Orchestrator) CallbackURL() string {
	if o.callbackConfig != nil {
		return o.callbackConfig.URL
	}
	return ""
}

// HandleEventCallback returns an http.HandlerFunc for receiving HTTP callbacks.
// Mount this at your callback URL endpoint.
//
// Example:
//
//	http.HandleFunc("/events", orch.HandleEventCallback())
func (o *Orchestrator) HandleEventCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		o.handleEvent(event)
		w.WriteHeader(http.StatusOK)
	}
}

// startEventPoller starts the file-based event poller.
func (o *Orchestrator) startEventPoller() {
	if o.callbackConfig == nil || o.callbackConfig.Dir == "" {
		return
	}

	poller := newEventPoller(o.callbackConfig.Dir)
	events := poller.Start()

	// Store poller for cleanup
	o.eventPoller = poller

	// Process events in background
	go func() {
		for event := range events {
			o.handleEvent(event)
		}
	}()
}

// handleEvent processes an incoming event from a worker.
func (o *Orchestrator) handleEvent(event Event) {
	// Find the process
	p := o.Get(event.ProcessID)
	if p == nil {
		return // Process not found, ignore event
	}

	switch event.Type {
	case EventCompleted:
		p.mu.Lock()
		if p.status != StatusCompleted && p.status != StatusFailed {
			p.status = StatusCompleted
			p.finalResult = event.Result
			p.metrics.CompletedAt = time.Now()
		}
		p.mu.Unlock()
		o.emitComplete(p, event.Result)

	case EventFailed:
		p.mu.Lock()
		if p.status != StatusCompleted && p.status != StatusFailed {
			p.status = StatusFailed
			p.metrics.CompletedAt = time.Now()
			p.metrics.Errors++
		}
		p.mu.Unlock()
		o.emitFailed(p, fmt.Errorf("%s", event.Error))

	case EventProgress:
		// Update metrics but don't change status
		p.mu.Lock()
		p.metrics.LastActiveAt = time.Now()
		p.iteration = event.Iteration
		p.mu.Unlock()

	case EventHeartbeat:
		p.mu.Lock()
		p.metrics.LastActiveAt = time.Now()
		p.mu.Unlock()
	}
}

// NewCallbackConfig creates a callback config for use by workers.
// Pass this to workers so they can report events back.
func NewCallbackConfig(dir, url string) *CallbackConfig {
	config := &CallbackConfig{
		Dir: dir,
		URL: url,
	}
	if url != "" {
		config.httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return config
}
