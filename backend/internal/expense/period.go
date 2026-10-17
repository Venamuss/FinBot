package expense

import "time"

type Period string

const (
	PeriodToday         Period = "today"
	PeriodYesterday     Period = "yesterday"
	PeriodLastSevenDays Period = "last_seven_days"
	PeriodThisWeek      Period = "this_week"
	PeriodThisMonth     Period = "this_month"
)

type DateRange struct {
	From Date
	To   Date
}

// DateRangeAt uses the supplied reference instant and configured location,
// never the wall clock. Calendar arithmetic preserves dates across DST changes.
func DateRangeAt(period Period, reference time.Time, location *time.Location) (DateRange, error) {
	if location == nil {
		return DateRange{}, ErrInvalidTimeZone
	}
	local := reference.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)

	from, to := today, today

	switch period {
	case PeriodToday:
	case PeriodYesterday:
		from = today.AddDate(0, 0, -1)
		to = from
	case PeriodLastSevenDays:
		from = today.AddDate(0, 0, -6)
	case PeriodThisWeek:
		daysSinceMonday := (int(today.Weekday()) + 6) % 7
		from = today.AddDate(0, 0, -daysSinceMonday)
	case PeriodThisMonth:
		from = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, location)
	default:
		return DateRange{}, ErrInvalidPeriod

	}

	dateFrom, err := ParseDate(from.Format(time.DateOnly))
	if err != nil {
		return DateRange{}, err
	}

	dateTo, err := ParseDate(to.Format(time.DateOnly))
	if err != nil {
		return DateRange{}, err
	}

	return DateRange{From: dateFrom, To: dateTo}, nil
}
