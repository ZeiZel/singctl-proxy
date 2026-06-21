package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInstanceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Instance{PID: 4242, Mode: "vpn", LogPath: "/tmp/x.log",
		ControlSocket: "/tmp/x.sock", ClashAPIAddr: "127.0.0.1:9090", StartedAt: "now"}
	if err := WriteInstance(dir, want); err != nil {
		t.Fatalf("WriteInstance: %v", err)
	}
	got, err := ReadInstance(dir)
	if err != nil {
		t.Fatalf("ReadInstance: %v", err)
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	RemoveInstance(dir)
	if _, err := os.Stat(InstancePath(dir)); !os.IsNotExist(err) {
		t.Error("RemoveInstance should delete the file")
	}
}

func TestIsAlive(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	if IsAlive(0) || IsAlive(1<<30) {
		t.Error("nonexistent PIDs should not be alive")
	}
}

func TestServerClient_StatusAndStop(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	var mu sync.Mutex
	stopped := false
	srv := NewServer(sock)
	srv.Handle("STATUS", func(string) (string, error) {
		data, _ := json.Marshal(Status{PID: 99, Mode: "proxy", StartedAt: "t0"})
		return string(data), nil
	})
	srv.Handle("STOP", func(string) (string, error) {
		mu.Lock()
		stopped = true
		mu.Unlock()
		return "OK", nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	st, err := QueryStatus(sock)
	if err != nil {
		t.Fatalf("QueryStatus: %v", err)
	}
	if st.PID != 99 || st.Mode != "proxy" {
		t.Errorf("status = %+v, want pid 99 proxy", st)
	}

	if err := Stop(sock); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	mu.Lock()
	done := stopped
	mu.Unlock()
	if !done {
		t.Error("STOP handler was not invoked")
	}
}

func TestRegistry_HandlerArgsAndErrors(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	srv := NewServer(sock)
	var gotArg string
	srv.Handle("ECHO", func(arg string) (string, error) { gotArg = arg; return "OK", nil })
	srv.Handle("FAIL", func(string) (string, error) { return "", errSample })
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	// Args (incl. JSON with spaces) pass through after the first token.
	if _, err := Request(sock, "ECHO", `{"a": 1, "b": 2}`); err != nil {
		t.Fatalf("Request ECHO: %v", err)
	}
	if gotArg != `{"a": 1, "b": 2}` {
		t.Errorf("handler arg = %q, want the full JSON", gotArg)
	}
	// Handler errors surface as Go errors via the ERR prefix.
	if _, err := Request(sock, "FAIL", ""); err == nil || err.Error() != "sample" {
		t.Errorf("FAIL should return the handler error, got %v", err)
	}
	// Unknown command.
	if _, err := Request(sock, "NOPE", ""); err == nil {
		t.Error("unknown command should error")
	}
}

var errSample = fmt.Errorf("sample")

func TestClient_NoServer(t *testing.T) {
	if err := Stop(filepath.Join(t.TempDir(), "absent.sock")); err == nil {
		t.Error("Stop against a missing socket should error")
	}
}
