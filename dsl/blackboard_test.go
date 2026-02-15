package dsl

import (
	"context"
	"testing"

	vega "github.com/everydev1618/govega"
)

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
