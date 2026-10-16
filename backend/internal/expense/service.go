package expense

import (
	"context"
	"net/url"
	"strconv"
)

type Filter struct {
	UserID   int64
	DateFrom Date
	DateTo   Date
	Category Category
	Limit    int
	Offset   int
	Sort     string
	Order    string
}

func ParseFilter(query url.Values) (Filter, error) {
	filter := Filter{
		Limit: 50,
		Sort:  "spent_at",
		Order: "desc",
	}
	fields := ValidationError{}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			fields[key] = ErrInvalidQueryValue.Error()
			continue
		}
		value := values[0]
		var err error
		switch key {
		case "user_id":
			filter.UserID, err = ParseID(value)
		case "date_from":
			filter.DateFrom, err = ParseDate(value)
		case "date_to":
			filter.DateTo, err = ParseDate(value)
		case "category":
			filter.Category, err = ParseCategory(value)
		case "limit", "offset":
			number, parseErr := parsePaginationValue(value)
			if parseErr != nil {
				err = parseErr
			} else if key == "limit" && (number < 1 || number > 100) {
				err = ErrInvalidPagination
			} else if key == "limit" {
				filter.Limit = number
			} else {
				filter.Offset = number
			}
		case "sort":
			switch value {
			case "spent_at", "created_at", "amount", "id":
				filter.Sort = value
			default:
				err = ErrInvalidSort
			}
		case "order":
			if value == "asc" || value == "desc" {
				filter.Order = value
			} else {
				err = ErrInvalidOrder
			}
		default:
			err = ErrUnknownParameter
		}
		if err != nil {
			fields[key] = err.Error()
		}
	}
	if filter.DateFrom != "" && filter.DateTo != "" && filter.DateFrom > filter.DateTo {
		fields["date_to"] = ErrInvalidDateRange.Error()
	}
	if len(fields) != 0 {
		return Filter{}, fields
	}
	return filter, nil
}

func parsePaginationValue(value string) (int, error) {
	// Only decimal digits are accepted: signs and exponent notation are invalid.
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, ErrInvalidPagination
		}
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, ErrInvalidPagination
	}
	return int(number), nil
}

type Page struct {
	Items  []Expense
	Total  int64
	Limit  int
	Offset int
}

// Repository is the persistence boundary for the current three operations.
type Repository interface {
	Create(context.Context, Expense) (Expense, error)
	GetByID(context.Context, int64) (Expense, error)
	List(context.Context, Filter) (Page, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, input Input) (Expense, error) {
	validatedExpense, err := input.Validate()
	if err != nil {
		return Expense{}, err
	}
	return s.repo.Create(ctx, validatedExpense)
}

func (s *Service) GetByID(ctx context.Context, rawID string) (Expense, error) {
	id, err := ParseID(rawID)
	if err != nil {
		return Expense{}, ValidationError{"id": err.Error()}
	}
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, query url.Values) (Page, error) {
	filter, err := ParseFilter(query)
	if err != nil {
		return Page{}, err
	}
	return s.repo.List(ctx, filter)
}
