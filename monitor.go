package gorchestra

import (
	"fmt"
	"io"
	"math"
	"text/tabwriter"
	"time"
)

type Health string

const (
	HealthOK       Health = "OK"
	HealthIdle     Health = "IDLE_TIMEOUT"
	HealthStopping Health = "STOPPING"
	HealthPanic    Health = "PANIC"
)

type Snapshot struct {
	ID            uint64
	Name          string
	State         RoutineState
	Health        Health
	Uptime        time.Duration
	IdleFor       time.Duration
	ActivePercent float64 // estimated
	QueueLen      int
	QueueCap      int
	QueueBytes    int64
	Restarts      uint64
	Err           string
}

func snapshotOf(r *Routine) Snapshot {
	now := time.Now()
	uptime := now.Sub(r.createdAt)
	if uptime < time.Nanosecond {
		uptime = time.Nanosecond
	}
	last := time.Unix(0, r.lastBeatNs.Load())
	idle := now.Sub(last)

	// Active% ≈ (busy + (uptime - blocked)) / uptime
	blocked := time.Duration(r.blockedNs.Load())
	busy := time.Duration(r.busyNs.Load())
	active := busy + (uptime - blocked)
	if active < 0 {
		active = 0
	}
	est := (float64(active) / float64(uptime)) * 100
	if est < 0 {
		est = 0
	}
	if est > 100 {
		est = 100
	}

	state := RoutineState(r.state.Load())
	health := HealthOK
	switch state {
	case StateTimedOut:
		health = HealthIdle
	case StateStopping:
		health = HealthStopping
	case StatePanicked:
		health = HealthPanic
	}

	r.errMu.Lock()
	errStr := ""
	if r.err != nil {
		errStr = r.err.Error()
	}
	r.errMu.Unlock()

	chst := r.mbox.Stats()

	return Snapshot{
		ID:            r.id,
		Name:          r.name,
		State:         state,
		Health:        health,
		Uptime:        uptime,
		IdleFor:       idle,
		ActivePercent: math.Round(est*10) / 10,
		QueueLen:      chst.Len,
		QueueCap:      chst.Cap,
		QueueBytes:    chst.ApproxBytes,
		Restarts:      r.Restarts(),
		Err:           errStr,
	}
}

func (o *Orchestrator) PrintStats(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tNAME\tSTATE\tHEALTH\tUPTIME\tIDLE\tACTIVE%%\tQ(LEN/CAP)\tQ(BYTES)\tRESTARTS\tERR\n")
	for _, r := range o.List() {
		s := snapshotOf(r)
		fmt.Fprintf(
			tw,
			"%d\t%s\t%s\t%s\t%s\t%s\t%.1f\t%d/%d\t%d\t%d\t%s\n",
			s.ID, nonEmpty(s.Name), s.State.String(), s.Health, dur(s.Uptime), dur(s.IdleFor), s.ActivePercent,
			s.QueueLen, s.QueueCap, s.QueueBytes, s.Restarts, s.Err,
		)
	}
	_ = tw.Flush()
}

func nonEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func dur(d time.Duration) string {
	if d > time.Hour {
		return (d - d%time.Second).String()
	}
	if d > time.Minute {
		return (d - d%time.Millisecond).String()
	}
	return d.Truncate(time.Millisecond).String()
}
