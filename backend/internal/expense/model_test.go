package expense

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

func TestAmount(t *testing.T) {
	validAmounts := map[string]string{
		"350":             "350.00",
		"350.5":           "350.50",
		"350.50":          "350.50",
		"0.01":            "0.01",
		"999999999999.99": "999999999999.99",
	}
	for input, want := range validAmounts {
		t.Run("valid/"+input, func(t *testing.T) {
			amount, err := ParseAmount(input)
			if err != nil {
				t.Fatal(err)
			}
			if amount.String() != want {
				t.Errorf("want %s, got %s", want, amount)
			}
		})
	}

	invalidAmounts := []string{
		"", "0", "0.00", "-1", "+1", "01", "00.1", " 1", "1 ",
		".1", "1.", "1,5", "1e2", "1.001", "1000000000000",
		"9999999999999999999999999999999", "NaN", "Infinity",
	}
	for _, input := range invalidAmounts {
		t.Run("invalid/"+input, func(t *testing.T) {
			if _, err := ParseAmount(input); err == nil {
				t.Errorf("accepted %q", input)
			}
		})
	}
}

func TestAmountAdditionPreservesPrecision(t *testing.T) {
	firstAmount, err := ParseAmount("0.10")
	if err != nil {
		t.Fatal(err)
	}
	secondAmount, err := ParseAmount("0.20")
	if err != nil {
		t.Fatal(err)
	}
	if (firstAmount + secondAmount).String() != "0.30" {
		t.Fatal("lost precision")
	}
}

func TestID(t *testing.T) {
	for _, input := range []string{"1", "9223372036854775807"} {
		t.Run("valid/"+input, func(t *testing.T) {
			if _, err := ParseID(input); err != nil {
				t.Fatal(err)
			}
		})
	}
	invalidIDs := []string{
		"", "0", "01", "-1", "+1", "1.0", " 1", "1 ",
		"9223372036854775808", "99999999999999999999",
	}
	for _, input := range invalidIDs {
		t.Run("invalid/"+input, func(t *testing.T) {
			if _, err := ParseID(input); err == nil {
				t.Errorf("accepted %q", input)
			}
		})
	}
}

func TestDate(t *testing.T) {
	for _, input := range []string{"0001-01-01", "9999-12-31", "2024-02-29", "2026-01-01", "2026-12-31"} {
		t.Run("valid/"+input, func(t *testing.T) {
			if _, err := ParseDate(input); err != nil {
				t.Fatal(err)
			}
		})
	}
	invalidDates := []string{
		"", "0000-01-01", "2026-02-29", "2024-02-30", "2026-04-31",
		"2026-13-01", "2026-1-01", "2026-01-01T00:00:00Z", "2026-01-01+03:00",
	}
	for _, input := range invalidDates {
		t.Run("invalid/"+input, func(t *testing.T) {
			if _, err := ParseDate(input); err == nil {
				t.Errorf("accepted %q", input)
			}
		})
	}
}

func TestInput(t *testing.T) {
	categories := []string{"еда", "транспорт", "жильё", "развлечения", "здоровье", "покупки", "другое"}
	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			input := Input{
				UserID:   "9223372036854775807",
				Amount:   "1",
				Category: category,
				SpentAt:  "2026-10-16",
			}
			validated, err := input.Validate()
			if err != nil {
				t.Fatal(err)
			}
			if validated.Comment != nil {
				t.Fatal("omitted comment is not null")
			}

			emptyComment := ""
			input.Comment = &emptyComment
			validated, err = input.Validate()
			if err != nil {
				t.Fatal(err)
			}
			if validated.Comment == nil || *validated.Comment != "" {
				t.Fatal("empty comment lost")
			}
		})
	}
}

func TestCategoryRequiresExactSpelling(t *testing.T) {
	if _, err := ParseCategory("Еда"); err == nil {
		t.Fatal("category case ignored")
	}
	if _, err := ParseCategory("жилье"); err == nil {
		t.Fatal("category spelling ignored")
	}
}

func TestInputRequiresMandatoryFields(t *testing.T) {
	_, err := (Input{}).Validate()
	var fields ValidationError
	if !errors.As(err, &fields) || len(fields) != 4 {
		t.Fatalf("mandatory fields: %v", err)
	}
}

func TestFilterDefaults(t *testing.T) {
	filter, err := ParseFilter(url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	want := Filter{Limit: 50, Sort: "spent_at", Order: "desc"}
	if filter != want {
		t.Fatalf("want %+v, got %+v", want, filter)
	}
}

func TestFilter(t *testing.T) {
	validQueries := []string{
		"user_id=9223372036854775807&date_from=2024-02-29&date_to=2024-02-29&category=еда&limit=100&offset=2147483647&sort=amount&order=asc",
		"limit=1&offset=0&sort=id",
		"sort=created_at",
		"sort=spent_at&order=desc",
	}
	for _, input := range validQueries {
		t.Run("valid/"+input, func(t *testing.T) {
			query, err := url.ParseQuery(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseFilter(query); err != nil {
				t.Fatal(err)
			}
		})
	}
	invalidQueries := []string{
		"unknown=1", "limit=", "limit=0", "limit=101", "limit=-1", "limit=1.0",
		"limit=1e2", "limit=%2B1", "limit=1&limit=2", "offset=-1",
		"offset=2147483648", "offset=99999999999999999", "user_id=01",
		"date_from=2026-02-29", "date_from=2026-10-17&date_to=2026-10-16",
		"category=food", "sort=user_id", "order=ASC",
	}
	for _, input := range invalidQueries {
		t.Run("invalid/"+input, func(t *testing.T) {
			query, err := url.ParseQuery(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseFilter(query); err == nil {
				t.Errorf("accepted %q", input)
			}
		})
	}
}

type countingRepo struct {
	calls int
}

func (r *countingRepo) Create(context.Context, Expense) (Expense, error) {
	r.calls++
	return Expense{}, nil
}

func (r *countingRepo) GetByID(context.Context, int64) (Expense, error) {
	r.calls++
	return Expense{}, ErrNotFound
}

func (r *countingRepo) List(context.Context, Filter) (Page, error) {
	r.calls++
	return Page{}, nil
}

func TestServiceRejectsInvalidInputBeforeRepository(t *testing.T) {
	repository := &countingRepo{}
	service := NewService(repository)
	ctx := context.Background()
	if _, err := service.Create(ctx, Input{}); err == nil {
		t.Fatal("accepted invalid create")
	}
	if _, err := service.GetByID(ctx, "0"); err == nil {
		t.Fatal("accepted invalid id")
	}
	if _, err := service.List(ctx, url.Values{"sort": {"injection"}}); err == nil {
		t.Fatal("accepted invalid filter")
	}
	if repository.calls != 0 {
		t.Fatal("invalid arguments reached repository")
	}
}
