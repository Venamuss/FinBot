package conversation

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
)

func (orchestrator *Orchestrator) createExpense(ctx context.Context, repo Repositories, owner int64, operation action, text string, reference time.Time, next *state) (string, error) {
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

	amount, err := expense.ParseAmount(operation.value("amount"))
	if err == nil && (ambiguousPrice(text, amount) || !matchesMarkedPrice(text, amount)) {
		draft := copyAction(operation)
		delete(draft.args, "amount")
		delete(draft.args, "amount_text")

		next.pending = &pending{field: "amount", action: draft, text: text, reference: reference}
		return clarificationQuestions["amount"], nil
	}

	source := text
	if next.pending != nil {
		source = next.pending.text + "\n" + text
	}

	comment, err := creationComment(operation, source)
	if errors.Is(err, errDescription) {
		next.pending = &pending{field: "request", text: source, reference: reference}
		return clarificationQuestions["request"], nil
	}
	if err != nil {
		return operationError(ctx, err)
	}

	item, err := expense.NewService(repo).Create(ctx, expense.Input{
		UserID:   strconv.FormatInt(owner, 10),
		Amount:   operation.value("amount"),
		Category: operation.value("category"),
		SpentAt:  date,
		Comment:  comment,
	})
	if err != nil {
		return operationError(ctx, err)
	}
	next.focus = &item
	next.pending = nil

	return botresponse.Created(item), nil
}
