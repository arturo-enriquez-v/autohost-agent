// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package autoheal

import (
	"context"
	"encoding/json"
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
		state.LastActionError = actionError(err, output)
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

		var output string
		var err error
		args, resolveErr := recreateArgs(ctx, runner, config.Recreate, containerID, serviceName)
		if resolveErr != nil {
			err = resolveErr
		} else {
			output, err = runner.Exec(ctx, "docker", args...)
		}

		state.RecreateAttempts++
		state.LastActionTimestamp = &now
		state.LastActionError = actionError(err, output)
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

// maxActionErrorLen bounds the error kept in the state, which travels with every check result.
const maxActionErrorLen = 500

// actionError describes a failed restart or recreate command for the server: the command's output when it
// has one (Docker's message), otherwise the error. Empty when the command succeeded.
func actionError(err error, output string) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(output)
	if msg == "" {
		msg = err.Error()
	}
	if len(msg) > maxActionErrorLen {
		msg = msg[:maxActionErrorLen] + "…"
	}
	return msg
}

// Labels Docker Compose sets on the containers it creates.
const (
	labelComposeProject     = "com.docker.compose.project"
	labelComposeWorkingDir  = "com.docker.compose.project.working_dir"
	labelComposeConfigFiles = "com.docker.compose.project.config_files"
	labelComposeService     = "com.docker.compose.service"
)

// recreateArgs builds the "docker compose ... up -d --force-recreate <service>" arguments. With a compose
// file configured it uses that file and the configured service name (or serviceName). Without one it reads
// the project, files and service from the Compose labels of the container, so users only have to turn the
// strategy on.
func recreateArgs(ctx context.Context, runner CommandRunner, rc RecreateConfig, containerID, serviceName string) ([]string, error) {
	if rc.ComposeFilePath != "" {
		target := rc.ServiceName
		if target == "" {
			target = serviceName
		}
		return []string{"compose", "-f", rc.ComposeFilePath, "up", "-d", "--force-recreate", target}, nil
	}

	out, err := runner.Exec(ctx, "docker", "inspect", "--format", "{{json .Config.Labels}}", containerID)
	if err != nil {
		return nil, fmt.Errorf("read compose labels of %s: %w", containerID, err)
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &labels); err != nil {
		return nil, fmt.Errorf("read compose labels of %s: %w", containerID, err)
	}
	project, files := labels[labelComposeProject], labels[labelComposeConfigFiles]
	if project == "" || files == "" {
		return nil, fmt.Errorf("container %s was not created by Docker Compose; set the compose file path", containerID)
	}
	// A service name equal to the container name is how older clients filled the field by default: the
	// label is the real compose service.
	target := rc.ServiceName
	if target == "" || target == containerID {
		target = labels[labelComposeService]
	}
	if target == "" {
		return nil, fmt.Errorf("container %s has no compose service label; set the service name", containerID)
	}

	args := []string{"compose", "-p", project}
	if dir := labels[labelComposeWorkingDir]; dir != "" {
		args = append(args, "--project-directory", dir)
	}
	for _, f := range strings.Split(files, ",") {
		if f = strings.TrimSpace(f); f != "" {
			args = append(args, "-f", f)
		}
	}
	return append(args, "up", "-d", "--force-recreate", target), nil
}
