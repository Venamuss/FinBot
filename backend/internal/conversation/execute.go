package conversation

import (
	"context"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/observability"
)

func (orchestrator *Orchestrator) execute(ctx context.Context, repo Repositories, owner int64, operation action, text string, reference time.Time, next *state) (string, error) {
	service := expense.NewOwnerService(repo)

	if operation.name == "acknowledge" {
		return pendingQuestion(next.pending), nil
	}
	if operation.name == "decline" {
		next.pending = nil
		return "Выбор отменён.", nil
	}
	if operation.name == "confirm_delete" {
		return orchestrator.confirmDelete(ctx, service, owner, text, next)
	}

	if !actionMatchesMessage(operation, text, *next) {
		observability.Logger(ctx, "conversation", "validate_action").WarnContext(ctx, "Conversation action rejected", "tool", operation.name, "validation_category", "intent_conflict")
		if next.pending != nil {
			return pendingQuestion(next.pending), nil
		}
		return invalidRequest, nil
	}

	if operation.name == "choose_expense" {
		return orchestrator.chooseExpense(ctx, service, owner, operation, text, next)
	}
	if operation.name == "cancel_request" {
		return cancelPending(ctx, text, next), nil
	}

	independent := operation.value("request_context") == "new" ||
		(operation.name == "create_expense" && operation.value("request_context") != "followup") ||
		(contains([]string{"update_expense", "delete_expense"}, operation.name) && messageAction(text) == operation.name)
	if independent {
		next.pending = nil
	}

	if next.pending.selecting() && operation.name != "list_expenses" && operation.name != "navigate_expenses" && operation.name != "total_expenses" {
		return rejectStateTool(ctx, next, operation.name), nil
	}
	if next.pending != nil && (operation.name == "update_expense" || operation.name == "delete_expense") && next.pending.action.name != operation.name {
		return rejectStateTool(ctx, next, operation.name), nil
	}
	if next.pending != nil && operation.name == "update_expense" && next.pending.action.name == "update_expense" &&
		contains([]string{"amount", "category", "spent_at", "currency"}, next.pending.field) {
		value := operation.args[next.pending.field]
		if value == nil {
			return rejectStateTool(ctx, next, operation.name), nil
		}

		// A field reply cannot supply a different record ID or unrelated changes.
		args := map[string]*string{"value": value}
		if next.pending.field == "currency" && operation.args["amount"] != nil {
			args["amount"] = operation.args["amount"]
		}
		operation = action{name: "answer_clarification", args: args}
	}

	if operation.name == "answer_clarification" {
		planned, dateReference, reply, err := orchestrator.answer(ctx, operation, text, reference, next)
		if err != nil {
			return operationError(ctx, err)
		}
		if reply != "" {
			return reply, nil
		}

		operation, reference = planned, dateReference
	} else if next.pending != nil && operation.value("request_context") == "followup" &&
		(operation.name == "create_expense" || operation.name == "update_expense" || operation.name == "delete_expense") {
		if next.pending.action.name != "" && next.pending.action.name != operation.name {
			return rejectStateTool(ctx, next, operation.name), nil
		}

		if next.pending.field == "currency" && explicitRubles(text) && !unsupportedCurrency(text) && operation.args["currency"] == nil {
			operation = copyAction(operation)
			currency := "RUB"
			operation.args["currency"] = &currency
		}

		if next.pending.field == "target" && next.pending.action.name != "" {
			// A target answer selects a record; it cannot add new patch fields.
			draft := copyAction(next.pending.action)
			delete(draft.args, "id")
			delete(draft.args, "query")

			for _, field := range []string{"id", "query", "request_context"} {
				if value := operation.args[field]; value != nil {
					draft.args[field] = value
				}
			}
			operation = draft
		} else {
			operation = mergeDraft(next.pending.action, operation, next.pending.field)
		}
	}

	// Check source currency even when the model omits or mislabels it.
	if operation.name == "create_expense" || operation.name == "update_expense" || operation.name == "clarify" {
		if unsupportedCurrency(text) {
			operation = currencyClarification(operation)
		} else if (operation.value("currency") != "" && operation.value("currency") != "RUB") ||
			(operation.name == "clarify" && operation.value("field") == "currency") {
			// A model-only currency claim is not evidence from the user.
			if next.pending == nil || !unsupportedCurrency(next.pending.text) {
				return invalidRequest, nil
			}

			operation = currencyClarification(operation)
		} else if next.pending != nil && next.pending.field == "currency" && !explicitRubles(text) {
			return rejectStateTool(ctx, next, operation.name), nil
		}
	}

	if operation.name == "clarify" {
		planned, dateReference, reply, err := orchestrator.clarify(ctx, operation, text, reference, next)
		if err != nil {
			return operationError(ctx, err)
		}
		if reply != "" {
			return reply, nil
		}

		operation, reference = planned, dateReference
	}

	if next.pending != nil && !next.pending.selecting() && operation.name != "list_expenses" && operation.name != "navigate_expenses" && operation.name != "total_expenses" {
		reference = next.pending.reference
	}

	switch operation.name {
	case "navigate_expenses":
		return orchestrator.navigateListing(ctx, service, owner, operation.value("direction"), next)
	case "create_expense":
		return orchestrator.createExpense(ctx, repo, owner, operation, text, reference, next)
	case "list_expenses", "total_expenses":
		return orchestrator.readExpenses(ctx, service, owner, operation, reference, next)
	case "update_expense", "delete_expense":
		return orchestrator.modifyExpense(ctx, service, owner, operation, text, reference, next)
	default:
		return invalidRequest, nil
	}
}

func (orchestrator *Orchestrator) date(value string, reference time.Time) (string, error) {
	if value == "today" || value == "yesterday" {
		dates, err := expense.DateRangeAt(expense.Period(value), reference, orchestrator.location)
		return string(dates.From), err
	}

	date, err := expense.ParseDate(value)
	return string(date), err
}
