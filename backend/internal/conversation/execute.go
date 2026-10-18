package conversation

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/observability"
)

func (orchestrator *Orchestrator) execute(ctx context.Context, repo Repositories, owner int64, operation action, text string, reference time.Time, next *state) (string, error) {
	service := expense.NewOwnerService(repo)

	if operation.name == "choose_expense" {
		return orchestrator.choose(ctx, service, owner, operation, text, next)
	}
	if operation.name == "cancel_request" {
		return cancelPending(ctx, text, next), nil
	}
	independent := operation.value("request_context") == "new" ||
		(operation.name == "create_expense" && operation.value("request_context") != "followup")
	if independent {
		next.pending = nil
	}
	if next.pending.selecting() && operation.name != "list_expenses" && operation.name != "total_expenses" {
		return rejectStateTool(ctx, next, operation.name), nil
	}
	if next.pending != nil && (operation.name == "update_expense" || operation.name == "delete_expense") && next.pending.action.name != operation.name {
		return rejectStateTool(ctx, next, operation.name), nil
	}
	if next.pending != nil && operation.name == "update_expense" && next.pending.action.name == "update_expense" {
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
		(operation.name == "create_expense" || operation.name == "update_expense") {
		if next.pending.action.name != "" && next.pending.action.name != operation.name {
			return rejectStateTool(ctx, next, operation.name), nil
		}
		if next.pending.field == "currency" && explicitRubles(text) && !unsupportedCurrency(text) && operation.args["currency"] == nil {
			operation = copyAction(operation)
			currency := "RUB"
			operation.args["currency"] = &currency
		}
		operation = mergeDraft(next.pending.action, operation, next.pending.field)
	}

	// Check source currency even when the model omits or mislabels it.
	if operation.name == "create_expense" || operation.name == "update_expense" || operation.name == "clarify" {
		if unsupportedCurrency(text) || (operation.value("currency") != "" && operation.value("currency") != "RUB") {
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
	if next.pending != nil && !next.pending.selecting() && operation.name != "list_expenses" && operation.name != "total_expenses" {
		reference = next.pending.reference
	}

	switch operation.name {
	case "create_expense":
		operation = copyAction(operation)
		if operation.value("category") == "" {
			category := string(expense.CategoryOther)
			operation.args["category"] = &category
		}
		if operation.value("spent_at") == "" {
			date := "today"
			operation.args["spent_at"] = &date
		}
		date, err := orchestrator.date(operation.value("spent_at"), reference)
		if err != nil {
			return operationError(ctx, expense.ValidationError{"spent_at": "invalid"})
		}
		item, err := expense.NewService(repo).Create(ctx, expense.Input{
			UserID:   strconv.FormatInt(owner, 10),
			Amount:   operation.value("amount"),
			Category: operation.value("category"),
			SpentAt:  date,
			Comment:  operation.args["comment"],
		})
		if err != nil {
			return operationError(ctx, err)
		}
		next.focus = &item
		next.pending = nil
		return botresponse.Created(item), nil
	case "list_expenses", "total_expenses":
		filter, err := orchestrator.filter(operation, reference)
		if err != nil {
			observability.Logger(ctx, "conversation", "execute_action").WarnContext(ctx, "Conversation action rejected", "error_category", "validation", "stage", "expense_filter", "tool", operation.name)
			return invalidFilter, nil
		}
		if operation.name == "total_expenses" {
			total, err := service.Total(ctx, owner, filter)
			if err != nil {
				return operationError(ctx, err)
			}
			next.pending = nil
			return botresponse.Total(total, filter, operation.value("period")), nil
		}
		limit, offset, err := listingPagination(operation)
		if err != nil {
			return invalidFilter, nil
		}
		page, err := service.List(ctx, owner, filter, limit, offset)
		if err != nil {
			return operationError(ctx, err)
		}
		next.pending = nil
		if len(page.Items) == 1 && page.Total == 1 {
			next.focus = &page.Items[0]
		}
		reply, shown := botresponse.Page(page, filter, operation.value("period"), offset)
		next.listing = &listingContext{
			DateFrom:   filter.DateFrom,
			DateTo:     filter.DateTo,
			Category:   filter.Category,
			Query:      filter.Query,
			Limit:      limit,
			NextOffset: offset + shown,
		}
		return reply, nil
	case "update_expense", "delete_expense":
		return orchestrator.modify(ctx, service, owner, operation, text, reference, next)
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

func (orchestrator *Orchestrator) filter(operation action, reference time.Time) (expense.OwnerFilter, error) {
	filter := expense.OwnerFilter{Category: expense.Category(operation.value("category")), Query: operation.value("query")}
	period := operation.value("period")

	if operation.name == "total_expenses" && period == "" && operation.value("date_from") == "" && operation.value("date_to") == "" {
		period = string(expense.PeriodToday)
	}
	if period != "" {
		if operation.value("date_from") != "" || operation.value("date_to") != "" {
			return filter, errArguments
		}
		dates, err := expense.DateRangeAt(expense.Period(period), reference, orchestrator.location)
		if err != nil {
			return filter, err
		}
		filter.DateFrom = dates.From
		filter.DateTo = dates.To
	} else {
		filter.DateFrom = expense.Date(operation.value("date_from"))
		filter.DateTo = expense.Date(operation.value("date_to"))
	}

	return filter, filter.Validate()
}

func (orchestrator *Orchestrator) updateInput(operation action, reference time.Time) (expense.OwnerUpdateInput, error) {
	_, commentSet := operation.args["comment"]

	input := expense.OwnerUpdateInput{
		Amount:     operation.args["amount"],
		Category:   operation.args["category"],
		Comment:    operation.args["comment"],
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

func (orchestrator *Orchestrator) modify(ctx context.Context, service *expense.OwnerService, owner int64, operation action, text string, reference time.Time, next *state) (string, error) {
	if next.pending != nil && len(next.pending.candidates) > 0 {
		return "Сначала завершите выбор: ответьте номером предложенной траты или отмените действие.", nil
	}
	if operation.name == "update_expense" {
		if _, err := orchestrator.updateInput(operation, reference); err != nil {
			return operationError(ctx, err)
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
	if err != nil {
		next.focus = nil
		next.pending = nil
		return operationError(ctx, err)
	}

	if len(candidates) == 0 {
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

func (orchestrator *Orchestrator) choose(ctx context.Context, service *expense.OwnerService, owner int64, operation action, text string, next *state) (string, error) {
	if !next.pending.selecting() {
		return rejectStateTool(ctx, next, "choose_expense"), nil
	}
	if operation.value("choice") == "cancel" {
		return cancelPending(ctx, text, next), nil
	}

	choice, err := expense.ParseID(operation.value("choice"))
	if err != nil || next.pending == nil || choice < 1 || choice > int64(len(next.pending.candidates)) {
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
		if err := service.Delete(ctx, owner, selected.ID, selected.UpdatedAt); err != nil {
			next.focus = nil
			return operationError(ctx, err)
		}
		if next.focus != nil && next.focus.ID == selected.ID {
			next.focus = nil
		}
		return botresponse.Deleted(selected), nil
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

// expenseSummary is compact plain-text model context, never a user-facing reply.
func expenseSummary(item expense.Expense) string {
	summary := fmt.Sprintf("#%d · %s · %s · %s", item.ID, item.Amount.String(), item.Category, item.SpentAt)
	if item.Comment != nil {
		comment := []rune(strings.Join(strings.Fields(*item.Comment), " "))
		if len(comment) > 60 {
			comment = append(comment[:60], '…')
		}
		summary += " · " + string(comment)
	}
	return summary
}

func listingPagination(operation action) (int, int, error) {
	limit, offset := 20, 0
	for _, field := range []string{"limit", "offset"} {
		value := operation.value(field)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || strconv.Itoa(parsed) != value || parsed < 0 || parsed > 2147483647 {
			return 0, 0, errArguments
		}
		if field == "limit" {
			limit = parsed
		} else {
			offset = parsed
		}
	}
	if limit < 1 || limit > 20 {
		return 0, 0, errArguments
	}
	return limit, offset, nil
}
