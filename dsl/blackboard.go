package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	vega "github.com/everydev1618/govega"
)

// GroupResolver returns the team ProcessGroup for the calling process.
type GroupResolver func(ctx context.Context) *vega.ProcessGroup

// NewBlackboardReadTool creates a tool that reads a key from the team blackboard.
func NewBlackboardReadTool(getGroup GroupResolver) vega.ToolDef {
	return vega.ToolDef{
		Description: "Read a value from the shared team blackboard by key.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			key, _ := params["key"].(string)
			if key == "" {
				return "", fmt.Errorf("key is required")
			}
			group := getGroup(ctx)
			if group == nil {
				return "", fmt.Errorf("no team blackboard available")
			}
			val, ok := group.BBGet(key)
			if !ok {
				return "", fmt.Errorf("key %q not found on blackboard", key)
			}
			b, err := json.Marshal(val)
			if err != nil {
				return fmt.Sprint(val), nil
			}
			return string(b), nil
		},
		Params: map[string]vega.ParamDef{
			"key": {
				Type:        "string",
				Description: "The key to read from the blackboard",
				Required:    true,
			},
		},
	}
}

// NewBlackboardWriteTool creates a tool that writes a key/value pair to the team blackboard.
func NewBlackboardWriteTool(getGroup GroupResolver) vega.ToolDef {
	return vega.ToolDef{
		Description: "Write a key/value pair to the shared team blackboard.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			key, _ := params["key"].(string)
			if key == "" {
				return "", fmt.Errorf("key is required")
			}
			value := params["value"]
			if value == nil {
				return "", fmt.Errorf("value is required")
			}
			group := getGroup(ctx)
			if group == nil {
				return "", fmt.Errorf("no team blackboard available")
			}
			group.BBSet(key, value)
			return fmt.Sprintf("wrote %q to blackboard", key), nil
		},
		Params: map[string]vega.ParamDef{
			"key": {
				Type:        "string",
				Description: "The key to write",
				Required:    true,
			},
			"value": {
				Type:        "string",
				Description: "The value to store",
				Required:    true,
			},
		},
	}
}

// NewBlackboardListTool creates a tool that lists all keys on the team blackboard.
func NewBlackboardListTool(getGroup GroupResolver) vega.ToolDef {
	return vega.ToolDef{
		Description: "List all keys on the shared team blackboard.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			group := getGroup(ctx)
			if group == nil {
				return "", fmt.Errorf("no team blackboard available")
			}
			keys := group.BBKeys()
			if len(keys) == 0 {
				return "blackboard is empty", nil
			}
			return strings.Join(keys, "\n"), nil
		},
		Params: map[string]vega.ParamDef{},
	}
}
