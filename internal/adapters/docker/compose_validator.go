// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	// ErrForbiddenPrivileged is returned when a service requests privileged mode.
	ErrForbiddenPrivileged = errors.New("security violation: 'privileged: true' is strictly forbidden")

	// ErrForbiddenNamespace is returned when a service shares host namespaces.
	ErrForbiddenNamespace = errors.New("security violation: sharing host namespaces (pid, ipc, uts, network) is forbidden")

	// ErrForbiddenCapability is returned when a service requests dangerous Linux capabilities.
	ErrForbiddenCapability = errors.New("security violation: dangerous capability in 'cap_add' is forbidden")

	// ErrForbiddenDevice is returned when a service tries to map host devices.
	ErrForbiddenDevice = errors.New("security violation: mapping host 'devices' is forbidden")

	// ErrForbiddenSecurityOpt is returned when a service disables security modules.
	ErrForbiddenSecurityOpt = errors.New("security violation: disabling seccomp/apparmor via 'security_opt' is forbidden")

	// ErrForbiddenVolume is returned when a volume mount targets sensitive host paths or escapes the app directory.
	ErrForbiddenVolume = errors.New("security violation: volume mount targets a forbidden path or escapes app directory")
)

// Dangerous Linux capabilities that allow container escape or host compromise.
var dangerousCapabilities = map[string]bool{
	"ALL":             true,
	"CAP_ALL":         true,
	"SYS_ADMIN":       true,
	"CAP_SYS_ADMIN":   true,
	"SYS_PTRACE":      true,
	"CAP_SYS_PTRACE":  true,
	"SYS_MODULE":      true,
	"CAP_SYS_MODULE":  true,
	"DAC_OVERRIDE":    true,
	"CAP_DAC_OVERRIDE": true,
	"DAC_READ_SEARCH": true,
	"CAP_DAC_READ_SEARCH": true,
	"SYS_RAWIO":       true,
	"CAP_SYS_RAWIO":   true,
	"SYS_BOOT":        true,
	"CAP_SYS_BOOT":    true,
	"SYS_CHROOT":      true,
	"CAP_SYS_CHROOT":  true,
	"SYS_TIME":        true,
	"CAP_SYS_TIME":    true,
	"AUDIT_CONTROL":   true,
	"CAP_AUDIT_CONTROL": true,
	"AUDIT_WRITE":     true,
	"CAP_AUDIT_WRITE": true,
	"MAC_ADMIN":       true,
	"CAP_MAC_ADMIN":   true,
	"MAC_OVERRIDE":    true,
	"CAP_MAC_OVERRIDE": true,
}

// Sensitive root/system paths that must never be mounted directly from the host.
var sensitiveHostPaths = []string{
	"/",
	"/root",
	"/etc",
	"/var/run",
	"/run",
	"/proc",
	"/sys",
	"/dev",
	"/boot",
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/opt",
	"/home",
	"/var/lib/docker",
}

// composeRoot represents the top-level structure of a docker-compose.yml file.
type composeRoot struct {
	Version  string                     `yaml:"version"`
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]any             `yaml:"volumes"`
}

// composeService represents a service block inside docker-compose.yml.
type composeService struct {
	Privileged  bool     `yaml:"privileged"`
	Pid         string   `yaml:"pid"`
	Ipc         string   `yaml:"ipc"`
	Uts         string   `yaml:"uts"`
	NetworkMode string   `yaml:"network_mode"`
	CapAdd      []string `yaml:"cap_add"`
	Devices     []any    `yaml:"devices"`
	SecurityOpt []string `yaml:"security_opt"`
	Volumes     []any    `yaml:"volumes"`
}

// ValidateCompose parses the given compose YAML content and enforces security policies.
// If appDir is non-empty, all bind-mounted host volumes must resolve strictly within appDir.
func ValidateCompose(composeContent string, appDir string) error {
	if strings.TrimSpace(composeContent) == "" {
		return errors.New("compose content is empty")
	}

	var root composeRoot
	if err := yaml.Unmarshal([]byte(composeContent), &root); err != nil {
		return fmt.Errorf("invalid YAML syntax in compose: %w", err)
	}

	if len(root.Services) == 0 {
		return errors.New("compose file contains no services")
	}

	// Validate each service in the compose file.
	for svcName, svc := range root.Services {
		if err := validateService(svcName, svc, appDir, root.Volumes); err != nil {
			return fmt.Errorf("service %q: %w", svcName, err)
		}
	}

	return nil
}

func validateService(svcName string, svc composeService, appDir string, topLevelVolumes map[string]any) error {
	// 1. Check privileged mode
	if svc.Privileged {
		return ErrForbiddenPrivileged
	}

	// 2. Check host namespaces
	if isHostNamespace(svc.Pid) || isHostNamespace(svc.Ipc) || isHostNamespace(svc.Uts) {
		return fmt.Errorf("%w: (pid: %q, ipc: %q, uts: %q)", ErrForbiddenNamespace, svc.Pid, svc.Ipc, svc.Uts)
	}
	if strings.EqualFold(strings.TrimSpace(svc.NetworkMode), "host") {
		return fmt.Errorf("%w: 'network_mode: host' is not permitted", ErrForbiddenNamespace)
	}

	// 3. Check capabilities (cap_add)
	for _, cap := range svc.CapAdd {
		normalized := strings.ToUpper(strings.TrimSpace(cap))
		if dangerousCapabilities[normalized] {
			return fmt.Errorf("%w: capability %q is not permitted", ErrForbiddenCapability, cap)
		}
	}

	// 4. Check devices
	if len(svc.Devices) > 0 {
		return fmt.Errorf("%w: requested %d device mappings", ErrForbiddenDevice, len(svc.Devices))
	}

	// 5. Check security options
	for _, secOpt := range svc.SecurityOpt {
		opt := strings.ToLower(strings.TrimSpace(secOpt))
		if strings.Contains(opt, "unconfined") ||
			strings.Contains(opt, "label:disable") ||
			strings.Contains(opt, "label=disable") ||
			strings.Contains(opt, "no-new-privileges:false") ||
			strings.Contains(opt, "no-new-privileges=false") {
			return fmt.Errorf("%w: security option %q is not permitted", ErrForbiddenSecurityOpt, secOpt)
		}
	}

	// 6. Check volumes
	for _, vol := range svc.Volumes {
		if err := validateVolume(vol, appDir, topLevelVolumes); err != nil {
			return err
		}
	}

	return nil
}

func isHostNamespace(ns string) bool {
	clean := strings.ToLower(strings.TrimSpace(ns))
	return clean == "host"
}

func validateVolume(vol any, appDir string, topLevelVolumes map[string]any) error {
	var hostSource string
	var volType string

	switch v := vol.(type) {
	case string:
		// Format: "source:target" or "source:target:mode"
		parts := strings.Split(v, ":")
		if len(parts) < 2 {
			// Anonymous volume or invalid format
			return nil
		}
		hostSource = strings.TrimSpace(parts[0])

	case map[string]any:
		// Long syntax: {type: bind, source: "./data", target: "/var/lib/data"}
		if t, ok := v["type"].(string); ok {
			volType = strings.ToLower(strings.TrimSpace(t))
		}
		if src, ok := v["source"].(string); ok {
			hostSource = strings.TrimSpace(src)
		}
		if volType == "tmpfs" {
			return nil
		}

	default:
		return nil
	}

	if hostSource == "" {
		return nil
	}

	// If the volume name matches a named volume defined in top-level `volumes:`, it is managed by Docker.
	if topLevelVolumes != nil {
		if _, exists := topLevelVolumes[hostSource]; exists {
			return nil
		}
	}

	// If it's a named volume without path separators (e.g. "db_data"), allow it.
	if !strings.Contains(hostSource, "/") && !strings.Contains(hostSource, "\\") && !strings.HasPrefix(hostSource, ".") && !strings.HasPrefix(hostSource, "~") {
		return nil
	}

	// Direct check for sensitive host root paths
	cleanHost := filepath.Clean(hostSource)
	for _, sens := range sensitiveHostPaths {
		if cleanHost == sens || strings.HasPrefix(cleanHost, sens+"/") {
			return fmt.Errorf("%w: path %q accesses sensitive host directory %q", ErrForbiddenVolume, hostSource, sens)
		}
	}

	// If appDir is defined, ensure bind mounts are strictly contained within appDir.
	if appDir != "" {
		cleanAppDir, err := filepath.Abs(filepath.Clean(appDir))
		if err != nil {
			return fmt.Errorf("failed to resolve appDir: %w", err)
		}

		var targetAbsPath string
		if filepath.IsAbs(cleanHost) {
			targetAbsPath = cleanHost
		} else {
			targetAbsPath = filepath.Clean(filepath.Join(cleanAppDir, cleanHost))
		}

		// Ensure targetAbsPath is inside cleanAppDir or equal to it
		rel, err := filepath.Rel(cleanAppDir, targetAbsPath)
		if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
			return fmt.Errorf("%w: bind path %q resolves to %q outside app directory %q", ErrForbiddenVolume, hostSource, targetAbsPath, cleanAppDir)
		}
	}

	return nil
}
