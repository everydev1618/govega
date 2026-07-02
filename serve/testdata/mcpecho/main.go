// Command mcpecho is a minimal stdio MCP server used by the e2e tests. It
// speaks just enough JSON-RPC to exercise the full client path: initialize,
// the initialized notification (no reply), tools/list with one echo tool,
// and tools/call.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func reply(id int64, result any) {
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	fmt.Printf("%s\n", out)
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		// Notifications (no id) get no reply — a well-behaved MCP server
		// stays silent, which is exactly what the client fix relies on.
		if req.ID == nil {
			continue
		}

		switch req.Method {
		case "initialize":
			reply(*req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "mcpecho", "version": "1.0.0"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})
		case "tools/list":
			reply(*req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echoes back the provided text.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"text": map[string]any{"type": "string"},
						},
						"required": []string{"text"},
					},
				}},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			text, _ := params.Arguments["text"].(string)
			reply(*req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "mcpecho: " + text,
				}},
			})
		default:
			reply(*req.ID, map[string]any{})
		}
	}
}
