package serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/everydev1618/govega/dsl"
)

// discordBotsSettingKey holds the JSON-encoded list of configured bots.
// Mirrors the Telegram scheme: one setting, marked sensitive because the
// tokens are sensitive.
const discordBotsSettingKey = "discord:bots"

// discordMaxMessageLen is Discord's hard per-message character ceiling.
const discordMaxMessageLen = 2000

// DiscordBotConfig persists the user's choice for a single bot. ID is the
// bot's numeric snowflake, derived from the token (see discordBotIDFromToken)
// so it's stable and knowable without opening a gateway session. Label is
// optional, lets users distinguish bots in the UI.
type DiscordBotConfig struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Agent string `json:"agent"`
	Label string `json:"label,omitempty"`
	// AllowedUsers restricts which Discord user IDs may talk to the bot.
	// Empty means open access (backward compatible). Set it to lock the bot
	// to its owner so strangers in shared guilds can't share the owner's
	// conversation.
	AllowedUsers []string `json:"allowed_users,omitempty"`
}

// runningDiscordBot ties a configured bot to its live gateway session.
type runningDiscordBot struct {
	cfg    DiscordBotConfig
	bot    *DiscordBot
	cancel context.CancelFunc
}

// discordBotIDFromToken extracts the bot's numeric id from a Discord bot
// token. A token looks like "<base64(userID)>.<base64(ts)>.<hmac>" where the
// first dot-segment base64-decodes to the bot's decimal snowflake. Returns ""
// if the token isn't well-formed.
func discordBotIDFromToken(token string) string {
	token = strings.TrimSpace(token)
	i := strings.Index(token, ".")
	if i <= 0 {
		return ""
	}
	seg := token[:i]
	// Discord uses unpadded standard base64; fall back to URL encoding.
	raw, err := base64.RawStdEncoding.DecodeString(seg)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(seg)
		if err != nil {
			return ""
		}
	}
	id := string(raw)
	if id == "" {
		return ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}

// loadPersistedDiscordBots reads the configured-bot list from settings.
// Returns nil + nil error when nothing is stored.
func (s *Server) loadPersistedDiscordBots() ([]DiscordBotConfig, error) {
	st, err := s.store.GetSetting(discordBotsSettingKey)
	if err != nil || st == nil || st.Value == "" {
		return nil, err
	}
	var list []DiscordBotConfig
	if err := json.Unmarshal([]byte(st.Value), &list); err != nil {
		return nil, fmt.Errorf("decode discord bots setting: %w", err)
	}
	return list, nil
}

// savePersistedDiscordBots overwrites the bots list in settings.
func (s *Server) savePersistedDiscordBots(list []DiscordBotConfig) error {
	if len(list) == 0 {
		return s.store.DeleteSetting(discordBotsSettingKey)
	}
	b, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("encode discord bots: %w", err)
	}
	return s.store.UpsertSetting(Setting{
		Key:       discordBotsSettingKey,
		Value:     string(b),
		Sensitive: true,
	})
}

// startPersistedDiscordBots boots any bots saved in the settings table.
// Failures (bad token, missing agent) are logged and skipped — the broken
// entry is left in place so the user can fix it via the dashboard.
func (s *Server) startPersistedDiscordBots(parent context.Context) {
	list, err := s.loadPersistedDiscordBots()
	if err != nil {
		slog.Warn("failed to load persisted discord bots", "error", err)
		return
	}
	for _, cfg := range list {
		if _, err := s.startDiscordBot(parent, cfg); err != nil {
			slog.Warn("discord bot init failed", "id", cfg.ID, "error", err)
		}
	}
}

// startDiscordBot constructs and starts a single bot under cfg, registering
// it in s.discords. Returns the running entry on success.
func (s *Server) startDiscordBot(parent context.Context, cfg DiscordBotConfig) (*runningDiscordBot, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.ID == "" {
		cfg.ID = discordBotIDFromToken(cfg.Token)
	}
	if cfg.ID == "" {
		return nil, fmt.Errorf("invalid discord token format")
	}
	if cfg.Agent == "" {
		cfg.Agent = s.cfg.Orchestrator.Name
	}
	if _, ok := s.interp.Document().Agents[cfg.Agent]; !ok {
		return nil, fmt.Errorf("agent %q does not exist", cfg.Agent)
	}

	s.discordMu.Lock()
	if parent == nil {
		parent = s.discordCtx
	}
	s.discordMu.Unlock()
	if parent == nil {
		return nil, fmt.Errorf("discord: server not started yet")
	}

	bot, err := NewDiscordBot(
		cfg.Token,
		cfg.Agent,
		s.interp,
		s.store,
		s.company,
		// onExchange: feed the finished exchange to the wiki memory curator
		// (govega#71) so Discord conversations write long-term memory the
		// same way web chats do. Detached from the turn's cancellation but
		// keeps its identity/BYOK values.
		func(ctx context.Context, userID, agent, userMsg, response string) {
			go s.curateMemory(carryRequestValues(ctx, context.Background()), userID, agent, userMsg, response)
		},
		// onIncoming: bind the addressed agent to a ReplyTarget so async
		// dispatch completions push back to this exact channel.
		func(agentName string, target dsl.ReplyTarget) {
			s.RegisterReplyTarget(agentName, target)
		},
	)
	if err != nil {
		return nil, err
	}
	if s.callerResolver != nil {
		bot.SetCallerResolver(s.callerResolver)
	}
	bot.SetAllowedUsers(cfg.AllowedUsers)

	// Open the gateway session synchronously so token/auth errors surface here.
	if err := bot.Open(); err != nil {
		return nil, err
	}

	botCtx, cancel := context.WithCancel(parent)

	s.discordMu.Lock()
	// Replace any existing bot under the same id (atomic reconfigure).
	if existing, ok := s.discords[cfg.ID]; ok && existing.cancel != nil {
		existing.cancel()
	}
	entry := &runningDiscordBot{cfg: cfg, bot: bot, cancel: cancel}
	s.discords[cfg.ID] = entry
	s.discordMu.Unlock()

	// Close the session when the bot's ctx is cancelled (server stop or
	// individual reconfigure/remove).
	go func() {
		<-botCtx.Done()
		bot.Stop()
	}()

	slog.Info("discord bot started", "id", cfg.ID, "agent", cfg.Agent, "label", cfg.Label)
	return entry, nil
}

// AddDiscordBot validates and persists a new bot, then starts it. allowedUsers
// (may be nil) restricts which Discord user IDs the bot responds to; nil/empty
// means open access.
func (s *Server) AddDiscordBot(parent context.Context, token, agent, label string, allowedUsers []string) (*DiscordBotConfig, error) {
	cfg := DiscordBotConfig{
		Token:        strings.TrimSpace(token),
		Agent:        strings.TrimSpace(agent),
		Label:        strings.TrimSpace(label),
		AllowedUsers: allowedUsers,
	}
	cfg.ID = discordBotIDFromToken(cfg.Token)
	if cfg.ID == "" {
		return nil, fmt.Errorf("invalid discord token format")
	}
	if cfg.Agent == "" {
		cfg.Agent = s.cfg.Orchestrator.Name
	}
	// Validate the agent up front so a bad name fails before we open a
	// gateway session (and before we bother persisting on the happy path).
	if _, ok := s.interp.Document().Agents[cfg.Agent]; !ok {
		return nil, fmt.Errorf("agent %q does not exist", cfg.Agent)
	}

	// Persist before starting so a crash mid-startup leaves a recoverable
	// configuration. Roll back if startDiscordBot fails.
	list, err := s.loadPersistedDiscordBots()
	if err != nil {
		return nil, err
	}
	replaced := false
	for i, existing := range list {
		if existing.ID == cfg.ID {
			list[i] = cfg
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, cfg)
	}
	if err := s.savePersistedDiscordBots(list); err != nil {
		return nil, err
	}

	if _, err := s.startDiscordBot(parent, cfg); err != nil {
		// Roll back persistence — but only the new entry, leave others.
		filtered := list[:0]
		for _, e := range list {
			if e.ID != cfg.ID {
				filtered = append(filtered, e)
			}
		}
		_ = s.savePersistedDiscordBots(filtered)
		return nil, err
	}
	return &cfg, nil
}

// RemoveDiscordBot stops a bot and drops it from the persisted list.
func (s *Server) RemoveDiscordBot(id string) error {
	s.discordMu.Lock()
	if existing, ok := s.discords[id]; ok {
		if existing.cancel != nil {
			existing.cancel()
		}
		delete(s.discords, id)
		slog.Info("discord bot stopped", "id", id)
	}
	s.discordMu.Unlock()

	list, err := s.loadPersistedDiscordBots()
	if err != nil {
		return err
	}
	filtered := list[:0]
	for _, e := range list {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	return s.savePersistedDiscordBots(filtered)
}

// DiscordBotStatus is the public-safe view of a single bot — no token.
type DiscordBotStatus struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Agent   string `json:"agent"`
	Running bool   `json:"running"`
}

// DiscordBotsSnapshot lists all configured bots (running or not) with
// non-sensitive fields. The token is never returned.
func (s *Server) DiscordBotsSnapshot() []DiscordBotStatus {
	persisted, _ := s.loadPersistedDiscordBots()

	s.discordMu.Lock()
	defer s.discordMu.Unlock()

	out := make([]DiscordBotStatus, 0, len(persisted))
	seen := make(map[string]bool, len(persisted))
	for _, cfg := range persisted {
		_, running := s.discords[cfg.ID]
		out = append(out, DiscordBotStatus{
			ID:      cfg.ID,
			Label:   cfg.Label,
			Agent:   cfg.Agent,
			Running: running,
		})
		seen[cfg.ID] = true
	}
	// Include env-only bots that aren't in the persisted list.
	for id, entry := range s.discords {
		if seen[id] {
			continue
		}
		out = append(out, DiscordBotStatus{
			ID:      id,
			Label:   entry.cfg.Label,
			Agent:   entry.cfg.Agent,
			Running: true,
		})
	}
	return out
}

// --- Single-bot type ---

// DiscordBot handles incoming Discord messages via the gateway websocket and
// routes them to a vega agent, storing history in the same store as the HTTP
// chat API. It responds to all direct messages and, in guild channels, only
// when the bot is mentioned.
type DiscordBot struct {
	session   *discordgo.Session
	agentName string

	// exch runs the shared hydrate → inject → send → persist → curate
	// turn core (see bot_exchange.go).
	exch *botExchange

	// onIncoming is called once per inbound message, BEFORE dispatch, with
	// the addressed agent name and a ReplyTarget that pushes to this exact
	// channel — so async dispatch completions can be delivered back.
	onIncoming func(agentName string, target dsl.ReplyTarget)

	// allowedUsers, when non-empty, restricts which user IDs may talk to the
	// bot. Empty means open access.
	allowedUsers map[string]bool
}

// SetAllowedUsers restricts which Discord user IDs the bot will respond to.
// An empty list means open access.
func (d *DiscordBot) SetAllowedUsers(ids []string) {
	d.allowedUsers = newAllowSet(ids)
}

// SetCallerResolver registers a CallerResolver applied to the dispatch context
// before SendToAgent on every inbound Discord message. Pass nil to clear.
func (d *DiscordBot) SetCallerResolver(r CallerResolver) {
	d.exch.resolver = r
}

// NewDiscordBot creates a DiscordBot connected to the given token. It does not
// open the gateway session — call Open for that. onExchange is called after
// each successful exchange (the serve layer wires it to the memory curator).
func NewDiscordBot(token, agentName string, interp *dsl.Interpreter, store Store, company *dsl.Company, onExchange func(ctx context.Context, userID, agent, userMsg, response string), onIncoming func(agentName string, target dsl.ReplyTarget)) (*DiscordBot, error) {
	session, err := discordgo.New("Bot " + strings.TrimSpace(token))
	if err != nil {
		return nil, fmt.Errorf("discord session init: %w", err)
	}
	// We need message content plus guild + DM message events. Message Content
	// is a privileged intent the operator must enable in the Discord developer
	// portal for the bot application.
	session.Identify.Intents = discordgo.IntentGuildMessages |
		discordgo.IntentDirectMessages |
		discordgo.IntentMessageContent
	d := &DiscordBot{
		session:   session,
		agentName: agentName,
		exch: &botExchange{
			interp:     interp,
			store:      store,
			company:    company,
			surface:    surfaceDiscord,
			baseAgent:  agentName,
			onExchange: onExchange,
		},
		onIncoming: onIncoming,
	}
	session.AddHandler(d.onMessageCreate)
	return d, nil
}

// Open connects the gateway session and begins receiving events.
func (d *DiscordBot) Open() error {
	if err := d.session.Open(); err != nil {
		return fmt.Errorf("discord gateway open: %w", err)
	}
	return nil
}

// Stop closes the gateway session.
func (d *DiscordBot) Stop() {
	if err := d.session.Close(); err != nil {
		slog.Warn("discord: session close failed", "error", err)
	}
}

// discordReplyTarget is the ReplyTarget implementation for a specific Discord
// channel. It captures the session and channel id so async dispatch
// completions can be pushed back as a fresh message to that exact channel.
type discordReplyTarget struct {
	session   *discordgo.Session
	channelID string
}

func (d *discordReplyTarget) Reply(ctx context.Context, content string) error {
	if content == "" {
		return nil
	}
	for _, chunk := range splitDiscordMessage(content) {
		if _, err := d.session.ChannelMessageSend(d.channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

// splitDiscordMessage breaks content into chunks no longer than
// discordMaxMessageLen runes, preferring to split on newline (then space)
// boundaries. Concatenating the chunks reproduces the input exactly — content
// is never dropped. Returns nil for empty input.
func splitDiscordMessage(s string) []string {
	if s == "" {
		return nil
	}
	r := []rune(s)
	if len(r) <= discordMaxMessageLen {
		return []string{s}
	}
	var out []string
	for len(r) > discordMaxMessageLen {
		cut := discordMaxMessageLen
		window := r[:cut]
		if i := lastRuneIndexRunes(window, '\n'); i > 0 {
			cut = i + 1
		} else if i := lastRuneIndexRunes(window, ' '); i > 0 {
			cut = i + 1
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// lastRuneIndexRunes returns the index of the last occurrence of target in r,
// or -1 if absent.
func lastRuneIndexRunes(r []rune, target rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == target {
			return i
		}
	}
	return -1
}

// stripBotMention removes the bot's mention tokens ("<@id>" and "<@!id>") from
// content and trims surrounding whitespace. A blank botID leaves content
// unchanged.
func stripBotMention(content, botID string) string {
	if botID == "" {
		return content
	}
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}

// parseAllowedUsers splits a comma-separated list of Discord user IDs (as set
// via the DISCORD_ALLOWED_USERS env var / portal field) into a trimmed slice,
// dropping blanks. Returns nil for empty input — meaning open access.
func parseAllowedUsers(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// discordShouldRespond decides whether an inbound message warrants a reply:
// always in DMs, and only when mentioned in a guild channel (so the bot stays
// quiet in busy shared channels).
func discordShouldRespond(isDM, mentioned bool) bool {
	return isDM || mentioned
}

// onMessageCreate is the gateway handler for inbound messages.
func (d *DiscordBot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Never respond to our own messages or to other bots (avoids loops).
	if s.State != nil && s.State.User != nil && m.Author != nil && m.Author.ID == s.State.User.ID {
		return
	}
	if m.Author == nil || m.Author.Bot {
		return
	}

	botUserID := ""
	if s.State != nil && s.State.User != nil {
		botUserID = s.State.User.ID
	}

	isDM := m.GuildID == ""
	mentioned := false
	for _, u := range m.Mentions {
		if u.ID == botUserID {
			mentioned = true
			break
		}
	}
	if !discordShouldRespond(isDM, mentioned) {
		return
	}

	text := stripBotMention(m.Content, botUserID)
	if text == "" {
		return
	}

	d.handle(context.Background(), m, text)
}

// handle routes one message's text through the agent and replies.
func (d *DiscordBot) handle(ctx context.Context, m *discordgo.MessageCreate, text string) {
	userID := m.Author.ID
	channelID := m.ChannelID

	// Enforce the owner allowlist (if configured). Silently ignore strangers.
	if !userAllowed(d.allowedUsers, userID) {
		slog.Debug("discord: ignoring message from unlisted user", "user_id", userID)
		return
	}

	// Escape hatch: address a specific agent inline with "!agent rest".
	// Unknown names pass through to the base agent (see telegram.go).
	name := d.agentName
	if target, body := parseAgentPrefix(text, d.exch.interp.HasAgent); target != "" {
		if body == "" {
			d.replyThreaded(m.Reference(), channelID, "What would you like to ask "+target+"?")
			return
		}
		name = target
		text = body
	}

	if d.onIncoming != nil {
		d.onIncoming(name, &discordReplyTarget{session: d.session, channelID: channelID})
	}

	// Show "typing..." in the channel for the duration of inference. Discord's
	// typing indicator expires after ~10s, so refresh every 8s until done.
	stopTyping := make(chan struct{})
	go func() {
		for {
			_ = d.session.ChannelTyping(channelID)
			select {
			case <-stopTyping:
				return
			case <-time.After(8 * time.Second):
			}
		}
	}()

	// Surface dispatch/progress as plain in-channel messages so the user sees
	// the multi-agent work happening, not just the final reply.
	onProgress := func(msg string) {
		if _, err := d.session.ChannelMessageSend(channelID, msg); err != nil {
			slog.Debug("discord: progress send failed", "error", err)
		}
	}
	resp, err := d.exch.run(ctx, name, text, userID, onProgress)
	close(stopTyping)
	if err != nil {
		slog.Warn("discord: SendToAgent failed", "error", err)
		reply := "Error: " + err.Error()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// The per-turn deadline fired (or the turn was cancelled). Say so
			// plainly instead of leaking a raw context error — and never leave
			// the user staring at a "typing…" indicator that goes nowhere.
			reply = "That took too long and I had to stop working on it. Try again, or break it into smaller steps."
		}
		d.replyThreaded(m.Reference(), channelID, reply)
		return
	}

	// Discord renders markdown natively, so the agent's output is sent as-is
	// (chunked to the 2000-char limit) and threaded as a reply to the
	// triggering message.
	d.replyThreaded(m.Reference(), channelID, resp)
}

// replyThreaded sends content back to the channel as a reply to the triggering
// message so it threads visually in Discord. The first chunk carries the reply
// reference; overflow chunks continue as plain follow-ups. A nil ref falls back
// to a plain message (used where there's no triggering message to reply to).
func (d *DiscordBot) replyThreaded(ref *discordgo.MessageReference, channelID, content string) {
	for i, chunk := range splitDiscordMessage(content) {
		var err error
		if i == 0 && ref != nil {
			_, err = d.session.ChannelMessageSendReply(channelID, chunk, ref)
		} else {
			_, err = d.session.ChannelMessageSend(channelID, chunk)
		}
		if err != nil {
			slog.Warn("discord: reply failed", "error", err)
			return
		}
	}
}
