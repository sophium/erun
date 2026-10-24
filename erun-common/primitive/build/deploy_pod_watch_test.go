package build

import (
	"time"
)

// fakeClock lets a test move time forward in arbitrary jumps instead of
// sleeping real wall-clock time, so the grace period's boundary is exercised
// deterministically.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}
