// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// ActiveConnection represents an established TCP socket on the system.
type ActiveConnection struct {
	LocalIP    string `json:"local_ip"`
	LocalPort  uint16 `json:"local_port"`
	RemoteIP   string `json:"remote_ip"`
	RemotePort uint16 `json:"remote_port"`
	Protocol   string `json:"protocol"`
	State      string `json:"state"`
}

// GetActiveConnections reads /proc/net/tcp and /proc/net/tcp6 for state 01 (ESTABLISHED).
func GetActiveConnections() ([]ActiveConnection, error) {
	var conns []ActiveConnection

	files := []struct {
		path  string
		proto string
		v6    bool
	}{
		{"/proc/net/tcp", "tcp", false},
		{"/proc/net/tcp6", "tcp6", true},
	}

	for _, f := range files {
		cList, err := parseProcNetConns(f.path, f.proto, f.v6)
		if err != nil {
			continue
		}
		conns = append(conns, cList...)
	}

	return conns, nil
}

func parseProcNetConns(path, proto string, v6 bool) ([]ActiveConnection, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var list []ActiveConnection
	scanner := bufio.NewScanner(file)
	scanner.Scan() // skip header

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}

		// State field: 01 = ESTABLISHED
		stateHex := strings.ToUpper(fields[3])
		if stateHex != "01" {
			continue
		}

		localParts := strings.Split(fields[1], ":")
		remoteParts := strings.Split(fields[2], ":")
		if len(partsCount(localParts)) != 2 || len(partsCount(remoteParts)) != 2 {
			continue
		}

		localPort, _ := strconv.ParseUint(localParts[1], 16, 16)
		remotePort, _ := strconv.ParseUint(remoteParts[1], 16, 16)

		localIP := parseHexAddr(localParts[0], v6)
		remoteIP := parseHexAddr(remoteParts[0], v6)

		// Filter out localhost loopback connections
		if remoteIP == "127.0.0.1" || remoteIP == "::1" || remoteIP == "0.0.0.0" {
			continue
		}

		list = append(list, ActiveConnection{
			LocalIP:    localIP,
			LocalPort:  uint16(localPort),
			RemoteIP:   remoteIP,
			RemotePort: uint16(remotePort),
			Protocol:   proto,
			State:      "ESTABLISHED",
		})
	}
	return list, scanner.Err()
}

func partsCount(p []string) []string {
	if len(p) == 2 {
		return p
	}
	return nil
}
