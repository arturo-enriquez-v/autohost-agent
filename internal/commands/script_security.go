// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"autohost-agent/internal/domain"
)

var (
	// ErrInsecureScriptPermissions is returned when a script has dangerous file permissions.
	ErrInsecureScriptPermissions = errors.New("security violation: script has insecure permissions (must not be world-writable)")

	// ErrInsecureScriptOwner is returned when a script is owned by an untrusted user.
	ErrInsecureScriptOwner = errors.New("security violation: script must be owned by root or the current agent user")
)

// ValidateScriptSecurity verifies path containment, symlink integrity, permissions, and ownership.
func ValidateScriptSecurity(scriptPath string) (string, error) {
	if scriptPath == "" {
		return "", errors.New("script path is empty")
	}

	allowedDir, err := filepath.Abs(domain.CustomCommandsDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve commands dir: %w", err)
	}

	// 1. Resolve canonical path, resolving any symlinks
	canonicalPath, err := filepath.EvalSymlinks(scriptPath)
	if err != nil {
		return "", fmt.Errorf("script not found or invalid link: %w", err)
	}

	absCanonical, err := filepath.Abs(canonicalPath)
	if err != nil {
		return "", fmt.Errorf("cannot resolve canonical path: %w", err)
	}

	// 2. Ensure canonical path resides strictly inside allowedDir
	rel, err := filepath.Rel(allowedDir, absCanonical)
	if err != nil || len(rel) > 1 && rel[:2] == ".." || rel == ".." {
		return "", fmt.Errorf("security violation: script %q resolves to %q outside allowed directory %q", scriptPath, absCanonical, allowedDir)
	}

	// 3. Check file metadata and permissions
	info, err := os.Stat(absCanonical)
	if err != nil {
		return "", fmt.Errorf("stat script failed: %w", err)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("security violation: %q is not a regular file", absCanonical)
	}

	perm := info.Mode().Perm()
	// Must not be world-writable
	if perm&0002 != 0 {
		return "", fmt.Errorf("%w: permissions are %o", ErrInsecureScriptPermissions, perm)
	}

	// 4. Check ownership (Unix)
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		currentUID := uint32(os.Geteuid())
		fileUID := stat.Uid
		// Allow root (0) or the user running the agent
		if fileUID != 0 && fileUID != currentUID {
			return "", fmt.Errorf("%w: file owner UID %d does not match agent UID %d or root (0)", ErrInsecureScriptOwner, fileUID, currentUID)
		}
	}

	return absCanonical, nil
}
