// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package paths

import (
	"os"
	"path/filepath"
)

func GetAutohostDir() string {
	return "/var/lib/autohost"
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
	}

	for _, sub := range subdirs {
		if err := os.MkdirAll(GetSubdir(sub), 0755); err != nil {
			return err
		}
	}
	return nil
}
