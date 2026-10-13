//go:build windows

package control

import (
	"os"
	"path/filepath"
)

// Windows is currently unsupported for daemon mode; keep the package buildable.
type InstanceLock struct{ f *os.File }

func (l *InstanceLock) FD() uintptr {
	if l == nil || l.f == nil {
		return 0
	}
	return l.f.Fd()
}
func (l *InstanceLock) File() *os.File {
	if l == nil {
		return nil
	}
	return l.f
}
func AdoptInstanceLock(dir string, fd uintptr) (*InstanceLock, error) {
	return &InstanceLock{f: os.NewFile(fd, "singctl-instance-lock")}, nil
}

func AcquireInstanceLock(dir string) (*InstanceLock, error) {
	return acquireLock(LockPath(dir))
}
func acquireLock(path string) (*InstanceLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &InstanceLock{f: f}, nil
}
func (l *InstanceLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}
