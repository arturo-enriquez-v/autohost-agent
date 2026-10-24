// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Variables para calcular CPU como delta entre lecturas
var (
	cpuMu        sync.Mutex
	prevCPUTotal uint64
	prevCPUIdle  uint64
	hasPrevCPU   bool
)

// GetCPUUsagePercent reads /proc/stat and calculates CPU usage as a delta
// between this reading and the previous one.
func GetCPUUsagePercent() (float64, error) {
	cpuMu.Lock()
	defer cpuMu.Unlock()

	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, scanner.Err()
	}

	line := scanner.Text()
	if !strings.HasPrefix(line, "cpu ") {
		// No pudimos leer la línea global de CPU
		return 0, nil
	}

	fields := strings.Fields(line)
	if len(fields) < 8 {
		return 0, nil
	}

	// Parse CPU times: user, nice, system, idle, iowait, irq, softirq, steal
	var times [8]uint64
	for i := 0; i < 8 && i+1 < len(fields); i++ {
		val, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			// Si algo falla, simplemente dejamos ese valor en 0
			continue
		}
		times[i] = val
	}

	// idle + iowait
	idle := times[3] + times[4]

	// total = suma de todos los campos
	total := uint64(0)
	for _, t := range times {
		total += t
	}

	var cpuUsage float64

	// Si ya tenemos una lectura previa, calculamos el uso como delta
	if hasPrevCPU {
		deltaTotal := total - prevCPUTotal
		deltaIdle := idle - prevCPUIdle

		if deltaTotal > 0 {
			cpuUsage = float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
		}
	}

	// Guardamos para la siguiente llamada
	prevCPUTotal = total
	prevCPUIdle = idle
	hasPrevCPU = true

	return cpuUsage, nil
}

// GetUptimeSeconds returns the system uptime in seconds.
// On Linux, it reads from /proc/uptime.
func GetUptimeSeconds() (uint64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}

	// /proc/uptime format: "uptime_seconds idle_seconds"
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return 0, nil
	}

	uptimeFloat, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}

	return uint64(uptimeFloat), nil
}
