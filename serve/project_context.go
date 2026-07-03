package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
)

// maxTreeDepth limits how deep we recurse when building the file tree.
const maxTreeDepth = 5

// maxTreeEntries caps the total number of entries to keep the context concise.
const maxTreeEntries = 200

// buildProjectContext generates a file tree summary for the active project
// that gets injected into the agent's system prompt so agents are aware
// of what files exist in the project.
func buildProjectContext(project string) string {
	if project == "" {
		return ""
	}

	root := filepath.Join(vega.WorkspacePath(), project)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Project Files\n\n")
	b.WriteString(fmt.Sprintf("Active project: `%s`\n", project))
	b.WriteString("```\n")

	count := 0
	buildTree(&b, root, "", &count)

	if count >= maxTreeEntries {
		b.WriteString("  ... (truncated)\n")
	}
	b.WriteString("```\n")
	b.WriteString("\nUse `list_files` and `read_file` to explore further.")

	return b.String()
}

// buildTree recursively writes a tree representation of the directory.
func buildTree(b *strings.Builder, dir string, prefix string, count *int) {
	if *count >= maxTreeEntries {
		return
	}

	depth := strings.Count(prefix, "│") + strings.Count(prefix, " ")
	if depth/4 >= maxTreeDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Sort: directories first, then files, both alphabetical.
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})

	// Filter out hidden files/dirs and common noise.
	var visible []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if skipDir(name) && e.IsDir() {
			continue
		}
		visible = append(visible, e)
	}

	for i, e := range visible {
		if *count >= maxTreeEntries {
			return
		}
		*count++

		isLast := i == len(visible)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(prefix + connector + name + "\n")

		if e.IsDir() {
			childPrefix := prefix + "│   "
			if isLast {
				childPrefix = prefix + "    "
			}
			buildTree(b, filepath.Join(dir, e.Name()), childPrefix, count)
		}
	}
}

// skipDir returns true for directories we should skip in the tree.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "__pycache__", ".git", ".venv", "venv",
		"dist", "build", ".next", ".cache", "vendor":
		return true
	}
	return false
}

// buildCompanyContext generates a company identity section for agent system prompts.
func buildCompanyContext(company *dsl.Company) string {
	if company == nil || company.Name == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Organization\n\n")
	b.WriteString(fmt.Sprintf("You work for **%s**. This is the company/organization name — do NOT assume the person chatting with you is the company owner or namesake.", company.Name))
	if company.Description != "" {
		b.WriteString(" " + company.Description)
	}
	if company.Location != "" {
		b.WriteString(fmt.Sprintf("\nBased in: %s", company.Location))
	}

	return b.String()
}

// surface identifies which chat surface a turn is being served on. The
// orchestrator narrates internal Vega actions differently depending on
// whether the user can actually see its web workspace.
type surface string

const (
	surfaceWeb      surface = "web"
	surfaceDiscord  surface = "discord"
	surfaceTelegram surface = "telegram"
)

// surfaceContext returns a per-surface descriptor paragraph that tells the
// agent where the user is and, crucially, whether they can see the internal
// Vega workspace (channels, tasks, agent roster). Returns "" for an unknown
// or empty surface so callers can pass it straight into buildExtraSystem.
func surfaceContext(s surface) string {
	workspace := os.Getenv("PUBLIC_URL")
	switch s {
	case surfaceWeb:
		return "The user is in the Vega web dashboard. They can see your channels, tasks, agent roster, and process tree directly."
	case surfaceDiscord:
		return chatSurfaceNote("Discord", workspace)
	case surfaceTelegram:
		return chatSurfaceNote("Telegram", workspace)
	default:
		return ""
	}
}

// chatSurfaceNote builds the shared descriptor for external chat surfaces
// (Discord, Telegram) where the user cannot see the Vega web workspace.
// workspace is the PUBLIC_URL, if set; it is woven in so the agent can point
// the user at where its channel/task work actually lives.
func chatSurfaceNote(name, workspace string) string {
	loc := "your Vega workspace"
	if workspace != "" {
		loc = "your Vega workspace (at " + workspace + ")"
	}
	return "You are talking to the user over " + name + " as a chat bot. They are NOT looking at your Vega web workspace — they cannot see your internal Vega channels, tasks, or agent roster unless you describe them in your reply. Your create_channel / post_to_channel / create_task tools act on " + loc + ", not on " + name + "; when you use them, say the result lives in your Vega workspace — do NOT imply a channel or board exists in this chat. Keep replies concise; " + name + " splits long messages."
}

// buildExtraSystem combines surface, memory text, project context, and
// company context into a single extra system prompt string. The surface
// descriptor leads the block so it is the most prominent guidance.
func buildExtraSystem(surfaceText, memText, projectContext, companyContext string) string {
	parts := make([]string, 0, 4)
	if surfaceText != "" {
		parts = append(parts, surfaceText)
	}
	if companyContext != "" {
		parts = append(parts, companyContext)
	}
	if memText != "" {
		parts = append(parts, memText)
	}
	if projectContext != "" {
		parts = append(parts, projectContext)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// composeExtraSystem builds the per-turn extra system prompt by combining
// the standard memory/project/company blocks with any content returned by
// the configured ExtraSystemProvider. The provider is a per-server hook —
// see Config.ExtraSystemProvider — used by embedding products to inject
// session-specific guidance (e.g. a per-book writing norm) that the
// agent's static configuration can't express. Returns "" when there is
// nothing to set.
func (s *Server) composeExtraSystem(ctx context.Context, agentName, baseAgent, userID, surfaceText, memText, projectContext, companyContext string) string {
	base := buildExtraSystem(surfaceText, memText, projectContext, companyContext)
	if s == nil || s.cfg.ExtraSystemProvider == nil {
		return base
	}
	extra := s.cfg.ExtraSystemProvider(ctx, agentName, baseAgent, userID)
	if extra == "" {
		return base
	}
	if base == "" {
		return extra
	}
	return base + "\n\n" + extra
}
