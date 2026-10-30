// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// NetworkInterfaceStats holds accumulated counters for one network interface,
// read directly from /proc/net/dev (no external commands needed).
type NetworkInterfaceStats struct {
	Interface string `json:"interface"`
	RxBytes   uint64 `json:"rx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	RxErrors  uint64 `json:"rx_errors"`
	RxDropped uint64 `json:"rx_dropped"`
	TxBytes   uint64 `json:"tx_bytes"`
	TxPackets uint64 `json:"tx_packets"`
	TxErrors  uint64 `json:"tx_errors"`
	TxDropped uint64 `json:"tx_dropped"`
}

// ListeningPort represents a TCP or UDP port actively listening on the host,
// parsed from /proc/net/tcp and /proc/net/tcp6 (state 0x0A = LISTEN).
type ListeningPort struct {
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"` // "tcp" | "udp"
	BindAddr string `json:"bind_addr"` // "0.0.0.0", "127.0.0.1", "100.64.0.x", "::" etc.
}

// GetNetworkStats reads /proc/net/dev and returns per-interface stats.
// It skips loopback ("lo") by default.
func GetNetworkStats() ([]NetworkInterfaceStats, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, fmt.Errorf("open /proc/net/dev: %w", err)
	}
	defer f.Close()

	var stats []NetworkInterfaceStats
	scanner := bufio.NewScanner(f)
	// Skip the two header lines.
	scanner.Scan()
	scanner.Scan()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Format: "iface: rx_bytes rx_packets rx_errs rx_drop rx_fifo rx_frame rx_compressed rx_multicast tx_bytes ..."
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colonIdx])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[colonIdx+1:])
		if len(fields) < 16 {
			continue
		}
		parse := func(s string) uint64 {
			v, _ := strconv.ParseUint(s, 10, 64)
			return v
		}
		stats = append(stats, NetworkInterfaceStats{
			Interface: iface,
			RxBytes:   parse(fields[0]),
			RxPackets: parse(fields[1]),
			RxErrors:  parse(fields[2]),
			RxDropped: parse(fields[3]),
			TxBytes:   parse(fields[8]),
			TxPackets: parse(fields[9]),
			TxErrors:  parse(fields[10]),
			TxDropped: parse(fields[11]),
		})
	}
	return stats, scanner.Err()
}

// GetListeningPorts reads /proc/net/tcp and /proc/net/tcp6, returning all
// ports in LISTEN state (0x0A). This is kernel-native and works on all Linux
// distributions without requiring ss, netstat, or any external binary.
func GetListeningPorts() ([]ListeningPort, error) {
	var ports []ListeningPort

	// Parse both IPv4 and IPv6 tables.
	files := []struct {
		path  string
		proto string
		v6    bool
	}{
		{"/proc/net/tcp", "tcp", false},
		{"/proc/net/tcp6", "tcp", true},
		{"/proc/net/udp", "udp", false},
		{"/proc/net/udp6", "udp", true},
	}

	seen := map[uint16]bool{}

	for _, f := range files {
		entries, err := parseProcNetFile(f.path, f.v6)
		if err != nil {
			// File may not exist on all kernels — skip silently.
			continue
		}
		for _, e := range entries {
			if seen[e.Port] {
				continue
			}
			seen[e.Port] = true
			ports = append(ports, ListeningPort{
				Port:     e.Port,
				Protocol: f.proto,
				BindAddr: e.BindAddr,
			})
		}
	}
	return ports, nil
}

type procEntry struct {
	Port     uint16
	BindAddr string
}

// parseProcNetFile reads a /proc/net/tcp[6] or /proc/net/udp[6] file and
// returns entries whose state == 0x0A (LISTEN for TCP, always "listening" for UDP).
func parseProcNetFile(path string, v6 bool) ([]procEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []procEntry
	scanner := bufio.NewScanner(f)
	scanner.Scan() // skip header

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Minimum fields: sl local_address rem_address st ...
		if len(fields) < 4 {
			continue
		}

		// State field: 0A = LISTEN
		state := strings.ToUpper(fields[3])
		if state != "0A" {
			continue
		}

		localAddr := fields[1] // "XXXXXXXX:PPPP" or "XXXX...XXXX:PPPP"
		parts := strings.Split(localAddr, ":")
		if len(parts) != 2 {
			continue
		}

		portHex := parts[1]
		port64, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}

		bindAddr := parseHexAddr(parts[0], v6)
		entries = append(entries, procEntry{
			Port:     uint16(port64),
			BindAddr: bindAddr,
		})
	}
	return entries, scanner.Err()
}

// parseHexAddr converts a kernel hex-encoded address to a dotted IP string.
// IPv4: "0100007F" → "127.0.0.1" (little-endian 32-bit)
// IPv6: 32 hex chars → standard IPv6 notation
func parseHexAddr(hexStr string, v6 bool) string {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return hexStr
	}

	if !v6 && len(b) == 4 {
		// Little-endian 32-bit
		ip := net.IP{b[3], b[2], b[1], b[0]}
		return ip.String()
	}

	if v6 && len(b) == 16 {
		// IPv6 is stored as 4 little-endian 32-bit words
		var out [16]byte
		for i := 0; i < 4; i++ {
			word := binary.LittleEndian.Uint32(b[i*4 : i*4+4])
			binary.BigEndian.PutUint32(out[i*4:i*4+4], word)
		}
		ip := net.IP(out[:])
		s := ip.String()
		// Simplify all-zeros (wildcard listen)
		if s == "::" || s == "0:0:0:0:0:0:0:0" {
			return "::"
		}
		return s
	}

	return hexStr
}
