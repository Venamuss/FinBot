package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/observability"
)

type Handler struct {
	service        *expense.Service
	mux            *http.ServeMux
	requestTimeout time.Duration
}

const maxJSONBodyBytes = 1 << 20

func New(service *expense.Service, requestTimeout time.Duration) http.Handler {
	handler := &Handler{service: service, mux: http.NewServeMux(), requestTimeout: requestTimeout}
	handler.mux.HandleFunc("GET /api/expenses", validateQuery(handler.list, true))
	handler.mux.HandleFunc("POST /api/expenses", validateQuery(handler.create, false))
	handler.mux.HandleFunc("GET /api/expenses/{id}", validateQuery(handler.getByID, false))
	handler.mux.HandleFunc("PATCH /api/expenses/{id}", validateQuery(handler.update, false))
	handler.mux.HandleFunc("DELETE /api/expenses/{id}", validateQuery(handler.delete, false))
	handler.mux.HandleFunc("GET /api/members", validateQuery(handler.listMembers, false))
	handler.mux.HandleFunc("PUT /api/members/{user_id}", validateQuery(handler.setMemberName, false))

	// Explicit HEAD patterns override ServeMux's implicit GET-to-HEAD matching.
	collectionMethods := methodNotAllowed("GET, POST")
	detailMethods := methodNotAllowed("GET, PATCH, DELETE")
	memberMethods := methodNotAllowed("GET")
	handler.mux.HandleFunc("HEAD /api/expenses", collectionMethods)
	handler.mux.HandleFunc("HEAD /api/expenses/{id}", detailMethods)
	handler.mux.HandleFunc("HEAD /api/members", memberMethods)
	handler.mux.HandleFunc("/api/expenses", collectionMethods)
	handler.mux.HandleFunc("/api/expenses/{id}", detailMethods)
	handler.mux.HandleFunc("/api/members", memberMethods)
	handler.mux.HandleFunc("/api/members/{user_id}", methodNotAllowed("PUT"))
	handler.mux.HandleFunc("/", routeNotFound)
	return handler
}

type expenseJSON struct {
	ID        string           `json:"id"`
	UserID    string           `json:"user_id"`
	Amount    string           `json:"amount"`
	Category  expense.Category `json:"category"`
	SpentAt   expense.Date     `json:"spent_at"`
	Comment   *string          `json:"comment"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

func representExpense(item expense.Expense) expenseJSON {
	return expenseJSON{
		ID:        strconv.FormatInt(item.ID, 10),
		UserID:    strconv.FormatInt(item.UserID, 10),
		Amount:    item.Amount.String(),
		Category:  item.Category,
		SpentAt:   item.SpentAt,
		Comment:   item.Comment,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// Headers are already sent; a second response would corrupt the JSON body.
		// Do not log encoding/write errors that might contain expense data.
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	if recorder, ok := w.(*responseWriter); ok && recorder.errorCategory == "" {
		recorder.errorCategory = code
	}
	if fields == nil {
		fields = map[string]string{}
	}
	response := map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"fields":  fields,
		},
	}
	writeJSON(w, status, response)
}

func operationError(w http.ResponseWriter, err error) {
	if recorder, ok := w.(*responseWriter); ok {
		recorder.errorCategory = observability.Category(err)
	}
	var validationErrors expense.ValidationError
	switch {
	case errors.As(err, &validationErrors):
		if recorder, ok := w.(*responseWriter); ok {
			recorder.errorCategory = "validation"
		}
		writeError(w, http.StatusBadRequest, "validation_error", "Некорректные данные", validationErrors)
	case errors.Is(err, expense.ErrNotFound):
		if recorder, ok := w.(*responseWriter); ok {
			recorder.errorCategory = "not_found"
		}
		writeError(w, http.StatusNotFound, "not_found", "Расход не найден", nil)
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка", nil)
	}
}

func sameOrigin(r *http.Request) bool {
	values, present := r.Header["Origin"]
	if !present {
		return true
	}
	if len(values) != 1 {
		return false
	}
	origin, err := url.Parse(values[0])
	if err != nil {
		return false
	}
	if origin.User != nil || origin.Opaque != "" {
		return false
	}
	if origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin.Scheme == scheme && strings.EqualFold(origin.Host, r.Host)
}

func routeNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Маршрут не найден", nil)
}

func methodNotAllowed(allowedMethods string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowedMethods)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Метод не поддерживается", nil)
	}
}

func validateQuery(next http.HandlerFunc, allowsQuery bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			operationError(w, expense.ValidationError{"query": "Некорректная строка запроса"})
			return
		}
		if len(query) != 0 && !allowsQuery {
			fields := expense.ValidationError{}
			for key := range query {
				fields[key] = "Неизвестный параметр"
			}
			operationError(w, fields)
			return
		}
		next(w, r)
	}
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	id, err := expense.ParseID(r.PathValue("id"))
	if err != nil {
		operationError(w, expense.ValidationError{"id": err.Error()})
		return
	}

	item, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, representExpense(item))
}

func readInput(w http.ResponseWriter, r *http.Request) (decodedInput, bool) {
	rawFields, ok := readObject(w, r)
	if !ok {
		return decodedInput{}, false
	}
	input, fields := decodeInput(rawFields)
	if len(fields) != 0 {
		operationError(w, fields)
		return decodedInput{}, false
	}
	return input, true
}

// readObject applies the same transport guards to expense and member writes.
func readObject(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "forbidden", "Origin запрещён", nil)
		return nil, false
	}
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Ожидается application/json в UTF-8", nil)
		return nil, false
	}
	for key, value := range parameters {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Ожидается application/json в UTF-8", nil)
			return nil, false
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Тело запроса превышает 1048576 байт", nil)
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "Ожидается один JSON-объект", nil)
		return nil, false
	}
	if !utf8.Valid(data) {
		writeError(w, http.StatusBadRequest, "invalid_json", "Ожидается один JSON-объект", nil)
		return nil, false
	}
	var rawFields map[string]json.RawMessage
	err = json.Unmarshal(data, &rawFields)
	if err != nil || rawFields == nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Ожидается один JSON-объект", nil)
		return nil, false
	}
	return rawFields, true
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	input, ok := readInput(w, r)
	if !ok {
		return
	}

	created, err := h.service.Create(r.Context(), input.Input)
	if err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, representExpense(created))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := expense.ParseID(r.PathValue("id"))
	if err != nil {
		operationError(w, expense.ValidationError{"id": err.Error()})
		return
	}
	input, ok := readInput(w, r)
	if !ok {
		return
	}

	updated, err := h.service.Update(r.Context(), id, input.updateInput())
	if err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, representExpense(updated))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "forbidden", "Origin запрещён", nil)
		return
	}
	id, err := expense.ParseID(r.PathValue("id"))
	if err != nil {
		operationError(w, expense.ValidationError{"id": err.Error()})
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		operationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := h.service.ListMembers(r.Context())
	if err != nil {
		operationError(w, err)
		return
	}
	items := make([]memberJSON, 0, len(members))
	for _, member := range members {
		items = append(items, representMember(member))
	}
	response := struct {
		Items []memberJSON `json:"items"`
	}{Items: items}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r.URL.Query())
	if err != nil {
		operationError(w, err)
		return
	}

	page, err := h.service.List(r.Context(), filter)
	if err != nil {
		operationError(w, err)
		return
	}
	items := make([]expenseJSON, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, representExpense(item))
	}
	response := struct {
		Items  []expenseJSON `json:"items"`
		Total  int64         `json:"total"`
		Limit  int           `json:"limit"`
		Offset int           `json:"offset"`
	}{
		Items:  items,
		Total:  page.Total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
	writeJSON(w, http.StatusOK, response)
}

type decodedInput struct {
	expense.Input
	present map[string]json.RawMessage
}

func (input decodedInput) updateInput() expense.UpdateInput {
	update := expense.UpdateInput{Comment: input.Comment}
	if _, ok := input.present["user_id"]; ok {
		update.UserID = &input.UserID
	}
	if _, ok := input.present["amount"]; ok {
		update.Amount = &input.Amount
	}
	if _, ok := input.present["category"]; ok {
		update.Category = &input.Category
	}
	if _, ok := input.present["spent_at"]; ok {
		update.SpentAt = &input.SpentAt
	}
	_, update.CommentSet = input.present["comment"]
	return update
}

func decodeInput(rawFields map[string]json.RawMessage) (decodedInput, expense.ValidationError) {
	fields := expense.ValidationError{}
	input := decodedInput{present: rawFields}
	for key, value := range rawFields {
		var destination *string
		switch key {
		case "user_id":
			destination = &input.UserID
		case "amount":
			destination = &input.Amount
		case "category":
			destination = &input.Category
		case "spent_at":
			destination = &input.SpentAt
		case "comment":
			if string(value) == "null" {
				continue
			}
			var comment string
			err := json.Unmarshal(value, &comment)
			if err != nil {
				fields[key] = "Ожидается строка или null"
				continue
			}
			input.Comment = &comment
			continue
		default:
			fields[key] = "Неизвестное или системное поле"
			continue
		}
		if string(value) == "null" {
			fields[key] = "Ожидается строка, null запрещён"
			continue
		}
		if err := json.Unmarshal(value, destination); err != nil {
			fields[key] = "Ожидается строка, null запрещён"
		}
	}
	return input, fields
}
