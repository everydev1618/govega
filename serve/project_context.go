package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	vega "github.com/everydev1618/govega"
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

// buildExtraSystem combines memory text and project context into a single
// extra system prompt string.
func buildExtraSystem(memText, projectContext string) string {
	parts := make([]string, 0, 2)
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
