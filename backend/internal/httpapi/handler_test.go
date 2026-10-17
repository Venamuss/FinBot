package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

const validBody = `{"user_id":"9223372036854775807","amount":"350.5","category":"еда","spent_at":"2026-10-16"}`

type fakeRepo struct {
	stored  expense.Expense
	filter  expense.Filter
	changes expense.Changes
	id      int64
	members []int64
	err     error
	calls   int
}

func (r *fakeRepo) Update(_ context.Context, id int64, changes expense.Changes) (expense.Expense, error) {
	r.calls++
	r.id = id
	r.changes = changes
	if r.err != nil {
		return expense.Expense{}, r.err
	}
	if r.stored.ID != id {
		return expense.Expense{}, expense.ErrNotFound
	}
	return r.stored, nil
}

func (r *fakeRepo) Delete(_ context.Context, id int64) error {
	r.calls++
	r.id = id
	if r.err != nil {
		return r.err
	}
	if r.stored.ID != id {
		return expense.ErrNotFound
	}
	r.stored = expense.Expense{}
	return nil
}

func (r *fakeRepo) ListMembers(context.Context) ([]int64, error) {
	r.calls++
	return r.members, r.err
}

func (r *fakeRepo) Create(_ context.Context, item expense.Expense) (expense.Expense, error) {
	r.calls++
	item.ID = 42
	item.CreatedAt = time.Date(2026, 10, 16, 12, 0, 0, 123, time.FixedZone("local", 3*3600))
	item.UpdatedAt = item.CreatedAt
	r.stored = item
	return item, r.err
}

func (r *fakeRepo) GetByID(_ context.Context, id int64) (expense.Expense, error) {
	r.calls++
	if r.err != nil {
		return expense.Expense{}, r.err
	}
	if r.stored.ID != id {
		return expense.Expense{}, expense.ErrNotFound
	}
	return r.stored, nil
}

func (r *fakeRepo) List(_ context.Context, filter expense.Filter) (expense.Page, error) {
	r.calls++
	r.filter = filter
	page := expense.Page{
		Items:  []expense.Expense{},
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	if r.stored.ID != 0 {
		page.Total = 1
		if filter.Offset == 0 {
			page.Items = append(page.Items, r.stored)
		}
	}
	return page, r.err
}

func request(handler http.Handler, method, path, body, mediaType, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if mediaType != "" {
		req.Header.Set("Content-Type", mediaType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestCreateGetList(t *testing.T) {
	repository := &fakeRepo{}
	handler := New(expense.NewService(repository))

	response := request(handler, http.MethodPost, "/api/expenses", validBody, "application/json; charset=utf-8", "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}

	var object map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 8 {
		t.Fatalf("want 8 response fields, got %d", len(object))
	}

	expectedFields := map[string]any{
		"id":         "42",
		"user_id":    "9223372036854775807",
		"amount":     "350.50",
		"category":   "еда",
		"spent_at":   "2026-10-16",
		"comment":    nil,
		"created_at": "2026-10-16T09:00:00.000000123Z",
		"updated_at": "2026-10-16T09:00:00.000000123Z",
	}

	for field, want := range expectedFields {
		if object[field] != want {
			t.Errorf("%s: want %v, got %v", field, want, object[field])
		}
	}

	get := request(handler, http.MethodGet, "/api/expenses/42", "", "", "")
	if get.Code != http.StatusOK || get.Body.String() != response.Body.String() {
		t.Fatalf("get: %d %s", get.Code, get.Body)
	}

	for _, path := range []string{"/api/expenses", "/api/expenses?offset=100"} {
		list := request(handler, http.MethodGet, path, "", "", "")
		var page struct {
			Items  []expenseJSON `json:"items"`
			Total  int           `json:"total"`
			Limit  int           `json:"limit"`
			Offset int           `json:"offset"`
		}
		if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if list.Code != http.StatusOK || page.Total != 1 || page.Limit != 50 || page.Items == nil {
			t.Fatalf("list: %d %s", list.Code, list.Body)
		}
		if page.Offset == 100 && len(page.Items) != 0 {
			t.Fatal("page past end")
		}
	}
}

func TestListFilters(t *testing.T) {
	repository := &fakeRepo{}
	handler := New(expense.NewService(repository))
	path := "/api/expenses?user_id=7&category=еда&date_from=2026-10-01&date_to=2026-10-16" +
		"&sort=amount&order=asc&limit=3&offset=2"
	response := request(handler, http.MethodGet, path, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list: %d %s", response.Code, response.Body)
	}
	want := expense.Filter{
		UserID:   7,
		Category: expense.CategoryFood,
		DateFrom: "2026-10-01",
		DateTo:   "2026-10-16",
		Sort:     "amount",
		Order:    "asc",
		Limit:    3,
		Offset:   2,
	}
	if repository.filter != want {
		t.Fatalf("want filter %+v, got %+v", want, repository.filter)
	}
}

func TestGetByIDTransportValidation(t *testing.T) {
	invalidIDs := []string{"0", "01", "-1", "+1", "1.0", "9223372036854775808"}

	for _, rawID := range invalidIDs {
		t.Run(rawID, func(t *testing.T) {
			repository := &fakeRepo{}
			handler := New(expense.NewService(repository))

			path := "/api/expenses/" + rawID
			response := request(handler, http.MethodGet, path, "", "", "")

			if response.Code != http.StatusBadRequest {
				t.Fatalf("want status 400, got %d", response.Code)
			}

			var envelope struct {
				Error struct {
					Code   string            `json:"code"`
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}

			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != "validation_error" {
				t.Fatalf("want validation_error, got %q", envelope.Error.Code)
			}
			if envelope.Error.Fields["id"] != expense.ErrInvalidID.Error() {
				t.Fatalf("unexpected ID validation message: %q", envelope.Error.Fields["id"])
			}
			if repository.calls != 0 {
				t.Fatalf("invalid ID reached repository: %d calls", repository.calls)
			}
		})
	}
}

func TestComment(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
		isNull bool
	}{
		{name: "omitted", isNull: true},
		{name: "null", suffix: `,"comment":null`, isNull: true},
		{name: "empty", suffix: `,"comment":""`},
		{name: "untrusted text", suffix: `,"comment":"<script>alert(1)</script>' OR 1=1"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepo{}
			handler := New(expense.NewService(repository))
			body := strings.TrimSuffix(validBody, "}") + tc.suffix + "}"
			response := request(handler, http.MethodPost, "/api/expenses", body, "application/json", "")
			if response.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", response.Code, response.Body)
			}
			if (repository.stored.Comment == nil) != tc.isNull {
				t.Fatalf("want null comment: %t, got %v", tc.isNull, repository.stored.Comment)
			}
		})
	}
}

func TestCreateBodySizeLimit(t *testing.T) {
	const limit = 1 << 20
	exactBody := validBody + strings.Repeat(" ", limit-len(validBody))
	oversizedComment := strings.Repeat("я", limit/2)
	commentBody := strings.TrimSuffix(validBody, "}") + `,"comment":"` + oversizedComment + `"}`
	tests := []struct {
		name            string
		body            string
		status          int
		repositoryCalls int
	}{
		{
			name:            "below limit",
			body:            exactBody[:limit-1],
			status:          http.StatusCreated,
			repositoryCalls: 1,
		},
		{
			name:            "exact limit",
			body:            exactBody,
			status:          http.StatusCreated,
			repositoryCalls: 1,
		},
		{
			name:   "one byte over",
			body:   exactBody + " ",
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name:   "malformed oversized JSON",
			body:   strings.Repeat("{", limit+1),
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name:   "oversized UTF-8 comment",
			body:   commentBody,
			status: http.StatusRequestEntityTooLarge,
		},
	}
	transfers := []struct {
		name          string
		unknownLength bool
	}{
		{name: "known length"},
		{name: "streamed", unknownLength: true},
	}
	for _, tc := range tests {
		for _, transfer := range transfers {
			t.Run(tc.name+"/"+transfer.name, func(t *testing.T) {
				repository := &fakeRepo{}
				handler := New(expense.NewService(repository))

				req := httptest.NewRequest(http.MethodPost, "/api/expenses", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")

				if transfer.unknownLength {
					req.ContentLength = -1
					req.TransferEncoding = []string{"chunked"}
				}

				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				if response.Code != tc.status {
					t.Fatalf("want status %d, got %d", tc.status, response.Code)
				}
				if repository.calls != tc.repositoryCalls {
					t.Fatalf("want %d repository calls, got %d", tc.repositoryCalls, repository.calls)
				}
				if tc.status == http.StatusRequestEntityTooLarge {
					assertRequestTooLargeResponse(t, response)
				}
			})
		}
	}
}

func assertRequestTooLargeResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("want application/json, got %q", response.Header().Get("Content-Type"))
	}

	var envelope struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "request_too_large" {
		t.Fatalf("want request_too_large, got %q", envelope.Error.Code)
	}
	if envelope.Error.Message == "" {
		t.Fatal("missing error message")
	}
	if envelope.Error.Fields == nil {
		t.Fatal("missing error fields object")
	}
	if len(envelope.Error.Fields) != 0 {
		t.Fatalf("want empty error fields, got %v", envelope.Error.Fields)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		path            string
		body            string
		mediaType       string
		origin          string
		status          int
		code            string
		repositoryCalls int
	}{
		{
			name:      "malformed",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      "{",
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "invalid_json",
		},
		{
			name:      "array",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      "[]",
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "invalid_json",
		},
		{
			name:      "null body",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      "null",
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "invalid_json",
		},
		{
			name:      "string body",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      `"x"`,
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "invalid_json",
		},
		{
			name:      "two objects",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      validBody + ` {}`,
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "invalid_json",
		},
		{
			name:      "empty body",
			method:    http.MethodPost,
			path:      "/api/expenses",
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "invalid_json",
		},
		{
			name:      "required",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      "{}",
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "number id",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, `"9223372036854775807"`, `123`, 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "null id",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, `"9223372036854775807"`, `null`, 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "number amount",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, `"350.5"`, `350.5`, 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "null amount",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, `"350.5"`, `null`, 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "null category",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, `"еда"`, `null`, 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "null date",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, `"2026-10-16"`, `null`, 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "bad date",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.Replace(validBody, "2026-10-16", "2026-02-29", 1),
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "system field",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.TrimSuffix(validBody, "}") + `,"id":"1"}`,
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "unknown field",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.TrimSuffix(validBody, "}") + `,"other":true}`,
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "comment type",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      strings.TrimSuffix(validBody, "}") + `,"comment":3}`,
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:      "media",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      validBody,
			mediaType: "text/plain",
			status:    http.StatusUnsupportedMediaType,
			code:      "unsupported_media_type",
		},
		{
			name:   "missing media",
			method: http.MethodPost,
			path:   "/api/expenses",
			body:   validBody,
			status: http.StatusUnsupportedMediaType,
			code:   "unsupported_media_type",
		},
		{
			name:      "charset",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      validBody,
			mediaType: "application/json; charset=iso-8859-1",
			status:    http.StatusUnsupportedMediaType,
			code:      "unsupported_media_type",
		},
		{
			name:      "origin",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      validBody,
			mediaType: "application/json",
			origin:    "http://evil.example",
			status:    http.StatusForbidden,
			code:      "forbidden",
		},
		{
			name:      "null origin",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      validBody,
			mediaType: "application/json",
			origin:    "null",
			status:    http.StatusForbidden,
			code:      "forbidden",
		},
		{
			name:      "malformed origin",
			method:    http.MethodPost,
			path:      "/api/expenses",
			body:      validBody,
			mediaType: "application/json",
			origin:    "http://example.com/path",
			status:    http.StatusForbidden,
			code:      "forbidden",
		},
		{
			name:   "bad id",
			method: http.MethodGet,
			path:   "/api/expenses/01",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "id overflow",
			method: http.MethodGet,
			path:   "/api/expenses/9223372036854775808",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:            "missing id",
			method:          http.MethodGet,
			path:            "/api/expenses/1",
			status:          http.StatusNotFound,
			code:            "not_found",
			repositoryCalls: 1,
		},
		{
			name:   "unknown query",
			method: http.MethodGet,
			path:   "/api/expenses?extra=1",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "detail query",
			method: http.MethodGet,
			path:   "/api/expenses/1?limit=1",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:      "post query",
			method:    http.MethodPost,
			path:      "/api/expenses?extra=1",
			body:      validBody,
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:   "empty query",
			method: http.MethodGet,
			path:   "/api/expenses?category=",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "duplicate query",
			method: http.MethodGet,
			path:   "/api/expenses?user_id=1&user_id=2",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "malformed query",
			method: http.MethodGet,
			path:   "/api/expenses?category=%zz",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "sort injection",
			method: http.MethodGet,
			path:   "/api/expenses?sort=amount;DROP+TABLE+expenses",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:      "empty patch",
			method:    http.MethodPatch,
			path:      "/api/expenses/1",
			body:      "{}",
			mediaType: "application/json",
			status:    http.StatusBadRequest,
			code:      "validation_error",
		},
		{
			name:   "put",
			method: http.MethodPut,
			path:   "/api/expenses/1",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
		},
		{
			name:   "head",
			method: http.MethodHead,
			path:   "/api/expenses",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
		},
		{
			name:   "unknown route",
			method: http.MethodGet,
			path:   "/api/missing",
			status: http.StatusNotFound,
			code:   "not_found",
		},
		{
			name:   "nested route",
			method: http.MethodGet,
			path:   "/api/expenses/1/extra",
			status: http.StatusNotFound,
			code:   "not_found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepo{}
			response := request(New(expense.NewService(repository)), tc.method, tc.path, tc.body, tc.mediaType, tc.origin)
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatal("content type")
			}
			var envelope struct {
				Error struct {
					Code    string            `json:"code"`
					Message string            `json:"message"`
					Fields  map[string]string `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != tc.code || envelope.Error.Message == "" || envelope.Error.Fields == nil {
				t.Fatalf("error shape: %s", response.Body)
			}
			if tc.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") == "" {
				t.Fatal("Allow missing")
			}
			if repository.calls != tc.repositoryCalls {
				t.Fatalf("want %d repository calls, got %d", tc.repositoryCalls, repository.calls)
			}
		})
	}
}

func TestInternalErrors(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list", method: http.MethodGet, path: "/api/expenses"},
		{name: "get", method: http.MethodGet, path: "/api/expenses/1"},
		{name: "create", method: http.MethodPost, path: "/api/expenses", body: validBody},
		{name: "update", method: http.MethodPatch, path: "/api/expenses/1", body: `{"amount":"1"}`},
		{name: "delete", method: http.MethodDelete, path: "/api/expenses/1"},
		{name: "members", method: http.MethodGet, path: "/api/members"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepo{err: errors.New("secret SQL details")}
			handler := New(expense.NewService(repository))
			response := request(handler, tc.method, tc.path, tc.body, "application/json", "")
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("internal error leak: %s", response.Body)
			}
			if !strings.Contains(response.Body.String(), `"fields":{}`) {
				t.Fatalf("missing empty error fields: %s", response.Body)
			}
		})
	}
}

func TestEmptyList(t *testing.T) {
	handler := New(expense.NewService(&fakeRepo{}))
	response := request(handler, http.MethodGet, "/api/expenses", "", "", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("missing empty items array: %s", response.Body)
	}
	if !strings.Contains(response.Body.String(), `"total":0`) {
		t.Fatalf("nonzero empty list total: %s", response.Body)
	}
}

func TestSameOriginAccepted(t *testing.T) {
	handler := New(expense.NewService(&fakeRepo{}))
	response := request(handler, http.MethodPost, "http://example.com/api/expenses",
		validBody, "application/json", "http://example.com")

	if response.Code != http.StatusCreated {
		t.Fatalf("same origin: %d %s", response.Code, response.Body)
	}
}
