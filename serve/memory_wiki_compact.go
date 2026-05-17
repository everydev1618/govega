package serve

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
)

// compactionMessageThreshold is the message-count at which a chat turn
// triggers Compact() before dispatching to the LLM. Long-running
// conversations bloat the prompt with old tool_use/tool_result pairs;
// at this point we distill the older portion into a wiki note so the
// agent retains the substance without dragging the raw history along.
//
// 40 messages comfortably covers ~10-20 turns of typical agent
// activity. Lower than MaxResidentMessages (100) so compaction fires
// well before the dumb count-based trim drops messages with no
// summary. Refs govega#100.
const compactionMessageThreshold = 40

// compactionKeepLast is the number of recent messages held verbatim
// after a compaction. Anything older gets folded into the session
// note. 12 is enough to keep the immediate context intact across a
// few turns.
const compactionKeepLast = 12

// maybeCompactToWiki runs Compact() on proc when its conversation
// has grown past the threshold. The distilled summary is written as
// a dated session note in the agent's private wiki, and MEMORY.md is
// updated to link the new note so the next turn's auto-injection
// surfaces it. Errors are logged but don't block the chat turn —
// falling through with the full history is the safe default.
func (s *Server) maybeCompactToWiki(ctx context.Context, proc *vega.Process, userID, agentName string) {
	if proc == nil {
		return
	}
	if len(proc.Messages()) < compactionMessageThreshold {
		return
	}

	sink := s.wikiCompactionSink(userID, agentName)
	if err := proc.Compact(ctx, compactionKeepLast, sink); err != nil {
		slog.Warn("wiki compaction failed", "agent", agentName, "error", err)
	}
}

// wikiCompactionSink returns a CompactionSink that writes the
// summary as a new wiki page in the agent's private scope and
// appends a link to MEMORY.md so it surfaces via auto-injection.
func (s *Server) wikiCompactionSink(userID, agentName string) vega.CompactionSink {
	return func(ctx context.Context, summary string, meta vega.CompactionMeta) error {
		ts := meta.DroppedAt
		if ts.IsZero() {
			ts = time.Now()
		}
		path := fmt.Sprintf("sessions/%s.md", ts.UTC().Format("2006-01-02-150405"))

		frontmatter := fmt.Sprintf(
			"source: auto-compaction (govega#100)\nprocess: %s\nagent: %s\ndropped: %d messages\nat: %s",
			meta.ProcessID, meta.AgentName, meta.DroppedCount, ts.UTC().Format(time.RFC3339),
		)

		if err := s.store.UpsertMemoryPage(MemoryPage{
			Scope: MemoryScopeAgent, ScopeID: agentName, UserID: userID,
			Path: path, Content: summary, Frontmatter: frontmatter,
		}); err != nil {
			return fmt.Errorf("write session note: %w", err)
		}
		if err := s.store.ReplaceMemoryLinks(MemoryScopeAgent, agentName, userID, path, extractMemoryLinks(summary)); err != nil {
			return fmt.Errorf("link session note: %w", err)
		}

		if err := s.linkSessionInIndex(userID, agentName, path, ts); err != nil {
			// Indexing is best-effort — the note itself is already saved.
			slog.Warn("failed to link session note in MEMORY.md", "agent", agentName, "error", err)
		}
		return nil
	}
}

// linkSessionInIndex prepends a `[[sessions/...]]` reference under a
// "## Past sessions" heading in the agent's private MEMORY.md so the
// most recent compaction appears first when MEMORY.md is injected
// into the next turn's prompt.
func (s *Server) linkSessionInIndex(userID, agentName, sessionPath string, ts time.Time) error {
	const heading = "## Past sessions"
	link := fmt.Sprintf("- [[%s]] — %s", sessionPath, ts.UTC().Format("2006-01-02 15:04 UTC"))

	existing, err := s.store.GetMemoryPage(MemoryScopeAgent, agentName, userID, "MEMORY.md")
	if err != nil {
		return fmt.Errorf("read index: %w", err)
	}

	var content, fm string
	if existing != nil {
		fm = existing.Frontmatter
	}
	switch {
	case existing == nil || existing.Content == "":
		content = heading + "\n" + link + "\n"
	case strings.Contains(existing.Content, heading):
		content = insertAfterHeading(existing.Content, heading, link)
	default:
		content = existing.Content
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + heading + "\n" + link + "\n"
	}

	if err := s.store.UpsertMemoryPage(MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: agentName, UserID: userID,
		Path: "MEMORY.md", Content: content, Frontmatter: fm,
	}); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	return s.store.ReplaceMemoryLinks(MemoryScopeAgent, agentName, userID, "MEMORY.md", extractMemoryLinks(content))
}

// insertAfterHeading places `line` immediately after the first line
// containing `heading` in `content`. Used to keep recent session
// links at the top of the "## Past sessions" section.
func insertAfterHeading(content, heading, line string) string {
	idx := strings.Index(content, heading)
	if idx < 0 {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + "\n" + heading + "\n" + line + "\n"
	}
	end := idx + len(heading)
	for end < len(content) && content[end] != '\n' {
		end++
	}
	if end < len(content) {
		end++ // skip the newline
	}
	return content[:end] + line + "\n" + content[end:]
}
