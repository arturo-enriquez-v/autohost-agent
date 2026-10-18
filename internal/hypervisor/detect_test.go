// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package hypervisor

import (
	"context"
	"errors"
	"testing"
)

func fakeDetector() *Detector {
	return &Detector{
		FileExists:   func(string) bool { return true },
		LookPath:     func(string) (string, error) { return "/usr/bin/virsh", nil },
		ReadMemTotal: func() (uint64, error) { return 32 << 30, nil },
		Statfs:       func(string) (uint64, uint64, error) { return 1000, 400, nil },
		NumCPU:       func() int { return 16 },
		Virsh: func(context.Context, ...string) (string, error) {
			return "Compiled against library: libvirt 10.0.0\nUsing library: libvirt 10.0.0\nRunning against daemon: 10.0.0\n", nil
		},
		PoolPath: DefaultPoolPath,
	}
}

func TestDetect_Capable(t *testing.T) {
	info := fakeDetector().Detect(context.Background())

	if !info.Capable || !info.KVMAvailable || !info.LibvirtAvailable {
		t.Fatalf("expected capable host, got %+v", info)
	}
	if info.LibvirtVersion != "10.0.0" {
		t.Errorf("version = %q, want 10.0.0", info.LibvirtVersion)
	}
	if info.CPUCores != 16 || info.RAMTotalBytes != 32<<30 {
		t.Errorf("unexpected resources: %+v", info)
	}
	if info.StorageTotalBytes != 1000 || info.StorageAvailableBytes != 400 {
		t.Errorf("unexpected storage: %+v", info)
	}
	if info.NetworkName != DefaultNetwork {
		t.Errorf("network = %q", info.NetworkName)
	}
}

func TestDetect_NoKVM(t *testing.T) {
	d := fakeDetector()
	d.FileExists = func(p string) bool { return p != kvmDevice }

	info := d.Detect(context.Background())
	if info.Capable || info.KVMAvailable || info.Reason != reasonNoKVM {
		t.Fatalf("expected no-KVM result, got %+v", info)
	}
	// Resources are still reported so the UI can show the host sizing.
	if info.CPUCores != 16 {
		t.Errorf("cores should be reported even when not capable")
	}
}

func TestDetect_NoVirsh(t *testing.T) {
	d := fakeDetector()
	d.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	info := d.Detect(context.Background())
	if info.Capable || !info.KVMAvailable || info.Reason != reasonNoVirsh {
		t.Fatalf("expected no-virsh result, got %+v", info)
	}
}

func TestDetect_LibvirtdDown(t *testing.T) {
	d := fakeDetector()
	d.Virsh = func(context.Context, ...string) (string, error) { return "", errors.New("connection refused") }

	info := d.Detect(context.Background())
	if info.Capable || info.LibvirtAvailable || info.Reason == "" {
		t.Fatalf("expected libvirtd-down result, got %+v", info)
	}
}

func TestStatPath_FallsBackToExistingParent(t *testing.T) {
	d := fakeDetector()
	d.FileExists = func(p string) bool { return p == "/var" }

	if got := d.statPath(); got != "/var" {
		t.Errorf("statPath = %q, want /var", got)
	}
}

func TestParseLibvirtVersion_FallbackToLibrary(t *testing.T) {
	if got := parseLibvirtVersion("Using library: libvirt 9.5.0\n"); got != "9.5.0" {
		t.Errorf("got %q", got)
	}
}
