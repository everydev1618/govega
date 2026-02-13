package skills

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFile parses a SKILL.md file.
// The file format is:
//
//	---
//	name: skill-name
//	description: Brief description
//	tags: [tag1, tag2]
//	triggers:
//	  - type: keyword
//	    keywords: [word1, word2]
//	---
//	# Skill Title
//	Instructions markdown here...
func ParseFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return Parse(data, path)
}

// Parse parses skill content from bytes.
func Parse(data []byte, path string) (*Skill, error) {
	frontmatter, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	skill := &Skill{
		path: path,
	}

	// Parse frontmatter
	if len(frontmatter) > 0 {
		if err := yaml.Unmarshal(frontmatter, skill); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}

	// Store instructions
	skill.Instructions = strings.TrimSpace(string(body))
	skill.loaded = true

	// Validate
	if skill.Name == "" {
		// Derive name from filename
		skill.Name = deriveNameFromPath(path)
	}

	return skill, nil
}

// ParseMetadataOnly parses only the frontmatter metadata without loading instructions.
func ParseMetadataOnly(path string) (*Skill, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Read until we find the frontmatter boundaries
	var frontmatter bytes.Buffer
	inFrontmatter := false
	lineCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		if lineCount == 1 && line == "---" {
			inFrontmatter = true
			continue
		}

		if inFrontmatter && line == "---" {
			// End of frontmatter
			break
		}

		if inFrontmatter {
			frontmatter.WriteString(line)
			frontmatter.WriteByte('\n')
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	skill := &Skill{
		path:   path,
		loaded: false,
	}

	if frontmatter.Len() > 0 {
		if err := yaml.Unmarshal(frontmatter.Bytes(), skill); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}

	if skill.Name == "" {
		skill.Name = deriveNameFromPath(path)
	}

	return skill, nil
}

// splitFrontmatter splits content into frontmatter and body.
func splitFrontmatter(data []byte) (frontmatter, body []byte, err error) {
	// Check for frontmatter delimiter
	if !bytes.HasPrefix(data, []byte("---\n")) && !bytes.HasPrefix(data, []byte("---\r\n")) {
		// No frontmatter, entire content is body
		return nil, data, nil
	}

	// Find closing delimiter
	rest := data[4:] // Skip opening "---\n"
	idx := bytes.Index(rest, []byte("\n---\n"))
	if idx == -1 {
		idx = bytes.Index(rest, []byte("\n---\r\n"))
	}
	if idx == -1 {
		idx = bytes.Index(rest, []byte("\r\n---\r\n"))
	}

	if idx == -1 {
		// No closing delimiter, treat as no frontmatter
		return nil, data, nil
	}

	frontmatter = rest[:idx]
	body = rest[idx+5:] // Skip "\n---\n"

	return frontmatter, body, nil
}

// deriveNameFromPath extracts a skill name from a file path.
func deriveNameFromPath(path string) string {
	// Get filename
	name := path
	if idx := strings.LastIndex(path, "/"); idx != -1 {
		name = path[idx+1:]
	}
	if idx := strings.LastIndex(name, "\\"); idx != -1 {
		name = name[idx+1:]
	}

	// Remove extension
	if strings.HasSuffix(strings.ToLower(name), ".md") {
		name = name[:len(name)-3]
	}

	// Remove SKILL. prefix if present
	if strings.HasPrefix(strings.ToUpper(name), "SKILL.") {
		name = name[6:]
	}

	return strings.ToLower(name)
}

// LoadInstructions loads the full instructions for a skill.
func (s *Skill) LoadInstructions() error {
	if s.loaded {
		return nil
	}

	if s.path == "" {
		return fmt.Errorf("skill has no path")
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	_, body, err := splitFrontmatter(data)
	if err != nil {
		return err
	}

	s.Instructions = strings.TrimSpace(string(body))
	s.loaded = true

	return nil
}
