package skills

import (
	"regexp"
	"sort"
	"strings"
)

// matchSkills finds skills that match the given message.
func matchSkills(skills map[string]*Skill, message string) []SkillMatch {
	var matches []SkillMatch
	messageLower := strings.ToLower(message)

	for _, skill := range skills {
		match := matchSkill(skill, message, messageLower)
		if match != nil {
			matches = append(matches, *match)
		}
	}

	// Sort by score descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	return matches
}

// matchSkill checks if a single skill matches the message.
func matchSkill(skill *Skill, message, messageLower string) *SkillMatch {
	var bestScore float64
	var bestReason string

	for _, trigger := range skill.Triggers {
		score, reason := matchTrigger(skill, trigger, message, messageLower)
		if score > bestScore {
			bestScore = score
			bestReason = reason
		}
	}

	if bestScore > 0 {
		return &SkillMatch{
			Skill:  skill,
			Score:  bestScore,
			Reason: bestReason,
		}
	}

	return nil
}

// matchTrigger checks if a trigger matches the message.
func matchTrigger(skill *Skill, trigger TriggerDef, message, messageLower string) (float64, string) {
	switch trigger.Type {
	case TriggerAlways:
		return 1.0, "always included"

	case TriggerKeyword:
		return matchKeywords(trigger.Keywords, messageLower)

	case TriggerPattern:
		return matchPattern_(skill, trigger.Pattern, message)

	default:
		return 0, ""
	}
}

// matchKeywords checks if any keywords are present in the message.
func matchKeywords(keywords []string, messageLower string) (float64, string) {
	matchedCount := 0
	var matchedKeywords []string

	for _, keyword := range keywords {
		keywordLower := strings.ToLower(keyword)

		// Check for word boundary match
		if containsWord(messageLower, keywordLower) {
			matchedCount++
			matchedKeywords = append(matchedKeywords, keyword)
		}
	}

	if matchedCount == 0 {
		return 0, ""
	}

	// Score based on number of matches relative to total keywords
	score := float64(matchedCount) / float64(len(keywords))

	// Boost score if multiple keywords match
	if matchedCount > 1 {
		score = score * 1.2
		if score > 1.0 {
			score = 1.0
		}
	}

	reason := "matched keywords: " + strings.Join(matchedKeywords, ", ")
	return score, reason
}

// containsWord checks if the message contains the word with word boundaries.
func containsWord(message, word string) bool {
	// Simple contains check for now
	// Could be enhanced with word boundary detection
	if strings.Contains(message, word) {
		return true
	}

	// Check for variations (e.g., plural, past tense)
	variations := getWordVariations(word)
	for _, v := range variations {
		if strings.Contains(message, v) {
			return true
		}
	}

	return false
}

// getWordVariations returns common variations of a word.
func getWordVariations(word string) []string {
	variations := []string{}

	// Plural (simple)
	if !strings.HasSuffix(word, "s") {
		variations = append(variations, word+"s")
	}

	// Past tense (simple)
	if !strings.HasSuffix(word, "ed") {
		if strings.HasSuffix(word, "e") {
			variations = append(variations, word+"d")
		} else {
			variations = append(variations, word+"ed")
		}
	}

	// Present participle
	if !strings.HasSuffix(word, "ing") {
		if strings.HasSuffix(word, "e") {
			variations = append(variations, word[:len(word)-1]+"ing")
		} else {
			variations = append(variations, word+"ing")
		}
	}

	return variations
}

// matchPattern_ checks if a regex pattern matches the message.
func matchPattern_(skill *Skill, pattern, message string) (float64, string) {
	// Compile pattern if not cached
	var re *regexp.Regexp
	for i, t := range skill.Triggers {
		if t.Pattern == pattern && len(skill.compiledPatterns) > i && skill.compiledPatterns[i] != nil {
			re = skill.compiledPatterns[i]
			break
		}
	}

	if re == nil {
		var err error
		re, err = regexp.Compile("(?i)" + pattern) // Case insensitive
		if err != nil {
			return 0, ""
		}

		// Cache the compiled pattern
		if skill.compiledPatterns == nil {
			skill.compiledPatterns = make([]*regexp.Regexp, len(skill.Triggers))
		}
		for i, t := range skill.Triggers {
			if t.Pattern == pattern {
				skill.compiledPatterns[i] = re
				break
			}
		}
	}

	if re.MatchString(message) {
		matches := re.FindStringSubmatch(message)
		if len(matches) > 0 {
			return 0.9, "matched pattern: " + pattern
		}
	}

	return 0, ""
}

// MatchOptions configures matching behavior.
type MatchOptions struct {
	// MaxResults limits the number of matches returned.
	MaxResults int

	// MinScore filters out matches below this score.
	MinScore float64

	// RequiredTags only matches skills with all these tags.
	RequiredTags []string
}

// MatchWithOptions finds skills with custom options.
func MatchWithOptions(skills map[string]*Skill, message string, opts MatchOptions) []SkillMatch {
	matches := matchSkills(skills, message)

	// Filter by minimum score
	if opts.MinScore > 0 {
		filtered := matches[:0]
		for _, m := range matches {
			if m.Score >= opts.MinScore {
				filtered = append(filtered, m)
			}
		}
		matches = filtered
	}

	// Filter by required tags
	if len(opts.RequiredTags) > 0 {
		filtered := matches[:0]
		for _, m := range matches {
			if hasAllTags(m.Skill.Tags, opts.RequiredTags) {
				filtered = append(filtered, m)
			}
		}
		matches = filtered
	}

	// Limit results
	if opts.MaxResults > 0 && len(matches) > opts.MaxResults {
		matches = matches[:opts.MaxResults]
	}

	return matches
}

// hasAllTags checks if skillTags contains all requiredTags.
func hasAllTags(skillTags, requiredTags []string) bool {
	tagSet := make(map[string]bool)
	for _, t := range skillTags {
		tagSet[strings.ToLower(t)] = true
	}

	for _, req := range requiredTags {
		if !tagSet[strings.ToLower(req)] {
			return false
		}
	}

	return true
}
