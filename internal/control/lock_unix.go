//go:build !windows

package control

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// InstanceLock is held for the complete lifetime of a daemon. The lock file
// itself is persistent; the kernel releases the lock when the process exits.
type InstanceLock struct{ f *os.File }

func (l *InstanceLock) FD() uintptr {
	if l == nil || l.f == nil {
		return 0
	}
	return l.f.Fd()
}

// File returns the owned file without creating another descriptor owner.
func (l *InstanceLock) File() *os.File {
	if l == nil {
		return nil
	}
	return l.f
}

// AdoptInstanceLock validates the inherited file against the expected lock inode.
func AdoptInstanceLock(dir string, fd uintptr) (*InstanceLock, error) {
	syscall.CloseOnExec(int(fd))
	f := os.NewFile(fd, "singctl-instance-lock")
	if f == nil {
		return nil, fmt.Errorf("invalid inherited lock descriptor")
	}
	actual, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	expected, err := os.Stat(LockPath(dir))
	if err != nil || !os.SameFile(actual, expected) || !actual.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("inherited descriptor is not the instance lock")
	}
	if err := syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return &InstanceLock{f: f}, nil
}

func AcquireInstanceLock(dir string) (*InstanceLock, error) {
	if dir == "" {
		return nil, fmt.Errorf("empty config directory")
	}
	return acquireLock(LockPath(dir))
}

func acquireLock(path string) (*InstanceLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("instance already locked: %w", err)
	}
	return &InstanceLock{f: f}, nil
}

func (l *InstanceLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Closing the descriptor releases the lock. Do not issue LOCK_UN first:
	// --daemon passes the same open file description to its child, and an
	// explicit unlock would release the child's lifetime lock as well.
	return l.f.Close()
}
