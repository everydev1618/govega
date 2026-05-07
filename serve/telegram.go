package serve

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/everydev1618/govega/dsl"
)

// Settings keys for runtime-configured Telegram credentials. Values stored
// here are used as a fallback when the env-based config is empty, and
// they're written by the integrations API so the configuration persists
// across server restarts.
const (
	telegramTokenSettingKey = "telegram:bot_token"
	telegramAgentSettingKey = "telegram:agent_name"
)

func settingValue(s *Setting) string {
	if s == nil {
		return ""
	}
	return s.Value
}

// resolveTelegramToken returns the Telegram bot token from env first, then
// from the settings table. Empty string means "not configured".
func (s *Server) resolveTelegramToken() string {
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		return v
	}
	if st, _ := s.store.GetSetting(telegramTokenSettingKey); settingValue(st) != "" {
		return settingValue(st)
	}
	if s.cfg.TelegramToken != "" {
		return s.cfg.TelegramToken
	}
	return ""
}

// resolveTelegramAgent returns the agent name configured for the Telegram
// bridge: env var → settings table → cfg.TelegramAgent → orchestrator
// (default).
func (s *Server) resolveTelegramAgent() string {
	if v := os.Getenv("TELEGRAM_AGENT"); v != "" {
		return v
	}
	if st, _ := s.store.GetSetting(telegramAgentSettingKey); settingValue(st) != "" {
		return settingValue(st)
	}
	if s.cfg.TelegramAgent != "" {
		return s.cfg.TelegramAgent
	}
	return s.cfg.Orchestrator.Name
}

// TelegramStatus describes the current bot lifecycle state.
type TelegramStatus struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	Agent      string `json:"agent,omitempty"`
}

// TelegramSnapshot returns the current bot state for the integrations API.
func (s *Server) TelegramSnapshot() TelegramStatus {
	s.telegramMu.Lock()
	defer s.telegramMu.Unlock()
	configured := s.resolveTelegramToken() != ""
	return TelegramStatus{
		Configured: configured,
		Running:    s.telegram != nil,
		Agent:      s.telegramAgent,
	}
}

// ConfigureTelegram (re)starts the Telegram bot with the given token + agent.
// Stops any existing bot first, then constructs and starts a fresh one
// rooted at parent (typically the server's Start ctx). Returns the API
// error from telegram bot construction (e.g. invalid token, missing agent).
func (s *Server) ConfigureTelegram(parent context.Context, token, agentName string) error {
	if token == "" {
		s.StopTelegram()
		return nil
	}
	if agentName == "" {
		agentName = s.cfg.Orchestrator.Name
	}

	// Validate the agent exists. The bot derives per-user clones as
	// "<agent>:<userid>" and reaches for the base via doc.Agents, so a
	// missing base produces hard failures on every incoming Telegram
	// message. Better to refuse to start.
	if _, ok := s.interp.Document().Agents[agentName]; !ok {
		return fmt.Errorf("agent %q does not exist", agentName)
	}

	s.telegramMu.Lock()
	if parent == nil {
		parent = s.telegramCtx
	}
	s.telegramMu.Unlock()

	if parent == nil {
		return fmt.Errorf("telegram: server not started yet")
	}

	bot, err := NewTelegramBot(token, agentName, s.interp, s.store, s.company, func(userID, agent, userMsg, response string) {
		s.extractMemory(userID, agent, userMsg, response)
	})
	if err != nil {
		return err
	}

	// Swap in the new bot, cancelling any previous polling loop.
	s.telegramMu.Lock()
	if s.telegramCancel != nil {
		s.telegramCancel()
	}
	botCtx, cancel := context.WithCancel(parent)
	s.telegram = bot
	s.telegramCancel = cancel
	s.telegramAgent = agentName
	s.telegramMu.Unlock()

	go bot.Start(botCtx)
	slog.Info("telegram bot started", "agent", agentName)
	return nil
}

// StopTelegram cancels the polling loop and clears the bot. Safe to call
// when the bot isn't running.
func (s *Server) StopTelegram() {
	s.telegramMu.Lock()
	defer s.telegramMu.Unlock()
	if s.telegramCancel != nil {
		s.telegramCancel()
		s.telegramCancel = nil
	}
	if s.telegram != nil {
		slog.Info("telegram bot stopped")
	}
	s.telegram = nil
	s.telegramAgent = ""
}

// TelegramBot handles incoming Telegram messages via long polling and routes
// them to a vega agent, storing history in the same store as the HTTP chat API.
type TelegramBot struct {
	bot        *tgbotapi.BotAPI
	interp     *dsl.Interpreter
	store      Store
	agentName  string
	company    *dsl.Company
	onExchange func(userID, agent, userMsg, response string)
}

// NewTelegramBot creates a TelegramBot connected to the given token.
// onExchange is called after each successful exchange for async memory extraction.
func NewTelegramBot(token, agentName string, interp *dsl.Interpreter, store Store, company *dsl.Company, onExchange func(userID, agent, userMsg, response string)) (*TelegramBot, error) {
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
	}, nil
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
	if err := t.store.InsertChatMessage(name, "user", text); err != nil {
		slog.Warn("telegram: failed to insert user message", "error", err)
	}

	// Add memory context so tools can access the store.
	ctx = ContextWithMemory(ctx, t.store, userID, t.agentName)
	if ss, ok := t.store.(*SQLiteStore); ok {
		ctx = ContextWithDomainStore(ctx, ss)
	}

	response, err := t.interp.SendToAgent(ctx, name, text)
	if err != nil {
		slog.Error("telegram: agent error", "agent", name, "error", err)
		t.bot.Send(tgbotapi.NewMessage(chatID, "Error: "+err.Error()))
		return
	}

	// Persist assistant response.
	if err := t.store.InsertChatMessage(name, "assistant", response); err != nil {
		slog.Warn("telegram: failed to insert assistant message", "error", err)
	}

	if _, err := t.bot.Send(tgbotapi.NewMessage(chatID, response)); err != nil {
		slog.Warn("telegram: failed to send message", "error", err)
	}

	// Fire async memory extraction.
	if t.onExchange != nil {
		go t.onExchange(userID, t.agentName, text, response)
	}
}
