// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeSharedPathRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AUTOHOST_SHARED_DIR", base)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "shadow"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "etc")); err != nil {
		t.Skip("symlinks not supported")
	}
	if err := os.Symlink(filepath.Join(outside, "shadow"), filepath.Join(base, "file-link")); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"/etc/shadow", "/etc", "/etc/new", "/file-link"} {
		if _, err := SafeSharedPath(rel); err == nil {
			t.Errorf("%s: symlink escape must be rejected", rel)
		}
	}
	for _, rel := range []string{"/", "/backup/app/x.back", "/docs/a.txt"} {
		if _, err := SafeSharedPath(rel); err != nil {
			t.Errorf("%s: legitimate path rejected: %v", rel, err)
		}
	}
}
