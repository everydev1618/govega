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

// BuiltinMCPServer is an in-process Go-native MCP server. Embedding
// applications register additional servers via RegisterBuiltinServer.
//
// govega ships only generic, agent-runtime-essential builtins (e.g.
// fetch). Vendor-specific integrations live in the consuming product
// and call RegisterBuiltinServer at startup.
type BuiltinMCPServer struct {
	// tools maps tool name (without server prefix) to its definition.
	tools map[string]ToolDef
}

// NewBuiltinMCPServer constructs a server with the given tool defs. The
// caller still has to call RegisterBuiltinServer to make it discoverable
// by name from agents.
func NewBuiltinMCPServer(tools map[string]ToolDef) *BuiltinMCPServer {
	return &BuiltinMCPServer{tools: tools}
}

// RegisterBuiltinServer adds an in-process MCP server to the global
// registry under the given name. After this returns nil, agents can
// connect via Tools.ConnectBuiltinServer(ctx, name) and tools will be
// registered as <name>__<toolname>.
//
// Returns an error if the name is already taken — re-registration is
// not supported (use a different name or restart the process).
func RegisterBuiltinServer(name string, server *BuiltinMCPServer) error {
	if server == nil {
		return fmt.Errorf("RegisterBuiltinServer: server is nil")
	}
	if _, exists := builtinServers[name]; exists {
		return fmt.Errorf("RegisterBuiltinServer: %q is already registered", name)
	}
	builtinServers[name] = server
	return nil
}

// builtinServers maps server names to their Go implementations. govega's
// hardcoded entries cover infrastructure-y essentials; product-specific
// servers (Vapi, Slack, etc.) are added via RegisterBuiltinServer.
//
// gmail and mssql are pending the same migration as vapi — they'll move
// to apexvega's integrations/ tree in follow-up commits.
var builtinServers = map[string]*BuiltinMCPServer{
	"fetch": fetchServer(),
	"mssql": mssqlServer(),
	"gmail": gmailServer(),
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

func fetchServer() *BuiltinMCPServer {
	return &BuiltinMCPServer{
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

// browserUserAgent is a current desktop-Chrome UA. Fetching public pages on
// the user's behalf with a realistic UA avoids the reflexive bot-block that a
// tool-flavored UA triggers. This is not evasion of a real access decision —
// sites that truly gate content (login/paywall/hard challenge) still return
// 401/403 and we report that honestly.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

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
		Timeout: 20 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	// Present as a real browser. A bot-flavored User-Agent (the old
	// "Vega/1.0 (MCP Fetch Server)") gets 403'd on sight by anti-bot layers
	// like Cloudflare, which the user experiences as "it got blocked".
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", urlStr, err)
	}
	defer resp.Body.Close()

	// 401/403/429 are the anti-bot / rate-limit signatures — say so plainly so
	// the agent relays an honest, actionable message rather than a bare code.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%s blocked automated access (HTTP %d) — the site likely requires a real browser or login; try a different source", urlStr, resp.StatusCode)
	}
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
	stripScriptRe = regexp.MustCompile(`(?is)<script[\s>].*?</script>`)
	stripStyleRe  = regexp.MustCompile(`(?is)<style[\s>].*?</style>`)
	stripNavRe    = regexp.MustCompile(`(?is)<nav[\s>].*?</nav>`)
	stripHeaderRe = regexp.MustCompile(`(?is)<header[\s>].*?</header>`)
	stripFooterRe = regexp.MustCompile(`(?is)<footer[\s>].*?</footer>`)
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
