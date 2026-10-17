package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Venamuss/FinBot/internal/expense"
)

type memberJSON struct {
	UserID      string  `json:"user_id"`
	DisplayName *string `json:"display_name"`
}

func representMember(member expense.Member) memberJSON {
	return memberJSON{
		UserID:      strconv.FormatInt(member.UserID, 10),
		DisplayName: member.DisplayName,
	}
}

func (h *Handler) setMemberName(w http.ResponseWriter, r *http.Request) {
	rawFields, ok := readObject(w, r)
	if !ok {
		return
	}
	fields := expense.ValidationError{}
	for key := range rawFields {
		if key != "display_name" {
			fields[key] = "Неизвестное или системное поле"
		}
	}
	var displayName *string
	rawName, present := rawFields["display_name"]
	if !present {
		fields["display_name"] = "Укажите имя или null"
	} else if err := json.Unmarshal(rawName, &displayName); err != nil {
		fields["display_name"] = "Ожидается строка или null"
	}
	if len(fields) != 0 {
		operationError(w, fields)
		return
	}
	member, err := h.service.SetMemberName(r.Context(), r.PathValue("user_id"), displayName)
	if err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, representMember(member))
}
