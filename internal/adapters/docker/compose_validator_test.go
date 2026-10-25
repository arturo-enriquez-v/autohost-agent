// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import (
	"errors"
	"testing"
)

func TestValidateCompose_LegitimateApp(t *testing.T) {
	compose := `
version: '3.8'
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    volumes:
      - ./data:/usr/share/nginx/html
      - app_cache:/var/cache/nginx
volumes:
  app_cache:
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err != nil {
		t.Fatalf("expected valid compose, got error: %v", err)
	}
}

func TestValidateCompose_PrivilegedForbidden(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    privileged: true
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenPrivileged) {
		t.Fatalf("expected ErrForbiddenPrivileged, got: %v", err)
	}
}

func TestValidateCompose_HostPidForbidden(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    pid: host
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenNamespace) {
		t.Fatalf("expected ErrForbiddenNamespace, got: %v", err)
	}
}

func TestValidateCompose_NetworkHostForbidden(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    network_mode: host
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenNamespace) {
		t.Fatalf("expected ErrForbiddenNamespace, got: %v", err)
	}
}

func TestValidateCompose_DangerousCapAdd(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    cap_add:
      - SYS_ADMIN
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenCapability) {
		t.Fatalf("expected ErrForbiddenCapability, got: %v", err)
	}
}

func TestValidateCompose_DevicesForbidden(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    devices:
      - /dev/sda:/dev/sda
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenDevice) {
		t.Fatalf("expected ErrForbiddenDevice, got: %v", err)
	}
}

func TestValidateCompose_SecurityOptUnconfined(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    security_opt:
      - seccomp:unconfined
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenSecurityOpt) {
		t.Fatalf("expected ErrForbiddenSecurityOpt, got: %v", err)
	}
}

func TestValidateCompose_SensitiveVolumeMountRoot(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    volumes:
      - /:/host
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenVolume) {
		t.Fatalf("expected ErrForbiddenVolume, got: %v", err)
	}
}

func TestValidateCompose_SensitiveVolumeDockerSock(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenVolume) {
		t.Fatalf("expected ErrForbiddenVolume, got: %v", err)
	}
}

func TestValidateCompose_PathTraversalVolume(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    volumes:
      - ../../../../etc:/target_etc
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenVolume) {
		t.Fatalf("expected ErrForbiddenVolume, got: %v", err)
	}
}

func TestValidateCompose_LongSyntaxBindEscape(t *testing.T) {
	compose := `
version: '3.8'
services:
  evil:
    image: alpine
    volumes:
      - type: bind
        source: /etc/shadow
        target: /app/shadow
`
	err := ValidateCompose(compose, "/var/lib/autohost/apps/myapp")
	if err == nil || !errors.Is(err, ErrForbiddenVolume) {
		t.Fatalf("expected ErrForbiddenVolume, got: %v", err)
	}
}
