package internalutil

import "sync/atomic"

type IDGen struct {
	n atomic.Uint64
}

func (g *IDGen) Next() uint64 { return g.n.Add(1) }
