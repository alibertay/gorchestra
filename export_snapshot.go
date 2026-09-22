package gorchestra

// Exported, kararlı snapshot (metrics ve dashboard için)
type PublicSnapshot struct {
	ID          uint64  `json:"id"`
	Name        string  `json:"name"`
	State       string  `json:"state"`
	Health      Health  `json:"health"`
	UptimeSec   float64 `json:"uptimeSeconds"`
	IdleSec     float64 `json:"idleSeconds"`
	BusySec     float64 `json:"busySeconds"`
	BlockedSec  float64 `json:"blockedSeconds"`
	BusyPercent float64 `json:"busyPercent"`
	// SupervisorState is empty for non-supervised routines.
	SupervisorState string `json:"supervisorState,omitempty"`
	QueueLen        int    `json:"queueLen"`
	QueueBytes      int64  `json:"queueBytes"`
	Restarts        uint64 `json:"restarts"`
}

func (o *Orchestrator) PublicSnapshots() []PublicSnapshot {
	rs := o.List()
	out := make([]PublicSnapshot, 0, len(rs))
	for _, r := range rs {
		s := snapshotOf(r)
		sup := ""
		if s.SupervisorState != SupervisorNone {
			sup = s.SupervisorState.String()
		}
		out = append(out, PublicSnapshot{
			ID:              s.ID,
			Name:            s.Name,
			State:           s.State.String(),
			Health:          s.Health,
			UptimeSec:       s.Uptime.Seconds(),
			IdleSec:         s.IdleFor.Seconds(),
			BusySec:         s.Busy.Seconds(),
			BlockedSec:      s.Blocked.Seconds(),
			BusyPercent:     s.BusyPercent,
			SupervisorState: sup,
			QueueLen:        s.QueueLen,
			QueueBytes:      s.QueueBytes,
			Restarts:        s.Restarts,
		})
	}
	return out
}
