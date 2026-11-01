// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import "testing"

func TestValidateDomain_HomelabAndPublic(t *testing.T) {
	validDomains := []string{
		"app.example.com",
		"jellyfin.local",
		"homeassistant.lan",
		"nas.home.arpa",
		"pi.internal",
		"mynode.tailnet-abc.ts.net",
		"192.168.1.50",
		"10.0.0.15",
		"100.64.0.25",
	}

	for _, d := range validDomains {
		if err := validateDomain(d); err != nil {
			t.Errorf("expected domain %q to be valid, got error: %v", d, err)
		}
	}

	invalidDomains := []string{
		"",
		"localhost",
		"127.0.0.1",
		"::1",
		"0.0.0.0",
		"169.254.169.254",
		"singlelabel",
		"-invalid.local",
		"invalid-.local",
	}

	for _, d := range invalidDomains {
		if err := validateDomain(d); err == nil {
			t.Errorf("expected domain %q to be rejected, but it passed", d)
		}
	}
}

func TestValidateUpstream(t *testing.T) {
	if err := validateUpstream("localhost:3000"); err != nil {
		t.Errorf("expected localhost:3000 to be valid, got: %v", err)
	}
	if err := validateUpstream("127.0.0.1:8096"); err != nil {
		t.Errorf("expected 127.0.0.1:8096 to be valid, got: %v", err)
	}
	if err := validateUpstream("localhost:2019"); err == nil {
		t.Errorf("expected Caddy admin port 2019 to be forbidden")
	}
	if err := validateUpstream("localhost:5432"); err == nil {
		t.Errorf("expected PostgreSQL port 5432 to be forbidden")
	}
}
