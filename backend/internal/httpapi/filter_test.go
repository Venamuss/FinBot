package httpapi

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Venamuss/FinBot/internal/expense"
)

func TestFilterDefaults(t *testing.T) {
	filter, err := parseFilter(url.Values{})
	if err != nil {
		t.Fatal(err)
	}

	want := expense.Filter{Limit: 50, Sort: "spent_at", Order: "desc"}
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
			if _, err := parseFilter(query); err != nil {
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
			if _, err := parseFilter(query); err == nil {
				t.Errorf("accepted %q", input)
			}

			repository := &fakeRepo{}
			handler := New(expense.NewService(repository))

			path := "/api/expenses?" + input
			response := request(handler, http.MethodGet, path, "", "", "")

			if response.Code != http.StatusBadRequest {
				t.Fatalf("want status 400, got %d", response.Code)
			}

			if repository.calls != 0 {
				t.Fatalf("invalid query reached repository: %d calls", repository.calls)
			}
		})
	}
}
