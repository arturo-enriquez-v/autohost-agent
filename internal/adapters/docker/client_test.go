// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
)

func TestCalculateCPUPercent(t *testing.T) {
	stats := &types.StatsJSON{
		Stats: types.Stats{
			CPUStats: types.CPUStats{
				CPUUsage: types.CPUUsage{
					TotalUsage: 2000000,
				},
				SystemUsage: 10000000,
				OnlineCPUs:  2,
			},
			PreCPUStats: types.CPUStats{
				CPUUsage: types.CPUUsage{
					TotalUsage: 1000000,
				},
				SystemUsage: 5000000,
			},
		},
	}

	// cpuDelta = 1,000,000, systemDelta = 5,000,000 => (1/5) * 2 * 100 = 40.0%
	percent := CalculateCPUPercent(stats)
	if percent != 40.0 {
		t.Fatalf("expected 40.0%%, got %.2f%%", percent)
	}

	// Test nil or zero delta
	if CalculateCPUPercent(nil) != 0.0 {
		t.Fatalf("expected 0.0 for nil stats")
	}

	statsZero := &types.StatsJSON{}
	if CalculateCPUPercent(statsZero) != 0.0 {
		t.Fatalf("expected 0.0 for zero stats")
	}
}

func TestCalculateMemUsageAndLimit(t *testing.T) {
	stats := &types.StatsJSON{
		Stats: types.Stats{
			MemoryStats: types.MemoryStats{
				Usage: 200 * 1024 * 1024, // 200 MiB
				Limit: 500 * 1024 * 1024, // 500 MiB
				Stats: map[string]uint64{
					"inactive_file": 50 * 1024 * 1024, // 50 MiB cache
				},
			},
		},
	}

	used, limit, perc := CalculateMemUsageAndLimit(stats)
	expectedUsed := float64(150 * 1024 * 1024)
	expectedLimit := float64(500 * 1024 * 1024)
	expectedPerc := (150.0 / 500.0) * 100.0 // 30.0%

	if used != expectedUsed {
		t.Fatalf("expected used %.0f, got %.0f", expectedUsed, used)
	}
	if limit != expectedLimit {
		t.Fatalf("expected limit %.0f, got %.0f", expectedLimit, limit)
	}
	if perc != expectedPerc {
		t.Fatalf("expected perc %.2f%%, got %.2f%%", expectedPerc, perc)
	}

	memStr := FormatMemString(used, limit)
	if !strings.Contains(memStr, "150.00MiB") || !strings.Contains(memStr, "500.00MiB") {
		t.Fatalf("unexpected formatted mem string: %s", memStr)
	}
}
