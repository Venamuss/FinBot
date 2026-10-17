package conversation

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/observability"
)

func (orchestrator *Orchestrator) execute(ctx context.Context, repo Repositories, owner int64, operation action, text string, reference time.Time, next *state) (string, error) {
	service := expense.NewOwnerService(repo)

	if operation.name == "choose_expense" {
		return orchestrator.choose(ctx, service, owner, operation, next)
	}
	if operation.name == "clarify" {
		questions := map[string]string{
			"amount":   "Какую сумму записать?",
			"category": "Уточните категорию траты.",
			"spent_at": "Уточните дату траты.",
			"target":   "Какую трату выбрать?",
			"request":  "Уточните действие и данные траты.",
		}

		question, ok := questions[operation.value("field")]
		if !ok {
			return invalidRequest, nil
		}

		if next.pending != nil && next.pending.action.name == "" {
			text = next.pending.text + "\n" + text
			reference = next.pending.reference
		}

		if len(text) > 16384 {
			next.pending = nil
			return "Уточните запрос одним сообщением.", nil

		}
		next.pending = &pending{text: text, reference: reference}

		return question, nil
	}
	if next.pending != nil && next.pending.action.name == "" {
		// A missing-field follow-up retains the original request's calendar reference.
		reference = next.pending.reference
	}
	switch operation.name {
	case "create_expense":
		date, err := orchestrator.date(operation.value("spent_at"), reference)
		if err != nil {
			return invalidRequest, nil
		}
		item, err := expense.NewService(repo).Create(ctx, expense.Input{
			UserID:   strconv.FormatInt(owner, 10),
			Amount:   operation.value("amount"),
			Category: operation.value("category"),
			SpentAt:  date,
			Comment:  operation.args["comment"],
		})
		if err != nil {
			return operationError(err)
		}
		next.focus = &item
		next.pending = nil
		return "Добавлено: " + expenseSummary(item), nil
	case "list_expenses", "total_expenses":
		filter, err := orchestrator.filter(operation, reference)
		if err != nil {
			observability.Logger(ctx, "conversation", "execute_action").WarnContext(ctx, "Conversation action rejected", "error_category", "validation", "stage", "expense_filter", "tool", operation.name)
			return invalidFilter, nil
		}
		if operation.name == "total_expenses" {
			total, err := service.Total(ctx, owner, filter)
			if err != nil {
				return operationError(err)
			}
			next.pending = nil
			return "Всего: " + total, nil
		}
		page, err := service.List(ctx, owner, filter, 20, 0)
		if err != nil {
			return operationError(err)
		}
		next.pending = nil
		if len(page.Items) == 1 && page.Total == 1 {
			next.focus = &page.Items[0]
		}
		return formatPage(page), nil
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

	if period == "" && operation.value("date_from") == "" && operation.value("date_to") == "" {
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
			return invalidRequest, nil
		}
	}

	var previous expense.Amount
	if operation.args["previous_amount"] != nil {
		value, err := expense.ParseAmount(operation.value("previous_amount"))
		if err != nil {
			return invalidRequest, nil
		}
		previous = value
	}

	candidates, err := selectExpenses(ctx, service, owner, operation, next.focus)
	if err != nil {
		next.focus = nil
		next.pending = nil
		return operationError(err)
	}

	if len(candidates) == 0 {
		return "Траты не найдены.", nil
	}
	if len(candidates) > 10 {
		next.pending = nil
		return "Найдено слишком много трат. Уточните описание или укажите ID.", nil
	}
	if len(candidates) > 1 || (previous != 0 && candidates[0].Amount != previous) {
		next.pending = &pending{
			action:        operation,
			candidates:    candidates,
			text:          text,
			reference:     reference,
			confirmAmount: len(candidates) == 1,
		}
		return selectionPrompt(candidates, next.pending.confirmAmount), nil
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

func (orchestrator *Orchestrator) choose(ctx context.Context, service *expense.OwnerService, owner int64, operation action, next *state) (string, error) {
	if operation.value("choice") == "cancel" {
		next.pending = nil
		return "Выбор отменён. Укажите нужное действие.", nil
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
			return operationError(err)
		}
		return staleSelection, nil
	}

	if previous := deferred.action.value("previous_amount"); previous != "" && !deferred.confirmAmount {
		amount, err := expense.ParseAmount(previous)
		if err != nil {
			return invalidRequest, nil
		}
		if selected.Amount != amount {
			next.pending = &pending{
				action:        deferred.action,
				candidates:    []expense.Expense{selected},
				text:          deferred.text,
				reference:     deferred.reference,
				confirmAmount: true,
			}
			return selectionPrompt(next.pending.candidates, true), nil
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
			return operationError(err)
		}
		if next.focus != nil && next.focus.ID == selected.ID {
			next.focus = nil
		}
		return "Удалено: " + expenseSummary(selected), nil
	}
	input, err := orchestrator.updateInput(operation, reference)
	if err != nil {
		return invalidRequest, nil
	}
	item, err := service.Update(ctx, owner, selected.ID, selected.UpdatedAt, input)
	if err != nil {
		next.focus = nil
		return operationError(err)
	}
	next.focus = &item
	return "Изменено: " + expenseSummary(item), nil
}

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

func formatPage(page expense.Page) string {
	if page.Total == 0 {
		return "Траты не найдены."
	}
	lines := []string{fmt.Sprintf("Траты: показано %d из %d", len(page.Items), page.Total)}
	for _, item := range page.Items {
		lines = append(lines, expenseSummary(item))
	}
	return strings.Join(lines, "\n")
}

func selectionPrompt(candidates []expense.Expense, confirmAmount bool) string {
	heading := "Какую трату выбрать? Ответьте номером."
	if confirmAmount {
		heading = "Предыдущая сумма не совпадает. Подтвердите выбор номером 1 или отмените действие."
	}
	lines := []string{heading}
	for index, item := range candidates {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, expenseSummary(item)))
	}
	return strings.Join(lines, "\n")
}
