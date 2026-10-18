// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"context"
	"strings"
	"testing"
)

func TestCaddyUpsertRoute_RejectsForbiddenPorts(t *testing.T) {
	cmd := &CaddyUpsertRoute{}

	forbiddenCases := []struct {
		domain   string
		upstream string
	}{
		{"app.example.com", "localhost:2019"}, // Caddy Admin
		{"app.example.com", "127.0.0.1:5432"}, // Postgres
		{"app.example.com", "127.0.0.1:6379"}, // Redis
		{"app.example.com", "127.0.0.1:22"},   // SSH
		{"app.example.com", "127.0.0.1:2375"}, // Docker
		{"app.example.com", "127.0.0.1:9090"}, // gRPC
	}

	for _, tc := range forbiddenCases {
		err := cmd.Execute(context.Background(), map[string]any{
			"domain":   tc.domain,
			"upstream": tc.upstream,
		})
		if err == nil {
			t.Errorf("expected error for forbidden upstream %s, got nil", tc.upstream)
		}
		if !strings.Contains(err.Error(), "security violation") && !strings.Contains(err.Error(), "reserved") {
			t.Errorf("expected security violation error for upstream %s, got: %v", tc.upstream, err)
		}
	}
}

func TestCaddyUpsertRoute_RejectsMetadataUpstream(t *testing.T) {
	cmd := &CaddyUpsertRoute{}
	err := cmd.Execute(context.Background(), map[string]any{
		"domain":   "app.example.com",
		"upstream": "169.254.169.254:80",
	})
	if err == nil {
		t.Fatalf("expected error for metadata upstream, got nil")
	}
}

func TestCaddyUpsertRoute_RejectsInvalidDomains(t *testing.T) {
	cmd := &CaddyUpsertRoute{}

	invalidDomains := []string{
		"localhost",
		"127.0.0.1",
		"app..com",
		"-badlabel.com",
		"singleword",
	}

	for _, dom := range invalidDomains {
		err := cmd.Execute(context.Background(), map[string]any{
			"domain":   dom,
			"upstream": "localhost:3000",
		})
		if err == nil {
			t.Errorf("expected error for invalid domain %q, got nil", dom)
		}
	}
}
