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

// ErrIdleTimeout is returned by Routine.Wait when the routine was cancelled
// because it stopped sending heartbeats while an idle timeout was configured.
var ErrIdleTimeout = errors.New("gorchestra: idle timeout")

// Terminal reports whether the state is final. Terminal states never
// transition to another state.
func (s RoutineState) Terminal() bool {
	return s == StateStopped || s == StateTimedOut || s == StatePanicked
}

// canTransition reports whether from -> to is a legal lifecycle transition.
//
//	INIT     -> RUNNING | STOPPING | STOPPED | TIMED_OUT | PANICKED
//	RUNNING  -> STOPPING | STOPPED | TIMED_OUT | PANICKED
//	STOPPING -> STOPPED | TIMED_OUT | PANICKED
//	terminal -> (nothing)
func canTransition(from, to RoutineState) bool {
	if from.Terminal() {
		return false
	}
	switch from {
	case StateInit:
		return to != StateInit
	case StateRunning:
		return to != StateInit && to != StateRunning
	case StateStopping:
		return to != StateInit && to != StateRunning && to != StateStopping
	default:
		return false
	}
}

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
func (r *Routine) Beat()                    { r.lastBeatNs.Store(r.clock.Now().UnixNano()) }

// State returns the current lifecycle state. Terminal states
// (STOPPED, TIMED_OUT, PANICKED) are final.
func (r *Routine) State() RoutineState { return RoutineState(r.state.Load()) }

// Kill requests cancellation. It is a no-op on terminal routines: a routine
// that already finished (or timed out / panicked) keeps its final state.
func (r *Routine) Kill() {
	r.transition(StateStopping)
	r.cancel()
}

func (r *Routine) AddBusy(d time.Duration) {
	if statsEnabled {
		r.busyNs.Add(d.Nanoseconds())
	}
}
func (r *Routine) Wait() error { <-r.doneCh; r.errMu.Lock(); defer r.errMu.Unlock(); return r.err }

// transition atomically moves the routine to the given state. It returns
// false if the transition is not allowed (e.g. the routine is already in a
// terminal state).
func (r *Routine) transition(to RoutineState) bool {
	for {
		cur := r.State()
		if !canTransition(cur, to) {
			return false
		}
		if r.state.CompareAndSwap(int32(cur), int32(to)) {
			return true
		}
	}
}

func (r *Routine) finish(state RoutineState, err error) {
	r.doneOnce.Do(func() {
		if !r.transition(state) {
			// A terminal state (TIMED_OUT / PANICKED / STOPPED) won the race;
			// keep it. Surface the timeout even if the worker returned nil.
			if r.State() == StateTimedOut && err == nil {
				err = ErrIdleTimeout
			}
		}
		r.errMu.Lock()
		r.err = err
		r.errMu.Unlock()
		close(r.doneCh)
	})
}

func (r *Routine) run(fn func(ctx context.Context, self *Routine) error) {
	if !r.transition(StateRunning) {
		// Killed before the worker had a chance to start.
		r.finish(StateStopped, nil)
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			r.finish(StatePanicked, fmt.Errorf("panic: %v", rec))
		}
		// Cancel after the worker exits so the idle watchdog stops and the
		// routine context is released even on natural completion.
		r.cancel()
	}()

	if r.idleTimeout > 0 {
		go r.idleWatchdog()
	}

	err := fn(withRoutine(r.ctx, r), r)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if r.State() == StateTimedOut {
			err = ErrIdleTimeout
		} else {
			err = nil
		}
	}
	r.finish(StateStopped, err)
}

func (r *Routine) idleWatchdog() {
	interval := 500 * time.Millisecond
	if half := r.idleTimeout / 2; half < interval {
		interval = half
	}
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	t := r.clock.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
			last := time.Unix(0, r.lastBeatNs.Load())
			if r.clock.Since(last) > r.idleTimeout {
				if r.transition(StateTimedOut) {
					r.cancel()
				}
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
