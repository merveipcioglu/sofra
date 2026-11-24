package clock

import (
	"fmt"
	"time"

	"sofra/internal/domain"
)

const defaultNow = "2026-08-20T12:00:00+03:00"

func ParseReference(value string) (time.Time, error) {
	if value == "" {
		value = defaultNow
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(domain.Istanbul), nil
	}
	day, err := domain.ParseDate(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("reference now %q: want RFC 3339 or YYYY-MM-DD", value)
	}
	return day.Add(12 * time.Hour), nil
}
