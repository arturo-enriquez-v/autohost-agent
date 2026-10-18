// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"autohost-agent/pkg/shell"
	"fmt"
	"os"
	"os/user"
)

func AddUserToDockerGroup() error {

	if runningInContainer() {
		fmt.Println("⚠️  En contenedor no modifico grupos. Omite este paso.")
		return nil
	}
	current, _ := user.Current()
	uid0 := current != nil && current.Uid == "0"

	// Detecta usuario adecuado:
	u := os.Getenv("SUDO_USER")
	if u == "" && !uid0 && current != nil {
		u = current.Username
	}
	if u == "" || u == "root" {
		fmt.Println("ℹ️  Saltando: no hay usuario no-root claro para agregar a 'docker'.")
		return nil
	}

	// Crea grupo si falta y agrega usuario
	if err := shell.ExecShell(`getent group docker >/dev/null 2>&1 || sudo groupadd docker`); err != nil {
		fmt.Println("⚠️  No pude crear/verificar grupo docker:", err)
	}
	if err := shell.Exec("sudo", "usermod", "-aG", "docker", u); err != nil {
		fmt.Printf("⚠️  No pude agregar el usuario '%s' al grupo docker: %v\n", u, err)
		return err
	}
	fmt.Printf("✅ Usuario '%s' agregado al grupo 'docker'. Cierra sesión y vuelve a entrar para aplicar cambios.\n", u)
	return nil
}
