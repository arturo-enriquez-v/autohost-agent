// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package security

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestIsDangerousIP(t *testing.T) {
	dangerous := []string{
		"169.254.169.254",
		"169.254.0.1",
		"169.254.255.255",
		"0.0.0.0",
		"224.0.0.1",
		"239.255.255.250",
		"240.0.0.1",
		"100.64.0.1",
		"fe80::1",
		"ff02::1",
	}

	for _, ipStr := range dangerous {
		ip := net.ParseIP(ipStr)
		if !IsDangerousIP(ip) {
			t.Errorf("expected IP %s to be dangerous, but got safe", ipStr)
		}
	}

	safe := []string{
		"8.8.8.8",
		"1.1.1.1",
		"142.250.190.46",
		"2607:f8b0:4005:805::200e",
	}

	for _, ipStr := range safe {
		ip := net.ParseIP(ipStr)
		if IsDangerousIP(ip) {
			t.Errorf("expected IP %s to be safe, but got dangerous", ipStr)
		}
	}
}

func TestResolveAndValidateIPs_BlocksMetadata(t *testing.T) {
	blocked := []string{
		"169.254.169.254",
		"metadata.google.internal",
		"instance-data",
		"0.0.0.0",
		"224.0.0.1",
	}

	for _, host := range blocked {
		_, err := ResolveAndValidateIPs(context.Background(), host)
		if err == nil {
			t.Errorf("expected error for blocked host %s, got nil", host)
		}
		if !errors.Is(err, ErrDangerousDestination) {
			t.Errorf("expected ErrDangerousDestination, got: %v", err)
		}
	}
}
