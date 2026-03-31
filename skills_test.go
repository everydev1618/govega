package vega

import (
	"testing"
)

func TestCombinedPrompt(t *testing.T) {
	p1 := StaticPrompt("You are helpful.")
	p2 := StaticPrompt("You are concise.")

	combined := NewCombinedPrompt(p1, p2)
	result := combined.Prompt()

	if result != "You are helpful.\n\nYou are concise." {
		t.Errorf("CombinedPrompt.Prompt() = %q", result)
	}
}

func TestCombinedPrompt_Empty(t *testing.T) {
	combined := NewCombinedPrompt()
	result := combined.Prompt()

	if result != "" {
		t.Errorf("Empty CombinedPrompt.Prompt() = %q, want empty", result)
	}
}

func TestCombinedPrompt_SkipsEmpty(t *testing.T) {
	p1 := StaticPrompt("You are helpful.")
	p2 := StaticPrompt("")
	p3 := StaticPrompt("Be safe.")

	combined := NewCombinedPrompt(p1, p2, p3)
	result := combined.Prompt()

	if result != "You are helpful.\n\nBe safe." {
		t.Errorf("CombinedPrompt should skip empty prompts, got %q", result)
	}
}

func TestSkillsPrompt_NoLoader(t *testing.T) {
	base := StaticPrompt("Base prompt.")
	sp := NewSkillsPrompt(base, nil)

	result := sp.Prompt()
	if result != "Base prompt." {
		t.Errorf("SkillsPrompt without loader should return base, got %q", result)
	}
}

func TestSkillsPrompt_NoContext(t *testing.T) {
	base := StaticPrompt("Base prompt.")
	sp := NewSkillsPrompt(base, nil)

	// No context set
	result := sp.Prompt()
	if result != "Base prompt." {
		t.Errorf("SkillsPrompt without context should return base, got %q", result)
	}
}

func TestSkillsPrompt_SetContext(t *testing.T) {
	base := StaticPrompt("Base prompt.")
	sp := NewSkillsPrompt(base, nil)

	sp.SetContext("test context")

	sp.mu.RLock()
	ctx := sp.context
	sp.mu.RUnlock()

	if ctx != "test context" {
		t.Errorf("context = %q, want %q", ctx, "test context")
	}
}

func TestSkillsPrompt_GetMatchedSkills_NoLoader(t *testing.T) {
	sp := NewSkillsPrompt(StaticPrompt(""), nil)
	matches := sp.GetMatchedSkills()
	if matches != nil {
		t.Error("GetMatchedSkills with no loader should return nil")
	}
}

func TestSkillsPrompt_GetMatchedSkills_NoContext(t *testing.T) {
	sp := NewSkillsPrompt(StaticPrompt(""), nil)
	matches := sp.GetMatchedSkills()
	if matches != nil {
		t.Error("GetMatchedSkills with no context should return nil")
	}
}

func TestSkillsPrompt_AvailableSkills_NoLoader(t *testing.T) {
	sp := NewSkillsPrompt(StaticPrompt(""), nil)
	skills := sp.AvailableSkills()
	if skills != nil {
		t.Error("AvailableSkills with no loader should return nil")
	}
}

func TestSkillsPrompt_ListSkillSummaries_NoLoader(t *testing.T) {
	sp := NewSkillsPrompt(StaticPrompt(""), nil)
	summaries := sp.ListSkillSummaries()
	if summaries != nil {
		t.Error("ListSkillSummaries with no loader should return nil")
	}
}

func TestWithMaxActiveSkills(t *testing.T) {
	sp := NewSkillsPrompt(StaticPrompt(""), nil, WithMaxActiveSkills(5))
	if sp.maxActive != 5 {
		t.Errorf("maxActive = %d, want 5", sp.maxActive)
	}
}

func TestSkillsPrompt_Loader(t *testing.T) {
	sp := NewSkillsPrompt(StaticPrompt(""), nil)
	if sp.Loader() != nil {
		t.Error("Loader() should return nil when no loader set")
	}
}

func TestSkillSummary(t *testing.T) {
	s := SkillSummary{
		Name:        "code_review",
		Description: "Review code for quality",
		Tags:        []string{"code", "review"},
	}

	if s.Name != "code_review" {
		t.Errorf("Name = %q, want %q", s.Name, "code_review")
	}
	if len(s.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(s.Tags))
	}
}

func TestSkillsConfig(t *testing.T) {
	config := SkillsConfig{
		Directories: []string{"/skills"},
		Include:     []string{"*"},
		Exclude:     []string{"internal_*"},
		MaxActive:   3,
	}

	if len(config.Directories) != 1 {
		t.Error("Directories should have 1 entry")
	}
	if config.MaxActive != 3 {
		t.Errorf("MaxActive = %d, want 3", config.MaxActive)
	}
}
