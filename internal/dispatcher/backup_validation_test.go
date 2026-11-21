// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"testing"
)

func TestValidatePgIdentifier(t *testing.T) {
	for _, ok := range []string{"postgres", "app_user", "my-db", "db.v2", "user@tenant"} {
		if err := validatePgIdentifier("user", ok); err != nil {
			t.Errorf("expected %q to be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "x; rm -rf /", "a b", "$(id)", "`id`", "db'--", "a\"b", "1abc"} {
		if err := validatePgIdentifier("user", bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestResolveBackupVolumesRejectsHostPathsWhenContainerMissing(t *testing.T) {
	ctx := context.Background()
	const missing = "autohost-test-container-that-does-not-exist"

	for _, bad := range []string{"/", "/etc", "/root/.ssh", "vol:/x", "vol,readonly", "../x"} {
		if _, err := resolveBackupVolumes(ctx, missing, []string{bad}); err == nil {
			t.Errorf("expected volume %q to be rejected", bad)
		}
	}

	got, err := resolveBackupVolumes(ctx, missing, []string{"pgdata", " app_data "})
	if err != nil {
		t.Fatalf("named volumes should be accepted: %v", err)
	}
	if len(got) != 2 || got[0] != "pgdata" || got[1] != "app_data" {
		t.Fatalf("unexpected volumes: %v", got)
	}

	if _, err := resolveBackupVolumes(ctx, missing, nil); err == nil {
		t.Errorf("expected error when no volumes requested and container cannot be inspected")
	}
}
