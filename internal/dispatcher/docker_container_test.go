// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"strings"
	"testing"
)

func TestExecContainerCommand_Validation(t *testing.T) {
	ctx := context.Background()

	// Invalid command prefixes
	res := execContainerCommand(ctx, "invalid")
	if res.Err == nil || !strings.Contains(res.Err.Error(), "invalid container command") {
		t.Fatalf("expected error for invalid job type, got %v", res.Err)
	}

	// Missing container names for action commands
	actions := []string{"start", "stop", "restart", "stats", "logs"}
	for _, act := range actions {
		res = execContainerCommand(ctx, "container."+act)
		if res.Err == nil || !strings.Contains(res.Err.Error(), "missing container name") {
			t.Fatalf("expected missing container name error for %s, got: %v", act, res.Err)
		}
	}

	// Injection / invalid container names
	badNames := []string{"container.start../etc/passwd", "container.stop.app;rm -rf /", "container.stats.app&&echo"}
	for _, bad := range badNames {
		res = execContainerCommand(ctx, bad)
		if res.Err == nil || !strings.Contains(res.Err.Error(), "invalid container name") {
			t.Fatalf("expected invalid container name error for %s, got: %v", bad, res.Err)
		}
	}

	// Unknown action
	res = execContainerCommand(ctx, "container.unknown.my-app")
	if res.Err == nil || !strings.Contains(res.Err.Error(), "unknown container action") {
		t.Fatalf("expected unknown action error, got %v", res.Err)
	}
}
