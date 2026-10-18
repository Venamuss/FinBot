package conversation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/observability"
)

func (orchestrator *Orchestrator) updateInput(operation action, reference time.Time) (expense.OwnerUpdateInput, error) {
	_, commentSet := operation.args["comment"]
	comment := operation.args["comment"]
	if comment != nil && *comment == "" {
		comment = nil
	}

	input := expense.OwnerUpdateInput{
		Amount:     operation.args["amount"],
		Category:   operation.args["category"],
		Comment:    comment,
		CommentSet: commentSet,
	}

	if operation.args["spent_at"] != nil {
		date, err := orchestrator.date(operation.value("spent_at"), reference)
		if err != nil {
			return input, err
		}
		input.SpentAt = &date
	}

	_, err := input.Validate()
	return input, err
}

func (orchestrator *Orchestrator) modifyExpense(ctx context.Context, service *expense.OwnerService, owner int64, operation action, text string, reference time.Time, next *state) (string, error) {
	if next.pending != nil && len(next.pending.candidates) > 0 {
		return "Сначала завершите выбор: ответьте номером предложенной траты или отмените действие.", nil
	}
	if !matchesExplicitID(operation, text) {
		return invalidRequest, nil
	}

	if operation.name == "delete_expense" && operation.value("id") == "" && operation.value("query") == "" {
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "последн") && !strings.Contains(lower, "выбран") &&
			!contains([]string{"удали её", "удали его", "удали эту трату", "удали этот расход"}, controlText(text)) {
			next.pending = &pending{field: "target", action: copyAction(operation), text: text, reference: reference}
			return clarificationQuestions["target"], nil
		}
	}

	if operation.name == "update_expense" {
		source := text
		if next.pending != nil {
			source = next.pending.text + "\n" + text
		}

		corrected, err := sourceCorrection(operation, source)
		if err != nil {
			return operationError(ctx, err)
		}

		operation = corrected
		if _, err := orchestrator.updateInput(operation, reference); err != nil {
			return operationError(ctx, err)
		}

		if !requestedUpdateFields(operation, source) {
			return invalidRequest, nil
		}
	}

	var previous expense.Amount

	if operation.args["previous_amount"] != nil {
		value, err := expense.ParseAmount(operation.value("previous_amount"))
		if err != nil {
			return operationError(ctx, expense.ValidationError{"amount": "invalid"})
		}

		previous = value
	}

	candidates, err := selectExpenses(ctx, service, owner, operation, next.focus)
	lookupLogger := observability.Logger(ctx, "conversation", "resolve_expense").With("tool", operation.name, "state_type", next.stateType())
	if err != nil {
		outcome := "failed"
		if errors.Is(err, expense.ErrNotFound) {
			outcome = "missing_or_stale"
		}
		lookupLogger.InfoContext(ctx, "Expense lookup completed", "lookup_outcome", outcome)
		next.focus = nil
		next.pending = nil
		return operationError(ctx, err)
	}

	outcome := "unique"
	if len(candidates) == 0 {
		outcome = "missing"
	} else if len(candidates) > 10 {
		outcome = "too_many"
	} else if len(candidates) > 1 {
		outcome = "ambiguous"
	} else if previous != 0 && candidates[0].Amount != previous {
		outcome = "previous_amount_mismatch"
	}

	lookupLogger.InfoContext(ctx, "Expense lookup completed", "lookup_outcome", outcome)

	if len(candidates) == 0 {
		next.pending = nil
		return "Траты не найдены.", nil
	}

	if len(candidates) > 10 {
		next.pending = nil
		return "Найдено слишком много трат. Уточните описание или укажите ID.", nil
	}

	if len(candidates) > 1 || (previous != 0 && candidates[0].Amount != previous) {
		field := "selection"
		if len(candidates) == 1 {
			field = "confirmation"
		}
		next.pending = &pending{
			field:      field,
			action:     operation,
			candidates: candidates,
			text:       text,
			reference:  reference,
		}
		return botresponse.Selection(candidates, next.pending.field == "confirmation"), nil
	}

	return orchestrator.applyModification(ctx, service, owner, operation, reference, candidates[0], next)
}

func selectExpenses(ctx context.Context, service *expense.OwnerService, owner int64, operation action, focus *expense.Expense) ([]expense.Expense, error) {
	if query := operation.value("query"); query != "" {
		page, err := service.List(ctx, owner, expense.OwnerFilter{Query: query}, 11, 0)
		return page.Items, err
	}

	var item expense.Expense
	var err error

	if value := operation.value("id"); value != "" {
		id, parseErr := expense.ParseID(value)
		if parseErr != nil {
			return nil, parseErr
		}
		item, err = service.GetByID(ctx, owner, id)
	} else if focus != nil {
		item, err = service.GetByID(ctx, owner, focus.ID)
	} else {
		item, err = service.Latest(ctx, owner)
	}

	if err != nil {
		return nil, err
	}
	if focus != nil && item.ID == focus.ID && !item.UpdatedAt.Equal(focus.UpdatedAt) {
		return nil, expense.ErrNotFound
	}

	return []expense.Expense{item}, nil
}

func (orchestrator *Orchestrator) chooseExpense(ctx context.Context, service *expense.OwnerService, owner int64, operation action, text string, next *state) (string, error) {
	if !next.pending.selecting() || next.pending.field == "delete_confirmation" {
		return rejectStateTool(ctx, next, "choose_expense"), nil
	}
	if operation.value("choice") == "cancel" {
		return cancelPending(ctx, text, next), nil
	}

	choice, err := expense.ParseID(operation.value("choice"))
	if err != nil || choice != explicitChoice(text, next.pending.field == "confirmation") || choice < 1 || choice > int64(len(next.pending.candidates)) {
		return "Нет такого варианта. Укажите нужную трату заново.", nil
	}

	selected := next.pending.candidates[choice-1]
	deferred := next.pending

	current, err := service.GetByID(ctx, owner, selected.ID)
	if err != nil || !current.UpdatedAt.Equal(selected.UpdatedAt) {
		next.pending = nil
		next.focus = nil
		if err != nil {
			return operationError(ctx, err)
		}
		return staleSelection, nil
	}

	if previous := deferred.action.value("previous_amount"); previous != "" && deferred.field != "confirmation" {
		amount, err := expense.ParseAmount(previous)
		if err != nil {
			return operationError(ctx, expense.ValidationError{"amount": "invalid"})
		}

		if selected.Amount != amount {
			next.pending = &pending{
				field:      "confirmation",
				action:     deferred.action,
				candidates: []expense.Expense{selected},
				text:       deferred.text,
				reference:  deferred.reference,
			}
			return botresponse.Selection(next.pending.candidates, true), nil
		}
	}

	// ID, owner and the original version are checked together by the SQL write.
	// An explicit choice confirms the displayed current amount after a mismatch.
	return orchestrator.applyModification(ctx, service, owner, deferred.action, deferred.reference, selected, next)
}

func (orchestrator *Orchestrator) applyModification(ctx context.Context, service *expense.OwnerService, owner int64, operation action, reference time.Time, selected expense.Expense, next *state) (string, error) {
	next.pending = nil
	if operation.name == "delete_expense" {
		next.pending = &pending{
			field:      "delete_confirmation",
			action:     copyAction(operation),
			candidates: []expense.Expense{selected},
			reference:  reference,
		}
		return botresponse.DeleteConfirmation(selected), nil
	}

	input, err := orchestrator.updateInput(operation, reference)
	if err != nil {
		return invalidRequest, nil
	}

	item, err := service.Update(ctx, owner, selected.ID, selected.UpdatedAt, input)
	if err != nil {
		next.focus = nil
		return operationError(ctx, err)
	}
	next.focus = &item

	return botresponse.Updated(selected, item), nil
}
