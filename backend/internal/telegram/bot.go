package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/observability"
)

const Denied = "Доступ к боту запрещён."

type API interface {
	Updates(context.Context, int64) ([]Update, error)
	Send(context.Context, int64, string) error
}

// Handler receives identity and time only from the original Telegram message.
// A nil error means the result is committed and can safely be acknowledged.
type Handler func(context.Context, int64, int64, string, time.Time) (string, error)

type Bot struct {
	api     API
	allowed map[int64]bool
	handle  Handler
}

func NewBot(api API, allowed map[int64]bool, handle Handler) *Bot {
	copyAllowed := make(map[int64]bool, len(allowed))
	for id, permitted := range allowed {
		copyAllowed[id] = permitted
	}
	return &Bot{api: api, allowed: copyAllowed, handle: handle}
}

func (bot *Bot) Process(ctx context.Context, update Update) error {
	ctx = observability.WithUpdateID(ctx, update.ID)
	logger := observability.Logger(ctx, "telegram", "process_update")

	message := update.Message
	if update.ID < 0 || message == nil || message.From == nil {
		logger.DebugContext(ctx, "Telegram update ignored", "reason", "missing_or_invalid_identity")
		return nil
	}

	logger = logger.With("user_id", message.From.ID)
	if message.From.IsBot || message.From.ID <= 0 || message.Chat.Type != "private" {
		logger.DebugContext(ctx, "Telegram update ignored", "reason", "unsupported_sender_or_chat")
		return nil
	}
	if message.Chat.ID != message.From.ID || message.Date <= 0 || strings.TrimSpace(message.Text) == "" {
		logger.DebugContext(ctx, "Telegram update ignored", "reason", "invalid_private_message")
		return nil
	}
	if !bot.allowed[message.From.ID] {
		logger.WarnContext(ctx, "Telegram access denied")
		err := bot.api.Send(ctx, message.Chat.ID, Denied)
		if err != nil && ctx.Err() == nil {
			logger.ErrorContext(ctx, "Telegram denial delivery failed", "error_category", errorCategory(err),
				"delivery_outcome", "best_effort_failed", "retryable", false)
		}
		// Authorization is complete; denial delivery cannot hold the update queue.
		return ctx.Err()
	}

	started := time.Now()
	logger.DebugContext(ctx, "Telegram update processing started")

	response, err := bot.handle(ctx, update.ID, message.From.ID, message.Text, time.Unix(message.Date, 0))
	if err != nil {
		if ctx.Err() == nil {
			// Handler and transport errors may contain secrets or expense data.
			logger.ErrorContext(ctx, "Telegram update processing failed", "error_category", errorCategory(err), "duration_ms", time.Since(started).Milliseconds())
			if sendErr := bot.api.Send(ctx, message.Chat.ID, botresponse.ProcessingFailure); sendErr != nil && ctx.Err() == nil {
				logger.ErrorContext(ctx, "Telegram failure notification delivery failed", "error_category", errorCategory(sendErr))
			}
		}
		return err
	}

	logger.InfoContext(ctx, "Telegram update processing completed", "duration_ms", time.Since(started).Milliseconds())

	if err := bot.api.Send(ctx, message.Chat.ID, response); err != nil {
		var failure *requestError
		terminal := errors.As(err, &failure) && failure.terminalDelivery()
		if ctx.Err() == nil {
			outcome := "retry_pending"
			if terminal {
				outcome = "terminal"
			}
			logger.ErrorContext(ctx, "Telegram response delivery failed", "error_category", errorCategory(err),
				"delivery_outcome", outcome, "retryable", !terminal)
		}
		if terminal {
			// The handler committed its result. A rejected send cannot undo that
			// transaction, and replay still protects against duplicate mutation.
			return ctx.Err()
		}
		return err
	}

	logger.InfoContext(ctx, "Telegram response delivered")
	return nil
}

func (bot *Bot) Run(ctx context.Context, retryDelay time.Duration) error {
	logger := observability.Logger(ctx, "telegram", "poll")
	var offset int64

	for ctx.Err() == nil {
		updates, err := bot.api.Updates(ctx, offset)
		if err == nil {
			logger.DebugContext(ctx, "Telegram updates received", "offset", offset, "count", len(updates))
			for _, update := range updates {
				if update.ID < offset {
					logger.DebugContext(ctx, "Telegram update already acknowledged", "update_id", update.ID, "offset", offset)
					continue
				}
				if err = bot.Process(ctx, update); err != nil {
					break
				}
				offset = update.ID + 1
			}
		}

		if err != nil {
			// Keep the unacknowledged update for retry, including failed delivery after
			// commit. The handler must replay its stored result without another mutation.
			delay := retryDelayFor(err, retryDelay)
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "Telegram polling retry scheduled", "offset", offset, "retry_delay", delay, "error_category", errorCategory(err))
			}
			if err := wait(ctx, delay); err != nil {
				return err
			}
		}
	}

	return ctx.Err()
}

func errorCategory(err error) string {
	var failure *requestError
	if errors.As(err, &failure) {
		return failure.category()
	}
	return observability.Category(err)
}
