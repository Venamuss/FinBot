package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

func TestMemberNamePut(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{body: `{"display_name":"  Анна  "}`, want: `{"user_id":"9223372036854775807","display_name":"Анна"}`},
		{body: `{"display_name":null}`, want: `{"user_id":"9223372036854775807","display_name":null}`},
		{body: `{"display_name":"  "}`, want: `{"user_id":"9223372036854775807","display_name":null}`},
	}
	for _, tc := range tests {
		repository := &fakeRepo{}
		response := request(New(expense.NewService(repository), 5*time.Second), http.MethodPut, "/api/members/9223372036854775807", tc.body, "application/json; charset=utf-8", "http://example.com")
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != tc.want || repository.calls != 1 {
			t.Fatalf("unexpected member response: %d %s", response.Code, response.Body)
		}
	}
}

func TestMemberNameGuards(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		media  string
		origin string
		code   string
		status int
	}{
		{name: "immutable ID", body: `{"display_name":"Анна","user_id":"202"}`, status: 400, code: "validation_error"},
		{name: "missing name", body: `{}`, status: 400, code: "validation_error"},
		{name: "number", body: `{"display_name":12}`, status: 400, code: "validation_error"},
		{name: "object", body: `{"display_name":{}}`, status: 400, code: "validation_error"},
		{name: "too long", body: `{"display_name":"` + strings.Repeat("я", 101) + `"}`, status: 400, code: "validation_error"},
		{name: "control", body: `{"display_name":"Анна\u0000"}`, status: 400, code: "validation_error"},
		{name: "invalid ID", path: "/api/members/01", status: 400, code: "validation_error"},
		{name: "overflow ID", path: "/api/members/9223372036854775808", status: 400, code: "validation_error"},
		{name: "unknown query", path: "/api/members/101?extra=1", status: 400, code: "validation_error"},
		{name: "invalid JSON", body: `[]`, status: 400, code: "invalid_json"},
		{name: "multiple objects", body: `{} {}`, status: 400, code: "invalid_json"},
		{name: "invalid UTF8", body: "{\xff}", status: 400, code: "invalid_json"},
		{name: "size limit", body: strings.Repeat(" ", maxJSONBodyBytes+1), status: 413, code: "request_too_large"},
		{name: "media type", media: "text/plain", status: 415, code: "unsupported_media_type"},
		{name: "cross origin", origin: "https://evil.example", status: 403, code: "forbidden"},
		{name: "unsupported method", method: "PATCH", status: 405, code: "method_not_allowed"},
		{name: "HEAD", method: "HEAD", status: 405, code: "method_not_allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			method, path, body, media := tc.method, tc.path, tc.body, tc.media
			if method == "" {
				method = http.MethodPut
			}
			if path == "" {
				path = "/api/members/101"
			}
			if body == "" {
				body = `{"display_name":"Анна"}`
			}
			if media == "" {
				media = "application/json"
			}
			repository := &fakeRepo{}
			response := request(New(expense.NewService(repository), 5*time.Second), method, path, body, media, tc.origin)
			assertCRUDStatus(t, response, tc.status, tc.code)
			if repository.calls != 0 {
				t.Fatal("invalid member write reached repository")
			}
			if tc.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != "PUT" {
				t.Fatal("missing member method Allow header")
			}
		})
	}
}

func TestMemberNameDatabaseError(t *testing.T) {
	repository := &fakeRepo{err: errors.New("private database details")}
	response := request(New(expense.NewService(repository), 5*time.Second), http.MethodPut, "/api/members/101", `{"display_name":"Анна"}`, "application/json", "")
	assertCRUDStatus(t, response, http.StatusInternalServerError, "internal_error")
	if strings.Contains(response.Body.String(), "private") {
		t.Fatal("database details leaked")
	}
}

func TestMembersReturnsAssignedNames(t *testing.T) {
	repository := &fakeRepo{members: []int64{101, 202}}
	handler := New(expense.NewService(repository), 5*time.Second)
	response := request(handler, http.MethodPut, "/api/members/101", `{"display_name":"  Анна  "}`, "application/json", "")
	if response.Code != http.StatusOK {
		t.Fatal("member name write failed")
	}
	response = request(handler, http.MethodGet, "/api/members", "", "", "")
	want := `{"items":[{"user_id":"101","display_name":"Анна"},{"user_id":"202","display_name":null}]}`
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != want {
		t.Fatal("member list did not return assigned names with an unnamed fallback")
	}
}
