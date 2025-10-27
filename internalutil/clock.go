package internalutil

import "time"

// Small indirection to make time testable/mokable.
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	NewTicker(d time.Duration) *time.Ticker
	After(d time.Duration) <-chan time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (RealClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
