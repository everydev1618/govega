package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Loader manages skill loading and discovery.
type Loader struct {
	directories []string
	skills      map[string]*Skill
	include     []string
	exclude     []string
	mu          sync.RWMutex
}

// NewLoader creates a new skill loader for the given directories.
func NewLoader(dirs ...string) *Loader {
	// Expand home directory
	expandedDirs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if strings.HasPrefix(dir, "~/") {
			home, err := os.UserHomeDir()
			if err == nil {
				dir = filepath.Join(home, dir[2:])
			}
		}
		expandedDirs = append(expandedDirs, dir)
	}

	return &Loader{
		directories: expandedDirs,
		skills:      make(map[string]*Skill),
	}
}

// WithConfig creates a loader from configuration.
func WithConfig(config LoaderConfig) *Loader {
	l := NewLoader(config.Directories...)
	l.include = config.Include
	l.exclude = config.Exclude
	return l
}

// Load scans directories and loads skill metadata.
func (l *Loader) Load(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, dir := range l.directories {
		if err := l.scanDirectory(ctx, dir); err != nil {
			// Continue on error, log it instead
			continue
		}
	}

	return nil
}

// scanDirectory scans a directory for skill files.
func (l *Loader) scanDirectory(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist, not an error
		}
		return fmt.Errorf("read directory: %w", err)
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if entry.IsDir() {
			// Check for SKILL.md in subdirectory
			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillPath); err == nil {
				if err := l.loadSkillFile(skillPath); err != nil {
					continue
				}
			}
			continue
		}

		name := entry.Name()
		lowerName := strings.ToLower(name)

		// Match skill files
		if lowerName == "skill.md" || strings.HasSuffix(lowerName, ".skill.md") {
			skillPath := filepath.Join(dir, name)
			if err := l.loadSkillFile(skillPath); err != nil {
				continue
			}
		}
	}

	return nil
}

// loadSkillFile loads a skill file and adds it to the loader.
func (l *Loader) loadSkillFile(path string) error {
	skill, err := ParseMetadataOnly(path)
	if err != nil {
		return err
	}

	// Apply include/exclude filters
	if !l.shouldInclude(skill.Name) {
		return nil
	}

	l.skills[skill.Name] = skill
	return nil
}

// shouldInclude checks if a skill should be included based on filters.
func (l *Loader) shouldInclude(name string) bool {
	// Check exclude first
	for _, pattern := range l.exclude {
		if matchPattern(name, pattern) {
			return false
		}
	}

	// If no include filters, include everything
	if len(l.include) == 0 {
		return true
	}

	// Check include
	for _, pattern := range l.include {
		if matchPattern(name, pattern) {
			return true
		}
	}

	return false
}

// matchPattern checks if a name matches a pattern.
// Supports * wildcard.
func matchPattern(name, pattern string) bool {
	if pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(name, prefix)
	}

	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(name, suffix)
	}

	return name == pattern
}

// Get retrieves a skill by name, loading full instructions if needed.
func (l *Loader) Get(name string) (*Skill, error) {
	l.mu.RLock()
	skill, ok := l.skills[name]
	l.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("skill not found: %s", name)
	}

	// Load full instructions if not already loaded
	if !skill.loaded {
		if err := skill.LoadInstructions(); err != nil {
			return nil, fmt.Errorf("load instructions: %w", err)
		}
	}

	return skill, nil
}

// List returns all loaded skills (metadata only).
func (l *Loader) List() []*Skill {
	l.mu.RLock()
	defer l.mu.RUnlock()

	skills := make([]*Skill, 0, len(l.skills))
	for _, skill := range l.skills {
		skills = append(skills, skill)
	}

	return skills
}

// Names returns the names of all loaded skills.
func (l *Loader) Names() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	names := make([]string, 0, len(l.skills))
	for name := range l.skills {
		names = append(names, name)
	}

	return names
}

// Match finds skills that match the given message.
func (l *Loader) Match(message string) []SkillMatch {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return matchSkills(l.skills, message)
}

// Count returns the number of loaded skills.
func (l *Loader) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.skills)
}

// Reload clears and reloads all skills.
func (l *Loader) Reload(ctx context.Context) error {
	l.mu.Lock()
	l.skills = make(map[string]*Skill)
	l.mu.Unlock()

	return l.Load(ctx)
}

// SetFilters updates the include/exclude filters.
func (l *Loader) SetFilters(include, exclude []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.include = include
	l.exclude = exclude
}
