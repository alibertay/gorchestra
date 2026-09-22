package obs

import (
	"context"
	"testing"
	"time"

	g "github.com/alibertay/gorchestra"
)

func TestWithSampleEvery_RejectsNonPositive(t *testing.T) {
	def := defaults()

	for _, d := range []time.Duration{0, -time.Second, -time.Nanosecond} {
		opt := defaults()
		WithCPUSampleEvery(d)(&opt)
		if opt.CPUSampleInterval != def.CPUSampleInterval {
			t.Fatalf("WithCPUSampleEvery(%v) must keep the default %v, got %v",
				d, def.CPUSampleInterval, opt.CPUSampleInterval)
		}

		opt = defaults()
		WithTopicSampleEvery(d)(&opt)
		if opt.TopicSampleInterval != def.TopicSampleInterval {
			t.Fatalf("WithTopicSampleEvery(%v) must keep the default %v, got %v",
				d, def.TopicSampleInterval, opt.TopicSampleInterval)
		}
	}
}

func TestWithSampleEvery_AcceptsPositive(t *testing.T) {
	opt := defaults()
	WithCPUSampleEvery(250 * time.Millisecond)(&opt)
	WithTopicSampleEvery(2 * time.Second)(&opt)

	if opt.CPUSampleInterval != 250*time.Millisecond {
		t.Fatalf("unexpected CPU interval %v", opt.CPUSampleInterval)
	}
	if opt.TopicSampleInterval != 2*time.Second {
		t.Fatalf("unexpected topic interval %v", opt.TopicSampleInterval)
	}
}

func TestServer_InvalidIntervalsDoNotPanic(t *testing.T) {
	s := NewServer(g.New(), WithAddr("127.0.0.1:0"), WithCPUSampleEvery(0), WithTopicSampleEvery(-1))

	if err := s.StartAsync(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
