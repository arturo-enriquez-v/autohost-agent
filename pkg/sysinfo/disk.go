// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"os"
	"strings"
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

// ResolveStorageDiskPath returns defaultPath if it already resides on a dedicated
// non-root block device, or checks /proc/mounts for a larger secondary data disk
// mounted at /media/*, /mnt/*, /data*, or /srv/* (e.g. when a secondary SSD is
// mounted after initial installation).
func ResolveStorageDiskPath(defaultPath string) string {
	var rootStat, defStat syscall.Stat_t
	if err := syscall.Stat("/", &rootStat); err == nil {
		if err := syscall.Stat(defaultPath, &defStat); err == nil {
			if defStat.Dev != rootStat.Dev {
				return defaultPath
			}
		}
	}

	rootInfo, err := GetDiskInfo("/")
	if err != nil {
		return defaultPath
	}

	f, err := os.Open("/proc/mounts")
	if err != nil {
		return defaultPath
	}
	defer f.Close()

	bestPath := defaultPath
	bestTotal := rootInfo.TotalBytes
	const minSecondaryBytes = 32 * 1024 * 1024 * 1024 // 32 GiB

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		dev := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]

		if !strings.HasPrefix(dev, "/dev/") || strings.HasPrefix(dev, "/dev/loop") || strings.HasPrefix(dev, "/dev/zram") {
			continue
		}
		switch fsType {
		case "ext4", "xfs", "btrfs", "zfs", "f2fs":
		default:
			continue
		}
		if !(strings.HasPrefix(mountPoint, "/media/") ||
			strings.HasPrefix(mountPoint, "/mnt/") ||
			strings.HasPrefix(mountPoint, "/data") ||
			strings.HasPrefix(mountPoint, "/srv/")) {
			continue
		}

		info, err := GetDiskInfo(mountPoint)
		if err == nil && info.TotalBytes >= minSecondaryBytes && info.TotalBytes > bestTotal {
			bestTotal = info.TotalBytes
			bestPath = mountPoint
		}
	}

	return bestPath
}
