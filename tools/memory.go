package tools

// WikiMemoryToolNames is the canonical list of wiki memory tools every
// agent gets in its tool surface. The DSL layer pulls this into the
// always-available bucket so a per-agent Tools allow-list never gates
// memory access: every agent sees the shared user wiki and can write
// durable facts back to it. Refs govega#71.
//
// The actual implementations live in serve/memory_wiki_tools.go because
// they need a Store. Co-locating the names here lets the DSL layer
// reference them without pulling in the storage dependency.
func WikiMemoryToolNames() []string {
	return []string{
		"memory_read", "memory_list", "memory_search",
		"memory_write", "memory_append", "memory_edit",
	}
}
