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
	return full, nil
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
