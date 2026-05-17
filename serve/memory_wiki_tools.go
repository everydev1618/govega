package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/tools"
)

// RegisterWikiMemoryTools registers the wiki-style memory tools on the
// interpreter's tool collection: memory_read, memory_list,
// memory_search, memory_write, memory_append, memory_edit,
// memory_delete, memory_rename. Refs govega#71.
//
// Tools share the same context wiring as the legacy remember/recall/
// forget tools (memoryFromContext); they coexist with those during
// the migration window.
func RegisterWikiMemoryTools(interp *dsl.Interpreter) {
	t := interp.Tools()

	// --- memory_read ---
	t.Register("memory_read", tools.ToolDef{
		Description: "Read one wiki memory page by path. Defaults to the shared user wiki (scope=user); pass scope=agent for your private working notes.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			path, _ := params["page"].(string)
			if path == "" {
				return "", fmt.Errorf("path is required")
			}
			// readMemoryPageForRecall handles store lookup, scope
			// resolution, and recall-ledger bookkeeping — same return
			// shape as the previous inline version (refs govega#100).
			return readMemoryPageForRecall(ctx, path, stringParam(params, "scope"))
		}),
		Params: map[string]tools.ParamDef{
			"page":  {Type: "string", Description: "Page path, e.g. 'MEMORY.md' or 'topics/sushi.md'", Required: true},
			"scope": wikiScopeParam,
		},
	})

	// --- memory_list ---
	t.Register("memory_list", tools.ToolDef{
		Description: "List wiki memory pages in the chosen scope. Optional path prefix filter ('topics/' to see only topic pages). Pages are returned newest-first.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}
			prefix, _ := params["prefix"].(string)
			scope, scopeID, err := resolveScope(stringParam(params, "scope"), userID, agent)
			if err != nil {
				return "", err
			}
			pages, err := store.ListMemoryPages(scope, scopeID, userID, prefix)
			if err != nil {
				return "", fmt.Errorf("list memory pages: %w", err)
			}
			if len(pages) == 0 {
				if prefix != "" {
					return fmt.Sprintf("(no pages under %q in %s wiki)", prefix, scope), nil
				}
				return fmt.Sprintf("(%s wiki is empty)", scope), nil
			}
			type row struct {
				Path      string `json:"path"`
				Bytes     int    `json:"bytes"`
				UpdatedAt string `json:"updated_at"`
			}
			rows := make([]row, len(pages))
			for i, p := range pages {
				rows[i] = row{
					Path:      p.Path,
					Bytes:     len(p.Content),
					UpdatedAt: p.UpdatedAt.Format("2006-01-02 15:04"),
				}
			}
			b, _ := json.MarshalIndent(rows, "", "  ")
			return string(b), nil
		}),
		Params: map[string]tools.ParamDef{
			"prefix": {Type: "string", Description: "Optional path prefix (e.g. 'topics/'). Omit for everything."},
			"scope":  wikiScopeParam,
		},
	})

	// --- memory_search ---
	t.Register("memory_search", tools.ToolDef{
		Description: "Substring-search wiki pages (path + content). Case-insensitive. Returns matching pages with a short content preview.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}
			query, _ := params["query"].(string)
			if query == "" {
				return "", fmt.Errorf("query is required")
			}
			limit := 25
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			scope, scopeID, err := resolveScope(stringParam(params, "scope"), userID, agent)
			if err != nil {
				return "", err
			}
			hits, err := store.SearchMemoryPages(scope, scopeID, userID, query, limit)
			if err != nil {
				return "", fmt.Errorf("search memory: %w", err)
			}
			if len(hits) == 0 {
				return "(no matches)", nil
			}
			type row struct {
				Path    string `json:"path"`
				Preview string `json:"preview"`
			}
			rows := make([]row, len(hits))
			for i, h := range hits {
				rows[i] = row{Path: h.Path, Preview: previewForSearch(h.Content, query)}
			}
			b, _ := json.MarshalIndent(rows, "", "  ")
			return string(b), nil
		}),
		Params: map[string]tools.ParamDef{
			"query": {Type: "string", Description: "Substring to match against path + content (case-insensitive)", Required: true},
			"limit": {Type: "number", Description: "Max hits (default 25)"},
			"scope": wikiScopeParam,
		},
	})

	// --- memory_write ---
	t.Register("memory_write", tools.ToolDef{
		Description: "Create or overwrite a wiki memory page. Use markdown. Wikilinks `[[other-page.md]]` and markdown links `[label](other-page.md)` are extracted into the link graph automatically. Prefer memory_edit for surgical changes — memory_write replaces the whole page.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			return writeWikiPage(ctx, params, false)
		}),
		Params: map[string]tools.ParamDef{
			"page":        {Type: "string", Description: "Page path. Conventions: 'MEMORY.md' is the index page (always loaded into your context). 'topics/<slug>.md', 'people/<name>.md', 'decisions.md' for shared facts.", Required: true},
			"content":     {Type: "string", Description: "Full markdown content of the page.", Required: true},
			"frontmatter": {Type: "string", Description: "Optional YAML frontmatter (without delimiters). Use for description, tags, etc."},
			"scope":       wikiScopeParam,
		},
	})

	// --- memory_append ---
	t.Register("memory_append", tools.ToolDef{
		Description: "Append text to an existing wiki page (or create it). Good for running logs and notes that accumulate. Re-extracts links from the merged content.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			return writeWikiPage(ctx, params, true)
		}),
		Params: map[string]tools.ParamDef{
			"page":    {Type: "string", Description: "Page path to append to.", Required: true},
			"content": {Type: "string", Description: "Markdown to append. A newline is inserted between existing content and the new chunk.", Required: true},
			"scope":   wikiScopeParam,
		},
	})

	// --- memory_edit ---
	t.Register("memory_edit", tools.ToolDef{
		Description: "Replace the first exact occurrence of `old` with `new` inside a wiki page. Fails if the page doesn't exist or `old` doesn't appear. Prefer this over memory_write when you only need to change a small part of a page — it's safer when the page is long.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}
			path, _ := params["page"].(string)
			oldStr, _ := params["old"].(string)
			newStr, _ := params["new"].(string)
			if path == "" || oldStr == "" {
				return "", fmt.Errorf("path and old are required")
			}
			scope, scopeID, err := resolveScope(stringParam(params, "scope"), userID, agent)
			if err != nil {
				return "", err
			}
			p, err := store.GetMemoryPage(scope, scopeID, userID, path)
			if err != nil {
				return "", fmt.Errorf("read page: %w", err)
			}
			if p == nil {
				return "", fmt.Errorf("page %q not found", path)
			}
			if !strings.Contains(p.Content, oldStr) {
				return "", fmt.Errorf("old string not found in page %q", path)
			}
			updated := strings.Replace(p.Content, oldStr, newStr, 1)
			if err := store.UpsertMemoryPage(MemoryPage{
				Scope: scope, ScopeID: scopeID, UserID: userID, Path: path,
				Content: updated, Frontmatter: p.Frontmatter,
			}); err != nil {
				return "", fmt.Errorf("write page: %w", err)
			}
			if err := store.ReplaceMemoryLinks(scope, scopeID, userID, path, extractMemoryLinks(updated)); err != nil {
				return "", fmt.Errorf("update link graph: %w", err)
			}
			return fmt.Sprintf("Edited %s page %q.", scope, path), nil
		}),
		Params: map[string]tools.ParamDef{
			"page":  {Type: "string", Description: "Page path.", Required: true},
			"old":   {Type: "string", Description: "Exact substring to replace. Must occur in the page.", Required: true},
			"new":   {Type: "string", Description: "Replacement text. Empty to delete the matched substring.", Required: true},
			"scope": wikiScopeParam,
		},
	})

	// --- memory_delete ---
	t.Register("memory_delete", tools.ToolDef{
		Description: "Delete a wiki page. Cascades through the link graph — any links into or out of this page are removed. Missing path is a no-op.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}
			path, _ := params["page"].(string)
			if path == "" {
				return "", fmt.Errorf("path is required")
			}
			scope, scopeID, err := resolveScope(stringParam(params, "scope"), userID, agent)
			if err != nil {
				return "", err
			}
			if err := store.DeleteMemoryPage(scope, scopeID, userID, path); err != nil {
				return "", fmt.Errorf("delete page: %w", err)
			}
			return fmt.Sprintf("Deleted %s page %q.", scope, path), nil
		}),
		Params: map[string]tools.ParamDef{
			"page":  {Type: "string", Description: "Page path to delete.", Required: true},
			"scope": wikiScopeParam,
		},
	})

	// --- memory_rename ---
	t.Register("memory_rename", tools.ToolDef{
		Description: "Move a wiki page from old_path to new_path. Rewrites every link in the graph so backreferences stay live.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}
			oldPath, _ := params["from"].(string)
			newPath, _ := params["to"].(string)
			if oldPath == "" || newPath == "" {
				return "", fmt.Errorf("from and to are required")
			}
			if oldPath == newPath {
				return "", fmt.Errorf("from and to are identical")
			}
			scope, scopeID, err := resolveScope(stringParam(params, "scope"), userID, agent)
			if err != nil {
				return "", err
			}
			existing, err := store.GetMemoryPage(scope, scopeID, userID, oldPath)
			if err != nil {
				return "", fmt.Errorf("read old page: %w", err)
			}
			if existing == nil {
				return "", fmt.Errorf("page %q not found", oldPath)
			}
			collision, err := store.GetMemoryPage(scope, scopeID, userID, newPath)
			if err != nil {
				return "", fmt.Errorf("check new path: %w", err)
			}
			if collision != nil {
				return "", fmt.Errorf("page %q already exists — delete it first or pick another name", newPath)
			}
			if err := store.RenameMemoryPage(scope, scopeID, userID, oldPath, newPath); err != nil {
				return "", fmt.Errorf("rename page: %w", err)
			}
			return fmt.Sprintf("Renamed %s page %q → %q.", scope, oldPath, newPath), nil
		}),
		Params: map[string]tools.ParamDef{
			"from":  {Type: "string", Description: "Current page path.", Required: true},
			"to":    {Type: "string", Description: "Destination path. Must not already exist.", Required: true},
			"scope": wikiScopeParam,
		},
	})
}

var wikiScopeParam = tools.ParamDef{
	Type:        "string",
	Description: "'user' (shared with every agent that talks to this user — default) or 'agent' (your private working notes). Pick deliberately: durable facts about the user go in 'user'; in-flight task state goes in 'agent'.",
	Enum:        []string{"user", "agent"},
}

// writeWikiPage handles the shared logic for memory_write and
// memory_append. append=true merges with the existing page (joining
// with a newline); append=false overwrites.
func writeWikiPage(ctx context.Context, params map[string]any, appendMode bool) (string, error) {
	store, userID, agent, err := memoryFromContext(ctx)
	if err != nil {
		return "", err
	}
	path, _ := params["page"].(string)
	content, _ := params["content"].(string)
	if path == "" || content == "" {
		return "", fmt.Errorf("path and content are required")
	}
	frontmatter, _ := params["frontmatter"].(string)
	scope, scopeID, err := resolveScope(stringParam(params, "scope"), userID, agent)
	if err != nil {
		return "", err
	}

	if appendMode {
		existing, err := store.GetMemoryPage(scope, scopeID, userID, path)
		if err != nil {
			return "", fmt.Errorf("read page: %w", err)
		}
		if existing != nil {
			frontmatter = existing.Frontmatter // append preserves frontmatter
			joiner := "\n"
			if strings.HasSuffix(existing.Content, "\n") {
				joiner = ""
			}
			content = existing.Content + joiner + content
		}
	}

	if err := store.UpsertMemoryPage(MemoryPage{
		Scope: scope, ScopeID: scopeID, UserID: userID, Path: path,
		Content: content, Frontmatter: frontmatter,
	}); err != nil {
		return "", fmt.Errorf("write page: %w", err)
	}
	if err := store.ReplaceMemoryLinks(scope, scopeID, userID, path, extractMemoryLinks(content)); err != nil {
		return "", fmt.Errorf("update link graph: %w", err)
	}

	action := "Wrote"
	if appendMode {
		action = "Appended to"
	}
	return fmt.Sprintf("%s %s page %q (%d bytes).", action, scope, path, len(content)), nil
}

// stringParam returns the string value of a param, or "" if missing /
// wrong type. Tool params arrive as map[string]any; this just keeps
// the call sites tidy.
func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}
