package domain

import (
	"errors"
	"math"
	"testing"
)

func TestTimes(t *testing.T) {
	cheeseburger := tl(195)
	cases := []struct {
		name     string
		m        Money
		n        int
		want     Money
		overflow bool
	}{
		{"an order line", cheeseburger, 2, tl(390), false},
		{"zero", cheeseburger, 0, 0, false},
		{"wraps to 400 TL", cheeseburger, 2246718829490265904, 0, true},
		{"just past the range", 2, math.MaxInt64/2 + 1, 0, true},
		{"most negative × -1", math.MinInt64, -1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.m.Times(c.n)
			if c.overflow {
				if !errors.Is(err, ErrOverflow) {
					t.Errorf("got %v, %v; want an overflow", got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("got %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestParseTL(t *testing.T) {
	ok := []struct {
		in   float64
		want Money
	}{{20, 2000}, {20.1, 2010}, {0.01, 1}, {-20, -2000}, {0, 0}}
	for _, c := range ok {
		if got, err := ParseTL(c.in); err != nil || got != c.want {
			t.Errorf("ParseTL(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []float64{20.005, 0.001, math.NaN(), math.Inf(1), math.Inf(-1), 1e13, -1e13, 1e20} {
		if got, err := ParseTL(bad); err == nil {
			t.Errorf("ParseTL(%v) = %v, want refused", bad, got)
		}
	}
}

func tl(lira float64) Money { return Money(math.Round(lira * 100)) }
