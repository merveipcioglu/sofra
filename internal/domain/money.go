package domain

import (
	"errors"
	"fmt"
	"math"
)

// Money is an amount in kuruş
type Money int64

const maxTL = 1e12

func ParseTL(lira float64) (Money, error) {
	if math.IsNaN(lira) || math.IsInf(lira, 0) || math.Abs(lira) > maxTL {
		return 0, fmt.Errorf("amount %v TL is out of range", lira)
	}
	kurus := lira * 100
	if math.Abs(kurus-math.Round(kurus)) > 1e-6 {
		return 0, fmt.Errorf("amount %v TL is finer than a kuruş", lira)
	}
	return Money(math.Round(kurus)), nil
}

var ErrOverflow = errors.New("amount overflows")

func (m Money) Times(n int) (Money, error) {
	if n == 0 || m == 0 {
		return 0, nil
	}
	p := m * Money(n)
	if p/Money(n) != m || (m == -1 && n == math.MinInt64) || (n == -1 && m == math.MinInt64) {
		return 0, fmt.Errorf("%v × %d: %w", m, n, ErrOverflow)
	}
	return p, nil
}

func (m Money) TL() float64 { return float64(m) / 100 }

func (m Money) String() string {
	sign, kurus := "", int64(m)
	if kurus < 0 {
		sign, kurus = "-", -kurus
	}
	return fmt.Sprintf("%s%d.%02d TL", sign, kurus/100, kurus%100)
}
