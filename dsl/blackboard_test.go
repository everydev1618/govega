package dsl

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	vega "github.com/everydev1618/govega"
)

// ---------- Blackboard Tool: Basic CRUD ----------

func TestBlackboardReadWriteList(t *testing.T) {
	group := vega.NewGroup("test-team")

	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	ctx := context.Background()

	// Write a value
	writeTool := NewBlackboardWriteTool(resolver)
	writeFn := writeTool.Fn.(func(context.Context, map[string]any) (string, error))
	result, err := writeFn(ctx, map[string]any{"key": "goal", "value": "ship by Friday"})
	if err != nil {
		t.Fatalf("bb_write error: %v", err)
	}
	if result == "" {
		t.Error("bb_write should return confirmation")
	}

	// Read it back
	readTool := NewBlackboardReadTool(resolver)
	readFn := readTool.Fn.(func(context.Context, map[string]any) (string, error))
	result, err = readFn(ctx, map[string]any{"key": "goal"})
	if err != nil {
		t.Fatalf("bb_read error: %v", err)
	}
	if result != `"ship by Friday"` {
		t.Errorf("bb_read = %q, want %q", result, `"ship by Friday"`)
	}

	// List keys
	listTool := NewBlackboardListTool(resolver)
	listFn := listTool.Fn.(func(context.Context, map[string]any) (string, error))
	result, err = listFn(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("bb_list error: %v", err)
	}
	if result != "goal" {
		t.Errorf("bb_list = %q, want %q", result, "goal")
	}
}

func TestBlackboardOverwriteViaTool(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}
	ctx := context.Background()

	writeFn := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	readFn := NewBlackboardReadTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))

	writeFn(ctx, map[string]any{"key": "status", "value": "draft"})
	writeFn(ctx, map[string]any{"key": "status", "value": "published"})

	result, err := readFn(ctx, map[string]any{"key": "status"})
	if err != nil {
		t.Fatalf("bb_read error: %v", err)
	}
	if result != `"published"` {
		t.Errorf("bb_read after overwrite = %q, want %q", result, `"published"`)
	}
}

func TestBlackboardMultipleKeys(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}
	ctx := context.Background()

	writeFn := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	listFn := NewBlackboardListTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))

	writeFn(ctx, map[string]any{"key": "alpha", "value": "1"})
	writeFn(ctx, map[string]any{"key": "beta", "value": "2"})
	writeFn(ctx, map[string]any{"key": "gamma", "value": "3"})

	result, err := listFn(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("bb_list error: %v", err)
	}

	keys := strings.Split(result, "\n")
	sort.Strings(keys)
	if len(keys) != 3 || keys[0] != "alpha" || keys[1] != "beta" || keys[2] != "gamma" {
		t.Errorf("bb_list = %v, want [alpha beta gamma]", keys)
	}
}

func TestBlackboardReadNumericValue(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}
	ctx := context.Background()

	writeFn := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	readFn := NewBlackboardReadTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))

	// Write a numeric value (as the tool receives from JSON params)
	writeFn(ctx, map[string]any{"key": "count", "value": float64(42)})

	result, err := readFn(ctx, map[string]any{"key": "count"})
	if err != nil {
		t.Fatalf("bb_read error: %v", err)
	}
	if result != "42" {
		t.Errorf("bb_read numeric = %q, want %q", result, "42")
	}
}

func TestBlackboardWriteConfirmation(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	writeFn := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	result, err := writeFn(context.Background(), map[string]any{"key": "status", "value": "ok"})
	if err != nil {
		t.Fatalf("bb_write error: %v", err)
	}
	if !strings.Contains(result, "status") {
		t.Errorf("bb_write confirmation should mention the key, got %q", result)
	}
}

// ---------- Blackboard Tool: Error Cases ----------

func TestBlackboardReadMissing(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	readTool := NewBlackboardReadTool(resolver)
	readFn := readTool.Fn.(func(context.Context, map[string]any) (string, error))
	_, err := readFn(context.Background(), map[string]any{"key": "nonexistent"})
	if err == nil {
		t.Error("bb_read should error on missing key")
	}
}

func TestBlackboardNoGroup(t *testing.T) {
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return nil
	}

	ctx := context.Background()

	writeTool := NewBlackboardWriteTool(resolver)
	writeFn := writeTool.Fn.(func(context.Context, map[string]any) (string, error))
	_, err := writeFn(ctx, map[string]any{"key": "k", "value": "v"})
	if err == nil {
		t.Error("bb_write should error when no group available")
	}

	readTool := NewBlackboardReadTool(resolver)
	readFn := readTool.Fn.(func(context.Context, map[string]any) (string, error))
	_, err = readFn(ctx, map[string]any{"key": "k"})
	if err == nil {
		t.Error("bb_read should error when no group available")
	}

	listTool := NewBlackboardListTool(resolver)
	listFn := listTool.Fn.(func(context.Context, map[string]any) (string, error))
	_, err = listFn(ctx, map[string]any{})
	if err == nil {
		t.Error("bb_list should error when no group available")
	}
}

func TestBlackboardEmptyParams(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	ctx := context.Background()

	writeTool := NewBlackboardWriteTool(resolver)
	writeFn := writeTool.Fn.(func(context.Context, map[string]any) (string, error))
	_, err := writeFn(ctx, map[string]any{"key": "", "value": "v"})
	if err == nil {
		t.Error("bb_write should error on empty key")
	}

	_, err = writeFn(ctx, map[string]any{"key": "k"})
	if err == nil {
		t.Error("bb_write should error on missing value")
	}

	readTool := NewBlackboardReadTool(resolver)
	readFn := readTool.Fn.(func(context.Context, map[string]any) (string, error))
	_, err = readFn(ctx, map[string]any{"key": ""})
	if err == nil {
		t.Error("bb_read should error on empty key")
	}
}

func TestBlackboardReadMissingKey(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	readFn := NewBlackboardReadTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))

	// Missing "key" param entirely
	_, err := readFn(context.Background(), map[string]any{})
	if err == nil {
		t.Error("bb_read should error when key param is missing")
	}
}

func TestBlackboardWriteNilValue(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	writeFn := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	_, err := writeFn(context.Background(), map[string]any{"key": "k", "value": nil})
	if err == nil {
		t.Error("bb_write should error on nil value")
	}
}

func TestBlackboardListEmpty(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}

	listTool := NewBlackboardListTool(resolver)
	listFn := listTool.Fn.(func(context.Context, map[string]any) (string, error))
	result, err := listFn(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("bb_list error: %v", err)
	}
	if result != "blackboard is empty" {
		t.Errorf("bb_list empty = %q, want %q", result, "blackboard is empty")
	}
}

// ---------- Blackboard Tool: ToolDef Metadata ----------

func TestBlackboardToolDefStructure(t *testing.T) {
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return vega.NewGroup("test")
	}

	tests := []struct {
		name   string
		tool   vega.ToolDef
		params []string
	}{
		{"bb_read", NewBlackboardReadTool(resolver), []string{"key"}},
		{"bb_write", NewBlackboardWriteTool(resolver), []string{"key", "value"}},
		{"bb_list", NewBlackboardListTool(resolver), []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tool.Description == "" {
				t.Error("tool should have a description")
			}
			if tt.tool.Fn == nil {
				t.Error("tool should have a function")
			}
			for _, p := range tt.params {
				if _, ok := tt.tool.Params[p]; !ok {
					t.Errorf("tool should have param %q", p)
				}
			}
		})
	}
}

// ---------- Blackboard Tool: Concurrent Access ----------

func TestBlackboardToolConcurrent(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}
	ctx := context.Background()

	writeFn := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	readFn := NewBlackboardReadTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	listFn := NewBlackboardListTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			writeFn(ctx, map[string]any{"key": "k", "value": "v"})
		}()
		go func() {
			defer wg.Done()
			readFn(ctx, map[string]any{"key": "k"})
		}()
		go func() {
			defer wg.Done()
			listFn(ctx, map[string]any{})
		}()
	}
	wg.Wait()
}

// ---------- Blackboard Tool: Shared Group ----------

func TestBlackboardToolsShareGroup(t *testing.T) {
	group := vega.NewGroup("test-team")
	resolver := func(ctx context.Context) *vega.ProcessGroup {
		return group
	}
	ctx := context.Background()

	// Write via one tool instance
	writeFn1 := NewBlackboardWriteTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	writeFn1(ctx, map[string]any{"key": "shared", "value": "data"})

	// Read via a different tool instance (same resolver → same group)
	readFn2 := NewBlackboardReadTool(resolver).Fn.(func(context.Context, map[string]any) (string, error))
	result, err := readFn2(ctx, map[string]any{"key": "shared"})
	if err != nil {
		t.Fatalf("bb_read from second tool instance error: %v", err)
	}
	if result != `"data"` {
		t.Errorf("bb_read = %q, want %q", result, `"data"`)
	}
}
