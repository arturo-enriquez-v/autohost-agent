// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"net"
	"os/exec"
	"strings"
)

// GetLocalIP returns the node's local LAN/bridged IP address.
func GetLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "unknown"
	}
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "unknown"
	}
	return localAddr.IP.String()
}

// GetVPNIP returns the node's private Headscale/Tailscale IP address if connected.
func GetVPNIP() string {
	tsPath, err := exec.LookPath("tailscale")
	if err != nil {
		tsPath = "/usr/bin/tailscale"
	}
	if out, err := exec.Command(tsPath, "ip", "-4").Output(); err == nil {
		ip := strings.TrimSpace(string(out))
		if i := strings.IndexByte(ip, '\n'); i > -1 {
			ip = ip[:i]
		}
		if ip != "" {
			return ip
		}
	}
	return ""
}
