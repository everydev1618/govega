// Package v39a posts cost and conversation events from govega to v39a's
// /api/instances/conversation-event endpoint. Govega runs inside per-tenant
// Fly apps; v39a is the control panel that aggregates spend across tenants
// for the cost UI.
//
// The reporter is best-effort: failures are logged but never block the LLM
// call path. Callers always fire RecordCost in a goroutine.
package v39a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const defaultTimeout = 5 * time.Second

// Reporter posts events to v39a's conversation-event endpoint.
// Construct via NewReporterFromEnv (production) or directly (tests).
type Reporter struct {
	// BaseURL is the v39a origin, e.g. "https://admin.v39a.com".
	BaseURL string
	// Token authenticates the caller. Sent as the X-V39A-Token header.
	// v39a matches it against instances.api_token.
	Token string
	// Client is the HTTP client to use. Nil means http.DefaultClient.
	Client *http.Client
	// Timeout caps each request. Zero means defaultTimeout.
	Timeout time.Duration
}

// CostEvent is a single billable event. Mirrors the cost_recorded shape
// v39a's conversation-event endpoint accepts, plus step_type/model so the
// UI can slice spend by routing tier.
type CostEvent struct {
	ConversationID string  `json:"conversation_id"`
	Kind           string  `json:"kind"`
	Vendor         string  `json:"vendor,omitempty"`
	Units          float64 `json:"units"`
	UnitCostUSD    float64 `json:"unit_cost_usd"`
	StepType       string  `json:"step_type,omitempty"`
	Model          string  `json:"model,omitempty"`
}

// NewReporterFromEnv returns a Reporter configured from V39A_REPORTER_URL
// and V39A_INSTANCE_TOKEN. Returns nil if either env var is unset — the
// caller should treat that as "telemetry disabled" and skip the call.
func NewReporterFromEnv() *Reporter {
	base := os.Getenv("V39A_REPORTER_URL")
	token := os.Getenv("V39A_INSTANCE_TOKEN")
	if base == "" || token == "" {
		return nil
	}
	return &Reporter{BaseURL: base, Token: token}
}

// RecordCost posts a cost_recorded event. Returns an error on transport
// failure or non-2xx response; the caller decides whether to log it.
func (r *Reporter) RecordCost(ctx context.Context, ev CostEvent) error {
	envelope := map[string]any{"type": "cost_recorded"}
	envelope["conversation_id"] = ev.ConversationID
	envelope["kind"] = ev.Kind
	if ev.Vendor != "" {
		envelope["vendor"] = ev.Vendor
	}
	envelope["units"] = ev.Units
	envelope["unit_cost_usd"] = ev.UnitCostUSD
	if ev.StepType != "" {
		envelope["step_type"] = ev.StepType
	}
	if ev.Model != "" {
		envelope["model"] = ev.Model
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	timeout := r.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		r.BaseURL+"/api/instances/conversation-event", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-V39A-Token", r.Token)

	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("v39a returned %d: %s", resp.StatusCode, preview)
	}
	return nil
}
