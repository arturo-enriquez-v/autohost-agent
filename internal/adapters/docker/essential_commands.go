// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"autohost-agent/internal/domain"
	pexec "autohost-agent/internal/platform/exec"
	"autohost-agent/internal/platform/paths"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
)

func Stop(appName domain.AppName) error {
	ymlPath := filepath.Join(paths.GetSubdir("apps"), string(appName), "compose.yml")

	return pexec.ExecWithDir(filepath.Dir(ymlPath), "docker", "compose", "stop")
}

func Start(appName domain.AppName) error {
	appDir := filepath.Join(paths.GetSubdir("apps"), string(appName))
	ymlPath := filepath.Join(appDir, "compose.yml")

	// Validar si existe el archivo compose.yml
	content, err := os.ReadFile(ymlPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("el archivo de configuración no existe: %s", ymlPath)
	} else if err != nil {
		return fmt.Errorf("error al leer compose.yml: %w", err)
	}

	// Validar seguridad del compose antes de levantar contenedores
	if err := ValidateCompose(string(content), appDir); err != nil {
		return fmt.Errorf("validación de seguridad fallida para %s: %w", appName, err)
	}

	fmt.Printf("🔄 Levantando aplicación '%s'...\n", appName)
	// Run from the app dir — compose.yml is auto-discovered, no -f needed.
	output, err := pexec.ExecWithDirOutput(appDir, "docker", "compose", "up", "-d")
	if err != nil {
		return fmt.Errorf("exit status: %w\n%s", err, output)
	}
	return nil
}

func Remove(appName domain.AppName) error {
	if err := appName.Validate(); err != nil {
		return err
	}

	appDir := filepath.Join(paths.GetSubdir("apps"), string(appName))

	if err := pexec.ExecWithDir(appDir, "docker", "compose", "down"); err != nil {
		return fmt.Errorf("failed to stop app: %w", err)
	}

	return pexec.Exec("rm", "-rf", appDir)
}

func ListContainers() error {
	cli, err := GetClient()
	if err != nil {
		return err
	}
	containers, err := cli.ContainerList(context.Background(), container.ListOptions{})
	if err != nil {
		return err
	}
	fmt.Println("Contenedores en ejecución:")
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		fmt.Printf("%s\t%s\n", name, c.Status)
	}
	return nil
}

func GetAppStatus(appName domain.AppName) (string, error) {
	ymlPath := filepath.Join(paths.GetSubdir("apps"), string(appName), "compose.yml")

	cmd := exec.Command("docker", "compose", "ps", "--status=running")
	cmd.Dir = filepath.Dir(ymlPath)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if strings.Contains(string(out), "Up") {
		return "en ejecución", nil
	}
	return "detenida", nil
}
