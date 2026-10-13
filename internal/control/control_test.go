package control

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLockHelper(t *testing.T) {
	if os.Getenv("SINGCTL_LOCK_HELPER") != "1" {
		return
	}
	dir := os.Getenv("SINGCTL_LOCK_DIR")
	var lock *InstanceLock
	var err error
	if os.Getenv("SINGCTL_LOCK_ADOPT") == "1" {
		lock, err = AdoptInstanceLock(dir, 3)
	} else {
		lock, err = AcquireInstanceLock(dir)
	}
	if err != nil {
		os.Exit(2)
	}
	defer lock.Close()
	if os.Getenv("SINGCTL_CHECK_EXEC") == "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestNoInheritedLockHelper")
		cmd.Env = append(os.Environ(), "SINGCTL_CHECK_FD=1")
		if err := cmd.Run(); err != nil {
			os.Exit(3)
		}
	}
	if err := RecoverState(dir); err != nil {
		os.Exit(6)
	}
	token := time.Now().Format(time.RFC3339Nano)
	srv := NewServer(SocketPath(dir))
	srv.Handle("STATUS", func(string) (string, error) {
		return fmt.Sprintf(`{"pid":%d,"started_at":%q}`, os.Getpid(), token), nil
	})
	if err := srv.Start(); err != nil {
		os.Exit(7)
	}
	defer srv.Close()
	if err := WriteInstance(dir, Instance{PID: os.Getpid(), StartedAt: token, ControlSocket: SocketPath(dir)}); err != nil {
		os.Exit(8)
	}
	defer RemoveInstance(dir)
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestNoInheritedLockHelper(t *testing.T) {
	if os.Getenv("SINGCTL_CHECK_FD") != "1" {
		return
	}
	expected, err := os.Stat(LockPath(os.Getenv("SINGCTL_LOCK_DIR")))
	if err != nil {
		os.Exit(4)
	}
	for fd := uintptr(3); fd < 64; fd++ {
		var stat syscall.Stat_t
		if syscall.Fstat(int(fd), &stat) == nil {
			want := expected.Sys().(*syscall.Stat_t)
			if stat.Dev == want.Dev && stat.Ino == want.Ino {
				os.Exit(5)
			}
		}
	}
	os.Exit(0)
}

func lockChild(t *testing.T, dir string, inherited *os.File) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelper$")
	cmd.Env = append(os.Environ(), "SINGCTL_LOCK_HELPER=1", "SINGCTL_LOCK_DIR="+dir)
	if inherited != nil {
		cmd.ExtraFiles = []*os.File{inherited}
		cmd.Env = append(cmd.Env, "SINGCTL_LOCK_ADOPT=1", "SINGCTL_CHECK_EXEC=1")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill() })
	return cmd, stdin, bufio.NewReader(stdout)
}

func awaitReady(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		line, err := reader.ReadString('\n')
		if err == nil && line != "READY\n" {
			err = fmt.Errorf("unexpected handshake %q", line)
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no lock readiness handshake")
	}
}

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

func TestInstanceLockExclusiveAndReusableAfterClose(t *testing.T) {
	dir := t.TempDir()
	one, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := AcquireInstanceLock(dir); err == nil {
		t.Fatal("second holder acquired the lifetime lock")
	}
	if err := one.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}
	two, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	defer two.Close()
	if _, err := os.Stat(LockPath(dir)); err != nil {
		t.Fatalf("lock file must persist: %v", err)
	}
}

func TestInstanceLockReleasedAfterSIGKILL(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cmd, _, reader := lockChild(t, dir, nil)
	awaitReady(t, reader)
	if l, err := AcquireInstanceLock(dir); err == nil {
		l.Close()
		t.Fatal("helper did not hold lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("lock not released after SIGKILL: %v", err)
	}
	defer lock.Close()
	if _, err := ReadInstance(dir); err != nil {
		t.Fatal("crash fixture did not leave advertisement")
	}
	if err := RecoverState(dir); err != nil {
		t.Fatal(err)
	}
}

func TestInheritedLifetimeLockAndExecIsolation(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	parent, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd, stdin, reader := lockChild(t, dir, parent.File())
	awaitReady(t, reader) // also confirms a subsequent exec did not inherit it
	parent.Close()
	if l, err := AcquireInstanceLock(dir); err == nil {
		l.Close()
		t.Fatal("parent close unlocked child")
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestParallelLaunchExactlyOneWinner(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	one, in1, r1 := lockChild(t, dir, nil)
	two, in2, r2 := lockChild(t, dir, nil)
	ready := make(chan int, 2)
	for i, r := range []*bufio.Reader{r1, r2} {
		go func(i int, r *bufio.Reader) {
			line, _ := r.ReadString('\n')
			if line == "READY\n" {
				ready <- i
			} else {
				ready <- -1
			}
		}(i, r)
	}
	winners := 0
	for i := 0; i < 2; i++ {
		select {
		case winner := <-ready:
			if winner >= 0 {
				winners++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("parallel startup hung")
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winners", winners)
	}
	in1.Close()
	in2.Close()
	_ = one.Wait()
	_ = two.Wait()
}

func TestRecoveryPreservesConfigurationAndRejectsReusedPID(t *testing.T) {
	for _, pid := range []int{1 << 30, os.Getpid()} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			dir, err := os.MkdirTemp("", "sr-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			for name, value := range map[string]string{"profile.txt": "fixture-key", "sysproxy.ini": "fixture-settings", "autostart-mode.txt": "off"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// A raw listener closed without unlinking models a socket left by reboot.
			ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: SocketPath(dir), Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			ln.SetUnlinkOnClose(false)
			ln.Close()
			stale := Instance{PID: pid, StartedAt: "old", ControlSocket: SocketPath(dir)}
			if err := WriteInstance(dir, stale); err != nil {
				t.Fatal(err)
			}
			if IsCurrent(stale) {
				t.Fatal("stale/reused PID accepted")
			}
			lock, err := AcquireInstanceLock(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := RecoverState(dir); err != nil {
				t.Fatal(err)
			}
			for name, value := range map[string]string{"profile.txt": "fixture-key", "sysproxy.ini": "fixture-settings", "autostart-mode.txt": "off"} {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(got) != value {
					t.Fatalf("changed %s", name)
				}
			}
			srv := NewServer(SocketPath(dir))
			srv.Handle("STATUS", func(string) (string, error) { return fmt.Sprintf(`{"pid":%d,"started_at":"new"}`, os.Getpid()), nil })
			if err := srv.Start(); err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			if IsCurrent(stale) {
				t.Fatal("mismatched start token accepted")
			}
			if err := RecoverState(dir); err == nil {
				t.Fatal("live socket removed")
			}
			if _, err := QueryStatus(SocketPath(dir)); err != nil {
				t.Fatal(err)
			}
		})
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

func TestServerStartDoesNotUnlinkLiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := SocketPath(dir)
	first := NewServer(sock)
	first.Handle("STATUS", func(string) (string, error) { return `{"pid":1,"started_at":"t"}`, nil })
	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer first.Close()
	second := NewServer(sock)
	if err := second.Start(); err == nil {
		t.Fatal("second server stole a live socket")
	}
	if _, err := QueryStatus(sock); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}

func TestQueryTraffic(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	srv := NewServer(sock)
	srv.Handle("TRAFFIC", func(string) (string, error) {
		data, _ := json.Marshal(Traffic{Up: 1024, Down: 4096})
		return string(data), nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	tr, err := QueryTraffic(sock)
	if err != nil {
		t.Fatalf("QueryTraffic: %v", err)
	}
	if tr.Up != 1024 || tr.Down != 4096 {
		t.Errorf("traffic = %+v, want up 1024 down 4096", tr)
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

// wireguardConfigFixture is a realistic multi-line WireGuard INI config —
// the case that motivates KEYS-ADD-CONFIG's base64 framing below.
const wireguardConfigFixture = `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = 10.66.66.2/32

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
`

// TestServer_OneLineFraming pins the constraint documented on HandlerFunc:
// the wire protocol is one line per request, so a plain multi-line argument
// arrives at the handler truncated at the first newline, with no error. This
// is not a bug to fix — it is the reason KEYS-ADD-CONFIG (base64) exists —
// but the truncation must stay exactly this predictable so callers can rely
// on it never silently doing something else.
func TestServer_OneLineFraming(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	srv := NewServer(sock)
	var gotArg string
	srv.Handle("ECHO", func(arg string) (string, error) { gotArg = arg; return "OK", nil })
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	if _, err := Request(sock, "ECHO", wireguardConfigFixture); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if gotArg != "[Interface]" {
		t.Fatalf("a raw multi-line argument must truncate at the first newline; got %q, want %q",
			gotArg, "[Interface]")
	}
}

// TestKeysAddConfig_RoundTrip is the regression that motivates
// KEYS-ADD-CONFIG: a WireGuard config sent via plain KEYS-ADD would be
// silently truncated to its first line. Base64-encoding it and sending it
// through a dedicated command (mirroring cmd/singctl/main.go's
// KEYS-ADD-CONFIG handler) must deliver it to the handler byte-for-byte.
func TestKeysAddConfig_RoundTrip(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	srv := NewServer(sock)
	var got string
	srv.Handle("KEYS-ADD-CONFIG", func(arg string) (string, error) {
		data, err := base64.StdEncoding.DecodeString(arg)
		if err != nil {
			return "", err
		}
		got = string(data)
		return "OK", nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	enc := base64.StdEncoding.EncodeToString([]byte(wireguardConfigFixture))
	reply, err := Request(sock, "KEYS-ADD-CONFIG", enc)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if reply != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
	if got != wireguardConfigFixture {
		t.Errorf("config did not round-trip intact:\n got: %q\nwant: %q", got, wireguardConfigFixture)
	}
}

func TestClient_NoServer(t *testing.T) {
	if err := Stop(filepath.Join(t.TempDir(), "absent.sock")); err == nil {
		t.Error("Stop against a missing socket should error")
	}
}

func TestRecoveryPreservesAmbiguousControlPath(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := os.WriteFile(SocketPath(dir), []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteInstance(dir, Instance{PID: 1, StartedAt: "stale"}); err != nil {
		t.Fatal(err)
	}
	if err := RecoverState(dir); err == nil {
		t.Fatal("removed ambiguous control path")
	}
	if _, err := ReadInstance(dir); err != nil {
		t.Fatal("advertisement removed before ownership resolved")
	}
	got, err := os.ReadFile(SocketPath(dir))
	if err != nil || string(got) != "not a socket" {
		t.Fatal("control path modified")
	}
}

func TestAdoptRejectsUnrelatedDescriptor(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	unrelated, err := os.CreateTemp(dir, "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.Close()
	fd, err := syscall.Dup(int(unrelated.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if adopted, err := AdoptInstanceLock(dir, uintptr(fd)); err == nil {
		adopted.Close()
		t.Fatal("adopted unrelated file")
	}
	if _, err := AcquireInstanceLock(""); err == nil {
		t.Fatal("empty configuration directory accepted")
	}
}
