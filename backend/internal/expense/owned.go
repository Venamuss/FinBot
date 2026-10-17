package expense

import (
	"context"
	"strings"
	"time"
)

// OwnerFilter has no owner field: the caller supplies the authenticated owner
// separately, never from model arguments. Date boundaries are inclusive.
type OwnerFilter struct {
	// Query is a literal case-insensitive substring of the comment.
	Query    string
	DateFrom Date
	DateTo   Date
	Category Category
}

func (filter OwnerFilter) Validate() error {
	fields := ValidationError{}
	if strings.ContainsRune(filter.Query, 0) || len(filter.Query) > 4096 {
		fields["query"] = ErrInvalidComment.Error()
	}
	if filter.DateFrom != "" {
		if _, err := ParseDate(string(filter.DateFrom)); err != nil {
			fields["date_from"] = err.Error()
		}
	}
	if filter.DateTo != "" {
		if _, err := ParseDate(string(filter.DateTo)); err != nil {
			fields["date_to"] = err.Error()
		}
	}
	if len(fields) == 0 && filter.DateFrom != "" && filter.DateTo != "" && filter.DateFrom > filter.DateTo {
		fields["date_to"] = ErrInvalidDateRange.Error()
	}
	if filter.Category != "" {
		if _, err := ParseCategory(string(filter.Category)); err != nil {
			fields["category"] = err.Error()
		}
	}
	if len(fields) != 0 {
		return fields
	}
	return nil
}

// OwnerUpdateInput deliberately cannot reassign an expense to another owner.
type OwnerUpdateInput struct {
	Amount     *string
	Category   *string
	SpentAt    *string
	Comment    *string
	CommentSet bool
}

func (input OwnerUpdateInput) Validate() (Changes, error) {
	return UpdateInput{
		Amount:     input.Amount,
		Category:   input.Category,
		SpentAt:    input.SpentAt,
		Comment:    input.Comment,
		CommentSet: input.CommentSet,
	}.Validate()
}

// OwnerRepository keeps scoped operations separate from the panel's privileges.
// Mutations require the updated_at returned when the expense was selected.
// A missing, foreign, or stale row returns ErrNotFound without disclosure.
type OwnerRepository interface {
	GetOwned(context.Context, int64, int64) (Expense, error)
	ListOwned(context.Context, int64, OwnerFilter, int, int) (Page, error)
	TotalOwned(context.Context, int64, OwnerFilter) (string, error)
	LatestOwned(context.Context, int64) (Expense, error)
	UpdateOwned(context.Context, int64, int64, time.Time, Changes) (Expense, error)
	DeleteOwned(context.Context, int64, int64, time.Time) error
}

// OwnerService expects an owner established by a trusted caller. Bot allowlist
// checks belong to that caller, before any service or stored-result access.
type OwnerService struct {
	repo OwnerRepository
}

func NewOwnerService(repo OwnerRepository) *OwnerService {
	return &OwnerService{repo: repo}
}

func (s *OwnerService) GetByID(ctx context.Context, owner, id int64) (Expense, error) {
	if owner <= 0 || id <= 0 {
		return Expense{}, ErrInvalidID
	}
	return s.repo.GetOwned(ctx, owner, id)
}

func (s *OwnerService) List(ctx context.Context, owner int64, filter OwnerFilter, limit, offset int) (Page, error) {
	if owner <= 0 {
		return Page{}, ErrInvalidID
	}
	if err := filter.Validate(); err != nil {
		return Page{}, err
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 2147483647 {
		return Page{}, ErrInvalidPagination
	}
	return s.repo.ListOwned(ctx, owner, filter, limit, offset)
}

// Total returns a decimal string with two fractional digits, including "0.00"
// for no matches. Aggregates can exceed both MaxAmount and int64 hundredths.
func (s *OwnerService) Total(ctx context.Context, owner int64, filter OwnerFilter) (string, error) {
	if owner <= 0 {
		return "", ErrInvalidID
	}
	if err := filter.Validate(); err != nil {
		return "", err
	}
	return s.repo.TotalOwned(ctx, owner, filter)
}

func (s *OwnerService) Latest(ctx context.Context, owner int64) (Expense, error) {
	if owner <= 0 {
		return Expense{}, ErrInvalidID
	}
	return s.repo.LatestOwned(ctx, owner)
}

func (s *OwnerService) Update(ctx context.Context, owner, id int64, expectedUpdatedAt time.Time, input OwnerUpdateInput) (Expense, error) {
	if owner <= 0 || id <= 0 {
		return Expense{}, ErrInvalidID
	}
	if expectedUpdatedAt.IsZero() {
		return Expense{}, ErrInvalidVersion
	}
	changes, err := input.Validate()
	if err != nil {
		return Expense{}, err
	}
	return s.repo.UpdateOwned(ctx, owner, id, expectedUpdatedAt, changes)
}

func (s *OwnerService) Delete(ctx context.Context, owner, id int64, expectedUpdatedAt time.Time) error {
	if owner <= 0 || id <= 0 {
		return ErrInvalidID
	}
	if expectedUpdatedAt.IsZero() {
		return ErrInvalidVersion
	}
	return s.repo.DeleteOwned(ctx, owner, id, expectedUpdatedAt)
}
