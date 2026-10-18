package conversation

import (
	"context"
	"strconv"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/observability"
)

func (orchestrator *Orchestrator) readExpenses(ctx context.Context, service *expense.OwnerService, owner int64, operation action, reference time.Time, next *state) (string, error) {
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

	limit, err := listingLimit(operation)
	if err != nil {
		return invalidFilter, nil
	}

	listing := listingContext{
		DateFrom: filter.DateFrom,
		DateTo:   filter.DateTo,
		Category: filter.Category,
		Query:    filter.Query,
		Limit:    limit,
		Period:   operation.value("period"),
	}

	return orchestrator.listPage(ctx, service, owner, listing, next)
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

func listingLimit(operation action) (int, error) {
	value := operation.value("limit")
	if value == "" {
		return 20, nil
	}

	limit, err := strconv.Atoi(value)
	if err != nil || strconv.Itoa(limit) != value || limit < 1 || limit > 20 {
		return 0, errArguments
	}

	return limit, nil
}

func (orchestrator *Orchestrator) navigateListing(ctx context.Context, service *expense.OwnerService, owner int64, direction string, next *state) (string, error) {
	if next.listing == nil {
		return "Сначала запросите список расходов.", nil
	}

	listing := *next.listing
	switch direction {
	case "next":
		if listing.NextOffset >= listing.Total {
			return "Больше расходов нет.", nil
		}
		if listing.NextOffset > 2147483647 {
			return invalidFilter, nil
		}

		// Copy before appending: transaction retries must not mutate committed state.
		listing.PreviousOffsets = append([]int(nil), listing.PreviousOffsets...)
		listing.PreviousOffsets = append(listing.PreviousOffsets, listing.Offset)
		listing.Offset = int(listing.NextOffset)
	case "previous":
		if len(listing.PreviousOffsets) > 0 {
			last := len(listing.PreviousOffsets) - 1
			listing.Offset = listing.PreviousOffsets[last]
			listing.PreviousOffsets = listing.PreviousOffsets[:last]
		}
	default:
		return invalidRequest, nil
	}

	return orchestrator.listPage(ctx, service, owner, listing, next)
}

func (orchestrator *Orchestrator) listPage(ctx context.Context, service *expense.OwnerService, owner int64, listing listingContext, next *state) (string, error) {
	filter := expense.OwnerFilter{
		DateFrom: listing.DateFrom,
		DateTo:   listing.DateTo,
		Category: listing.Category,
		Query:    listing.Query,
	}

	page, err := service.List(ctx, owner, filter, listing.Limit, listing.Offset)
	if err != nil {
		return operationError(ctx, err)
	}
	reply, shown := botresponse.Page(page, filter, listing.Period, listing.Offset)
	// An empty continuation must not replace the last displayed page.
	if shown == 0 && listing.Offset > 0 {
		return reply, nil
	}

	listing.NextOffset = int64(listing.Offset) + int64(shown)
	listing.Total = page.Total

	next.listing = &listing
	next.pending = nil
	if len(page.Items) == 1 && page.Total == 1 {
		next.focus = &page.Items[0]
	}

	return reply, nil
}
