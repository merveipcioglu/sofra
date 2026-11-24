package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Fixed struct {
	mu  sync.Mutex
	now time.Time
}

func NewFixed(now time.Time) *Fixed {
	return &Fixed{now: now}
}

func (c *Fixed) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Fixed) Advance(d time.Duration) {
	if d < 0 {
		panic("clock: Advance with a negative duration")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type Running struct {
	ref   time.Time
	start time.Time
}

func NewRunning(ref time.Time) *Running {
	return &Running{ref: ref, start: time.Now()}
}

func (c *Running) Now() time.Time {
	return c.ref.Add(time.Since(c.start))
}
