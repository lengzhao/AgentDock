// Package lock provides a single-instance guard for one installation.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// ErrAlreadyRunning means another process holds the lock.
var ErrAlreadyRunning = errors.New("another agentdock process is already running for this installation")

// Acquire takes an exclusive advisory lock on path. The returned release func
// must be called; it unlocks and removes the file.
//
// flock is used (not a pid file) because it is released by the kernel when the
// process dies, so a crashed run never leaves a stale lock behind.
func Acquire(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := readHolder(f)
		f.Close()
		if holder != "" {
			return nil, fmt.Errorf("%w (pid %s)", ErrAlreadyRunning, holder)
		}
		return nil, fmt.Errorf("%w: %v", ErrAlreadyRunning, err)
	}
	// Record the pid for diagnostics only; the flock is what enforces exclusivity.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}

func readHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 0)
	if n > 0 && err == nil || n > 0 {
		return string(trimSpace(buf[:n]))
	}
	return ""
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}
