package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	lockStaleAfter   = 2 * time.Minute
	lockTakeoverWait = 3 * time.Second
	lockPollInterval = 100 * time.Millisecond
)

type lockRecord struct {
	PID            int
	Owner          string
	Implementation string
}

type lockManager struct {
	stateDir   string
	runtimeDir string
	lockDir    string
	pidFile    string
	source     string
	impl       string
	now        func() time.Time
	alive      func(int) bool
	kill       func(int, syscall.Signal) error
	sleep      func(time.Duration)
	pid        int
}

func newLockManager(a *app) *lockManager {
	lockDir := filepath.Join(a.runtimeDir, "ticker.lock")
	return &lockManager{
		stateDir:   a.stateDir,
		runtimeDir: a.runtimeDir,
		lockDir:    lockDir,
		pidFile:    filepath.Join(lockDir, "pid"),
		source:     pluginID,
		impl:       implementationID,
		now:        a.now,
		alive:      pidIsAlive,
		kill:       syscall.Kill,
		sleep:      time.Sleep,
		pid:        os.Getpid(),
	}
}

func readLockRecord(path string) (lockRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockRecord{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return lockRecord{}, fmt.Errorf("lock PID is empty")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return lockRecord{}, err
	}
	record := lockRecord{PID: pid}
	if len(lines) >= 2 {
		record.Owner = strings.TrimSpace(lines[1])
	}
	if len(lines) >= 3 {
		record.Implementation = strings.TrimSpace(lines[2])
	}
	return record, nil
}

func pidIsAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (manager *lockManager) healthy(record lockRecord, pidFile string) bool {
	if record.PID <= 0 || !manager.alive(record.PID) {
		return false
	}
	info, err := os.Stat(pidFile)
	if err != nil {
		return false
	}
	return manager.now().Sub(info.ModTime()) < lockStaleAfter
}

func (manager *lockManager) remove(expectedPID int, lockDir, pidFile string) bool {
	record, recordErr := readLockRecord(pidFile)
	if expectedPID == 0 {
		if recordErr == nil || fileExists(pidFile) {
			return false
		}
	} else {
		if recordErr != nil || record.PID != expectedPID {
			return false
		}
		if err := os.Remove(pidFile); err != nil && !isNotExist(err) {
			return false
		}
	}
	if err := os.Remove(lockDir); err != nil && !isNotExist(err) {
		return false
	}
	return !fileExists(lockDir)
}

func (manager *lockManager) stopOwner(pid int, lockDir, pidFile string) bool {
	if err := manager.kill(pid, syscall.SIGTERM); err != nil &&
		!errors.Is(err, syscall.ESRCH) {
		return false
	}
	deadline := time.Now().Add(lockTakeoverWait)
	for fileExists(lockDir) && time.Now().Before(deadline) {
		current, err := readLockRecord(pidFile)
		if err == nil && current.PID != pid {
			return false
		}
		if !manager.alive(pid) {
			manager.remove(pid, lockDir, pidFile)
			break
		}
		manager.sleep(lockPollInterval)
	}
	return !fileExists(lockDir)
}

func (manager *lockManager) legacyLockDirs() []string {
	candidates := []string{
		filepath.Join(manager.stateDir, "ticker.lock"),
		filepath.Join(filepath.Dir(manager.stateDir), "ntj.agent-quota", "ticker.lock"),
	}
	shared, _ := filepath.Abs(manager.lockDir)
	seen := make(map[string]bool)
	var result []string
	for _, candidate := range candidates {
		absolute, _ := filepath.Abs(candidate)
		if absolute == shared || seen[absolute] {
			continue
		}
		seen[absolute] = true
		result = append(result, candidate)
	}
	return result
}

func (manager *lockManager) retireLegacyLocks() bool {
	for _, lockDir := range manager.legacyLockDirs() {
		if !fileExists(lockDir) {
			continue
		}
		pidFile := filepath.Join(lockDir, "pid")
		record, err := readLockRecord(pidFile)
		if err == nil && manager.healthy(record, pidFile) {
			if !manager.stopOwner(record.PID, lockDir, pidFile) {
				return false
			}
			continue
		}
		expectedPID := 0
		if err == nil {
			expectedPID = record.PID
		}
		if !manager.remove(expectedPID, lockDir, pidFile) {
			return false
		}
	}
	return true
}

func (manager *lockManager) acquire() bool {
	if !manager.retireLegacyLocks() {
		return false
	}
	if os.MkdirAll(manager.runtimeDir, 0o755) != nil {
		return false
	}
	for range 3 {
		err := os.Mkdir(manager.lockDir, 0o755)
		if errors.Is(err, os.ErrExist) {
			record, recordErr := readLockRecord(manager.pidFile)
			if recordErr == nil && manager.healthy(record, manager.pidFile) {
				if record.Owner == "" {
					// The owner-less format cannot distinguish PID reuse.
					return false
				}
				if record.Owner == manager.source && record.Implementation == manager.impl {
					return false
				}
				// A healthy same-plugin record without an implementation marker
				// is the Python ticker. Stop it before starting the Go ticker.
				if !manager.stopOwner(record.PID, manager.lockDir, manager.pidFile) {
					return false
				}
			} else {
				expectedPID := 0
				if recordErr == nil {
					expectedPID = record.PID
				}
				if !manager.remove(expectedPID, manager.lockDir, manager.pidFile) {
					return false
				}
			}
			continue
		}
		if err != nil {
			return false
		}
		content := fmt.Sprintf("%d\n%s\n%s\n", manager.pid, manager.source, manager.impl)
		file, err := os.OpenFile(manager.pidFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, err = file.WriteString(content)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err == nil {
			return true
		}
		if record, recordErr := readLockRecord(manager.pidFile); recordErr == nil &&
			record.PID == manager.pid {
			manager.remove(manager.pid, manager.lockDir, manager.pidFile)
		} else if !fileExists(manager.pidFile) {
			_ = os.Remove(manager.lockDir)
		}
		return false
	}
	return false
}

func (manager *lockManager) cleanup() {
	manager.remove(manager.pid, manager.lockDir, manager.pidFile)
}

func (manager *lockManager) heartbeat() bool {
	record, err := readLockRecord(manager.pidFile)
	if err != nil || record.PID != manager.pid {
		return false
	}
	now := manager.now()
	return os.Chtimes(manager.pidFile, now, now) == nil
}
