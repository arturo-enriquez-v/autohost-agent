// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

var (
	dockerCli     *client.Client
	dockerCliOnce sync.Once
	dockerCliErr  error
)

// GetClient returns a thread-safe singleton Docker Engine API client connected
// to the UNIX socket (/var/run/docker.sock or DOCKER_HOST) with automatic API
// version negotiation.
func GetClient() (*client.Client, error) {
	dockerCliOnce.Do(func() {
		dockerCli, dockerCliErr = client.NewClientWithOpts(
			client.FromEnv,
			client.WithAPIVersionNegotiation(),
		)
	})
	if dockerCliErr != nil {
		return nil, dockerCliErr
	}
	return dockerCli, nil
}

// Ping checks if the Docker daemon is responding on the socket.
func Ping(ctx context.Context) error {
	cli, err := GetClient()
	if err != nil {
		return err
	}
	_, err = cli.Ping(ctx)
	return err
}

// CalculateCPUPercent calculates the CPU usage percentage from a StatsJSON snapshot.
// Formula: ((cpuDelta / systemDelta) * onlineCPUs) * 100.0
func CalculateCPUPercent(stats *types.StatsJSON) float64 {
	if stats == nil {
		return 0.0
	}

	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage) - float64(stats.PreCPUStats.SystemUsage)
	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs == 0.0 {
		onlineCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		return (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}
	return 0.0
}

// CalculateMemUsageAndLimit computes the memory usage (excluding cache/inactive buffers),
// total memory limit, and percentage.
func CalculateMemUsageAndLimit(stats *types.StatsJSON) (usedBytes float64, limitBytes float64, memPercent float64) {
	if stats == nil {
		return 0, 0, 0
	}

	limitBytes = float64(stats.MemoryStats.Limit)
	rawUsage := float64(stats.MemoryStats.Usage)

	// cgroup v1: inactive_file or total_inactive_file or cache
	var cache float64
	if v, ok := stats.MemoryStats.Stats["total_inactive_file"]; ok {
		cache = float64(v)
	} else if v, ok := stats.MemoryStats.Stats["inactive_file"]; ok {
		cache = float64(v)
	} else if v, ok := stats.MemoryStats.Stats["cache"]; ok {
		cache = float64(v)
	}

	if rawUsage > cache {
		usedBytes = rawUsage - cache
	} else {
		usedBytes = rawUsage
	}

	if limitBytes > 0 {
		memPercent = (usedBytes / limitBytes) * 100.0
	}

	return usedBytes, limitBytes, memPercent
}

// FormatBytes formats a byte count into a human-readable string (KiB, MiB, GiB).
func FormatBytes(val float64) string {
	const (
		kib = 1024.0
		mib = kib * 1024.0
		gib = mib * 1024.0
	)

	switch {
	case val >= gib:
		return fmt.Sprintf("%.2fGiB", val/gib)
	case val >= mib:
		return fmt.Sprintf("%.2fMiB", val/mib)
	case val >= kib:
		return fmt.Sprintf("%.2fKiB", val/kib)
	default:
		return fmt.Sprintf("%.0fB", val)
	}
}

// FormatMemString formats memory usage and limit into "used / limit" string (e.g. "45.20MiB / 1.00GiB").
func FormatMemString(used, limit float64) string {
	if limit <= 0 {
		return FormatBytes(used)
	}
	return fmt.Sprintf("%s / %s", FormatBytes(used), FormatBytes(limit))
}

// FormatPercent formats a float percentage (e.g. 1.25%).
func FormatPercent(val float64) string {
	return fmt.Sprintf("%.2f%%", val)
}

// DecodeStats decodes a Docker stats JSON response body into types.StatsJSON.
func DecodeStats(r io.Reader) (*types.StatsJSON, error) {
	var s types.StatsJSON
	dec := json.NewDecoder(r)
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}
