package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// builtinMCPServer describes a Go-native MCP server implementation.
type builtinMCPServer struct {
	// tools maps tool name (without server prefix) to its definition.
	tools map[string]ToolDef
}

// builtinServers maps server names to their Go implementations.
var builtinServers = map[string]*builtinMCPServer{
	"fetch": fetchServer(),
	"mssql": mssqlServer(),
}

// HasBuiltinServer reports whether a Go-native implementation exists for the named MCP server.
func (t *Tools) HasBuiltinServer(name string) bool {
	_, ok := builtinServers[name]
	return ok
}

// BuiltinServerConnected reports whether a built-in server's tools are already registered.
func (t *Tools) BuiltinServerConnected(name string) bool {
	server, ok := builtinServers[name]
	if !ok {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for toolName := range server.tools {
		if _, exists := t.tools[name+"__"+toolName]; exists {
			return true
		}
	}
	return false
}

// ConnectBuiltinServer registers all tools from a built-in Go MCP server implementation.
// Tools are registered with the standard "servername__toolname" prefix.
// Returns the number of tools registered.
func (t *Tools) ConnectBuiltinServer(ctx context.Context, name string) (int, error) {
	server, ok := builtinServers[name]
	if !ok {
		return 0, fmt.Errorf("no built-in server %q", name)
	}

	var count int
	for toolName, def := range server.tools {
		prefixed := name + "__" + toolName
		if err := t.Register(prefixed, def); err != nil {
			// Skip if already registered.
			if strings.Contains(err.Error(), "already registered") {
				continue
			}
			return count, fmt.Errorf("register %s: %w", prefixed, err)
		}
		count++
	}
	return count, nil
}

// DisconnectBuiltinServer unregisters all tools from a built-in Go MCP server.
func (t *Tools) DisconnectBuiltinServer(name string) error {
	server, ok := builtinServers[name]
	if !ok {
		return fmt.Errorf("no built-in server %q", name)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for toolName := range server.tools {
		delete(t.tools, name+"__"+toolName)
	}
	return nil
}

// --- Fetch server ---

func fetchServer() *builtinMCPServer {
	return &builtinMCPServer{
		tools: map[string]ToolDef{
			"fetch": {
				Description: "Fetches a URL from the internet and returns its content. When raw=false (default), HTML is stripped to plain text for readability. Use start_index and max_length to paginate through large responses.",
				Fn:          ToolFunc(fetchToolFunc),
				Params: map[string]ParamDef{
					"url": {
						Type:        "string",
						Description: "URL to fetch",
						Required:    true,
					},
					"max_length": {
						Type:        "integer",
						Description: "Maximum number of characters to return (default 5000)",
					},
					"start_index": {
						Type:        "integer",
						Description: "Character offset to start from for pagination (default 0)",
					},
					"raw": {
						Type:        "boolean",
						Description: "Return raw content without HTML stripping (default false)",
					},
				},
			},
		},
	}
}

func fetchToolFunc(ctx context.Context, params map[string]any) (string, error) {
	urlStr, _ := params["url"].(string)
	if urlStr == "" {
		return "", fmt.Errorf("url is required")
	}

	maxLength := 5000
	if v, ok := toInt(params["max_length"]); ok && v > 0 {
		maxLength = v
	}

	startIndex := 0
	if v, ok := toInt(params["start_index"]); ok && v >= 0 {
		startIndex = v
	}

	raw := false
	if v, ok := params["raw"].(bool); ok {
		raw = v
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Vega/1.0 (MCP Fetch Server)")
	req.Header.Set("Accept", "text/html, application/json, text/plain, */*")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", urlStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, urlStr)
	}

	// Read body with 5MB limit.
	const maxBody = 5 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	content := string(body)
	title := ""

	if !raw {
		title = extractTitle(content)
		content = stripHTML(content)
	}

	totalLen := len(content)

	// Apply pagination.
	if startIndex > 0 {
		if startIndex >= len(content) {
			return fmt.Sprintf("URL: %s\nContent-Length: %d\n\nstart_index %d exceeds content length %d", urlStr, totalLen, startIndex, totalLen), nil
		}
		content = content[startIndex:]
	}

	truncated := false
	if len(content) > maxLength {
		content = content[:maxLength]
		truncated = true
	}

	// Build result.
	var sb strings.Builder
	sb.WriteString("URL: ")
	sb.WriteString(urlStr)
	sb.WriteByte('\n')
	if title != "" {
		sb.WriteString("Title: ")
		sb.WriteString(title)
		sb.WriteByte('\n')
	}
	sb.WriteString(fmt.Sprintf("Content-Length: %d\n", totalLen))
	if truncated {
		nextIndex := startIndex + maxLength
		sb.WriteString(fmt.Sprintf("Truncated: showing %d-%d of %d. Use start_index=%d to continue.\n", startIndex, nextIndex, totalLen, nextIndex))
	}
	sb.WriteByte('\n')
	sb.WriteString(content)

	return sb.String(), nil
}

// --- HTML processing helpers ---

// Tags whose entire content (including children) should be removed.
var (
	stripScriptRe  = regexp.MustCompile(`(?is)<script[\s>].*?</script>`)
	stripStyleRe   = regexp.MustCompile(`(?is)<style[\s>].*?</style>`)
	stripNavRe     = regexp.MustCompile(`(?is)<nav[\s>].*?</nav>`)
	stripHeaderRe  = regexp.MustCompile(`(?is)<header[\s>].*?</header>`)
	stripFooterRe  = regexp.MustCompile(`(?is)<footer[\s>].*?</footer>`)
)

// Any remaining HTML tag.
var stripTagRe = regexp.MustCompile(`<[^>]+>`)

// Consecutive whitespace (but not newlines).
var collapseSpaceRe = regexp.MustCompile(`[^\S\n]+`)

// Three or more consecutive newlines.
var collapseNewlineRe = regexp.MustCompile(`\n{3,}`)

// extractTitle pulls the <title> text from HTML.
func extractTitle(s string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(stripTagRe.ReplaceAllString(m[1], "")))
}

// stripHTML removes HTML tags and cleans up whitespace.
func stripHTML(s string) string {
	// Remove script, style, nav, header, footer blocks entirely.
	s = stripScriptRe.ReplaceAllString(s, "")
	s = stripStyleRe.ReplaceAllString(s, "")
	s = stripNavRe.ReplaceAllString(s, "")
	s = stripHeaderRe.ReplaceAllString(s, "")
	s = stripFooterRe.ReplaceAllString(s, "")

	// Strip remaining tags.
	s = stripTagRe.ReplaceAllString(s, " ")

	// Decode common HTML entities.
	s = html.UnescapeString(s)

	// Collapse whitespace.
	s = collapseSpaceRe.ReplaceAllString(s, " ")
	s = collapseNewlineRe.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}

// toInt converts various numeric types to int.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	default:
		return 0, false
	}
}
