package httpapi

import (
	"context"
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
)

type Handler struct {
	service *expense.Service
}

const maxJSONBodyBytes = 1 << 20

func New(service *expense.Service) http.Handler {
	return &Handler{service: service}
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
	var validationErrors expense.ValidationError
	switch {
	case errors.As(err, &validationErrors):
		writeError(w, http.StatusBadRequest, "validation_error", "Некорректные данные", validationErrors)
	case errors.Is(err, expense.ErrNotFound):
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

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	r = r.WithContext(ctx)
	isCollection := r.URL.Path == "/api/expenses"
	id := strings.TrimPrefix(r.URL.Path, "/api/expenses/")
	isDetail := strings.HasPrefix(r.URL.Path, "/api/expenses/") && id != "" && !strings.Contains(id, "/")
	if !isCollection && !isDetail {
		writeError(w, http.StatusNotFound, "not_found", "Маршрут не найден", nil)
		return
	}

	allowedMethods := "GET"
	methodAllowed := r.Method == http.MethodGet
	if isCollection {
		allowedMethods = "GET, POST"
		methodAllowed = methodAllowed || r.Method == http.MethodPost
	}
	if !methodAllowed {
		w.Header().Set("Allow", allowedMethods)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Метод не поддерживается", nil)
		return
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		operationError(w, expense.ValidationError{"query": "Некорректная строка запроса"})
		return
	}
	allowsQuery := isCollection && r.Method == http.MethodGet
	if len(query) != 0 && !allowsQuery {
		fields := expense.ValidationError{}
		for key := range query {
			fields[key] = "Неизвестный параметр"
		}
		operationError(w, fields)
		return
	}

	switch {
	case r.Method == http.MethodPost:
		h.create(w, r)
	case isDetail:
		h.getByID(w, r, id)
	default:
		h.list(w, r, query)
	}
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := expense.ParseID(rawID)
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

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "forbidden", "Origin запрещён", nil)
		return
	}

	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Ожидается application/json в UTF-8", nil)
		return
	}
	for key, value := range parameters {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Ожидается application/json в UTF-8", nil)
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	input, fields, err := decodeInput(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Тело запроса превышает 1048576 байт", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "Ожидается один JSON-объект", nil)
		return
	}
	if len(fields) != 0 {
		operationError(w, fields)
		return
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, representExpense(created))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, query url.Values) {
	filter, err := parseFilter(query)
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

func decodeInput(body io.Reader) (expense.Input, expense.ValidationError, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return expense.Input{}, nil, err
	}
	if !utf8.Valid(data) {
		return expense.Input{}, nil, errors.New("invalid body")
	}

	var rawFields map[string]json.RawMessage
	err = json.Unmarshal(data, &rawFields)
	if err != nil || rawFields == nil {
		return expense.Input{}, nil, errors.New("invalid object")
	}

	fields := expense.ValidationError{}
	input := expense.Input{}
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
	return input, fields, nil
}
