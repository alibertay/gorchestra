package gorchestra

import (
	"context"
	"sync/atomic"
	"time"
)

type Sizer interface{ SizeBytes() int }

type Channel[T any] struct {
	ch               chan T
	capacity         int
	sends            atomic.Uint64
	recvs            atomic.Uint64
	blockedSendNanos atomic.Int64
	blockedRecvNanos atomic.Int64
	approxBytes      atomic.Int64
}

func NewChannel[T any](capacity int) *Channel[T] {
	if capacity < 0 {
		capacity = 0
	}
	return &Channel[T]{ch: make(chan T, capacity), capacity: capacity}
}

func (c *Channel[T]) Send(ctx context.Context, v T) error {
	if !statsEnabled {
		select {
		case c.ch <- v:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	start := time.Now()
	select {
	case c.ch <- v:
	case <-ctx.Done():
		waited := time.Since(start)
		c.blockedSendNanos.Add(waited.Nanoseconds())
		addBlocked(ctx, waited)
		return ctx.Err()
	}
	waited := time.Since(start)
	c.blockedSendNanos.Add(waited.Nanoseconds())
	addBlocked(ctx, waited)
	c.sends.Add(1)
	if sz := sizeOf(v); sz > 0 {
		c.approxBytes.Add(int64(sz))
	}
	return nil
}

func (c *Channel[T]) Recv(ctx context.Context) (T, error) {
	var zero T
	if !statsEnabled {
		select {
		case v := <-c.ch:
			return v, nil
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	start := time.Now()
	select {
	case v := <-c.ch:
		waited := time.Since(start)
		c.blockedRecvNanos.Add(waited.Nanoseconds())
		addBlocked(ctx, waited)
		c.recvs.Add(1)
		if sz := sizeOf(v); sz > 0 {
			c.approxBytes.Add(-int64(sz))
		}
		return v, nil
	case <-ctx.Done():
		waited := time.Since(start)
		c.blockedRecvNanos.Add(waited.Nanoseconds())
		addBlocked(ctx, waited)
		return zero, ctx.Err()
	}
}

func (c *Channel[T]) Len() int { return len(c.ch) }
func (c *Channel[T]) Cap() int { return c.capacity }

type ChannelStats struct {
	Len, Cap      int
	Sends, Recvs  uint64
	BlockedSendNs int64
	BlockedRecvNs int64
	ApproxBytes   int64
}

func (c *Channel[T]) Stats() ChannelStats {
	if !statsEnabled {
		return ChannelStats{Len: len(c.ch), Cap: c.capacity}
	}
	return ChannelStats{
		Len:           len(c.ch),
		Cap:           c.capacity,
		Sends:         c.sends.Load(),
		Recvs:         c.recvs.Load(),
		BlockedSendNs: c.blockedSendNanos.Load(),
		BlockedRecvNs: c.blockedRecvNanos.Load(),
		ApproxBytes:   c.approxBytes.Load(),
	}
}

func sizeOf[T any](v T) int {
	switch any(v).(type) {
	case []byte:
		return len(any(v).([]byte))
	case string:
		return len(any(v).(string))
	case Sizer:
		return any(v).(Sizer).SizeBytes()
	default:
		return 0
	}
}

// Bus (aynı)
type Bus[T any] struct {
	lookup map[string]*Channel[T]
}

func NewBus[T any]() *Bus[T] { return &Bus[T]{lookup: make(map[string]*Channel[T])} }

func (b *Bus[T]) Topic(name string, capacity int) *Channel[T] {
	if ch, ok := b.lookup[name]; ok {
		return ch
	}
	ch := NewChannel[T](capacity)
	b.lookup[name] = ch
	return ch
}
