package vega

import (
	"errors"
	"testing"

	"github.com/everydev1618/govega/tools"
)

func TestStandardErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ErrProcessNotRunning", ErrProcessNotRunning, "process is not running"},
		{"ErrNotCompleted", ErrNotCompleted, "operation not completed"},
		{"ErrMaxIterationsExceeded", ErrMaxIterationsExceeded, "maximum iterations exceeded"},
		{"ErrBudgetExceeded", ErrBudgetExceeded, "budget exceeded"},
		{"ErrRateLimited", ErrRateLimited, "rate limited"},
		{"ErrCircuitOpen", ErrCircuitOpen, "circuit breaker is open"},
		{"ErrToolNotFound", tools.ErrToolNotFound, "tool not found"},
		{"ErrSandboxViolation", ErrSandboxViolation, "sandbox violation: path escapes allowed directory"},
		{"ErrMaxProcessesReached", ErrMaxProcessesReached, "maximum number of processes reached"},
		{"ErrProcessNotFound", ErrProcessNotFound, "process not found"},
		{"ErrAgentNotFound", ErrAgentNotFound, "agent not found"},
		{"ErrWorkflowNotFound", ErrWorkflowNotFound, "workflow not found"},
		{"ErrInvalidInput", ErrInvalidInput, "invalid input"},
		{"ErrTimeout", ErrTimeout, "operation timed out"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("%s.Error() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestProcessError(t *testing.T) {
	err := &ProcessError{
		ProcessID: "abc123",
		AgentName: "test-agent",
		Err:       ErrBudgetExceeded,
	}

	want := "process abc123 (test-agent): budget exceeded"
	if got := err.Error(); got != want {
		t.Errorf("ProcessError.Error() = %q, want %q", got, want)
	}

	// Test Unwrap
	if got := err.Unwrap(); got != ErrBudgetExceeded {
		t.Errorf("ProcessError.Unwrap() = %v, want %v", got, ErrBudgetExceeded)
	}

	// Test errors.Is
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Error("errors.Is(ProcessError, ErrBudgetExceeded) should be true")
	}
}

func TestToolError(t *testing.T) {
	err := &tools.ToolError{
		ToolName: "read_file",
		Err:      ErrSandboxViolation,
	}

	want := "tool read_file: sandbox violation: path escapes allowed directory"
	if got := err.Error(); got != want {
		t.Errorf("ToolError.Error() = %q, want %q", got, want)
	}

	// Test Unwrap
	if got := err.Unwrap(); got != ErrSandboxViolation {
		t.Errorf("ToolError.Unwrap() = %v, want %v", got, ErrSandboxViolation)
	}

	// Test errors.Is
	if !errors.Is(err, ErrSandboxViolation) {
		t.Error("errors.Is(ToolError, ErrSandboxViolation) should be true")
	}
}

func TestValidationError(t *testing.T) {
	tests := []struct {
		name string
		err  *ValidationError
		want string
	}{
		{
			name: "with field only",
			err: &ValidationError{
				Field:   "model",
				Message: "required field is missing",
			},
			want: "model: required field is missing",
		},
		{
			name: "with line number",
			err: &ValidationError{
				Field:   "agents.coder",
				Message: "invalid model name",
				Line:    15,
			},
			want: "agents.coder at line \x0f: invalid model name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("ValidationError.Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorWrapping(t *testing.T) {
	baseErr := errors.New("connection refused")
	procErr := &ProcessError{
		ProcessID: "proc-1",
		AgentName: "agent-1",
		Err:       baseErr,
	}

	// Should be able to unwrap to base error
	var unwrapped error = procErr
	for {
		next := errors.Unwrap(unwrapped)
		if next == nil {
			break
		}
		unwrapped = next
	}

	if unwrapped != baseErr {
		t.Errorf("Final unwrapped error = %v, want %v", unwrapped, baseErr)
	}
}
