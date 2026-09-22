package gorchestra

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBus_ConcurrentTopicReturnsSameChannel(t *testing.T) {
	bus := NewBus[int]()
	const goroutines = 64

	chans := make([]*Channel[int], goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chans[i] = bus.Topic("orders", 16)
		}(i)
	}
	wg.Wait()

	for i := 1; i < goroutines; i++ {
		if chans[i] != chans[0] {
			t.Fatalf("goroutine %d got a different channel instance", i)
		}
	}
	if got := bus.Topics(); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("expected a single topic named orders, got %v", got)
	}
}

func TestBus_TopicCapacityFromFirstCaller(t *testing.T) {
	bus := NewBus[int]()
	first := bus.Topic("prices", 32)
	second := bus.Topic("prices", 4096)

	if first != second {
		t.Fatal("expected the same channel for repeated Topic calls")
	}
	if got := second.Cap(); got != 32 {
		t.Fatalf("expected capacity of the first caller (32), got %d", got)
	}
}

func TestChannel_ConcurrentSendRecv(t *testing.T) {
	const (
		producers = 4
		perProd   = 250
		total     = producers * perProd
	)

	ch := NewChannel[int](64)

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProd; i++ {
				if err := ch.Send(context.Background(), i); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}()
	}

	recvd := 0
	for recvd < total {
		if _, err := ch.Recv(context.Background()); err != nil {
			t.Fatalf("recv: %v", err)
		}
		recvd++
	}
	wg.Wait()

	st := ch.Stats()
	if st.Sends != total || st.Recvs != total {
		t.Fatalf("expected %d sends and recvs, got %d/%d", total, st.Sends, st.Recvs)
	}
	if st.Len != 0 {
		t.Fatalf("expected empty queue, got len=%d", st.Len)
	}
}

func TestChannel_FastPathRecordsNoBlockedTime(t *testing.T) {
	const n = 1000
	ch := NewChannel[int](n)

	for i := 0; i < n; i++ {
		if err := ch.Send(context.Background(), i); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	for i := 0; i < n; i++ {
		if _, err := ch.Recv(context.Background()); err != nil {
			t.Fatalf("recv %d: %v", i, err)
		}
	}

	st := ch.Stats()
	if st.BlockedSendNs != 0 {
		t.Fatalf("non-blocking sends must not record blocked time, got %dns", st.BlockedSendNs)
	}
	if st.BlockedRecvNs != 0 {
		t.Fatalf("non-blocking recvs must not record blocked time, got %dns", st.BlockedRecvNs)
	}
	if st.Sends != n || st.Recvs != n {
		t.Fatalf("expected %d sends/recvs, got %d/%d", n, st.Sends, st.Recvs)
	}
}

func TestChannel_BlockedTimeRecordedOnSlowPath(t *testing.T) {
	ch := NewChannel[int](0)

	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = ch.Recv(context.Background())
	}()

	if err := ch.Send(context.Background(), 1); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := ch.Stats().BlockedSendNs; got < int64(10*time.Millisecond) {
		t.Fatalf("expected a meaningful blocked time, got %dns", got)
	}
}

func TestChannel_CancelledContextFailsEvenWithBufferSpace(t *testing.T) {
	ch := NewChannel[int](8)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := ch.Send(ctx, 1); err == nil {
		t.Fatal("expected context error on a cancelled context")
	}
	if _, err := ch.Recv(ctx); err == nil {
		t.Fatal("expected context error on a cancelled context")
	}
	if got := ch.Stats(); got.Sends != 0 || got.Recvs != 0 || got.Len != 0 {
		t.Fatalf("cancelled operations must not touch the channel: %+v", got)
	}
}

func TestChannel_SendRespectsContextCancel(t *testing.T) {
	ch := NewChannel[int](0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := ch.Send(ctx, 1); err == nil {
		t.Fatal("expected context deadline error on full unbuffered channel")
	}
	if got := ch.Stats().Sends; got != 0 {
		t.Fatalf("cancelled send must not be counted, got %d", got)
	}
}
