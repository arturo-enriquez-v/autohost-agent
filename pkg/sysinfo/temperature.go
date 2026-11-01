// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// GetCPUTemperatureCelsius reads Linux thermal zones and hwmon sensors to return
// the current CPU/SoC temperature in degrees Celsius. Returns 0 if no sensor is available.
func GetCPUTemperatureCelsius() float64 {
	// 1. Check /sys/class/thermal/thermal_zone*
	if temp := readThermalZones("/sys/class/thermal"); temp > 0 {
		return temp
	}

	// 2. Fallback: check /sys/class/hwmon/hwmon*
	if temp := readHwmonSensors("/sys/class/hwmon"); temp > 0 {
		return temp
	}

	return 0
}

func readThermalZones(baseDir string) float64 {
	zones, err := filepath.Glob(filepath.Join(baseDir, "thermal_zone*"))
	if err != nil || len(zones) == 0 {
		return 0
	}

	var preferredTemp float64
	var fallbackTemp float64

	for _, zone := range zones {
		tempBytes, err := os.ReadFile(filepath.Join(zone, "temp"))
		if err != nil {
			continue
		}
		tempC := parseMillidegrees(string(tempBytes))
		if tempC <= 0 || tempC > 130 {
			continue
		}

		typeBytes, _ := os.ReadFile(filepath.Join(zone, "type"))
		zoneType := strings.ToLower(strings.TrimSpace(string(typeBytes)))

		// Prioritize CPU / SoC / Package sensors (Intel x86_pkg_temp, Raspberry Pi cpu-thermal, AMD, etc.)
		if strings.Contains(zoneType, "cpu") ||
			strings.Contains(zoneType, "pkg") ||
			strings.Contains(zoneType, "soc") ||
			strings.Contains(zoneType, "core") ||
			strings.Contains(zoneType, "k10temp") ||
			strings.Contains(zoneType, "Tdie") {
			if tempC > preferredTemp {
				preferredTemp = tempC
			}
		} else if tempC > fallbackTemp {
			fallbackTemp = tempC
		}
	}

	if preferredTemp > 0 {
		return preferredTemp
	}
	return fallbackTemp
}

func readHwmonSensors(baseDir string) float64 {
	inputs, err := filepath.Glob(filepath.Join(baseDir, "hwmon*", "temp*_input"))
	if err != nil || len(inputs) == 0 {
		return 0
	}

	var preferredTemp float64
	var fallbackTemp float64

	for _, inputPath := range inputs {
		data, err := os.ReadFile(inputPath)
		if err != nil {
			continue
		}
		tempC := parseMillidegrees(string(data))
		if tempC <= 0 || tempC > 130 {
			continue
		}

		hwmonDir := filepath.Dir(inputPath)
		nameBytes, _ := os.ReadFile(filepath.Join(hwmonDir, "name"))
		sensorName := strings.ToLower(strings.TrimSpace(string(nameBytes)))

		if strings.Contains(sensorName, "coretemp") ||
			strings.Contains(sensorName, "k10temp") ||
			strings.Contains(sensorName, "cpu") ||
			strings.Contains(sensorName, "zenpower") ||
			strings.Contains(sensorName, "soc") {
			if tempC > preferredTemp {
				preferredTemp = tempC
			}
		} else if tempC > fallbackTemp {
			fallbackTemp = tempC
		}
	}

	if preferredTemp > 0 {
		return preferredTemp
	}
	return fallbackTemp
}

func parseMillidegrees(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	// Linux sysfs reports millidegrees Celsius (e.g., 45000 = 45.0°C)
	if val >= 1000 {
		return val / 1000.0
	}
	return val
}

