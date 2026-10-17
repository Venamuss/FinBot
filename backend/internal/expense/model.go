package expense

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxAmount Amount = 99_999_999_999_999

var (
	ErrNotFound              = errors.New("expense not found")
	ErrInvalidAmount         = errors.New("Ожидается положительная сумма с точностью до сотых")
	ErrInvalidAmountWhole    = errors.New("Некорректная целая часть суммы")
	ErrInvalidAmountFraction = errors.New("Некорректная дробная часть суммы")
	ErrAmountOutOfRange      = errors.New("Сумма должна быть от 0.01 до 999999999999.99")
	ErrInvalidID             = errors.New("Ожидается строка ID от 1 до 9223372036854775807 без ведущих нулей")
	ErrInvalidDate           = errors.New("Ожидается существующая дата YYYY-MM-DD с годом 0001–9999")
	ErrInvalidCategory       = errors.New("Неизвестная категория")
	ErrValidation            = errors.New("Некорректные данные")
	ErrInvalidComment        = errors.New("Нулевой символ не поддерживается")
	ErrInvalidDisplayName    = errors.New("Имя должно содержать не более 100 символов без управляющих символов")
	ErrInvalidQueryValue     = errors.New("Нужно одно непустое значение")
	ErrInvalidPagination     = errors.New("Некорректная пагинация")
	ErrInvalidSort           = errors.New("Неизвестное поле сортировки")
	ErrInvalidOrder          = errors.New("Ожидается asc или desc")
	ErrUnknownParameter      = errors.New("Неизвестный параметр")
	ErrInvalidDateRange      = errors.New("Конец периода раньше начала")
	ErrInvalidPeriod         = errors.New("Неизвестный календарный период")
	ErrInvalidTimeZone       = errors.New("Не задан часовой пояс")
	ErrInvalidVersion        = errors.New("Не задана версия выбранной траты")
)

// Amount stores hundredths; no floating point conversion is used.
type Amount int64

var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?$`)
var idPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var datePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

func ParseAmount(value string) (Amount, error) {
	if !amountPattern.MatchString(value) {
		return 0, ErrInvalidAmount
	}
	whole, fraction, _ := strings.Cut(value, ".")
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, ErrInvalidAmountWhole
	}
	var cents int64
	if fraction != "" {
		if len(fraction) == 1 {
			fraction += "0"
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidAmountFraction
		}
	}
	amount := Amount(units*100 + cents)
	if amount <= 0 || amount > MaxAmount {
		return 0, ErrAmountOutOfRange
	}
	return amount, nil
}

func (amount Amount) String() string {
	return fmt.Sprintf("%d.%02d", amount/100, amount%100)
}

func ParseID(value string) (int64, error) {
	if idPattern.MatchString(value) {
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return id, nil
		}
	}
	return 0, ErrInvalidID
}

// Date is a validated calendar date, independent of the server time zone.
type Date string

func ParseDate(value string) (Date, error) {
	if datePattern.MatchString(value) && value[:4] != "0000" {
		if _, err := time.Parse(time.DateOnly, value); err == nil {
			return Date(value), nil
		}
	}
	return "", ErrInvalidDate
}

type Category string

const (
	CategoryFood          Category = "еда"
	CategoryTransport     Category = "транспорт"
	CategoryHousing       Category = "жильё"
	CategoryEntertainment Category = "развлечения"
	CategoryHealth        Category = "здоровье"
	CategoryShopping      Category = "покупки"
	CategoryOther         Category = "другое"
)

func ParseCategory(value string) (Category, error) {
	category := Category(value)
	switch category {
	case CategoryFood,
		CategoryTransport,
		CategoryHousing,
		CategoryEntertainment,
		CategoryHealth,
		CategoryShopping,
		CategoryOther:
		return category, nil
	default:
		return "", ErrInvalidCategory
	}
}

type Expense struct {
	ID        int64
	UserID    int64
	Amount    Amount
	Category  Category
	SpentAt   Date
	Comment   *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Input struct {
	UserID   string
	Amount   string
	Category string
	SpentAt  string
	Comment  *string
}

type ValidationError map[string]string

func (ValidationError) Error() string {
	return ErrValidation.Error()
}

func (input Input) Validate() (Expense, error) {
	var result Expense
	fields := ValidationError{}
	var err error

	result.UserID, err = ParseID(input.UserID)
	if err != nil {
		fields["user_id"] = err.Error()
	}
	result.Amount, err = ParseAmount(input.Amount)
	if err != nil {
		fields["amount"] = err.Error()
	}
	result.Category, err = ParseCategory(input.Category)
	if err != nil {
		fields["category"] = err.Error()
	}
	result.SpentAt, err = ParseDate(input.SpentAt)
	if err != nil {
		fields["spent_at"] = err.Error()
	}
	if input.Comment != nil && strings.ContainsRune(*input.Comment, 0) {
		fields["comment"] = ErrInvalidComment.Error()
	}
	if len(fields) != 0 {
		return Expense{}, fields
	}
	result.Comment = input.Comment
	return result, nil
}
