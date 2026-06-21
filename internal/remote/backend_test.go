package remote

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"singctl/internal/control"
	"singctl/internal/procproxy"
	"singctl/internal/ui"
)

// recordingServer starts a control.Server that records the last (cmd,arg) and
// returns canned replies.
type recorder struct {
	mu   sync.Mutex
	cmds map[string]string
}

func startServer(t *testing.T) (*Backend, *recorder) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "c.sock")
	rec := &recorder{cmds: map[string]string{}}
	srv := control.NewServer(sock)
	for _, cmd := range []string{"MODE", "KEYS-ADD", "SETTINGS-SET"} {
		c := cmd
		srv.Handle(c, func(arg string) (string, error) {
			rec.mu.Lock()
			rec.cmds[c] = arg
			rec.mu.Unlock()
			return "OK", nil
		})
	}
	srv.Handle("KEYS-GET", func(string) (string, error) { return "vless://a@h:1\nvless://b@h:2", nil })
	srv.Handle("STOP", func(string) (string, error) {
		rec.mu.Lock()
		rec.cmds["STOP"] = "1"
		rec.mu.Unlock()
		return "OK", nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	// No clash addr → no poller; inject a fake router for routing assertions.
	b := New(control.Instance{ControlSocket: sock}, 1080, nil)
	return b, rec
}

func (r *recorder) get(cmd string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmds[cmd]
}

func TestRemote_ModeCommands(t *testing.T) {
	b, rec := startServer(t)
	if err := b.EnableProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.get("MODE") != "proxy" {
		t.Errorf("EnableProxy → MODE %q, want proxy", rec.get("MODE"))
	}
	_ = b.EnableVPN(context.Background())
	if rec.get("MODE") != "vpn" {
		t.Errorf("EnableVPN → MODE %q", rec.get("MODE"))
	}
	_ = b.Stop(context.Background())
	if rec.get("MODE") != "off" {
		t.Errorf("Stop → MODE %q, want off", rec.get("MODE"))
	}
}

func TestRemote_KeysAndSettings(t *testing.T) {
	b, rec := startServer(t)
	if err := b.AddLink(context.Background(), "vless://x@h:1"); err != nil {
		t.Fatal(err)
	}
	if rec.get("KEYS-ADD") != "vless://x@h:1" {
		t.Errorf("AddLink → KEYS-ADD %q", rec.get("KEYS-ADD"))
	}
	links := b.CurrentLinks()
	if len(links) != 2 || links[0] != "vless://a@h:1" {
		t.Errorf("CurrentLinks = %v", links)
	}
	want := ui.Settings{SocksPort: 1090, ClashEnabled: true, ClashAddr: "127.0.0.1:9091"}
	if err := b.ApplySettings(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	var got ui.Settings
	if err := json.Unmarshal([]byte(rec.get("SETTINGS-SET")), &got); err != nil {
		t.Fatalf("settings json: %v", err)
	}
	if got != want {
		t.Errorf("SETTINGS-SET payload = %+v, want %+v", got, want)
	}
}

func TestRemote_StopDaemon(t *testing.T) {
	b, rec := startServer(t)
	if err := b.StopDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.get("STOP") != "1" {
		t.Error("StopDaemon should send STOP")
	}
}

func TestRemote_DaemonizeRefused(t *testing.T) {
	b, _ := startServer(t)
	if err := b.Daemonize(context.Background()); err == nil {
		t.Error("Daemonize should be refused in attached mode")
	}
}

func TestRemote_RoutingIsLocal(t *testing.T) {
	b, rec := startServer(t)
	fake := &procproxy.FakeRouter{}
	b.router = fake // white-box inject
	b.routerOnce.Do(func() {})
	if err := b.RoutePID(context.Background(), 4242); err != nil {
		t.Fatal(err)
	}
	if len(fake.Added) != 1 || fake.Added[0] != 4242 {
		t.Errorf("RoutePID should use the local router, got %v", fake.Added)
	}
	if rec.get("MODE") != "" {
		t.Error("routing must NOT go over the control socket")
	}
}
