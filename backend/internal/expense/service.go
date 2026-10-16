package expense

import "context"

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

func (s *Service) GetByID(ctx context.Context, id int64) (Expense, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, filter Filter) (Page, error) {
	return s.repo.List(ctx, filter)
}
