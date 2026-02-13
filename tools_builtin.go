package vega

import (
	"context"
	"encoding/json"
	"os"
)

// RegisterBuiltins adds the built-in tools.
func (t *Tools) RegisterBuiltins() {
	t.Register("read_file", func(path string) (string, error) {
		data, err := os.ReadFile(path)
		return string(data), err
	})

	t.Register("write_file", ToolDef{
		Description: "Write content to a file",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			path := params["path"].(string)
			content := params["content"].(string)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", err
			}
			return "File written successfully", nil
		},
		Params: map[string]ParamDef{
			"path":    {Type: "string", Description: "File path", Required: true},
			"content": {Type: "string", Description: "Content to write", Required: true},
		},
	})

	t.Register("list_files", func(path string) (string, error) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", err
		}
		var names []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		result, _ := json.Marshal(names)
		return string(result), nil
	})

	t.Register("append_file", ToolDef{
		Description: "Append content to a file",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			path := params["path"].(string)
			content := params["content"].(string)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return "", err
			}
			defer f.Close()
			if _, err := f.WriteString(content); err != nil {
				return "", err
			}
			return "Content appended successfully", nil
		},
		Params: map[string]ParamDef{
			"path":    {Type: "string", Description: "File path", Required: true},
			"content": {Type: "string", Description: "Content to append", Required: true},
		},
	})
}
