package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

const (
	pluginID            = "boooowy.agent-quota"
	implementationID    = "go-v1"
	defaultStateDirName = ".cache/herdr-agent-quota"
)

type app struct {
	herdr       string
	home        string
	stateDir    string
	runtimeDir  string
	executable  string
	now         func() time.Time
	credentials func() (claudeCredentials, error)
	httpClient  *http.Client
}

func newApp() (*app, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	stateDir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(home, defaultStateDirName)
	}
	runtimeDir := os.Getenv("AGENT_QUOTA_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = filepath.Join(home, defaultStateDirName)
	}
	herdr := os.Getenv("HERDR_BIN_PATH")
	if herdr == "" {
		herdr = "herdr"
	}
	result := &app{
		herdr:      herdr,
		home:       home,
		stateDir:   stateDir,
		runtimeDir: runtimeDir,
		executable: executable,
		now:        time.Now,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	result.credentials = result.loadClaudeCredentials
	return result, nil
}

func main() {
	a, err := newApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := a.run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a *app) run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agent-quota-meter <event|ticker|update|dashboard|open-dashboard>")
	}
	switch args[0] {
	case "event":
		force := len(args) > 1 && args[1] == "--force"
		return a.handleEvent(force)
	case "ticker":
		return a.runTicker()
	case "update":
		force := len(args) > 1 && args[1] == "--force"
		return a.updateQuota(force)
	case "dashboard":
		return a.runDashboard()
	case "open-dashboard":
		return a.openDashboard()
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *app) handleEvent(force bool) error {
	cmd := exec.Command(a.executable, "ticker")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ticker: %w", err)
	}
	_ = cmd.Process.Release()

	// An already-running ticker may own the lock, while a newly spawned ticker
	// needs a brief moment to publish its PID.
	pidFile := filepath.Join(a.runtimeDir, "ticker.lock", "pid")
	for range 10 {
		if record, err := readLockRecord(pidFile); err == nil && record.PID > 0 {
			_ = syscall.Kill(record.PID, syscall.SIGUSR1)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return a.updateQuota(force)
}

func (a *app) runHerdr(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.herdr, args...)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, err
}

func (a *app) openDashboard() error {
	cmd := exec.Command(
		a.herdr,
		"plugin", "pane", "open",
		"--plugin", pluginID,
		"--entrypoint", "dashboard",
		"--placement", "overlay",
		"--focus",
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
