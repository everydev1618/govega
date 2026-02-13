package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkillFile(t *testing.T) {
	content := `---
name: test-skill
description: A test skill
tags: [test, example]
triggers:
  - type: keyword
    keywords: [test, example]
  - type: pattern
    pattern: "run test"
---
# Test Skill

This is the test skill instructions.

## Usage

Use this skill for testing.
`
	// Create temp file
	tmpDir := t.TempDir()
	skillPath := filepath.Join(tmpDir, "test.skill.md")
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	skill, err := ParseFile(skillPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("Expected name 'test-skill', got '%s'", skill.Name)
	}

	if skill.Description != "A test skill" {
		t.Errorf("Expected description 'A test skill', got '%s'", skill.Description)
	}

	if len(skill.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(skill.Tags))
	}

	if len(skill.Triggers) != 2 {
		t.Errorf("Expected 2 triggers, got %d", len(skill.Triggers))
	}

	if skill.Triggers[0].Type != TriggerKeyword {
		t.Errorf("Expected keyword trigger, got %s", skill.Triggers[0].Type)
	}

	if len(skill.Triggers[0].Keywords) != 2 {
		t.Errorf("Expected 2 keywords, got %d", len(skill.Triggers[0].Keywords))
	}

	if !skill.loaded {
		t.Error("Skill should be marked as loaded")
	}

	if skill.Instructions == "" {
		t.Error("Instructions should not be empty")
	}
}

func TestParseMetadataOnly(t *testing.T) {
	content := `---
name: metadata-only
description: Only metadata is loaded
tags: [meta]
triggers:
  - type: always
---
# Full Instructions

This is a lot of text that should not be loaded...
`
	tmpDir := t.TempDir()
	skillPath := filepath.Join(tmpDir, "meta.skill.md")
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	skill, err := ParseMetadataOnly(skillPath)
	if err != nil {
		t.Fatalf("ParseMetadataOnly failed: %v", err)
	}

	if skill.Name != "metadata-only" {
		t.Errorf("Expected name 'metadata-only', got '%s'", skill.Name)
	}

	if skill.loaded {
		t.Error("Skill should not be marked as loaded")
	}

	if skill.Instructions != "" {
		t.Error("Instructions should be empty for metadata-only load")
	}

	// Now load instructions
	if err := skill.LoadInstructions(); err != nil {
		t.Fatalf("LoadInstructions failed: %v", err)
	}

	if !skill.loaded {
		t.Error("Skill should be marked as loaded after LoadInstructions")
	}

	if skill.Instructions == "" {
		t.Error("Instructions should not be empty after loading")
	}
}

func TestLoader(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skill files
	skills := []struct {
		name    string
		content string
	}{
		{
			name: "skill1.skill.md",
			content: `---
name: skill1
description: First skill
tags: [one]
triggers:
  - type: keyword
    keywords: [first, one]
---
# Skill 1
Instructions for skill 1.
`,
		},
		{
			name: "skill2.skill.md",
			content: `---
name: skill2
description: Second skill
tags: [two]
triggers:
  - type: keyword
    keywords: [second, two]
---
# Skill 2
Instructions for skill 2.
`,
		},
	}

	for _, s := range skills {
		path := filepath.Join(tmpDir, s.name)
		if err := os.WriteFile(path, []byte(s.content), 0644); err != nil {
			t.Fatalf("Failed to write skill file: %v", err)
		}
	}

	loader := NewLoader(tmpDir)
	ctx := context.Background()

	if err := loader.Load(ctx); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loader.Count() != 2 {
		t.Errorf("Expected 2 skills, got %d", loader.Count())
	}

	names := loader.Names()
	if len(names) != 2 {
		t.Errorf("Expected 2 names, got %d", len(names))
	}

	// Test Get
	skill1, err := loader.Get("skill1")
	if err != nil {
		t.Fatalf("Get skill1 failed: %v", err)
	}

	if skill1.Description != "First skill" {
		t.Errorf("Expected description 'First skill', got '%s'", skill1.Description)
	}

	if skill1.Instructions == "" {
		t.Error("Instructions should be loaded after Get")
	}
}

func TestLoaderMatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skill with keyword trigger
	content := `---
name: coding
description: Coding assistant
triggers:
  - type: keyword
    keywords: [code, programming, debug]
---
# Coding Skill
Help with coding tasks.
`
	path := filepath.Join(tmpDir, "coding.skill.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	loader := NewLoader(tmpDir)
	ctx := context.Background()
	loader.Load(ctx)

	// Test matching
	matches := loader.Match("Can you help me debug this code?")
	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}

	if len(matches) > 0 && matches[0].Skill.Name != "coding" {
		t.Errorf("Expected skill 'coding', got '%s'", matches[0].Skill.Name)
	}

	// Test no match
	matches = loader.Match("What's the weather like?")
	if len(matches) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matches))
	}
}

func TestLoaderFilters(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple skills
	skills := []string{"skill-a", "skill-b", "other-skill"}
	for _, name := range skills {
		content := `---
name: ` + name + `
description: Test skill
triggers:
  - type: always
---
# ` + name + `
`
		path := filepath.Join(tmpDir, name+".skill.md")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write skill file: %v", err)
		}
	}

	// Test include filter
	loader := NewLoader(tmpDir)
	loader.SetFilters([]string{"skill-*"}, nil)
	ctx := context.Background()
	loader.Load(ctx)

	if loader.Count() != 2 {
		t.Errorf("Expected 2 skills with include filter, got %d", loader.Count())
	}

	// Test exclude filter
	loader = NewLoader(tmpDir)
	loader.SetFilters(nil, []string{"other-*"})
	loader.Load(ctx)

	if loader.Count() != 2 {
		t.Errorf("Expected 2 skills with exclude filter, got %d", loader.Count())
	}
}

func TestMatcher(t *testing.T) {
	skills := map[string]*Skill{
		"coding": {
			Name: "coding",
			Triggers: []TriggerDef{
				{Type: TriggerKeyword, Keywords: []string{"code", "programming"}},
			},
		},
		"writing": {
			Name: "writing",
			Triggers: []TriggerDef{
				{Type: TriggerKeyword, Keywords: []string{"write", "essay", "document"}},
			},
		},
		"always": {
			Name: "always",
			Triggers: []TriggerDef{
				{Type: TriggerAlways},
			},
		},
	}

	tests := []struct {
		message       string
		expectedCount int
		topSkill      string
	}{
		{"Help me with programming", 2, "always"}, // "always" has score 1.0, "coding" lower
		{"Write an essay", 2, "always"},
		{"Random message", 1, "always"},
		{"Code and write", 3, "always"}, // All match
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			matches := matchSkills(skills, tt.message)
			if len(matches) != tt.expectedCount {
				t.Errorf("Expected %d matches for '%s', got %d",
					tt.expectedCount, tt.message, len(matches))
			}
		})
	}
}

func TestDeriveNameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/SKILL.md", "skill"},
		{"/path/to/coding.skill.md", "coding.skill"},
		{"/path/to/coding-assistant.md", "coding-assistant"},
		{"SKILL.coding.md", "coding"},
		{"/path/to/SKILL.review.md", "review"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := deriveNameFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("deriveNameFromPath(%s) = %s, want %s", tt.path, result, tt.expected)
			}
		})
	}
}
