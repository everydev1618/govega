package dsl

import "strings"

// promptTemplateMarker is the substring that flags a prompt as using
// explicit placeholders (refs govega#32 item E). When present, the
// renderer routes through renderPromptPlaceholders, where {{key}}
// substitutions can't collide. When absent, falls back to legacy
// substring substitution so existing custom prompts that use literal
// "Iris"/"Hera"/"Vega" names keep working.
const promptTemplateMarker = "{{"

// usesPlaceholders reports whether a prompt template opts into the
// placeholder grammar. Cheap substring check — the renderer hot-path
// can call it on every template without paying for a parse.
func usesPlaceholders(template string) bool {
	return strings.Contains(template, promptTemplateMarker)
}

// renderPromptPlaceholders substitutes {{key}} occurrences in template
// with vars[key]. Unknown placeholders are left untouched (failing
// open) so a typo in one variable doesn't blank out the whole prompt
// before the agent is even spawned.
//
// Backed by strings.Replacer so the cost is one linear pass over
// template — same complexity as the legacy NewReplacer path, just
// with collision-free keys.
func renderPromptPlaceholders(template string, vars map[string]string) string {
	if !usesPlaceholders(template) || len(vars) == 0 {
		return template
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}
