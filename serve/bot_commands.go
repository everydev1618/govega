package serve

import (
	"fmt"
	"sort"
	"strings"

	vega "github.com/everydev1618/govega"
)

// Comms bridges (Discord/Telegram/…) are a linear chat window onto a
// multi-agent system: the user can't see the agent roster or the internal
// channels the way the dashboard shows them. These bare, on-demand commands
// give that visibility on any surface. They short-circuit the agent turn (see
// botExchange.run) so they're instant and don't pollute chat history.
//
// Recognized as the whole message, case-insensitive, with or without a
// leading slash (so both Discord "/agents" and a plain WhatsApp "agents" work).

// botCommand returns the canonical visibility command for a message, or "".
func botCommand(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimPrefix(t, "/")
	switch t {
	case "agents", "team", "who":
		return "agents"
	case "channels", "rooms":
		return "channels"
	case "status", "activity":
		return "status"
	case "help", "commands", "?":
		return "help"
	}
	return ""
}

// formatCommand renders a visibility command's response.
func (b *botExchange) formatCommand(cmd, userID string) string {
	switch cmd {
	case "agents":
		return b.formatAgents()
	case "channels":
		return b.formatChannels(userID)
	case "status":
		return b.formatStatus()
	case "help":
		return botHelpText
	}
	return ""
}

// botHelpText lists the visibility commands. Kept short — comms surfaces are
// linear and the point is discoverability, not a manual.
const botHelpText = "**Commands**\n" +
	"• `agents` — the team and who's working\n" +
	"• `channels` — the multi-agent channels\n" +
	"• `status` — what's running right now\n" +
	"• `!<agent> <message>` — talk to a specific agent directly\n" +
	"Anything else goes to me and I'll route it."

// formatAgents lists every defined agent with its role and live status. The
// roster comes from the document (all agents, even lazily-spawned ones);
// status is cross-referenced against live processes.
func (b *botExchange) formatAgents() string {
	doc := b.interp.Document()
	if doc == nil || len(doc.Agents) == 0 {
		return "No agents yet. Ask me to build a team and I'll have them created."
	}
	live := b.interp.Agents()

	names := make([]string, 0, len(doc.Agents))
	for n := range doc.Agents {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "**Agents** (%d)\n", len(names))
	for _, n := range names {
		def := doc.Agents[n]
		status := "idle"
		if p, ok := live[n]; ok && p != nil && p.Status() == vega.StatusRunning {
			status = "working"
		}
		role := firstLine(def.System)
		suffix := ""
		if def.IsMeta {
			suffix = " · coordinator"
		}
		fmt.Fprintf(&sb, "• **%s** — %s%s · %s\n", n, role, suffix, status)
	}
	return sb.String()
}

// formatChannels lists the multi-agent channels and their unread counts.
func (b *botExchange) formatChannels(userID string) string {
	chans, err := b.store.ListChannels(userID)
	if err != nil {
		return "Couldn't read channels right now."
	}
	if len(chans) == 0 {
		return "No channels yet. Channels open automatically when a team collaborates."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Channels** (%d)\n", len(chans))
	for _, c := range chans {
		line := "• **#" + c.Name + "**"
		if len(c.Team) > 0 {
			line += " — " + strings.Join(c.Team, ", ")
		}
		if c.UnreadCount > 0 {
			line += fmt.Sprintf(" · %d unread", c.UnreadCount)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

// formatStatus reports which agents are actively working right now.
func (b *botExchange) formatStatus() string {
	live := b.interp.Agents()
	var working []string
	for n, p := range live {
		if p != nil && p.Status() == vega.StatusRunning {
			working = append(working, n)
		}
	}
	if len(working) == 0 {
		return "All agents are idle right now."
	}
	sort.Strings(working)
	return "Working now: " + strings.Join(working, ", ")
}

// firstLine returns the first non-empty line of s, trimmed and length-capped —
// used as a one-line role summary from an agent's system prompt.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if len(ln) > 80 {
			return ln[:77] + "…"
		}
		return ln
	}
	return "(no description)"
}
