package vega

import (
	"strings"
	"sync"

	"github.com/everydev1618/govega/internal/skills"
)

// SkillMatch is a type alias for skills.SkillMatch, keeping it in the public API.
type SkillMatch = skills.SkillMatch

// SkillsPrompt wraps a base SystemPrompt and dynamically injects relevant skills.
type SkillsPrompt struct {
	base      SystemPrompt
	loader    *skills.Loader
	maxActive int
	context   string // Last message for matching
	mu        sync.RWMutex
}

// SkillsPromptOption configures a SkillsPrompt.
type SkillsPromptOption func(*SkillsPrompt)

// NewSkillsPrompt creates a new SkillsPrompt that wraps a base prompt.
func NewSkillsPrompt(base SystemPrompt, loader *skills.Loader, opts ...SkillsPromptOption) *SkillsPrompt {
	sp := &SkillsPrompt{
		base:      base,
		loader:    loader,
		maxActive: 3, // Default max skills
	}

	for _, opt := range opts {
		opt(sp)
	}

	return sp
}

// WithMaxActiveSkills sets the maximum number of skills to inject.
func WithMaxActiveSkills(n int) SkillsPromptOption {
	return func(sp *SkillsPrompt) {
		sp.maxActive = n
	}
}

// Prompt generates the system prompt with injected skills.
func (s *SkillsPrompt) Prompt() string {
	// Get base prompt
	prompt := s.base.Prompt()

	// Get context for matching
	s.mu.RLock()
	context := s.context
	s.mu.RUnlock()

	if context == "" || s.loader == nil {
		return prompt
	}

	// Find matching skills
	matches := s.loader.Match(context)

	if len(matches) == 0 {
		return prompt
	}

	// Build skills section
	var builder strings.Builder
	builder.WriteString(prompt)
	builder.WriteString("\n\n# Active Skills\n")

	for i, match := range matches {
		if i >= s.maxActive {
			break
		}

		// Load full skill content
		skill, err := s.loader.Get(match.Skill.Name)
		if err != nil {
			continue
		}

		builder.WriteString("\n## ")
		builder.WriteString(skill.Name)
		builder.WriteString("\n")
		builder.WriteString(skill.Instructions)
		builder.WriteString("\n")
	}

	return builder.String()
}

// SetContext sets the context message for skill matching.
// This should be called with the user's message before Prompt() is called.
func (s *SkillsPrompt) SetContext(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.context = message
}

// GetMatchedSkills returns the skills that would be matched for the current context.
func (s *SkillsPrompt) GetMatchedSkills() []skills.SkillMatch {
	s.mu.RLock()
	context := s.context
	s.mu.RUnlock()

	if context == "" || s.loader == nil {
		return nil
	}

	matches := s.loader.Match(context)

	if len(matches) > s.maxActive {
		matches = matches[:s.maxActive]
	}

	return matches
}

// Loader returns the underlying skills loader.
func (s *SkillsPrompt) Loader() *skills.Loader {
	return s.loader
}

// AvailableSkills returns a list of all available skills.
func (s *SkillsPrompt) AvailableSkills() []string {
	if s.loader == nil {
		return nil
	}
	return s.loader.Names()
}

// SkillsConfig configures skills for an agent.
type SkillsConfig struct {
	// Directories to load skills from.
	Directories []string

	// Include filters skills by name pattern.
	Include []string

	// Exclude filters out skills by name pattern.
	Exclude []string

	// MaxActive is the maximum number of skills to inject.
	MaxActive int
}

// SkillsPromptFromConfig creates a SkillsPrompt from configuration.
func SkillsPromptFromConfig(base SystemPrompt, config SkillsConfig) (*SkillsPrompt, error) {
	loader := skills.NewLoader(config.Directories...)
	loader.SetFilters(config.Include, config.Exclude)

	opts := []SkillsPromptOption{}
	if config.MaxActive > 0 {
		opts = append(opts, WithMaxActiveSkills(config.MaxActive))
	}

	return NewSkillsPrompt(base, loader, opts...), nil
}

// CombinedPrompt combines multiple SystemPrompts into one.
type CombinedPrompt struct {
	prompts []SystemPrompt
}

// NewCombinedPrompt creates a prompt that combines multiple prompts.
func NewCombinedPrompt(prompts ...SystemPrompt) *CombinedPrompt {
	return &CombinedPrompt{prompts: prompts}
}

// Prompt returns the combined prompt from all sources.
func (c *CombinedPrompt) Prompt() string {
	var parts []string
	for _, p := range c.prompts {
		content := p.Prompt()
		if content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// SkillSummary provides a brief summary of a skill.
type SkillSummary struct {
	Name        string
	Description string
	Tags        []string
}

// ListSkillSummaries returns summaries of all available skills.
func (s *SkillsPrompt) ListSkillSummaries() []SkillSummary {
	if s.loader == nil {
		return nil
	}

	skills := s.loader.List()
	summaries := make([]SkillSummary, len(skills))

	for i, skill := range skills {
		summaries[i] = SkillSummary{
			Name:        skill.Name,
			Description: skill.Description,
			Tags:        skill.Tags,
		}
	}

	return summaries
}
