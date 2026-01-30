// Package skills provides skill loading and matching for agents.
package skills

import "regexp"

// TriggerType identifies the type of skill trigger.
type TriggerType string

const (
	// TriggerKeyword matches specific keywords in the message.
	TriggerKeyword TriggerType = "keyword"
	// TriggerPattern matches a regex pattern in the message.
	TriggerPattern TriggerType = "pattern"
	// TriggerAlways always includes the skill.
	TriggerAlways TriggerType = "always"
)

// Skill represents a loadable skill definition.
type Skill struct {
	// Name is the unique identifier for the skill.
	Name string `yaml:"name"`

	// Description briefly describes what the skill does.
	Description string `yaml:"description"`

	// Tags are categories for the skill.
	Tags []string `yaml:"tags"`

	// Triggers define when the skill should be activated.
	Triggers []TriggerDef `yaml:"triggers"`

	// Instructions is the markdown body (lazy loaded).
	Instructions string `yaml:"-"`

	// path is the file path (internal).
	path string

	// loaded indicates if Instructions has been loaded.
	loaded bool

	// compiledPatterns caches compiled regex patterns.
	compiledPatterns []*regexp.Regexp
}

// TriggerDef defines a skill trigger.
type TriggerDef struct {
	// Type is the trigger type.
	Type TriggerType `yaml:"type"`

	// Keywords are words/phrases that trigger the skill (for keyword type).
	Keywords []string `yaml:"keywords,omitempty"`

	// Pattern is a regex pattern (for pattern type).
	Pattern string `yaml:"pattern,omitempty"`
}

// SkillMatch represents a matched skill with its relevance score.
type SkillMatch struct {
	// Skill is the matched skill.
	Skill *Skill

	// Score is the relevance score (0.0-1.0).
	Score float64

	// Reason explains why the skill matched.
	Reason string
}

// SkillMetadata contains only the metadata portion of a skill.
// Used for listing skills without loading full instructions.
type SkillMetadata struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Tags        []string     `yaml:"tags"`
	Triggers    []TriggerDef `yaml:"triggers"`
}

// LoaderConfig configures the skill loader.
type LoaderConfig struct {
	// Directories to scan for skills.
	Directories []string

	// Include filters skills by name pattern.
	Include []string

	// Exclude filters out skills by name pattern.
	Exclude []string

	// WatchForChanges enables file watching.
	WatchForChanges bool
}
