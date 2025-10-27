package gorchestra

// Exported, kararlı snapshot (metrics ve dashboard için)
type PublicSnapshot struct {
	ID         uint64 `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Health     Health `json:"health"`
	QueueLen   int    `json:"queueLen"`
	QueueBytes int64  `json:"queueBytes"`
	Restarts   uint64 `json:"restarts"`
}

func (o *Orchestrator) PublicSnapshots() []PublicSnapshot {
	rs := o.List()
	out := make([]PublicSnapshot, 0, len(rs))
	for _, r := range rs {
		s := snapshotOf(r)
		out = append(out, PublicSnapshot{
			ID:         s.ID,
			Name:       s.Name,
			State:      s.State.String(),
			Health:     s.Health,
			QueueLen:   s.QueueLen,
			QueueBytes: s.QueueBytes,
			Restarts:   s.Restarts,
		})
	}
	return out
}
