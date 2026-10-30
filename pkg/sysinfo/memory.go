// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// MemoryInfo represents memory statistics.
type MemoryInfo struct {
	TotalBytes     uint64
	UsedBytes      uint64
	AvailableBytes uint64
	UsagePercent   float64
}

// GetMemoryInfo reads /proc/meminfo to get memory statistics.
func GetMemoryInfo() (*MemoryInfo, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	memInfo := make(map[string]uint64)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSuffix(parts[0], ":")
		value, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}

		// Convert from KB to bytes
		memInfo[key] = value * 1024
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	totalBytes := memInfo["MemTotal"]
	availableBytes := memInfo["MemAvailable"]

	// Fallback por si MemAvailable no existe (sistemas muy viejos)
	if availableBytes == 0 {
		free := memInfo["MemFree"]
		buffers := memInfo["Buffers"]
		cached := memInfo["Cached"]
		availableBytes = free + buffers + cached
	}

	usedBytes := totalBytes - availableBytes

	var usagePercent float64
	if totalBytes > 0 {
		usagePercent = float64(usedBytes) / float64(totalBytes) * 100
	}

	return &MemoryInfo{
		TotalBytes:     totalBytes,
		UsedBytes:      usedBytes,
		AvailableBytes: availableBytes,
		UsagePercent:   usagePercent,
	}, nil
}
