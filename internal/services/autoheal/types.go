// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package autoheal

import (
	"context"
)

type ServiceStatus string

const (
	StatusHealthy        ServiceStatus = "HEALTHY"
	StatusDegraded       ServiceStatus = "DEGRADED"
	StatusRestarting     ServiceStatus = "RESTARTING"
	StatusRecreating     ServiceStatus = "RECREATING"
	StatusCriticalFailed ServiceStatus = "CRITICAL_FAILED"
	StatusCoolingDown    ServiceStatus = "COOLING_DOWN"
)

type AutoHealConfig struct {
	Enabled                      bool           `json:"enabled"`
	ConsecutiveFailuresThreshold int            `json:"consecutive_failures_threshold"`
	GracePeriodSeconds           int64          `json:"grace_period_seconds"`
	Restart                      RestartConfig  `json:"restart"`
	Recreate                     RecreateConfig `json:"recreate"`
}

type RestartConfig struct {
	Enabled       bool  `json:"enabled"`
	MaxAttempts   int   `json:"max_attempts"`
	WindowSeconds int64 `json:"window_seconds"`
}

type RecreateConfig struct {
	Enabled         bool   `json:"enabled"`
	ComposeFilePath string `json:"compose_file_path"`
	ServiceName     string `json:"service_name"`
	MaxAttempts     int    `json:"max_attempts"`
}

type ServiceHealthState struct {
	Status              ServiceStatus `json:"status"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
	RestartTimestamps   []int64       `json:"restart_timestamps"`
	RecreateAttempts    int           `json:"recreate_attempts"`
	LastActionTimestamp *int64        `json:"last_action_timestamp"`
	FailureStartedAt    *int64        `json:"failure_started_at,omitempty"`
	RecoveredAt         *int64        `json:"recovered_at,omitempty"`
}

type CommandRunner interface {
	Exec(ctx context.Context, cmd string, args ...string) (string, error)
}

type AutoHealCallbacks struct {
	OnStatusChange   func(serviceName string, previousStatus, newStatus ServiceStatus)
	OnCriticalFailure func(serviceName string, state *ServiceHealthState, err error)
	OnLog             func(message string, level string)
}
