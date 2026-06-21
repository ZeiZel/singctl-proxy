package control

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
	srv := NewServer(sock,
		func() Status { return Status{PID: 99, Mode: "proxy", StartedAt: "t0"} },
		func() { mu.Lock(); stopped = true; mu.Unlock() },
	)
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
	// onStop runs asynchronously.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := stopped
		mu.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("onStop was not invoked after STOP")
}

func TestClient_NoServer(t *testing.T) {
	if err := Stop(filepath.Join(t.TempDir(), "absent.sock")); err == nil {
		t.Error("Stop against a missing socket should error")
	}
}
