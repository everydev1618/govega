package vega

import (
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/memory"
)

func TestStaticPrompt(t *testing.T) {
	prompt := StaticPrompt("You are a helpful assistant.")
	if got := prompt.Prompt(); got != "You are a helpful assistant." {
		t.Errorf("StaticPrompt.Prompt() = %q, want %q", got, "You are a helpful assistant.")
	}
}

func TestDynamicPrompt(t *testing.T) {
	counter := 0
	prompt := DynamicPrompt(func() string {
		counter++
		return "Call #" + string(rune('0'+counter))
	})

	// First call
	if got := prompt.Prompt(); got != "Call #1" {
		t.Errorf("DynamicPrompt.Prompt() call 1 = %q, want %q", got, "Call #1")
	}

	// Second call should return different value
	if got := prompt.Prompt(); got != "Call #2" {
		t.Errorf("DynamicPrompt.Prompt() call 2 = %q, want %q", got, "Call #2")
	}
}

func TestBudgetAction(t *testing.T) {
	tests := []struct {
		action BudgetAction
		want   BudgetAction
	}{
		{BudgetBlock, 0},
		{BudgetWarn, 1},
		{BudgetAllow, 2},
	}

	for _, tt := range tests {
		if tt.action != tt.want {
			t.Errorf("BudgetAction = %d, want %d", tt.action, tt.want)
		}
	}
}

func TestBackoffType(t *testing.T) {
	tests := []struct {
		btype BackoffType
		want  BackoffType
	}{
		{BackoffExponential, 0},
		{BackoffLinear, 1},
		{BackoffConstant, 2},
	}

	for _, tt := range tests {
		if tt.btype != tt.want {
			t.Errorf("BackoffType = %d, want %d", tt.btype, tt.want)
		}
	}
}

func TestErrorClass(t *testing.T) {
	tests := []struct {
		class ErrorClass
		want  ErrorClass
	}{
		{ErrClassRateLimit, 0},
		{ErrClassOverloaded, 1},
		{ErrClassTimeout, 2},
		{ErrClassTemporary, 3},
		{ErrClassInvalidRequest, 4},
		{ErrClassAuthentication, 5},
		{ErrClassBudgetExceeded, 6},
	}

	for _, tt := range tests {
		if tt.class != tt.want {
			t.Errorf("ErrorClass = %d, want %d", tt.class, tt.want)
		}
	}
}

func TestRole(t *testing.T) {
	if llm.RoleUser != "user" {
		t.Errorf("RoleUser = %q, want %q", llm.RoleUser, "user")
	}
	if llm.RoleAssistant != "assistant" {
		t.Errorf("RoleAssistant = %q, want %q", llm.RoleAssistant, "assistant")
	}
	if llm.RoleSystem != "system" {
		t.Errorf("RoleSystem = %q, want %q", llm.RoleSystem, "system")
	}
}

func TestAgentDefaults(t *testing.T) {
	agent := Agent{
		Name:  "test-agent",
		Model: "claude-sonnet-4-20250514",
	}

	if agent.Name != "test-agent" {
		t.Errorf("Agent.Name = %q, want %q", agent.Name, "test-agent")
	}

	if agent.Tools != nil {
		t.Error("Agent.Tools should be nil by default")
	}

	if agent.Budget != nil {
		t.Error("Agent.Budget should be nil by default")
	}

	if agent.Temperature != nil {
		t.Error("Agent.Temperature should be nil by default")
	}
}

func TestBudgetConfiguration(t *testing.T) {
	budget := Budget{
		Limit:    5.0,
		OnExceed: BudgetBlock,
	}

	if budget.Limit != 5.0 {
		t.Errorf("Budget.Limit = %f, want %f", budget.Limit, 5.0)
	}

	if budget.OnExceed != BudgetBlock {
		t.Errorf("Budget.OnExceed = %d, want %d", budget.OnExceed, BudgetBlock)
	}
}

func TestRetryPolicyConfiguration(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts: 3,
		Backoff: BackoffConfig{
			Initial:    100 * time.Millisecond,
			Multiplier: 2.0,
			Max:        5 * time.Second,
			Jitter:     0.1,
			Type:       BackoffExponential,
		},
		RetryOn: []ErrorClass{ErrClassRateLimit, ErrClassTemporary},
	}

	if policy.MaxAttempts != 3 {
		t.Errorf("RetryPolicy.MaxAttempts = %d, want %d", policy.MaxAttempts, 3)
	}

	if policy.Backoff.Multiplier != 2.0 {
		t.Errorf("RetryPolicy.Backoff.Multiplier = %f, want %f", policy.Backoff.Multiplier, 2.0)
	}

	if len(policy.RetryOn) != 2 {
		t.Errorf("len(RetryPolicy.RetryOn) = %d, want %d", len(policy.RetryOn), 2)
	}
}

func TestCircuitBreakerConfiguration(t *testing.T) {
	openCalled := false
	closeCalled := false

	cb := CircuitBreaker{
		Threshold:   5,
		ResetAfter:  30 * time.Second,
		HalfOpenMax: 2,
		OnOpen:      func() { openCalled = true },
		OnClose:     func() { closeCalled = true },
	}

	if cb.Threshold != 5 {
		t.Errorf("CircuitBreaker.Threshold = %d, want %d", cb.Threshold, 5)
	}

	if cb.ResetAfter != 30*time.Second {
		t.Errorf("CircuitBreaker.ResetAfter = %v, want %v", cb.ResetAfter, 30*time.Second)
	}

	// Test callbacks
	cb.OnOpen()
	if !openCalled {
		t.Error("OnOpen callback was not called")
	}

	cb.OnClose()
	if !closeCalled {
		t.Error("OnClose callback was not called")
	}
}

func TestMessageConstruction(t *testing.T) {
	msg := llm.Message{
		Role:    llm.RoleUser,
		Content: "Hello, world!",
	}

	if msg.Role != llm.RoleUser {
		t.Errorf("Message.Role = %q, want %q", msg.Role, llm.RoleUser)
	}

	if msg.Content != "Hello, world!" {
		t.Errorf("Message.Content = %q, want %q", msg.Content, "Hello, world!")
	}
}

func TestMemoryItem(t *testing.T) {
	now := time.Now()
	item := memory.MemoryItem{
		Key:       "test-key",
		Value:     "test-value",
		Metadata:  map[string]any{"tag": "important"},
		CreatedAt: now,
		UpdatedAt: now,
		Score:     0.95,
	}

	if item.Key != "test-key" {
		t.Errorf("MemoryItem.Key = %q, want %q", item.Key, "test-key")
	}

	if item.Score != 0.95 {
		t.Errorf("MemoryItem.Score = %f, want %f", item.Score, 0.95)
	}

	if item.Metadata["tag"] != "important" {
		t.Errorf("MemoryItem.Metadata[tag] = %v, want %v", item.Metadata["tag"], "important")
	}
}

func TestRateLimitConfiguration(t *testing.T) {
	rl := RateLimit{
		RequestsPerMinute: 60,
		TokensPerMinute:   100000,
	}

	if rl.RequestsPerMinute != 60 {
		t.Errorf("RateLimit.RequestsPerMinute = %d, want %d", rl.RequestsPerMinute, 60)
	}

	if rl.TokensPerMinute != 100000 {
		t.Errorf("RateLimit.TokensPerMinute = %d, want %d", rl.TokensPerMinute, 100000)
	}
}
