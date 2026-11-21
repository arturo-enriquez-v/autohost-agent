// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func GetAutohostDir() string {
	if dir := strings.TrimSpace(os.Getenv("AUTOHOST_STORAGE_DIR")); dir != "" {
		return dir
	}
	return "/var/lib/autohost"
}

func GetSharedDir() string {
	if dir := strings.TrimSpace(os.Getenv("AUTOHOST_SHARED_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(GetAutohostDir(), "shared")
}

// SafeSharedPath resolves a relative path inside the node's shared directory
// and prevents path traversal outside the shared directory.
func SafeSharedPath(relPath string) (string, error) {
	base := filepath.Clean(GetSharedDir())
	cleaned := filepath.Clean("/" + strings.TrimSpace(relPath))
	full := filepath.Join(base, cleaned)

	rel, err := filepath.Rel(base, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid path outside shared directory")
	}
	// The agent runs as root: a symlink inside the shared folder (e.g. created by a container that mounts
	// it) must not let file operations reach the rest of the host. Resolve the existing part of the path.
	if !resolvedWithin(base, full) {
		return "", fmt.Errorf("invalid path: symlink escapes the shared directory")
	}
	return full, nil
}

// resolvedWithin resolves symlinks in the deepest existing ancestor of target (target itself may not exist
// yet, e.g. an upload destination) and reports whether the result is base or inside it.
func resolvedWithin(base, target string) bool {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return true // base does not exist yet: nothing under it can be a symlink
	}
	existing := target
	var rest []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return false
	}
	resolved := filepath.Join(append([]string{realExisting}, rest...)...)
	return resolved == realBase || strings.HasPrefix(resolved, realBase+string(filepath.Separator))
}

func GetSubdir(subdir string) string {
	return filepath.Join(GetAutohostDir(), subdir)
}

func GetRootAppDir() string {
	return filepath.Join("/opt/autohost")
}

func EnsureAutohostDirs() error {
	subdirs := []string{
		"config",
		"templates",
		"apps",
		"logs",
		"state",
		"backups",
		"shared",
	}

	for _, sub := range subdirs {
		if err := os.MkdirAll(GetSubdir(sub), 0755); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(GetSharedDir(), 0755); err != nil {
		return err
	}
	return nil
}
