package conversation

import (
	"context"
	"strconv"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
)

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
