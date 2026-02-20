# Vega Tool System

Tools are the bridge between agents and the outside world. Vega's tool system is designed to be:

1. **Type-safe** — Go functions with automatic schema generation
2. **Dynamic** — YAML definitions loaded at runtime
3. **Sandboxed** — File operations restricted to safe directories
4. **Extensible** — Easy to add custom implementations

## Quick Start

### Compiled Tools (Go Functions)

```go
tools := vega.NewTools()

// Simple function - schema auto-generated
tools.Register("greet", func(name string) string {
    return fmt.Sprintf("Hello, %s!", name)
})

// With error handling
tools.Register("read_file", func(path string) (string, error) {
    data, err := os.ReadFile(path)
    return string(data), err
})

// Struct parameters
type SearchParams struct {
    Query string `json:"query" desc:"The search query"`
    Limit int    `json:"limit" desc:"Max results" default:"10"`
}

tools.Register("search", func(p SearchParams) ([]Result, error) {
    return doSearch(p.Query, p.Limit)
})
```

### Dynamic Tools (YAML Definitions)

```yaml
# tools/definitions/send_slack.yaml
name: send_slack
description: Send a message to a Slack channel
params:
  - name: channel
    type: string
    description: Channel name (without #)
    required: true
  - name: message
    type: string
    description: Message to send
    required: true
implementation:
  type: http
  method: POST
  url: https://slack.com/api/chat.postMessage
  headers:
    Authorization: Bearer ${SLACK_BOT_TOKEN}
    Content-Type: application/json
  body:
    channel: "{{channel}}"
    text: "{{message}}"
```

Load all YAML tools:

```go
tools.LoadDirectory("tools/definitions/")
```

## Schema Generation

Vega automatically generates JSON schemas from Go function signatures.

### Basic Types

| Go Type | JSON Schema |
|---------|-------------|
| `string` | `{"type": "string"}` |
| `int`, `int64` | `{"type": "integer"}` |
| `float64` | `{"type": "number"}` |
| `bool` | `{"type": "boolean"}` |
| `[]T` | `{"type": "array", "items": {...}}` |
| `map[string]T` | `{"type": "object"}` |

### Struct Tags

```go
type CreateFileParams struct {
    Path    string `json:"path" desc:"File path" required:"true"`
    Content string `json:"content" desc:"File contents" required:"true"`
    Mode    int    `json:"mode" desc:"File permissions" default:"0644"`
}
```

Tags:
- `json:"name"` — Parameter name in schema
- `desc:"..."` — Description for LLM
- `required:"true"` — Mark as required
- `default:"value"` — Default value
- `enum:"a,b,c"` — Restrict to values

### Explicit Schema

For full control, use `ToolDef`:

```go
tools.Register("complex_tool", vega.ToolDef{
    Description: "A tool with complex schema",
    Fn: myFunction,
    Params: vega.Params{
        "action": {
            Type:        "string",
            Description: "Action to perform",
            Required:    true,
            Enum:        []string{"create", "update", "delete"},
        },
        "data": {
            Type:        "object",
            Description: "Action payload",
            Properties: vega.Params{
                "id":   {Type: "string"},
                "name": {Type: "string"},
            },
        },
    },
})
```

## Dynamic Tool Definitions

### YAML Structure

```yaml
name: tool_name                    # Required: unique identifier
description: What this tool does   # Required: shown to LLM
params:                            # Required: input parameters
  - name: param1
    type: string                   # string, integer, number, boolean, array, object
    description: What this param is
    required: true
  - name: param2
    type: integer
    default: 10
implementation:                    # Required: how to execute
  type: http                       # http, exec, file_read, file_write, builtin
  # ... type-specific config
```

### Implementation Types

#### HTTP

Make HTTP requests to external APIs.

```yaml
implementation:
  type: http
  method: POST                     # GET, POST, PUT, DELETE, PATCH
  url: https://api.example.com/endpoint
  headers:
    Authorization: Bearer ${API_KEY}
    Content-Type: application/json
  query:                           # URL query parameters
    format: json
  body:                            # Request body (for POST/PUT/PATCH)
    field1: "{{param1}}"
    field2: "{{param2}}"
  timeout: 30s                     # Request timeout
  retry:
    attempts: 3
    backoff: 1s
```

#### Exec

Execute shell commands.

```yaml
implementation:
  type: exec
  command: "curl -s '{{url}}' | jq '{{filter}}'"
  timeout: 60s
  env:
    PATH: /usr/local/bin:/usr/bin
```

#### File Read

Read files (respects sandbox).

```yaml
implementation:
  type: file_read
  path: "{{path}}"
  encoding: utf-8                  # utf-8, base64, binary
```

#### File Write

Write files (respects sandbox).

```yaml
implementation:
  type: file_write
  path: "{{path}}"
  content: "{{content}}"
  mode: 0644
  mkdir: true                      # Create parent directories
```

#### Builtin

Reference a compiled Go function.

```yaml
implementation:
  type: builtin
  function: myCustomFunction       # Must be registered in Tools
```

### Template Syntax

Dynamic tools use Go templates with extensions.

**Variable Interpolation:**
```yaml
url: https://api.example.com/users/{{user_id}}
```

**Default Values:**
```yaml
count: "{{count | default:10}}"
```

**Conditionals:**
```yaml
body:
  query: "{{query}}"
  {{if advanced_mode}}
  options:
    detailed: true
  {{end}}
```

**Environment Variables:**
```yaml
headers:
  Authorization: Bearer ${API_KEY}
```

## Sandboxing

File operations can be restricted to specific directories.

```go
tools := vega.NewTools(
    vega.WithSandbox("/app/workspace"),
)
```

With sandboxing enabled:

```go
// Allowed
tools.Execute("read_file", map[string]any{"path": "data/file.txt"})
// Resolves to: /app/workspace/data/file.txt

// Blocked
tools.Execute("read_file", map[string]any{"path": "/etc/passwd"})
// Error: path escapes sandbox

// Blocked
tools.Execute("read_file", map[string]any{"path": "../../../etc/passwd"})
// Error: path escapes sandbox
```

### Multiple Sandboxes

```go
tools := vega.NewTools(
    vega.WithSandboxes(map[string]string{
        "workspace": "/app/workspace",
        "outputs":   "/app/outputs",
        "readonly":  "/app/data",
    }),
    vega.WithReadOnlySandbox("readonly"),
)
```

## Tool Middleware

Add cross-cutting concerns to all tools.

### Logging

```go
tools.Use(vega.LoggingMiddleware(logger))
```

### Rate Limiting

```go
tools.Use(vega.RateLimitMiddleware(
    vega.PerTool(map[string]rate.Limit{
        "web_search": rate.Every(time.Second),
        "send_email": rate.Every(time.Minute),
    }),
))
```

### Timeout

```go
tools.Use(vega.TimeoutMiddleware(30 * time.Second))
```

### Custom Middleware

```go
tools.Use(func(next vega.ToolFunc) vega.ToolFunc {
    return func(ctx context.Context, params map[string]any) (string, error) {
        start := time.Now()
        result, err := next(ctx, params)
        metrics.RecordToolCall(ctx, time.Since(start))
        return result, err
    }
})
```

## Tool Discovery

Agents see tools as a list with schemas.

```go
tools.Schema() // Returns []ToolSchema

// Example output:
[
  {
    "name": "read_file",
    "description": "Read contents of a file",
    "input_schema": {
      "type": "object",
      "properties": {
        "path": {
          "type": "string",
          "description": "File path to read"
        }
      },
      "required": ["path"]
    }
  },
  {
    "name": "web_search",
    "description": "Search the web using Brave Search API",
    "input_schema": {
      "type": "object",
      "properties": {
        "query": {
          "type": "string",
          "description": "The search query"
        },
        "count": {
          "type": "integer",
          "description": "Number of results (default 10)"
        }
      },
      "required": ["query"]
    }
  }
]
```

## Tool Filtering

Agents can have different tool access.

```go
// All tools
allTools := vega.NewTools()
allTools.Register("read_file", readFile)
allTools.Register("write_file", writeFile)
allTools.Register("delete_file", deleteFile)
allTools.Register("web_search", webSearch)
allTools.Register("send_email", sendEmail)

// Researcher: read-only + search
researcherTools := allTools.Filter("read_file", "web_search")

// Writer: read + write
writerTools := allTools.Filter("read_file", "write_file")

// Admin: everything
adminTools := allTools
```

Or from config:

```yaml
# team.yaml
members:
  - name: Gary
    role: Developer
    skills:  # Tool whitelist
      - read_file
      - write_file
      - exec
```

```go
garyTools := allTools.Filter(member.Skills...)
```

## Error Handling

Tools should return errors, not panic.

```go
tools.Register("risky_operation", func(id string) (string, error) {
    result, err := riskyCall(id)
    if err != nil {
        // Vega will format this for the LLM
        return "", fmt.Errorf("operation failed: %w", err)
    }
    return result, nil
})
```

The LLM sees:
```
Tool call failed: operation failed: connection refused
```

### Retryable Errors

Mark errors as retryable:

```go
return "", vega.RetryableError(err)
```

Vega will automatically retry based on supervision config.

## Testing Tools

```go
func TestReadFile(t *testing.T) {
    tools := vega.NewTools(vega.WithSandbox(t.TempDir()))
    tools.Register("read_file", readFile)

    // Write test file
    os.WriteFile(filepath.Join(t.TempDir(), "test.txt"), []byte("hello"), 0644)

    // Execute tool
    result, err := tools.Execute(context.Background(), "read_file", map[string]any{
        "path": "test.txt",
    })

    assert.NoError(t, err)
    assert.Equal(t, "hello", result)
}
```

### Mock Tools

```go
mockTools := vega.NewMockTools()
mockTools.On("web_search", mock.Anything).Return("mock results", nil)

agent := vega.Agent{
    Tools: mockTools,
    // ...
}
```

## Built-in Tools

The following tools are registered automatically by `RegisterBuiltins()` and are available to any agent that lists them by name.

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file |
| `write_file` | Write content to a file (path, content) |
| `append_file` | Append content to an existing file |
| `list_files` | List directory contents as a JSON array |
| `exec` | Execute a shell command inside the sandbox |
| `send_email` | Send an email via SMTP |

### `send_email`

Sends email using stdlib `net/smtp`. Configuration is read from environment variables at call time — no restart needed when changing SMTP settings.

**Required env vars:**
- `SMTP_HOST` — SMTP server hostname (e.g. `smtp.gmail.com`)
- `SMTP_USER` — Login username / sender address
- `SMTP_PASS` — Login password or app password

**Optional env vars:**
- `SMTP_PORT` — Port number (default `587`)
- `SMTP_FROM` — From address (defaults to `SMTP_USER`)

**Parameters:**
- `to` (required) — Recipient email address
- `subject` (required) — Email subject line
- `body` (required) — Email body content
- `is_html` (optional, bool) — Send HTML email instead of plain text

**Example agent config:**
```yaml
agents:
  reporter:
    model: claude-sonnet-4-20250514
    system: You compile and email daily summaries.
    tools:
      - send_email
```

**Example environment setup:**
```bash
export SMTP_HOST=smtp.gmail.com
export SMTP_USER=you@gmail.com
export SMTP_PASS=your-app-password
```

## Best Practices

1. **Descriptive names** — `create_github_issue` not `gh_issue`
2. **Clear descriptions** — The LLM uses these to decide when to call tools
3. **Validate inputs** — Don't trust LLM-provided parameters
4. **Handle errors gracefully** — Return useful error messages
5. **Use sandboxing** — Always sandbox file operations in production
6. **Limit scope** — Give agents only the tools they need
7. **Log tool calls** — Essential for debugging agent behavior
