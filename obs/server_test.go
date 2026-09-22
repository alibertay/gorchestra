package obs

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/goleak"

	g "github.com/alibertay/gorchestra"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newTestClient() *http.Client {
	return &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
}

func waitForAddr(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Addr() != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server never started listening")
}

func get(t *testing.T, s *Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := newTestClient().Get("http://" + s.Addr() + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(body)
}

func TestServer_StartBlockingAndIdempotentStop(t *testing.T) {
	s := NewServer(g.New(), WithAddr("127.0.0.1:0"))

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start() }()
	waitForAddr(t, s)

	resp, body := get(t, s, "/healthz")
	if resp.StatusCode != http.StatusOK || body != "ok" {
		t.Fatalf("unexpected /healthz response: %d %q", resp.StatusCode, body)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("blocking Start returned %v, want nil after Stop", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop must be a no-op, got %v", err)
	}
}

func TestServer_StartAsync_BindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := NewServer(g.New(), WithAddr(ln.Addr().String()))
	if err := s.StartAsync(); err == nil {
		t.Fatal("expected a bind error for an address that is already in use")
	}
}

func TestServer_DashboardDisabled(t *testing.T) {
	s := NewServer(g.New(), WithAddr("127.0.0.1:0"), WithDashboard(false))
	if err := s.StartAsync(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, _ := get(t, s, "/gorchestra")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled dashboard must return 404, got %d", resp.StatusCode)
	}
	resp, _ = get(t, s, "/gorchestra/snapshots")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled dashboard JSON must return 404, got %d", resp.StatusCode)
	}
}

func TestServer_DashboardEnabled(t *testing.T) {
	s := NewServer(g.New(), WithAddr("127.0.0.1:0"), WithDashboard(true))
	if err := s.StartAsync(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, body := get(t, s, "/gorchestra")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from dashboard, got %d", resp.StatusCode)
	}
	if len(body) == 0 {
		t.Fatal("dashboard body must not be empty")
	}
}

func TestServer_PProfDisabledByDefault(t *testing.T) {
	opt := defaults()
	if opt.EnablePProf {
		t.Fatal("pprof must be disabled by default")
	}
	if opt.Addr != "127.0.0.1:9090" {
		t.Fatalf("unexpected default address %q", opt.Addr)
	}

	s := NewServer(g.New(), WithAddr("127.0.0.1:0"))
	if err := s.StartAsync(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, _ := get(t, s, "/debug/pprof/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pprof must be unmounted by default, got %d", resp.StatusCode)
	}
}

func TestServer_PProfEnabled(t *testing.T) {
	s := NewServer(g.New(), WithAddr("127.0.0.1:0"), WithPProf(true))
	if err := s.StartAsync(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, _ := get(t, s, "/debug/pprof/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from pprof index, got %d", resp.StatusCode)
	}
}
