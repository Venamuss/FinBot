package conversation

import (
	"context"
	"errors"
	"html"
	"strings"
	"sync"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/observability"
)

const Denied = "Доступ к боту запрещён."
const invalidRequest = botresponse.InvalidRequest
const invalidModelResponse = botresponse.InvalidModelResponse
const invalidFilter = botresponse.InvalidFilter
const staleSelection = botresponse.StaleSelection

// ErrCommitUncertain means the transaction may have committed despite its error.
// Callers must replay by update ID and discard transient state based on the old outcome.
var ErrCommitUncertain = errors.New("conversation transaction commit outcome uncertain")

// Repositories are supplied by the transaction boundary. Business validation
// still goes through the existing expense services.
type Repositories interface {
	expense.Repository
	expense.OwnerRepository
}

type Store interface {
	Lookup(context.Context, int64, int64) (string, bool, error)
	// Execute returns committed=true only for a newly confirmed commit. Errors
	// with an unknown commit outcome must match ErrCommitUncertain; other errors
	// guarantee that this attempt did not commit. Replays never execute the callback.
	Execute(context.Context, int64, int64, func(context.Context, Repositories) (string, error)) (string, bool, error)
}

type Model interface {
	Complete(context.Context, llm.Request) (llm.Response, error)
}

type pending struct {
	action     action
	candidates []expense.Expense
	text       string
	reference  time.Time
	field      string
	expiresAt  time.Time
}

type state struct {
	focus   *expense.Expense
	pending *pending
	listing *listingContext
	touched time.Time
}

type listingContext struct {
	DateFrom   expense.Date
	DateTo     expense.Date
	Category   expense.Category
	Query      string
	Limit      int
	Period     string
	Offset     int
	NextOffset int64
	Total      int64
	// Message length can shorten a page, so subtraction by Limit is unsafe.
	PreviousOffsets []int
}

type Orchestrator struct {
	model    Model
	store    Store
	allowed  map[int64]bool
	location *time.Location
	now      func() time.Time
	// Polling is sequential. This lock also serializes direct concurrent callers
	// so they cannot race on conversation focus between planning and commit.
	mu     sync.Mutex
	states map[int64]state
}

func New(model Model, store Store, allowed map[int64]bool, location *time.Location) *Orchestrator {
	copyAllowed := make(map[int64]bool, len(allowed))
	for id, permitted := range allowed {
		copyAllowed[id] = permitted
	}

	return &Orchestrator{
		model:    model,
		store:    store,
		allowed:  copyAllowed,
		location: location,
		now:      time.Now,
		states:   make(map[int64]state),
	}
}

func (orchestrator *Orchestrator) Handle(ctx context.Context, updateID, owner int64, text string, reference time.Time) (string, error) {
	// This guard precedes even replay reads, and remains useful for non-transport callers.
	if owner <= 0 || !orchestrator.allowed[owner] {
		return Denied, nil
	}

	ctx = observability.WithUpdateID(ctx, updateID)

	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return "", err
	}

	response, found, err := orchestrator.store.Lookup(ctx, updateID, owner)
	if err != nil || found {
		return storedReply(response), err
	}

	now := orchestrator.now()
	for id, current := range orchestrator.states {
		if now.Sub(current.touched) >= 30*time.Minute {
			observability.Logger(ctx, "conversation", "expire_state").InfoContext(ctx, "Conversation state expired", "state_type", current.stateType(), "reset_reason", "expired")
			delete(orchestrator.states, id)
		}
	}

	current := orchestrator.states[owner]
	if current.pending != nil && !current.pending.expiresAt.IsZero() && !now.Before(current.pending.expiresAt) {
		current.pending = nil
	}

	observability.Logger(ctx, "conversation", "load_state").DebugContext(ctx, "Conversation state loaded", "state_type", current.stateType())

	// Cancellation is a control command, never a model-selected expense action.
	// It uses the same commit/replay boundary as all other conversation changes.
	operation, control := controlAction(text, current)
	if control {
		observability.Logger(ctx, "conversation", "route_action").InfoContext(ctx, "Conversation control recognized", "state_type", current.stateType(), "tool", operation.name)
	} else {
		planned, err := orchestrator.model.Complete(ctx, orchestrator.request(text, reference, current))
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if errors.Is(err, llm.ErrToolCallCount) || errors.Is(err, llm.ErrInvalidResponse) {
				return orchestrator.saveReply(ctx, updateID, owner, interpretationFailure(text, invalidModelResponse))
			}
			// Persist a safe failure result so one poison update cannot block polling.
			return orchestrator.saveReply(ctx, updateID, owner, botresponse.Unavailable)
		}

		planLogger := observability.Logger(ctx, "conversation", "plan_action").With(
			"tool_call_count", len(planned.ToolCalls), "state_type", current.stateType())
		if len(planned.ToolCalls) == 1 {
			name := planned.ToolCalls[0].Name
			if _, known := toolSpecs[name]; known {
				planLogger = planLogger.With("tool", name, "action_category", name)
			}
		}
		operation, err = parseAction(planned)
		if err != nil {
			category := "arguments"
			var invalid *argumentError
			if errors.As(err, &invalid) {
				category = invalid.category
			}
			planLogger.WarnContext(ctx, "Conversation action rejected", "error_category", "invalid_tool_call", "stage", "tool_arguments", "validation_category", category)
			return orchestrator.saveReply(ctx, updateID, owner, interpretationFailure(text, invalidRequest))
		}

		planLogger.InfoContext(ctx, "Conversation action selected")
	}

	var next state
	response, committed, err := orchestrator.store.Execute(ctx, updateID, owner, func(transactionCtx context.Context, repo Repositories) (string, error) {
		// Each transaction retry starts from the same pre-message state.
		next = current
		next.touched = now
		if next.pending != nil && !next.pending.expiresAt.IsZero() && !orchestrator.now().Before(next.pending.expiresAt) {
			next.pending = nil
		}

		reply, err := orchestrator.execute(transactionCtx, repo, owner, operation, text, reference, &next)
		if err != nil {
			return "", err
		}

		if next.pending != nil && next.pending != current.pending && next.pending.expiresAt.IsZero() {
			next.pending.expiresAt = now.Add(30 * time.Minute)
		}

		return htmlReplyPrefix + reply, nil
	})

	orchestrator.invalidateUncertainState(ctx, owner, err)

	if err == nil && committed {
		orchestrator.states[owner] = next
		logger := observability.Logger(ctx, "conversation", "commit_state").With("from_state", current.stateType(), "to_state", next.stateType(), "tool", operation.name)
		if next.pending != nil && !next.pending.selecting() {
			logger = logger.With("clarification_type", next.pending.field)
		}
		logger.InfoContext(ctx, "Conversation state committed")
		if current.pending != nil && (next.pending == nil || operation.value("request_context") == "new") {
			reason := "completed"
			if operation.name == "cancel_request" || (operation.name == "choose_expense" && operation.value("choice") == "cancel") {
				reason = "canceled"
			} else if operation.value("request_context") == "new" || (operation.name == "create_expense" && operation.value("request_context") != "followup") {
				reason = "new_request"
			}
			logger.InfoContext(ctx, "Conversation state reset", "reset_reason", reason)
		}
	}

	return storedReply(response), err
}

func (orchestrator *Orchestrator) saveReply(ctx context.Context, updateID, owner int64, reply string) (string, error) {
	result, _, err := orchestrator.store.Execute(ctx, updateID, owner, func(context.Context, Repositories) (string, error) {
		return htmlReplyPrefix + reply, nil
	})

	orchestrator.invalidateUncertainState(ctx, owner, err)

	return storedReply(result), err
}

func (orchestrator *Orchestrator) invalidateUncertainState(ctx context.Context, owner int64, err error) {
	if !errors.Is(err, ErrCommitUncertain) {
		return
	}

	// The durable reply may describe a completed cancellation or mutation. Keeping
	// the pre-transaction draft could arm it again after replay. Known rollbacks
	// retain their state; ordinary old replays never invalidate newer state.
	delete(orchestrator.states, owner)
	observability.Logger(ctx, "conversation", "invalidate_state").WarnContext(ctx, "Conversation state invalidated",
		"transaction_outcome", "uncertain", "reset_reason", "uncertain_commit")
}

func operationError(ctx context.Context, err error) (string, error) {
	if errors.Is(err, expense.ErrNotFound) {
		return staleSelection, nil
	}

	var validation expense.ValidationError
	if errors.As(err, &validation) {
		category := "request"
		for _, field := range []string{"amount", "category", "spent_at", "comment"} {
			if _, invalid := validation[field]; invalid {
				category = field
				break
			}
		}
		observability.Logger(ctx, "conversation", "validate_action").WarnContext(ctx, "Conversation validation failed", "error_category", "validation", "validation_category", category)
		if _, invalid := validation["amount"]; invalid {
			return botresponse.InvalidAmount, nil
		}
		return invalidRequest, nil
	}
	if errors.Is(err, errArguments) || errors.Is(err, expense.ErrInvalidID) || errors.Is(err, expense.ErrInvalidPeriod) {
		observability.Logger(ctx, "conversation", "validate_action").WarnContext(ctx, "Conversation validation failed", "error_category", "validation", "validation_category", "arguments")
		return invalidRequest, nil
	}
	if errors.Is(err, expense.ErrInvalidDate) {
		observability.Logger(ctx, "conversation", "validate_action").WarnContext(ctx, "Conversation validation failed", "error_category", "validation", "validation_category", "spent_at")
		return invalidRequest, nil
	}

	return "", err
}

// Version the stored representation without changing the store or its transaction.
// Replies saved before HTML mode are plain text, including untrusted comments.
const htmlReplyPrefix = "finbot:html:v1\n"

func storedReply(value string) string {
	if reply, ok := strings.CutPrefix(value, htmlReplyPrefix); ok {
		return reply
	}
	return html.EscapeString(value)
}
