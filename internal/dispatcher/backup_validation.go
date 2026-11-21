// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// safePgIdentifier matches PostgreSQL role and database names we are willing to pass to pg tools.
var safePgIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.@-]{0,62}$`)

// safeDockerVolumeName matches Docker named volumes (no host paths, no mount option syntax).
var safeDockerVolumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

func validatePgIdentifier(kind, value string) error {
	if !safePgIdentifier.MatchString(value) {
		return fmt.Errorf("invalid PostgreSQL %s: %q", kind, value)
	}
	return nil
}

// resolveBackupVolumes returns the volumes/mount sources to archive or restore for a container.
//
// Only mounts that actually belong to the container are accepted: an API request cannot make the
// agent bind-mount arbitrary host paths (e.g. "/", "/etc", "/root") into the helper container.
// If the container cannot be inspected (e.g. it was removed before a restore), only Docker named
// volumes are accepted, never host paths.
func resolveBackupVolumes(ctx context.Context, containerName string, requested []string) ([]string, error) {
	mounts, inspectErr := inspectContainerVolumes(ctx, containerName)

	if len(requested) == 0 {
		if inspectErr != nil {
			return nil, fmt.Errorf("failed to inspect container volumes: %w", inspectErr)
		}
		return mounts, nil
	}

	allowed := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		allowed[m] = true
	}

	out := make([]string, 0, len(requested))
	for _, raw := range requested {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		// ":" and "," would let the value smuggle extra docker -v options or targets.
		if strings.ContainsAny(v, ":,\n\r\x00") {
			return nil, fmt.Errorf("invalid volume %q", v)
		}
		if inspectErr == nil {
			if !allowed[v] {
				return nil, fmt.Errorf("volume %q is not mounted by container %q", v, containerName)
			}
		} else if !safeDockerVolumeName.MatchString(v) {
			return nil, fmt.Errorf("volume %q rejected: container %q could not be inspected and only named volumes are allowed", v, containerName)
		}
		out = append(out, v)
	}
	return out, nil
}
