// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package autoheal

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

type DefaultRunner struct{}

func (r *DefaultRunner) Exec(ctx context.Context, cmd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

func CreateInitialState() *ServiceHealthState {
	return &ServiceHealthState{
		Status:              StatusHealthy,
		ConsecutiveFailures: 0,
		RestartTimestamps:   []int64{},
		RecreateAttempts:    0,
		LastActionTimestamp: nil,
		FailureStartedAt:    nil,
		RecoveredAt:         nil,
	}
}

func HandleServiceCheck(
	ctx context.Context,
	config AutoHealConfig,
	state *ServiceHealthState,
	isHealthy bool,
	containerIdOrName string,
	runner CommandRunner,
	callbacks *AutoHealCallbacks,
) error {
	if runner == nil {
		runner = &DefaultRunner{}
	}

	serviceName := config.Recreate.ServiceName
	if strings.TrimSpace(serviceName) == "" {
		serviceName = containerIdOrName
	}
	if strings.TrimSpace(serviceName) == "" {
		serviceName = "unnamed-service"
	}

	containerID := containerIdOrName
	if strings.TrimSpace(containerID) == "" {
		containerID = serviceName
	}

	now := time.Now().Unix()

	logMsg := func(msg string, level string) {
		formatted := fmt.Sprintf("[AutoHeal] Service %s: %s", serviceName, msg)
		if callbacks != nil && callbacks.OnLog != nil {
			callbacks.OnLog(formatted, level)
		} else {
			log.Println(formatted)
		}
	}

	updateStatus := func(newStatus ServiceStatus) {
		if state.Status != newStatus {
			old := state.Status
			state.Status = newStatus
			if callbacks != nil && callbacks.OnStatusChange != nil {
				callbacks.OnStatusChange(serviceName, old, newStatus)
			}
		}
	}

	// 0. If AutoHeal is globally disabled
	if !config.Enabled {
		if isHealthy {
			if state.FailureStartedAt != nil {
				state.RecoveredAt = &now
			}
			state.ConsecutiveFailures = 0
			updateStatus(StatusHealthy)
		} else {
			if state.FailureStartedAt == nil || state.Status == StatusHealthy {
				state.FailureStartedAt = &now
				state.RecoveredAt = nil
			}
			state.ConsecutiveFailures++
			updateStatus(StatusDegraded)
		}
		return nil
	}

	// 1. Service is HEALTHY
	if isHealthy {
		if state.FailureStartedAt != nil {
			state.RecoveredAt = &now
		}
		state.ConsecutiveFailures = 0
		updateStatus(StatusHealthy)
		logMsg("Healthcheck PASSED. Status: HEALTHY", "info")
		return nil
	}

	// 2. Service is UNHEALTHY
	if state.FailureStartedAt == nil || state.Status == StatusHealthy {
		state.FailureStartedAt = &now
		state.RecoveredAt = nil
		state.RecreateAttempts = 0
	}

	// 2a. Check Grace Period (COOLING_DOWN)
	if state.LastActionTimestamp != nil {
		elapsed := now - *state.LastActionTimestamp
		if elapsed < config.GracePeriodSeconds {
			updateStatus(StatusCoolingDown)
			logMsg(fmt.Sprintf("Healthcheck FAILED, but is within grace period (%ds / %ds). Action: SKIPPED (COOLING_DOWN)", elapsed, config.GracePeriodSeconds), "warn")
			return nil
		}
	}

	// 2b. Increment consecutive failures
	state.ConsecutiveFailures++

	// 2c. Threshold check
	threshold := config.ConsecutiveFailuresThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if state.ConsecutiveFailures < threshold {
		updateStatus(StatusDegraded)
		logMsg(fmt.Sprintf("Healthcheck FAILED (Consecutive: %d/%d). Status: DEGRADED", state.ConsecutiveFailures, threshold), "warn")
		return nil
	}

	// 3. RESTART Strategy (docker restart)
	windowSeconds := config.Restart.WindowSeconds
	if windowSeconds <= 0 {
		windowSeconds = 900
	}
	cutoff := now - windowSeconds

	// Filter timestamps within window
	validTimestamps := make([]int64, 0, len(state.RestartTimestamps))
	for _, ts := range state.RestartTimestamps {
		if ts > cutoff {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	state.RestartTimestamps = validTimestamps

	restartMax := config.Restart.MaxAttempts
	if config.Restart.Enabled && len(state.RestartTimestamps) < restartMax {
		attemptNum := len(state.RestartTimestamps) + 1
		updateStatus(StatusRestarting)
		logMsg(fmt.Sprintf("Healthcheck FAILED. Action: RESTART (Attempt %d/%d)", attemptNum, restartMax), "warn")

		output, err := runner.Exec(ctx, "docker", "restart", containerID)
		state.RestartTimestamps = append(state.RestartTimestamps, now)
		state.LastActionTimestamp = &now
		updateStatus(StatusCoolingDown)

		if err != nil {
			logMsg(fmt.Sprintf("docker restart failed: %v output: %s", err, output), "error")
			return fmt.Errorf("docker restart %s: %w", containerID, err)
		}

		logMsg(fmt.Sprintf("Container restart executed successfully. Entered COOLING_DOWN for %ds", config.GracePeriodSeconds), "info")
		return nil
	}

	// 4. RECREATION Strategy (docker compose up -d --force-recreate)
	recreateMax := config.Recreate.MaxAttempts
	if config.Recreate.Enabled && state.RecreateAttempts < recreateMax {
		attemptNum := state.RecreateAttempts + 1
		updateStatus(StatusRecreating)
		logMsg(fmt.Sprintf("Healthcheck FAILED. Action: RECREATE Compose Service (Attempt %d/%d)", attemptNum, recreateMax), "warn")

		composeFile := config.Recreate.ComposeFilePath
		targetService := config.Recreate.ServiceName
		if targetService == "" {
			targetService = serviceName
		}

		var output string
		var err error
		if composeFile != "" {
			output, err = runner.Exec(ctx, "docker", "compose", "-f", composeFile, "up", "-d", "--force-recreate", targetService)
		} else {
			output, err = runner.Exec(ctx, "docker", "compose", "up", "-d", "--force-recreate", targetService)
		}

		state.RecreateAttempts++
		state.LastActionTimestamp = &now
		updateStatus(StatusCoolingDown)

		if err != nil {
			logMsg(fmt.Sprintf("docker compose recreate failed: %v output: %s", err, output), "error")
			return fmt.Errorf("docker compose up: %w", err)
		}

		logMsg(fmt.Sprintf("Docker compose recreate executed successfully. Entered COOLING_DOWN for %ds", config.GracePeriodSeconds), "info")
		return nil
	}

	// CRITICAL FAILURE
	updateStatus(StatusCriticalFailed)
	errMsg := fmt.Sprintf("All recovery strategies exhausted (Restarts: %d/%d, Recreates: %d/%d)", len(state.RestartTimestamps), restartMax, state.RecreateAttempts, recreateMax)
	logMsg(fmt.Sprintf("Healthcheck FAILED. Status: CRITICAL_FAILED. %s", errMsg), "error")

	if callbacks != nil && callbacks.OnCriticalFailure != nil {
		callbacks.OnCriticalFailure(serviceName, state, fmt.Errorf("%s", errMsg))
	}

	return fmt.Errorf("%s", errMsg)
}
