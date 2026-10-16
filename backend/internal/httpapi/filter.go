package httpapi

import (
	"net/url"
	"strconv"

	"github.com/Venamuss/FinBot/internal/expense"
)

func parseFilter(query url.Values) (expense.Filter, error) {
	filter := expense.Filter{
		Limit: 50,
		Sort:  "spent_at",
		Order: "desc",
	}
	fields := expense.ValidationError{}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			fields[key] = expense.ErrInvalidQueryValue.Error()
			continue
		}
		value := values[0]
		var err error
		switch key {
		case "user_id":
			filter.UserID, err = expense.ParseID(value)
		case "date_from":
			filter.DateFrom, err = expense.ParseDate(value)
		case "date_to":
			filter.DateTo, err = expense.ParseDate(value)
		case "category":
			filter.Category, err = expense.ParseCategory(value)
		case "limit":
			filter.Limit, err = parsePaginationValue(value)
			if err == nil && (filter.Limit < 1 || filter.Limit > 100) {
				err = expense.ErrInvalidPagination
			}
		case "offset":
			filter.Offset, err = parsePaginationValue(value)
		case "sort":
			switch value {
			case "spent_at", "created_at", "amount", "id":
				filter.Sort = value
			default:
				err = expense.ErrInvalidSort
			}
		case "order":
			if value == "asc" || value == "desc" {
				filter.Order = value
			} else {
				err = expense.ErrInvalidOrder
			}
		default:
			err = expense.ErrUnknownParameter
		}
		if err != nil {
			fields[key] = err.Error()
		}
	}
	if filter.DateFrom != "" && filter.DateTo != "" && filter.DateFrom > filter.DateTo {
		fields["date_to"] = expense.ErrInvalidDateRange.Error()
	}
	if len(fields) != 0 {
		return expense.Filter{}, fields
	}
	return filter, nil
}

func parsePaginationValue(value string) (int, error) {
	// Only decimal digits are accepted: signs and exponent notation are invalid.
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, expense.ErrInvalidPagination
		}
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, expense.ErrInvalidPagination
	}
	return int(number), nil
}
