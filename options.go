package gorchestra

import "time"

type RoutineOption func(*RoutineOptions)

type RoutineOptions struct {
	Name        string
	QueueCap    int
	IdleTimeout time.Duration // if no heartbeat for this duration -> cancel
}

func defaultRoutineOptions() RoutineOptions {
	return RoutineOptions{
		Name:     "",
		QueueCap: 128,
		// Disabled by default: a routine is only cancelled for going idle
		// when the caller explicitly opts in with WithIdleTimeout(d > 0).
		IdleTimeout: 0,
	}
}

func WithName(name string) RoutineOption {
	return func(o *RoutineOptions) { o.Name = name }
}

func WithQueueCap(n int) RoutineOption {
	return func(o *RoutineOptions) {
		if n < 0 {
			n = 0
		}
		o.QueueCap = n
	}
}

// WithIdleTimeout cancels the routine (StateTimedOut) when it stops calling
// Beat() for the given duration. Pass 0 (or a negative value) to disable the
// idle watchdog; it is disabled by default.
func WithIdleTimeout(d time.Duration) RoutineOption {
	return func(o *RoutineOptions) {
		if d < 0 {
			d = 0
		}
		o.IdleTimeout = d
	}
}
