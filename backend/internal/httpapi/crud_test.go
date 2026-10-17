package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

func TestPatch(t *testing.T) {
	owner := int64(9223372036854775807)
	amount := expense.Amount(45050)
	category := expense.CategoryTransport
	date := expense.Date("2024-02-29")
	empty := ""
	comment := "synthetic"
	tests := []struct {
		name string
		body string
		want expense.Changes
	}{
		{name: "amount only", body: `{"amount":"450.5"}`, want: expense.Changes{Amount: &amount}},
		{name: "clear comment", body: `{"comment":null}`, want: expense.Changes{CommentSet: true}},
		{name: "empty comment", body: `{"comment":""}`, want: expense.Changes{CommentSet: true, Comment: &empty}},
		{name: "set comment", body: `{"comment":"synthetic"}`, want: expense.Changes{CommentSet: true, Comment: &comment}},
		{name: "reassign owner", body: `{"user_id":"9223372036854775807"}`, want: expense.Changes{UserID: &owner}},
		{
			name: "all fields",
			body: `{"user_id":"9223372036854775807","amount":"450.5","category":"транспорт","spent_at":"2024-02-29","comment":null}`,
			want: expense.Changes{UserID: &owner, Amount: &amount, Category: &category, SpentAt: &date, CommentSet: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepo{stored: expense.Expense{
				ID: 42, UserID: owner, Amount: amount, Category: category, SpentAt: date,
				CreatedAt: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC),
			}}
			handler := New(expense.NewService(repository))
			response := request(handler, http.MethodPatch, "/api/expenses/42", tc.body, "application/json; charset=utf-8", "http://example.com")
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			if repository.calls != 1 || repository.id != 42 || !reflect.DeepEqual(repository.changes, tc.want) {
				t.Fatalf("unexpected repository update: %+v", repository)
			}
			var object map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"id": "42", "user_id": "9223372036854775807", "amount": "450.50",
				"category": "транспорт", "spent_at": "2024-02-29", "comment": nil,
				"created_at": "2026-10-16T09:00:00Z", "updated_at": "2026-10-16T10:00:00Z",
			}
			if !reflect.DeepEqual(object, want) || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected full response: %s", response.Body)
			}
		})
	}
}

func TestPatchInvalidBodies(t *testing.T) {
	tests := []struct {
		body string
		code string
	}{
		{body: "", code: "invalid_json"},
		{body: "{", code: "invalid_json"},
		{body: "[]", code: "invalid_json"},
		{body: "null", code: "invalid_json"},
		{body: `"text"`, code: "invalid_json"},
		{body: "{} {}", code: "invalid_json"},
		{body: "{\xff}", code: "invalid_json"},
		{body: "{}", code: "validation_error"},
		{body: `{"user_id":null}`, code: "validation_error"},
		{body: `{"amount":null}`, code: "validation_error"},
		{body: `{"category":null}`, code: "validation_error"},
		{body: `{"spent_at":null}`, code: "validation_error"},
		{body: `{"user_id":123}`, code: "validation_error"},
		{body: `{"amount":1.23}`, code: "validation_error"},
		{body: `{"category":[]}`, code: "validation_error"},
		{body: `{"spent_at":true}`, code: "validation_error"},
		{body: `{"comment":{}}`, code: "validation_error"},
		{body: `{"comment":"\u0000"}`, code: "validation_error"},
		{body: `{"user_id":"01"}`, code: "validation_error"},
		{body: `{"amount":"1.001"}`, code: "validation_error"},
		{body: `{"category":"Еда"}`, code: "validation_error"},
		{body: `{"amount":"2","spent_at":"2026-02-29"}`, code: "validation_error"},
		{body: `{"amount":"2","id":"1"}`, code: "validation_error"},
		{body: `{"amount":"2","created_at":"2026-10-16T00:00:00Z"}`, code: "validation_error"},
		{body: `{"amount":"2","updated_at":"2026-10-16T00:00:00Z"}`, code: "validation_error"},
		{body: `{"amount":"2","unknown":true}`, code: "validation_error"},
	}
	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			repository := &fakeRepo{}
			response := request(New(expense.NewService(repository)), http.MethodPatch, "/api/expenses/42", tc.body, "application/json", "")
			assertCRUDStatus(t, response, http.StatusBadRequest, tc.code)
			if repository.calls != 0 {
				t.Fatal("invalid body reached repository")
			}
		})
	}
}

func assertCRUDStatus(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("want status %d, got %d: %s", status, response.Code, response.Body)
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
	if envelope.Error.Code != code || envelope.Error.Message == "" || envelope.Error.Fields == nil {
		t.Fatalf("invalid error envelope: %s", response.Body)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatal("error is not application/json")
	}
}

func TestCRUDRoutingAndGuards(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		media  string
		origin string
		status int
		code   string
		allow  string
		calls  int
	}{
		{
			name:   "patch invalid ID",
			method: http.MethodPatch,
			path:   "/api/expenses/01",
			media:  "application/json",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "delete invalid ID",
			method: http.MethodDelete,
			path:   "/api/expenses/9223372036854775808",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "patch missing",
			method: http.MethodPatch,
			path:   "/api/expenses/1",
			media:  "application/json",
			status: http.StatusNotFound,
			code:   "not_found",
			calls:  1,
		},
		{
			name:   "delete missing",
			method: http.MethodDelete,
			path:   "/api/expenses/1",
			status: http.StatusNotFound,
			code:   "not_found",
			calls:  1,
		},
		{
			name:   "patch query",
			method: http.MethodPatch,
			path:   "/api/expenses/1?extra=1",
			media:  "application/json",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "delete query",
			method: http.MethodDelete,
			path:   "/api/expenses/1?extra=1",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "members query",
			method: http.MethodGet,
			path:   "/api/members?limit=1",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "members malformed query",
			method: http.MethodGet,
			path:   "/api/members?a=%zz",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "patch origin",
			method: http.MethodPatch,
			path:   "/api/expenses/1",
			media:  "application/json",
			origin: "https://evil.example",
			status: http.StatusForbidden,
			code:   "forbidden",
		},
		{
			name:   "delete origin",
			method: http.MethodDelete,
			path:   "/api/expenses/1",
			origin: "null",
			status: http.StatusForbidden,
			code:   "forbidden",
		},
		{
			name:   "patch media",
			method: http.MethodPatch,
			path:   "/api/expenses/1",
			media:  "text/plain",
			status: http.StatusUnsupportedMediaType,
			code:   "unsupported_media_type",
		},
		{
			name:   "patch missing media",
			method: http.MethodPatch,
			path:   "/api/expenses/1",
			status: http.StatusUnsupportedMediaType,
			code:   "unsupported_media_type",
		},
		{
			name:   "patch charset",
			method: http.MethodPatch,
			path:   "/api/expenses/1",
			media:  "application/json; charset=latin1",
			status: http.StatusUnsupportedMediaType,
			code:   "unsupported_media_type",
		},
		{
			name:   "detail methods",
			method: http.MethodPut,
			path:   "/api/expenses/1",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
			allow:  "GET, PATCH, DELETE",
		},
		{
			name:   "members methods",
			method: http.MethodPost,
			path:   "/api/members",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
			allow:  "GET",
		},
		{
			name:   "collection methods",
			method: http.MethodPatch,
			path:   "/api/expenses",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
			allow:  "GET, POST",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepo{}
			response := request(New(expense.NewService(repository)), tc.method, tc.path, `{"amount":"1"}`, tc.media, tc.origin)
			assertCRUDStatus(t, response, tc.status, tc.code)
			if repository.calls != tc.calls || response.Header().Get("Allow") != tc.allow {
				t.Fatalf("unexpected calls or Allow: %d, %q", repository.calls, response.Header().Get("Allow"))
			}
		})
	}
}

func TestDeleteAndRepeat(t *testing.T) {
	repository := &fakeRepo{stored: expense.Expense{ID: 42}}
	handler := New(expense.NewService(repository))
	response := request(handler, http.MethodDelete, "/api/expenses/42", "", "", "http://example.com")
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
		t.Fatalf("expected bodyless 204, got %d: %s", response.Code, response.Body)
	}
	if repository.calls != 1 || repository.id != 42 {
		t.Fatal("wrong delete invocation")
	}
	response = request(handler, http.MethodDelete, "/api/expenses/42", "", "", "")
	assertCRUDStatus(t, response, http.StatusNotFound, "not_found")
}

func TestMembers(t *testing.T) {
	for _, members := range [][]int64{nil, {2, 10, 9223372036854775807}} {
		repository := &fakeRepo{members: members}
		response := request(New(expense.NewService(repository)), http.MethodGet, "/api/members", "", "", "")
		want := `{"items":[]}`
		if len(members) != 0 {
			want = `{"items":[{"user_id":"2"},{"user_id":"10"},{"user_id":"9223372036854775807"}]}`
		}
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != want || repository.calls != 1 {
			t.Fatalf("unexpected members response: %d %s", response.Code, response.Body)
		}
	}
}

func TestPatchBodySizeLimit(t *testing.T) {
	body := `{"comment":null}`
	exactBody := body + strings.Repeat(" ", maxJSONBodyBytes-len(body))
	for _, streamed := range []bool{false, true} {
		for _, oversized := range []bool{false, true} {
			repository := &fakeRepo{stored: expense.Expense{ID: 42}}
			input := exactBody
			if oversized {
				input += " "
			}
			req := httptest.NewRequest(http.MethodPatch, "/api/expenses/42", strings.NewReader(input))
			req.Header.Set("Content-Type", "application/json")
			if streamed {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			response := httptest.NewRecorder()
			New(expense.NewService(repository)).ServeHTTP(response, req)
			if oversized {
				assertCRUDStatus(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
				if repository.calls != 0 {
					t.Fatal("oversized patch reached repository")
				}
			} else if response.Code != http.StatusOK || repository.calls != 1 {
				t.Fatalf("exact limit rejected: %d", response.Code)
			}
		}
	}
}
