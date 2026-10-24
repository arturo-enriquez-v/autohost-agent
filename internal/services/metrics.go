// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package services

import (
	"autohost-agent/internal/domain"
	"autohost-agent/pkg/sysinfo"
)

// MetricsService collects system metrics from the host.
type MetricsService struct{}

// NewMetricsService creates a new metrics service.
func NewMetricsService() *MetricsService { return &MetricsService{} }

// Collect gathers CPU, memory, and disk metrics.
func (s *MetricsService) Collect() (*domain.Metrics, error) {
	m := &domain.Metrics{}

	cpuUsage, err := sysinfo.GetCPUUsagePercent()
	if err != nil {
		return nil, err
	}
	m.CPUUsagePercent = cpuUsage

	memInfo, err := sysinfo.GetMemoryInfo()
	if err != nil {
		return nil, err
	}
	m.MemoryTotalBytes = memInfo.TotalBytes
	m.MemoryUsedBytes = memInfo.UsedBytes
	m.MemoryAvailableBytes = memInfo.AvailableBytes
	m.MemoryUsagePercent = memInfo.UsagePercent

	diskInfo, err := sysinfo.GetDiskInfo("/")
	if err != nil {
		return nil, err
	}
	m.DiskTotalBytes = diskInfo.TotalBytes
	m.DiskUsedBytes = diskInfo.UsedBytes
	m.DiskAvailableBytes = diskInfo.AvailableBytes
	m.DiskUsagePercent = diskInfo.UsagePercent

	return m, nil
}
