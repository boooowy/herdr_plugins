package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func testLockManager(t *testing.T) *lockManager {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state", pluginID)
	runtimeDir := filepath.Join(root, "runtime")
	lockDir := filepath.Join(runtimeDir, "ticker.lock")
	return &lockManager{
		stateDir:   stateDir,
		runtimeDir: runtimeDir,
		lockDir:    lockDir,
		pidFile:    filepath.Join(lockDir, "pid"),
		source:     pluginID,
		impl:       implementationID,
		now:        time.Now,
		alive:      pidIsAlive,
		kill:       syscall.Kill,
		sleep:      func(time.Duration) {},
		pid:        os.Getpid(),
	}
}

func writeTestLock(t *testing.T, manager *lockManager, record lockRecord, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(manager.lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(
		fmt.Sprintf("%d\n%s\n%s\n", record.PID, record.Owner, record.Implementation),
	)
	if err := os.WriteFile(manager.pidFile, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(manager.pidFile, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewLockRecordsPIDOwnerAndImplementation(t *testing.T) {
	manager := testLockManager(t)
	if !manager.acquire() {
		t.Fatal("acquire returned false")
	}
	defer manager.cleanup()
	record, err := readLockRecord(manager.pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if record.PID != os.Getpid() || record.Owner != pluginID || record.Implementation != implementationID {
		t.Fatalf("record = %#v", record)
	}
}

func TestSameGoTickerDoesNotStartTwice(t *testing.T) {
	manager := testLockManager(t)
	writeTestLock(t, manager, lockRecord{
		PID: os.Getpid(), Owner: pluginID, Implementation: implementationID,
	}, 0)
	if manager.acquire() {
		t.Fatal("same Go ticker unexpectedly acquired lock")
	}
}

func TestPythonTickerIsStoppedAndReplaced(t *testing.T) {
	manager := testLockManager(t)
	oldPID := 12345
	writeTestLock(t, manager, lockRecord{PID: oldPID, Owner: pluginID}, 0)
	alive := true
	manager.alive = func(pid int) bool {
		if pid == oldPID {
			return alive
		}
		return pid == manager.pid
	}
	manager.kill = func(pid int, signal syscall.Signal) error {
		if pid != oldPID || signal != syscall.SIGTERM {
			t.Fatalf("unexpected kill(%d, %v)", pid, signal)
		}
		alive = false
		return nil
	}
	if !manager.acquire() {
		t.Fatal("Go ticker did not replace Python ticker")
	}
	defer manager.cleanup()
	record, err := readLockRecord(manager.pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if record.PID != manager.pid || record.Implementation != implementationID {
		t.Fatalf("replacement record = %#v", record)
	}
}

func TestLegacyStateLockIsRetired(t *testing.T) {
	manager := testLockManager(t)
	legacyDir := filepath.Join(manager.stateDir, "ticker.lock")
	legacyFile := filepath.Join(legacyDir, "pid")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPID := 12345
	if err := os.WriteFile(legacyFile, []byte("12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alive := true
	manager.alive = func(pid int) bool {
		if pid == oldPID {
			return alive
		}
		return pid == manager.pid
	}
	manager.kill = func(pid int, signal syscall.Signal) error {
		alive = false
		return nil
	}
	if !manager.acquire() {
		t.Fatal("acquire failed after legacy retirement")
	}
	defer manager.cleanup()
	if fileExists(legacyDir) {
		t.Fatal("legacy lock directory still exists")
	}
}

func TestStaleLockIsReclaimedWithoutSignal(t *testing.T) {
	manager := testLockManager(t)
	writeTestLock(t, manager, lockRecord{
		PID: os.Getpid(), Owner: "old.plugin", Implementation: "old",
	}, lockStaleAfter+time.Second)
	manager.kill = func(pid int, signal syscall.Signal) error {
		t.Fatalf("stale owner should not be signaled")
		return nil
	}
	if !manager.acquire() {
		t.Fatal("stale lock was not reclaimed")
	}
	defer manager.cleanup()
}
