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
		Name:        "",
		QueueCap:    128,
		IdleTimeout: 5 * time.Second,
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

func WithIdleTimeout(d time.Duration) RoutineOption {
	return func(o *RoutineOptions) {
		if d <= 0 {
			return
		}
		o.IdleTimeout = d
	}
}
