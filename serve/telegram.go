package serve

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/everydev1618/govega/dsl"
)

// TelegramBot handles incoming Telegram messages via long polling and routes
// them to a vega agent, storing history in the same store as the HTTP chat API.
type TelegramBot struct {
	bot        *tgbotapi.BotAPI
	interp     *dsl.Interpreter
	store      Store
	agentName  string
	onExchange func(userID, agent, userMsg, response string)
}

// NewTelegramBot creates a TelegramBot connected to the given token.
// onExchange is called after each successful exchange for async memory extraction.
func NewTelegramBot(token, agentName string, interp *dsl.Interpreter, store Store, onExchange func(userID, agent, userMsg, response string)) (*TelegramBot, error) {
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

	// Derive a per-user agent name (mirrors chatAgentName in handlers_api.go).
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
		if memories, err := t.store.GetUserMemory(userID, t.agentName); err == nil && len(memories) > 0 {
			if memText := formatMemoryForInjection(memories); memText != "" {
				proc.SetExtraSystem(memText)
			}
		}
	}

	// Persist user message.
	if err := t.store.InsertChatMessage(name, "user", text); err != nil {
		slog.Warn("telegram: failed to insert user message", "error", err)
	}

	// Add memory context so tools can access the store.
	ctx = ContextWithMemory(ctx, t.store, userID, t.agentName)

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
