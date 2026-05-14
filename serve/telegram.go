package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/everydev1618/govega/dsl"
)

// telegramBotsSettingKey holds the JSON-encoded list of configured bots.
// Persisting in a single setting keeps storage simple and lets us mark the
// whole entry sensitive (since the tokens are sensitive).
const telegramBotsSettingKey = "telegram:bots"

// TelegramBotConfig persists the user's choice for a single bot. ID is the
// numeric prefix of the token ("123456789:AAEhBO..." → "123456789") which
// is stable per bot and not sensitive on its own. Label is optional, lets
// users distinguish e.g. "Personal" vs "Work" bots in the UI.
type TelegramBotConfig struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Agent string `json:"agent"`
	Label string `json:"label,omitempty"`
}

// runningTelegramBot ties a configured bot to its live polling loop.
type runningTelegramBot struct {
	cfg    TelegramBotConfig
	bot    *TelegramBot
	cancel context.CancelFunc
}

// botIDFromToken extracts the bot's numeric id from a Telegram bot token.
// Returns "" if the token isn't well-formed.
func botIDFromToken(token string) string {
	i := strings.Index(token, ":")
	if i <= 0 {
		return ""
	}
	id := token[:i]
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}

// loadPersistedTelegramBots reads the configured-bot list from settings.
// Returns nil + nil error when nothing is stored.
func (s *Server) loadPersistedTelegramBots() ([]TelegramBotConfig, error) {
	st, err := s.store.GetSetting(telegramBotsSettingKey)
	if err != nil || st == nil || st.Value == "" {
		return nil, err
	}
	var list []TelegramBotConfig
	if err := json.Unmarshal([]byte(st.Value), &list); err != nil {
		return nil, fmt.Errorf("decode telegram bots setting: %w", err)
	}
	return list, nil
}

// savePersistedTelegramBots overwrites the bots list in settings.
func (s *Server) savePersistedTelegramBots(list []TelegramBotConfig) error {
	if len(list) == 0 {
		return s.store.DeleteSetting(telegramBotsSettingKey)
	}
	b, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("encode telegram bots: %w", err)
	}
	return s.store.UpsertSetting(Setting{
		Key:       telegramBotsSettingKey,
		Value:     string(b),
		Sensitive: true,
	})
}

// startPersistedTelegramBots boots any bots saved in the settings table.
// Failures (bad token, missing agent) are logged and skipped — the broken
// entry is left in place so the user can fix it via the dashboard.
func (s *Server) startPersistedTelegramBots(parent context.Context) {
	list, err := s.loadPersistedTelegramBots()
	if err != nil {
		slog.Warn("failed to load persisted telegram bots", "error", err)
		return
	}
	for _, cfg := range list {
		if _, err := s.startTelegramBot(parent, cfg); err != nil {
			slog.Warn("telegram bot init failed", "id", cfg.ID, "error", err)
		}
	}
}

// startTelegramBot constructs and starts a single bot under cfg, registering
// it in s.telegrams. Returns the running entry on success.
func (s *Server) startTelegramBot(parent context.Context, cfg TelegramBotConfig) (*runningTelegramBot, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.ID == "" {
		cfg.ID = botIDFromToken(cfg.Token)
	}
	if cfg.ID == "" {
		return nil, fmt.Errorf("invalid telegram token format")
	}
	if cfg.Agent == "" {
		cfg.Agent = s.cfg.Orchestrator.Name
	}
	if _, ok := s.interp.Document().Agents[cfg.Agent]; !ok {
		return nil, fmt.Errorf("agent %q does not exist", cfg.Agent)
	}

	s.telegramMu.Lock()
	if parent == nil {
		parent = s.telegramCtx
	}
	s.telegramMu.Unlock()
	if parent == nil {
		return nil, fmt.Errorf("telegram: server not started yet")
	}

	bot, err := NewTelegramBot(
		cfg.Token,
		cfg.Agent,
		s.interp,
		s.store,
		s.company,
		func(userID, agent, userMsg, response string) {
			s.extractMemory(userID, agent, userMsg, response)
		},
		// onIncoming: bind the per-user clone agent to a ReplyTarget so
		// async dispatch completions push back to this exact chat.
		func(agentName string, target dsl.ReplyTarget) {
			s.RegisterReplyTarget(agentName, target)
		},
	)
	if err != nil {
		return nil, err
	}

	s.telegramMu.Lock()
	// Replace any existing bot under the same id (atomic reconfigure).
	if existing, ok := s.telegrams[cfg.ID]; ok && existing.cancel != nil {
		existing.cancel()
	}
	botCtx, cancel := context.WithCancel(parent)
	entry := &runningTelegramBot{cfg: cfg, bot: bot, cancel: cancel}
	s.telegrams[cfg.ID] = entry
	s.telegramMu.Unlock()

	go bot.Start(botCtx)
	slog.Info("telegram bot started", "id", cfg.ID, "agent", cfg.Agent, "label", cfg.Label)
	return entry, nil
}

// AddTelegramBot validates and persists a new bot, then starts it.
// reuseLabel determines what label to assign when the user didn't supply
// one (e.g. "env" for the legacy env-var bot).
func (s *Server) AddTelegramBot(parent context.Context, token, agent, label string) (*TelegramBotConfig, error) {
	cfg := TelegramBotConfig{
		Token: strings.TrimSpace(token),
		Agent: strings.TrimSpace(agent),
		Label: strings.TrimSpace(label),
	}
	cfg.ID = botIDFromToken(cfg.Token)
	if cfg.ID == "" {
		return nil, fmt.Errorf("invalid telegram token format (expected '<id>:<secret>')")
	}

	// Persist before starting so a crash mid-startup leaves a recoverable
	// configuration. If startTelegramBot fails (bad token, missing agent),
	// roll back the persisted entry.
	list, err := s.loadPersistedTelegramBots()
	if err != nil {
		return nil, err
	}
	// Replace existing entry with same id, otherwise append.
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
	if err := s.savePersistedTelegramBots(list); err != nil {
		return nil, err
	}

	if _, err := s.startTelegramBot(parent, cfg); err != nil {
		// Roll back persistence — but only the new entry, leave others.
		filtered := list[:0]
		for _, e := range list {
			if e.ID != cfg.ID {
				filtered = append(filtered, e)
			}
		}
		_ = s.savePersistedTelegramBots(filtered)
		return nil, err
	}
	return &cfg, nil
}

// RemoveTelegramBot stops a bot and drops it from the persisted list.
func (s *Server) RemoveTelegramBot(id string) error {
	s.telegramMu.Lock()
	if existing, ok := s.telegrams[id]; ok {
		if existing.cancel != nil {
			existing.cancel()
		}
		delete(s.telegrams, id)
		slog.Info("telegram bot stopped", "id", id)
	}
	s.telegramMu.Unlock()

	list, err := s.loadPersistedTelegramBots()
	if err != nil {
		return err
	}
	filtered := list[:0]
	for _, e := range list {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	return s.savePersistedTelegramBots(filtered)
}

// TelegramBotStatus is the public-safe view of a single bot — no token.
type TelegramBotStatus struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Agent   string `json:"agent"`
	Running bool   `json:"running"`
}

// TelegramBotsSnapshot lists all configured bots (running or not) with
// non-sensitive fields. The token is never returned.
func (s *Server) TelegramBotsSnapshot() []TelegramBotStatus {
	persisted, _ := s.loadPersistedTelegramBots()

	s.telegramMu.Lock()
	defer s.telegramMu.Unlock()

	out := make([]TelegramBotStatus, 0, len(persisted))
	seen := make(map[string]bool, len(persisted))
	for _, cfg := range persisted {
		_, running := s.telegrams[cfg.ID]
		out = append(out, TelegramBotStatus{
			ID:      cfg.ID,
			Label:   cfg.Label,
			Agent:   cfg.Agent,
			Running: running,
		})
		seen[cfg.ID] = true
	}
	// Include env-only bots that aren't in the persisted list.
	for id, entry := range s.telegrams {
		if seen[id] {
			continue
		}
		out = append(out, TelegramBotStatus{
			ID:      id,
			Label:   entry.cfg.Label,
			Agent:   entry.cfg.Agent,
			Running: true,
		})
	}
	return out
}

// --- Single-bot type (unchanged from before) ---

// TelegramBot handles incoming Telegram messages via long polling and routes
// them to a vega agent, storing history in the same store as the HTTP chat API.
type TelegramBot struct {
	bot        *tgbotapi.BotAPI
	interp     *dsl.Interpreter
	store      Store
	agentName  string
	company    *dsl.Company
	onExchange func(userID, agent, userMsg, response string)

	// onIncoming is called once per inbound message, BEFORE the message
	// is dispatched to the agent. The serve layer uses this to register
	// a ReplyTarget keyed by the per-user clone agent name so async
	// dispatch completions can be pushed back to this exact Telegram chat.
	onIncoming func(agentName string, target dsl.ReplyTarget)
}

// NewTelegramBot creates a TelegramBot connected to the given token.
// onExchange is called after each successful exchange for async memory
// extraction. onIncoming (optional) is called for each inbound message
// with the per-user clone agent name and a ReplyTarget that will push
// to this user's chat — the serve layer registers it on the server's
// reply-target map so dispatch-complete callbacks can find it.
func NewTelegramBot(token, agentName string, interp *dsl.Interpreter, store Store, company *dsl.Company, onExchange func(userID, agent, userMsg, response string), onIncoming func(agentName string, target dsl.ReplyTarget)) (*TelegramBot, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	bot.Debug = false
	return &TelegramBot{
		bot:        bot,
		interp:     interp,
		store:      store,
		agentName:  agentName,
		company:    company,
		onExchange: onExchange,
		onIncoming: onIncoming,
	}, nil
}

// telegramReplyTarget is the ReplyTarget implementation for a specific
// Telegram chat — a user's bot conversation. It captures the bot handle
// and chat id so async dispatch completions can be pushed back as a
// fresh message to that exact chat.
type telegramReplyTarget struct {
	bot    *tgbotapi.BotAPI
	chatID int64
}

func (t *telegramReplyTarget) Reply(ctx context.Context, content string) error {
	if content == "" {
		return nil
	}
	_, err := t.bot.Send(tgbotapi.NewMessage(t.chatID, content))
	return err
}

// Start runs the long-polling loop until ctx is cancelled.
func (t *TelegramBot) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := t.bot.GetUpdatesChan(u)

	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			go t.handle(ctx, update)
		case <-ctx.Done():
			t.bot.StopReceivingUpdates()
			return
		}
	}
}

// handle processes a single Telegram update.
func (t *TelegramBot) handle(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	text := update.Message.Text
	if text == "" {
		return
	}

	userID := strconv.FormatInt(update.Message.From.ID, 10)
	chatID := update.Message.Chat.ID

	// Derive a per-user agent name for Telegram multi-user support.
	name := t.agentName + ":" + userID

	// Bind a ReplyTarget for this user's chat so async dispatch
	// completions originating from this turn can push back to the same
	// chat. Re-registering on every message keeps it fresh in case the
	// user's chat id changes (rare; basically never on Telegram).
	if t.onIncoming != nil {
		t.onIncoming(name, &telegramReplyTarget{bot: t.bot, chatID: chatID})
	}

	// Ensure the per-user agent clone exists.
	if agents := t.interp.Agents(); agents[name] == nil {
		doc := t.interp.Document()
		if baseDef, ok := doc.Agents[t.agentName]; ok {
			clone := *baseDef
			t.interp.AddAgent(name, &clone)
		}
	}

	// Load and inject memory into the process before sending.
	proc, err := t.interp.EnsureAgent(name)
	if err == nil && proc != nil {
		var memText string
		if memories, err := t.store.GetUserMemory(userID, t.agentName); err == nil && len(memories) > 0 {
			memText = formatMemoryForInjection(memories)
		}
		companyCtx := buildCompanyContext(t.company)
		if extra := buildExtraSystem(memText, "", companyCtx); extra != "" {
			proc.SetExtraSystem(extra)
		}
	}

	// Persist user message.
	if err := t.store.InsertChatMessage(name, "user", text, nil); err != nil {
		slog.Warn("telegram: failed to insert user message", "error", err)
	}

	// Add memory context so tools can access the store.
	ctx = ContextWithMemory(ctx, t.store, userID, t.agentName)

	// Show "typing..." in the user's chat for the duration of inference.
	// Telegram's typing indicator naturally expires after ~5s, so refresh
	// every 4s until the response is ready.
	stopTyping := make(chan struct{})
	go func() {
		for {
			_, _ = t.bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
			select {
			case <-stopTyping:
				return
			case <-time.After(4 * time.Second):
			}
		}
	}()

	// Send to agent.
	resp, err := t.interp.SendToAgent(ctx, name, text)
	close(stopTyping)
	if err != nil {
		slog.Warn("telegram: SendToAgent failed", "error", err)
		t.bot.Send(tgbotapi.NewMessage(chatID, "Error: "+err.Error()))
		return
	}

	// Persist assistant response and reply.
	if err := t.store.InsertChatMessage(name, "assistant", resp, nil); err != nil {
		slog.Warn("telegram: failed to insert assistant message", "error", err)
	}
	// Format the agent's markdown for Telegram's HTML parse mode. If Telegram
	// rejects the formatted message (e.g. due to malformed HTML from edge-case
	// model output), fall back to sending plain text so we never silently drop
	// a reply.
	msg := tgbotapi.NewMessage(chatID, markdownToTelegramHTML(resp))
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true
	if _, err := t.bot.Send(msg); err != nil {
		slog.Warn("telegram: HTML reply rejected, falling back to plain", "error", err)
		if _, err := t.bot.Send(tgbotapi.NewMessage(chatID, resp)); err != nil {
			slog.Warn("telegram: plain reply also failed", "error", err)
		}
	}
	if t.onExchange != nil {
		t.onExchange(userID, t.agentName, text, resp)
	}
}
