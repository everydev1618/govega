package dsl

import "hash/fnv"

// DefaultIconPalette is the curated set of Lucide icon names new agents
// pick from when none was supplied. Names are kept generic so the icon
// rarely contradicts the agent's role.
var DefaultIconPalette = []string{
	"Sparkles",
	"Bot",
	"Cpu",
	"Briefcase",
	"Compass",
	"Flame",
	"Gem",
	"Heart",
	"Rocket",
	"Star",
	"Wand2",
	"Zap",
}

// DefaultGradientPalette is the curated set of 2-stop CSS color arrays new
// agents pick from when none was supplied. Each pair reads well as a small
// avatar disc against both light and dark backgrounds.
var DefaultGradientPalette = [][]string{
	{"#A78BFA", "#7C3AED"}, // violet
	{"#0EA5E9", "#22D3EE"}, // sky → cyan
	{"#EF4444", "#DC2626"}, // red
	{"#F59E0B", "#D97706"}, // amber
	{"#10B981", "#059669"}, // emerald
	{"#EC4899", "#DB2777"}, // pink
	{"#3B82F6", "#2563EB"}, // blue
	{"#14B8A6", "#0D9488"}, // teal
	{"#F97316", "#EA580C"}, // orange
	{"#8B5CF6", "#6D28D9"}, // purple
	{"#84CC16", "#65A30D"}, // lime
	{"#06B6D4", "#0891B2"}, // cyan
}

// DefaultVisualIdentity picks an (icon, gradient) pair for a newly-created
// agent when the caller didn't supply one. The choice is deterministic in
// seed (typically the agent name) so the same agent always gets the same
// identity across restarts and caller paths. An empty seed picks the first
// entries — stable and harmless.
//
// Used by every agent-creation path (HTTP handleCreateAgent, Hera's DSL
// create_agent tool) to avoid blank icon/color placeholders on the
// frontend (refs govega#60).
func DefaultVisualIdentity(seed string) (icon string, gradient []string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	sum := h.Sum32()
	icon = DefaultIconPalette[int(sum)%len(DefaultIconPalette)]
	pair := DefaultGradientPalette[int(sum/uint32(len(DefaultIconPalette)))%len(DefaultGradientPalette)]
	gradient = append([]string(nil), pair...)
	return icon, gradient
}
