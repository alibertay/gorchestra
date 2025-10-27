package gorchestra

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alibertay/gorchestra/internalutil"
)

// Ergonomik alias
type Context = context.Context

type RoutineState int32

var statsEnabled = true

const (
	StateInit RoutineState = iota
	StateRunning
	StateStopping
	StateTimedOut
	StatePanicked
	StateStopped
)

func (s RoutineState) String() string {
	switch s {
	case StateInit:
		return "INIT"
	case StateRunning:
		return "RUNNING"
	case StateStopping:
		return "STOPPING"
	case StateTimedOut:
		return "TIMED_OUT"
	case StatePanicked:
		return "PANICKED"
	case StateStopped:
		return "STOPPED"
	default:
		return fmt.Sprintf("STATE(%d)", int(s))
	}
}

type Routine struct {
	id         uint64
	name       string
	createdAt  time.Time
	lastBeatNs atomic.Int64 // unix ns
	state      atomic.Int32

	ctx    context.Context
	cancel context.CancelFunc

	mbox *Channel[any]

	// Ölçümler:
	blockedNs atomic.Int64 // channel bekleme süreleri
	busyNs    atomic.Int64 // kullanıcı enstrümantasyonuyla toplanan iş süresi

	restarts atomic.Uint64

	doneOnce sync.Once
	doneCh   chan struct{}
	errMu    sync.Mutex
	err      error

	// config
	idleTimeout time.Duration
	clock       internalutil.Clock
}

func newRoutine(id uint64, name string, idle time.Duration, queueCap int, clock internalutil.Clock) *Routine {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Routine{
		id:          id,
		name:        name,
		createdAt:   clock.Now(),
		ctx:         ctx,
		cancel:      cancel,
		mbox:        NewChannel[any](queueCap),
		doneCh:      make(chan struct{}),
		idleTimeout: idle,
		clock:       clock,
	}
	r.state.Store(int32(StateInit))
	r.Beat()
	return r
}

func (r *Routine) ID() uint64               { return r.id }
func (r *Routine) Name() string             { return r.name }
func (r *Routine) Context() context.Context { return withRoutine(r.ctx, r) }
func (r *Routine) Mailbox() *Channel[any]   { return r.mbox }
func (r *Routine) Restarts() uint64         { return r.restarts.Load() }
func (r *Routine) incrementRestarts()       { r.restarts.Add(1) }
func (r *Routine) Beat()                    { r.lastBeatNs.Store(time.Now().UnixNano()) }
func (r *Routine) Kill()                    { r.cancel(); r.state.Store(int32(StateStopping)) }
func (r *Routine) AddBusy(d time.Duration) {
	if statsEnabled {
		r.busyNs.Add(d.Nanoseconds())
	}
}
func (r *Routine) Wait() error { <-r.doneCh; r.errMu.Lock(); defer r.errMu.Unlock(); return r.err }

func (r *Routine) finish(state RoutineState, err error) {
	r.doneOnce.Do(func() {
		if state != StateStopped && state != StateStopping && state != StateTimedOut && state != StatePanicked {
			state = StateStopped
		}
		r.state.Store(int32(state))
		r.errMu.Lock()
		r.err = err
		r.errMu.Unlock()
		close(r.doneCh)
	})
}

func (r *Routine) run(fn func(ctx context.Context, self *Routine) error) {
	r.state.Store(int32(StateRunning))
	defer func() {
		if rec := recover(); rec != nil {
			r.finish(StatePanicked, fmt.Errorf("panic: %v", rec))
		}
	}()

	go r.idleWatchdog()

	err := fn(withRoutine(r.ctx, r), r)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		r.finish(StateStopped, nil)
		return
	}
	r.finish(StateStopped, err)
}

func (r *Routine) idleWatchdog() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
			last := time.Unix(0, r.lastBeatNs.Load())
			if r.clock.Since(last) > r.idleTimeout {
				r.state.Store(int32(StateTimedOut))
				r.cancel()
				return
			}
		}
	}
}

// ==== ctx metadata & ölçüm yardımcıları ====

type routineKey struct{}

func withRoutine(ctx context.Context, r *Routine) context.Context {
	return context.WithValue(ctx, routineKey{}, r)
}

func routineFromCtx(ctx context.Context) (*Routine, bool) {
	v := ctx.Value(routineKey{})
	if v == nil {
		return nil, false
	}
	rr, ok := v.(*Routine)
	return rr, ok
}

// channel bekleme süresini (blocked) Routine'a yaz
func addBlocked(ctx context.Context, d time.Duration) {
	if !statsEnabled {
		return
	}
	if rr, ok := routineFromCtx(ctx); ok {
		rr.blockedNs.Add(d.Nanoseconds())
	}
}
