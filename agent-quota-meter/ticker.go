package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	animationTick = time.Second
	activeTick    = 5 * time.Second
	idleTick      = 30 * time.Second
	idleExitAfter = 10 * time.Minute
	collectEvery  = time.Minute
	tickTTL       = "300000"
	finalTTL      = "900000"
)

func (a *app) runTicker() error {
	lock := newLockManager(a)
	if !lock.acquire() {
		return nil
	}
	defer lock.cleanup()

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGUSR1, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)

	reader := newSessionReader(a.home, a.now)
	lastWorking := a.now()
	var lastCollect time.Time
	consecutiveFailures := 0
	frame := 0
	reportedContext := make(map[string]bool)
	reportedQuota := make(map[string]bool)
	clearedHidden := make(map[string]bool)
	previousStatus := make(map[string]string)
	idleGraceDeadlines := make(map[string]time.Time)

	for {
		agents, err := a.herdrAgents()
		if err != nil {
			consecutiveFailures++
			if consecutiveFailures >= 2 {
				return fmt.Errorf("herdr agent list failed twice: %w", err)
			}
			if stop := waitForTicker(activeTick, signals); stop {
				return nil
			}
			continue
		}
		consecutiveFailures = 0
		if !lock.heartbeat() {
			return nil
		}

		now := a.now()
		collectDue := lastCollect.IsZero() || now.Sub(lastCollect) >= collectEvery
		working := false
		for _, agent := range agents {
			if agent.AgentStatus == "working" {
				working = true
				break
			}
		}
		if working {
			lastWorking = now
		}
		finalize := !working && now.Sub(lastWorking) >= idleExitAfter
		ttl := tickTTL
		if finalize {
			ttl = finalTTL
		}
		fast := working && !finalize
		full := !fast || frame%5 == 0

		quotas := map[string]*quotaDisplay{
			"claude": a.quotaDisplay("claude"),
			"codex":  a.quotaDisplay("codex"),
		}
		ascii := asciiMode(a.stateDir)
		for _, agent := range agents {
			if agent.PaneID == "" || (agent.Agent != "claude" && agent.Agent != "codex") {
				continue
			}
			mode, graceDeadline := nextDisplayState(
				previousStatus[agent.PaneID],
				agent.AgentStatus,
				idleGraceDeadlines[agent.PaneID],
				now,
			)
			previousStatus[agent.PaneID] = agent.AgentStatus
			if graceDeadline.IsZero() {
				delete(idleGraceDeadlines, agent.PaneID)
			} else {
				idleGraceDeadlines[agent.PaneID] = graceDeadline
			}
			isWorking := mode == "working"
			if mode == "" {
				if !clearedHidden[agent.PaneID] {
					a.report(agent.PaneID, clearDisplayTokenArgs())
					clearedHidden[agent.PaneID] = true
				}
				delete(reportedContext, agent.PaneID)
				delete(reportedQuota, agent.PaneID)
				continue
			}
			delete(clearedHidden, agent.PaneID)
			if !full && !isWorking {
				continue
			}

			var percent float64
			hasPercent := false
			if agent.Agent == "claude" && agent.AgentSession.Value != "" {
				percent, hasPercent = reader.claudePercent(agent.AgentSession.Value)
			} else if agent.Agent == "codex" {
				percent, hasPercent = reader.codexPercent(agent.CWD)
			}

			var args []string
			setAny := false
			if hasPercent {
				args = append(args, contextTokenArgs(percent, frame, isWorking, ascii)...)
				setAny = true
				reportedContext[agent.PaneID] = true
			} else if reportedContext[agent.PaneID] {
				args = append(args, "--clear-token", "context")
				args = append(args, "--clear-token", "context_pacman")
				args = append(args, styledTokenArgs("context", "", "")...)
				delete(reportedContext, agent.PaneID)
			}

			quota := quotas[agent.Agent]
			if quota != nil {
				args = append(args, quotaTokenArgs(quota)...)
				setAny = true
				reportedQuota[agent.PaneID] = true
			} else if reportedQuota[agent.PaneID] {
				args = append(args, "--clear-token", "quota")
				for index := 1; index <= maxQuotaSlots; index++ {
					args = append(args, styledTokenArgs(fmt.Sprintf("quota_%d", index), "", "")...)
				}
				delete(reportedQuota, agent.PaneID)
			}
			if setAny {
				paneTTL := ttl
				if !graceDeadline.IsZero() {
					paneTTL = remainingTTL(graceDeadline, now)
				}
				args = append(args, "--ttl-ms", paneTTL)
			}
			if len(args) > 0 {
				a.report(agent.PaneID, args)
			}
		}

		if full {
			live := make(map[string]bool)
			for _, agent := range agents {
				live[agent.PaneID] = true
			}
			pruneBoolMap(reportedContext, live)
			pruneBoolMap(reportedQuota, live)
			pruneBoolMap(clearedHidden, live)
			for paneID := range previousStatus {
				if !live[paneID] {
					delete(previousStatus, paneID)
				}
			}
			for paneID := range idleGraceDeadlines {
				if !live[paneID] {
					delete(idleGraceDeadlines, paneID)
				}
			}
			reader.gc(agents)
		}
		if finalize {
			return nil
		}
		if collectDue {
			lastCollect = now
			_ = a.updateQuota(false)
			frame++
			continue
		}

		frame++
		sleepFor := idleTick
		if fast {
			sleepFor = animationTick
		} else if working {
			sleepFor = activeTick
		}
		for _, deadline := range idleGraceDeadlines {
			until := deadline.Sub(a.now())
			if until < 0 {
				until = 0
			}
			if until < sleepFor {
				sleepFor = until
			}
		}
		if stop := waitForTicker(sleepFor, signals); stop {
			return nil
		}
	}
}

func (a *app) quotaDisplay(kind string) *quotaDisplay {
	state, err := readQuotaState(filepath.Join(a.stateDir, kind+".json"))
	if err != nil || !state.OK {
		return nil
	}
	return renderQuotaWindows(state.Windows)
}

func waitForTicker(duration time.Duration, signals <-chan os.Signal) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case received := <-signals:
		return received != syscall.SIGUSR1
	case <-timer.C:
		return false
	}
}

func pruneBoolMap(values, live map[string]bool) {
	for key := range values {
		if !live[key] {
			delete(values, key)
		}
	}
}
