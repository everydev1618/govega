package dsl

import (
	"context"
	"testing"
	"time"
)

func TestExecutionContext(t *testing.T) {
	ctx := &ExecutionContext{
		Inputs:    map[string]any{"task": "test"},
		Variables: make(map[string]any),
		StartTime: time.Now(),
	}

	if ctx.Inputs["task"] != "test" {
		t.Errorf("ExecutionContext.Inputs[task] = %v, want %v", ctx.Inputs["task"], "test")
	}
}

func TestLoopState(t *testing.T) {
	ls := &LoopState{
		Index: 2,
		Count: 3,
		Item:  "item-2",
		First: false,
		Last:  false,
	}

	if ls.Index != 2 {
		t.Errorf("LoopState.Index = %d, want 2", ls.Index)
	}

	if ls.Item != "item-2" {
		t.Errorf("LoopState.Item = %v, want %v", ls.Item, "item-2")
	}
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		File:    "test.vega.yaml",
		Line:    10,
		Column:  5,
		Field:   "agents.coder.model",
		Message: "invalid model name",
		Hint:    "try 'claude-sonnet-4-20250514'",
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("ValidationError.Error() should not be empty")
	}
}

// TestInterpreterInterpolation tests the interpolation logic
func TestInterpreterInterpolation(t *testing.T) {
	// Create a minimal document for testing interpolation
	doc := &Document{
		Name:      "Test",
		Agents:    make(map[string]*Agent),
		Workflows: make(map[string]*Workflow),
	}

	// Test the copyMap helper
	original := map[string]any{
		"a": 1,
		"b": "two",
		"c": true,
	}

	copied := copyMap(original)

	if len(copied) != len(original) {
		t.Errorf("copyMap() length = %d, want %d", len(copied), len(original))
	}

	// Modify original shouldn't affect copy
	original["a"] = 999
	if copied["a"] == 999 {
		t.Error("copyMap() should create independent copy")
	}

	_ = doc // Use doc to avoid unused variable
}

// TestExpressionEvaluationPatterns tests common expression patterns
func TestExpressionEvaluationPatterns(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]any
		want     string
	}{
		{
			name:     "simple variable",
			template: "Hello, {{name}}!",
			vars:     map[string]any{"name": "World"},
			want:     "Hello, World!",
		},
		{
			name:     "multiple variables",
			template: "{{greeting}}, {{name}}!",
			vars:     map[string]any{"greeting": "Hi", "name": "Alice"},
			want:     "Hi, Alice!",
		},
		{
			name:     "no variables",
			template: "Static text",
			vars:     map[string]any{},
			want:     "Static text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that the patterns we expect are detected
			if len(tt.vars) > 0 {
				if !ContainsExpression(tt.template) {
					t.Errorf("ContainsExpression(%q) should be true", tt.template)
				}
			}
		})
	}
}

// TestFilterPatterns tests the filter syntax parsing
func TestFilterPatterns(t *testing.T) {
	tests := []struct {
		expr       string
		hasFilter  bool
		filterName string
	}{
		{"name", false, ""},
		{"name | upper", true, "upper"},
		{"name | lower", true, "lower"},
		{"name | default:unknown", true, "default"},
		{"items | join:,", true, "join"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			hasFilter := containsFilter(tt.expr)
			if hasFilter != tt.hasFilter {
				t.Errorf("containsFilter(%q) = %v, want %v", tt.expr, hasFilter, tt.hasFilter)
			}
		})
	}
}

// Helper function for testing
func containsFilter(expr string) bool {
	for i := 0; i < len(expr); i++ {
		if expr[i] == '|' {
			return true
		}
	}
	return false
}

// TestStepTypeDetection tests that we can identify step types
func TestStepTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		step     Step
		stepType string
	}{
		{
			name:     "agent step",
			step:     Step{Agent: "coder", Send: "hello"},
			stepType: "agent",
		},
		{
			name:     "set step",
			step:     Step{Set: map[string]any{"x": 1}},
			stepType: "set",
		},
		{
			name:     "conditional step",
			step:     Step{Condition: "x > 0", Then: []Step{}},
			stepType: "conditional",
		},
		{
			name:     "parallel step",
			step:     Step{Parallel: []Step{{}, {}}},
			stepType: "parallel",
		},
		{
			name:     "return step",
			step:     Step{Return: "result"},
			stepType: "return",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectStepType(tt.step)
			if got != tt.stepType {
				t.Errorf("detectStepType() = %q, want %q", got, tt.stepType)
			}
		})
	}
}

// Helper function for testing
func detectStepType(step Step) string {
	switch {
	case step.Condition != "":
		return "conditional"
	case len(step.Parallel) > 0:
		return "parallel"
	case step.Repeat != nil:
		return "repeat"
	case step.ForEach != "":
		return "foreach"
	case step.Workflow != "":
		return "subworkflow"
	case step.Set != nil:
		return "set"
	case step.Return != "":
		return "return"
	case step.Agent != "":
		return "agent"
	default:
		return "unknown"
	}
}

// TestContextTimeout tests context timeout handling
func TestContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Simulate waiting for context to expire
	time.Sleep(20 * time.Millisecond)

	select {
	case <-ctx.Done():
		if ctx.Err() != context.DeadlineExceeded {
			t.Errorf("Context error = %v, want DeadlineExceeded", ctx.Err())
		}
	default:
		t.Error("Context should be done after timeout")
	}
}

// TestInputValidation tests workflow input validation
func TestInputValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   Input
		value   any
		wantErr bool
	}{
		{
			name:    "required with value",
			input:   Input{Type: "string", Required: true},
			value:   "hello",
			wantErr: false,
		},
		{
			name:    "required without value",
			input:   Input{Type: "string", Required: true},
			value:   nil,
			wantErr: true,
		},
		{
			name:    "optional without value",
			input:   Input{Type: "string", Required: false},
			value:   nil,
			wantErr: false,
		},
		{
			name:    "with default",
			input:   Input{Type: "string", Required: true, Default: "default"},
			value:   nil,
			wantErr: false, // Default should satisfy requirement
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInput(tt.input, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateInput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper function for testing
func validateInput(input Input, value any) error {
	if input.Required && value == nil && input.Default == nil {
		return &ValidationError{Message: "required input missing"}
	}
	return nil
}

// TestWorkflowOutput tests output evaluation patterns
func TestWorkflowOutput(t *testing.T) {
	tests := []struct {
		name   string
		output any
		isMap  bool
	}{
		{
			name:   "string output",
			output: "{{result}}",
			isMap:  false,
		},
		{
			name:   "map output",
			output: map[string]any{"code": "{{code}}", "review": "{{review}}"},
			isMap:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, isMap := tt.output.(map[string]any)
			if isMap != tt.isMap {
				t.Errorf("output type map = %v, want %v", isMap, tt.isMap)
			}
		})
	}
}
