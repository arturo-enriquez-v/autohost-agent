// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
)

func CreateDockerNetwork() error {
	cli, err := GetClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	_, err = cli.NetworkInspect(ctx, "autohost_net", network.InspectOptions{})
	if err == nil {
		fmt.Println("✅ La red 'autohost_net' ya existe.")
		return nil
	}

	_, err = cli.NetworkCreate(ctx, "autohost_net", network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return fmt.Errorf("crear red autohost_net: %w", err)
	}
	fmt.Println("✅ Red 'autohost_net' creada exitosamente.")
	return nil
}
