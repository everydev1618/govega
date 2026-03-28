package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/everydev1618/govega/llm"
)

func generateCmd(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	format := fs.String("format", "vega", "Output format: vega (yaml), claude-skill (markdown)")
	output := fs.String("output", "", "Output file path (default: stdout)")
	from := fs.String("from", "", "Base persona from population (e.g., devops-lead, architect)")
	skills := fs.String("skills", "", "Comma-separated skills to include (e.g., aws-devops,terraform)")
	populationDir := fs.String("population-dir", "", "Path to vega-population repo")
	model := fs.String("model", "", "Model to use for generation (default: claude-sonnet-4-20250514)")
	list := fs.String("list", "", "List available components: personas, skills, profiles, or all")

	fs.Usage = func() {
		fmt.Println(`Usage: vega generate <description> [options]

Generate an AI agent from a natural language description.

The generate command creates agent definitions by either composing from the
vega-population library or generating from scratch using an LLM.

Output Formats:
  vega          A .vega.yaml agent definition (default)
  claude-skill  A markdown file for use as a Claude Code custom slash command

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  # List available components
  vega generate --list all
  vega generate --list personas
  vega generate --list skills

  # Generate from description
  vega generate "world class devops agent for AWS and Kubernetes"
  vega generate "security auditor for PR reviews" --format claude-skill

  # Compose from population
  vega generate --from devops-lead --skills aws-devops,kubernetes-ops,terraform
  vega generate --from architect --format claude-skill -o .claude/commands/architect.md

  # Generate and save
  vega generate "full-stack engineer" -o fullstack.vega.yaml
  vega generate "database expert" --format claude-skill -o .claude/commands/dba.md`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	// Handle --list before requiring API key
	if *list != "" {
		popDir := resolvePopulationDir(*populationDir)
		if popDir == "" {
			fmt.Fprintln(os.Stderr, "Error: vega-population not found. Use --population-dir to specify its location.")
			os.Exit(1)
		}
		listPopulation(popDir, *list)
		return
	}

	requireAPIKey()

	// Collect the description from remaining args
	description := strings.TrimSpace(strings.Join(fs.Args(), " "))

	if description == "" && *from == "" {
		fmt.Fprintln(os.Stderr, "Error: provide a description or use --from to specify a base persona")
		fs.Usage()
		os.Exit(1)
	}

	// Resolve population directory
	popDir := resolvePopulationDir(*populationDir)

	// Load population context if available
	populationContext := ""
	if popDir != "" {
		populationContext = loadPopulationContext(popDir)
	}

	// If --from is specified, try to load and compose directly
	var personaContent, skillsContent string
	if *from != "" && popDir != "" {
		personaContent = loadPersona(popDir, *from)
		if personaContent == "" {
			fmt.Fprintf(os.Stderr, "Warning: persona '%s' not found in %s\n", *from, popDir)
		}
	}
	if *skills != "" && popDir != "" {
		for _, skill := range strings.Split(*skills, ",") {
			skill = strings.TrimSpace(skill)
			content := loadSkill(popDir, skill)
			if content != "" {
				skillsContent += "\n---\n" + content
			} else {
				fmt.Fprintf(os.Stderr, "Warning: skill '%s' not found in %s\n", skill, popDir)
			}
		}
	}

	// Build the generation prompt
	genModel := "claude-sonnet-4-20250514"
	if *model != "" {
		genModel = *model
	}

	result, err := generateAgent(description, *format, personaContent, skillsContent, populationContext, genModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating agent: %v\n", err)
		os.Exit(1)
	}

	// Output
	if *output != "" {
		// Ensure parent directory exists
		dir := filepath.Dir(*output)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			os.Exit(1)
		}
		if err := os.WriteFile(*output, []byte(result), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing to %s: %v\n", *output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Generated: %s\n", *output)
	} else {
		fmt.Print(result)
	}
}

// listPopulation prints available components from the vega-population directory.
func listPopulation(popDir, kind string) {
	kind = strings.ToLower(kind)

	showPersonas := kind == "all" || kind == "personas"
	showSkills := kind == "all" || kind == "skills"
	showProfiles := kind == "all" || kind == "profiles"

	if !showPersonas && !showSkills && !showProfiles {
		fmt.Fprintf(os.Stderr, "Unknown list type: %s (use personas, skills, profiles, or all)\n", kind)
		os.Exit(1)
	}

	if showPersonas {
		fmt.Println("Personas (--from):")
		entries := listDir(filepath.Join(popDir, "personas"))
		for _, name := range entries {
			desc := loadDescription(filepath.Join(popDir, "personas", name, "vega.yaml"))
			fmt.Printf("  %-22s %s\n", name, desc)
		}
		fmt.Println()
	}

	if showSkills {
		fmt.Println("Skills (--skills):")
		entries := listDir(filepath.Join(popDir, "skills"))
		for _, name := range entries {
			desc := loadDescription(filepath.Join(popDir, "skills", name, "vega.yaml"))
			fmt.Printf("  %-22s %s\n", name, desc)
		}
		fmt.Println()
	}

	if showProfiles {
		fmt.Println("Profiles (--from):")
		entries := listDir(filepath.Join(popDir, "profiles"))
		for _, name := range entries {
			desc := loadDescription(filepath.Join(popDir, "profiles", name, "vega.yaml"))
			fmt.Printf("  %-22s %s\n", name, desc)
		}
		fmt.Println()
	}
}

// listDir returns sorted subdirectory names, excluding index files.
func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// loadDescription extracts the description field from a vega.yaml file.
func loadDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			desc := strings.TrimPrefix(line, "description:")
			desc = strings.TrimSpace(desc)
			return desc
		}
	}
	return ""
}

// resolvePopulationDir finds the vega-population directory.
func resolvePopulationDir(explicit string) string {
	if explicit != "" {
		if info, err := os.Stat(explicit); err == nil && info.IsDir() {
			return explicit
		}
		return ""
	}

	// Check sibling directory (../vega-population relative to the binary's likely repo)
	candidates := []string{}

	// Check relative to CWD
	cwd, _ := os.Getwd()
	candidates = append(candidates,
		filepath.Join(cwd, "..", "vega-population"),
		filepath.Join(cwd, "vega-population"),
	)

	// Check ~/.vega/population
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".vega", "population"))
	}

	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			// Verify it looks like a population dir
			if _, err := os.Stat(filepath.Join(dir, "personas")); err == nil {
				return dir
			}
		}
	}
	return ""
}

// loadPopulationContext loads the index files to give the LLM awareness of available personas/skills.
func loadPopulationContext(popDir string) string {
	var parts []string

	personaIndex := filepath.Join(popDir, "personas", "index.yaml")
	if data, err := os.ReadFile(personaIndex); err == nil {
		parts = append(parts, "## Available Personas\n```yaml\n"+string(data)+"\n```")
	}

	skillIndex := filepath.Join(popDir, "skills", "index.yaml")
	if data, err := os.ReadFile(skillIndex); err == nil {
		parts = append(parts, "## Available Skills\n```yaml\n"+string(data)+"\n```")
	}

	profileIndex := filepath.Join(popDir, "profiles", "index.yaml")
	if data, err := os.ReadFile(profileIndex); err == nil {
		parts = append(parts, "## Available Profiles\n```yaml\n"+string(data)+"\n```")
	}

	if len(parts) == 0 {
		return ""
	}
	return "# Vega Population Library\n\n" + strings.Join(parts, "\n\n")
}

func loadPersona(popDir, name string) string {
	path := filepath.Join(popDir, "personas", name, "vega.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func loadSkill(popDir, name string) string {
	path := filepath.Join(popDir, "skills", name, "vega.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func generateAgent(description, format, personaContent, skillsContent, populationContext, model string) (string, error) {
	client := llm.NewAnthropic(llm.WithModel(model))

	systemPrompt := buildGenerationSystemPrompt(format, populationContext)
	userPrompt := buildGenerationUserPrompt(description, format, personaContent, skillsContent)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := client.Generate(ctx, messages, nil)
	if err != nil {
		return "", err
	}

	// Extract the content between code fences if present
	content := resp.Content
	content = extractCodeBlock(content)

	return content, nil
}

func buildGenerationSystemPrompt(format, populationContext string) string {
	var sb strings.Builder

	sb.WriteString(`You are an expert AI agent designer. You create high-quality agent definitions that are specific, actionable, and deeply knowledgeable in their domain.

Your output must be ONLY the generated file content — no explanations, no preamble, no commentary. If you use a code fence, use exactly one.

`)

	if format == "claude-skill" {
		sb.WriteString(`## Output Format: Claude Code Custom Slash Command

Generate a markdown file that works as a Claude Code custom slash command.
The file should contain a system prompt that defines the agent's identity, expertise, approach, and communication style.

Structure:
- Start with a clear identity statement
- Define core expertise areas with specific, actionable knowledge
- Include decision frameworks and approaches for common scenarios
- Add communication style guidelines
- Include "red flags" or anti-patterns the agent watches for
- Keep it practical — every section should help the agent make better decisions

The markdown should be self-contained — it will be injected as additional context when the user invokes the slash command.
`)
	} else {
		sb.WriteString(`## Output Format: Vega YAML

Generate a .vega.yaml agent definition.

Structure:
` + "```yaml" + `
kind: persona  # or profile if combining persona + skills
name: agent-name
version: 1.0.0
description: One-line description
author: vega-generate
tags: [relevant, tags]

# If kind: profile, include:
# persona: base-persona-name
# skills:
#   - skill-name

# For tools (shell commands the agent can run):
# tools:
#   - name: tool_name
#     description: What it does
#     read_only: true  # or dangerous: true for write operations
#     params:
#       param_name:
#         type: string
#         required: true
#         description: Param description
#     run: shell command with {{ .param_name }} templates

system_prompt: |
  Detailed system prompt here...
` + "```" + `

The system_prompt should be comprehensive — identity, expertise areas, decision frameworks, communication style, anti-patterns to watch for.
`)
	}

	if populationContext != "" {
		sb.WriteString("\n## Available Population Components\n\n")
		sb.WriteString("When the user's request matches existing personas or skills, reference them by name rather than recreating them. Use `kind: profile` to compose existing pieces.\n\n")
		sb.WriteString(populationContext)
	}

	return sb.String()
}

func buildGenerationUserPrompt(description, format, personaContent, skillsContent string) string {
	var sb strings.Builder

	if description != "" {
		sb.WriteString("Generate an agent for: " + description + "\n\n")
	}

	if personaContent != "" {
		sb.WriteString("## Base Persona\n\nBuild on this existing persona:\n```yaml\n" + personaContent + "\n```\n\n")
	}

	if skillsContent != "" {
		sb.WriteString("## Skills to Include\n\nIncorporate these skills:\n```yaml\n" + skillsContent + "\n```\n\n")
	}

	if personaContent != "" && description == "" {
		if format == "claude-skill" {
			sb.WriteString("Convert the above persona (and any included skills) into a Claude Code custom slash command markdown file.\n")
		} else {
			sb.WriteString("Compose the above into a complete vega.yaml agent definition.\n")
		}
	}

	return sb.String()
}

// extractCodeBlock pulls content from a single code fence, or returns as-is if no fence found.
func extractCodeBlock(content string) string {
	lines := strings.Split(content, "\n")
	var inBlock bool
	var extracted []string

	for _, line := range lines {
		if !inBlock && (strings.HasPrefix(line, "```yaml") || strings.HasPrefix(line, "```markdown") || strings.HasPrefix(line, "```md") || strings.HasPrefix(line, "```")) {
			inBlock = true
			continue
		}
		if inBlock && strings.HasPrefix(line, "```") {
			// Return what we've collected
			return strings.Join(extracted, "\n") + "\n"
		}
		if inBlock {
			extracted = append(extracted, line)
		}
	}

	// No code fence found or unclosed — return original
	return content
}
