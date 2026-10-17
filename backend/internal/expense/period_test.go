package expense

import (
	"errors"
	"testing"
	"time"
)

func TestDateRangeAt(t *testing.T) {
	tests := []struct {
		name      string
		zone      string
		reference string
		period    Period
		from      Date
		to        Date
	}{
		{"before Moscow midnight", "Europe/Moscow", "2026-10-16T20:59:59Z", PeriodToday, "2026-10-16", "2026-10-16"},
		{"Moscow midnight", "Europe/Moscow", "2026-10-16T21:00:00Z", PeriodToday, "2026-10-17", "2026-10-17"},
		{"configured zone", "Asia/Yekaterinburg", "2026-10-16T19:00:00Z", PeriodToday, "2026-10-17", "2026-10-17"},
		{"yesterday leap day", "Europe/Moscow", "2024-02-29T21:00:00Z", PeriodYesterday, "2024-02-29", "2024-02-29"},
		{"seven days cross year", "Europe/Moscow", "2025-12-31T21:00:00Z", PeriodLastSevenDays, "2025-12-26", "2026-01-01"},
		{"seven days leap year", "UTC", "2024-03-01T12:00:00Z", PeriodLastSevenDays, "2024-02-24", "2024-03-01"},
		{"Monday", "Europe/Moscow", "2026-10-19T10:00:00Z", PeriodThisWeek, "2026-10-19", "2026-10-19"},
		{"Sunday", "Europe/Moscow", "2026-10-18T10:00:00Z", PeriodThisWeek, "2026-10-12", "2026-10-18"},
		{"week cross year", "UTC", "2026-01-01T12:00:00Z", PeriodThisWeek, "2025-12-29", "2026-01-01"},
		{"month begins", "Europe/Moscow", "2026-10-31T21:00:00Z", PeriodThisMonth, "2026-11-01", "2026-11-01"},
		{"leap month", "UTC", "2024-02-29T23:00:00Z", PeriodThisMonth, "2024-02-01", "2024-02-29"},
		{"DST spring", "America/New_York", "2026-03-09T04:00:00Z", PeriodLastSevenDays, "2026-03-03", "2026-03-09"},
		{"DST autumn", "America/New_York", "2026-11-02T05:00:00Z", PeriodLastSevenDays, "2026-10-27", "2026-11-02"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			location, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			reference, err := time.Parse(time.RFC3339, tc.reference)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DateRangeAt(tc.period, reference, location)
			if err != nil {
				t.Fatal(err)
			}
			if got != (DateRange{From: tc.from, To: tc.to}) {
				t.Fatalf("want %s through %s, got %+v", tc.from, tc.to, got)
			}
		})
	}
}

func TestDateRangeAtRejectsInvalidInput(t *testing.T) {
	if _, err := DateRangeAt(PeriodToday, time.Now(), nil); !errors.Is(err, ErrInvalidTimeZone) {
		t.Fatalf("nil location: %v", err)
	}
	if _, err := DateRangeAt("unknown", time.Now(), time.UTC); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("unknown period: %v", err)
	}
	for _, reference := range []time.Time{
		time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC),
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		if _, err := DateRangeAt(PeriodLastSevenDays, reference, time.UTC); !errors.Is(err, ErrInvalidDate) {
			t.Fatalf("out-of-range calendar date: %v", err)
		}
	}
}
