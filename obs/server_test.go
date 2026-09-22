package obs

import (
	"context"
	"encoding/json"
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

	for _, path := range []string{
		"/gorchestra",
		"/gorchestra/snapshots",
		"/gorchestra/history",
		"/gorchestra/terminals",
	} {
		resp, _ := get(t, s, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("disabled dashboard: %s must return 404, got %d", path, resp.StatusCode)
		}
	}
}

func TestServer_HistoryAndTerminalsEndpoints(t *testing.T) {
	o := g.New()
	r := o.Go(func(ctx context.Context, self *g.Routine) error { return nil }, g.WithName("worker"))
	if err := r.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	s := NewServer(o, WithAddr("127.0.0.1:0"))
	if err := s.StartAsync(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, body := get(t, s, "/gorchestra/history")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history status: %d", resp.StatusCode)
	}
	var hist []map[string]any
	if err := json.Unmarshal([]byte(body), &hist); err != nil {
		t.Fatalf("history json: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(hist))
	}
	if hist[0]["name"] != "worker" || hist[0]["state"] != "STOPPED" {
		t.Fatalf("unexpected history record: %v", hist[0])
	}

	resp, body = get(t, s, "/gorchestra/terminals")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("terminals status: %d", resp.StatusCode)
	}
	var terms []struct {
		Name  string `json:"name"`
		State string `json:"state"`
		Count uint64 `json:"count"`
	}
	if err := json.Unmarshal([]byte(body), &terms); err != nil {
		t.Fatalf("terminals json: %v", err)
	}
	if len(terms) != 1 || terms[0].Name != "worker" || terms[0].State != "STOPPED" || terms[0].Count != 1 {
		t.Fatalf("unexpected terminal counts: %+v", terms)
	}

	resp, body = get(t, s, "/gorchestra/snapshots")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshots status: %d", resp.StatusCode)
	}
	var snaps []map[string]any
	if err := json.Unmarshal([]byte(body), &snaps); err != nil {
		t.Fatalf("snapshots json: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("expected no active routines, got %d", len(snaps))
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
