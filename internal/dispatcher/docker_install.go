// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"log"

	"autohost-agent/internal/adapters/docker"
)

// DockerInstall installs Docker on the host system.
type DockerInstall struct{}

func (c *DockerInstall) Execute(_ context.Context, _ map[string]any) error {
	log.Println("Installing Docker...")
	return docker.Install()
}
