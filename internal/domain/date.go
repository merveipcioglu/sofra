package domain

import "time"

var Istanbul = time.FixedZone("Europe/Istanbul", 3*60*60)

const dateLayout = "2006-01-02"

func ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, s, Istanbul)
}


func FormatDate(t time.Time) string {
	return t.In(Istanbul).Format(dateLayout)
}

func DaysBetween(from, to time.Time) int {
	return int(civilDate(to).Sub(civilDate(from)).Hours() / 24)
}

func civilDate(t time.Time) time.Time {
	y, m, d := t.In(Istanbul).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
