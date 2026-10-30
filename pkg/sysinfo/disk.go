// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"syscall"
)

// DiskInfo represents disk statistics for a filesystem path.
type DiskInfo struct {
	TotalBytes     uint64
	UsedBytes      uint64
	AvailableBytes uint64
	UsagePercent   float64
}

// GetDiskInfo uses syscall.Statfs to get disk statistics for the given path.
func GetDiskInfo(path string) (*DiskInfo, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}

	blockSize := uint64(stat.Bsize)
	totalBytes := stat.Blocks * blockSize
	availableBytes := stat.Bavail * blockSize
	usedBytes := totalBytes - availableBytes

	var usagePercent float64
	if totalBytes > 0 {
		usagePercent = float64(usedBytes) / float64(totalBytes) * 100
	}

	return &DiskInfo{
		TotalBytes:     totalBytes,
		UsedBytes:      usedBytes,
		AvailableBytes: availableBytes,
		UsagePercent:   usagePercent,
	}, nil
}
