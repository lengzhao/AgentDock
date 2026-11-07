package config

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// Update serializes a read-modify-write with the same lock used by /env.
// Long-running probes must happen before calling it; the latest config is read
// only after acquiring the lock. The lock file stays in place for other waiters.
func Update(ctx context.Context, path string, apply func(*Config) error) (Config, error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	for {
		if err := ctx.Err(); err != nil {
			return Config{}, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN && err != syscall.EINTR {
			return Config{}, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Config{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if !info.Mode().IsRegular() {
		return Config{}, errors.New("config update requires an existing regular file")
	}
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	if err := apply(&cfg); err != nil {
		return Config{}, err
	}
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	if err := Save(path, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
