// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"autohost-agent/pkg/shell"
	"autohost-agent/pkg/sysinfo"

	"fmt"
	"os"
	"os/exec"
	"strings"
)

func Install() error {
	if runningInContainer() {
		fmt.Println("⚠️  Detecté contenedor. No instalo Docker aquí. Usa el socket del host o dind para pruebas.")
		return nil
	}
	if dockerAvailable() {
		fmt.Println("✅ Docker ya está instalado.")
		return nil
	}
	fmt.Println("🔄 Instalando Docker...")

	// Asegura curl
	if err := ensureCurl(); err != nil {
		panic("❌ No pude instalar/ubicar curl: " + err.Error())
	}

	// Script oficial SIN pipe ciego
	if err := shell.ExecShell(`
set -e
tmp="$(mktemp)"
curl -fsSL https://get.docker.com -o "$tmp"
sh "$tmp"
rm -f "$tmp"
`); err != nil {
		panic("❌ Error ejecutando el instalador de Docker: " + err.Error())
	}

	// Arrancar/enable del daemon (si hay systemd)
	if systemctlAvailable() {
		_ = shell.Exec("sudo", "systemctl", "enable", "--now", "docker")
	} else {
		// fallback best-effort
		_ = shell.Exec("sudo", "service", "docker", "start")
	}

	// Verificar CLI + daemon
	if err := exec.Command("sudo", "docker", "--version").Run(); err != nil {
		panic("❌ Docker CLI no quedó instalado correctamente.")
	}
	if err := exec.Command("sudo", "docker", "info").Run(); err != nil {
		fmt.Println("⚠️  Docker instalado, pero el daemon no responde aún. Revisa el servicio o reinicia el host.")
	} else {
		fmt.Println("✅ Docker instalado y en ejecución.")
	}
	return nil
}

func ensureCurl() error {
	osr := sysinfo.ReadOSRelease()
	id := osr.ID + " " + osr.IDLike

	switch {
	case strings.Contains(id, "debian") || strings.Contains(id, "ubuntu"):
		return shell.ExecShell(`sudo apt-get update -y && sudo apt-get install -y curl ca-certificates && sudo update-ca-certificates`)

	default:
		return shell.Exec("which", "curl")
	}
}

func systemctlAvailable() bool { return exec.Command("which", "systemctl").Run() == nil }

func runningInContainer() bool {
	// Detectar Docker
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// Detectar LXC/LXD/Incus
	if _, err := os.Stat("/dev/lxc"); err == nil {
		return true
	}
	// Check cgroups para otros entornos
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "docker") || strings.Contains(string(data), "lxc") {
			return true
		}
	}
	// opcional: variable para forzar
	return os.Getenv("AUTOHOST_IN_CONTAINER") == "true"
}

func dockerAvailable() bool { return exec.Command("docker", "version").Run() == nil }
