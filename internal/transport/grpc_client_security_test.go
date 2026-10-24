// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildDialOptions_RejectsInsecureRemoteHost(t *testing.T) {
	remoteHosts := []string{
		"grpc.autohost.cloud:9090",
		"54.210.10.2:9090",
		"8.8.8.8:9090",
		"controlplane.publicdomain.com:9090",
	}

	for _, host := range remoteHosts {
		_, _, err := BuildDialOptions(host, GRPCTLSConfig{Insecure: true})
		if err == nil {
			t.Errorf("expected security violation error for insecure remote host %q, got nil", host)
		}
		if !strings.Contains(err.Error(), "security violation") {
			t.Errorf("expected security violation error message, got: %v", err)
		}
	}
}

func TestBuildDialOptions_AllowsInsecurePrivateAndLoopback(t *testing.T) {
	allowedHosts := []string{
		"localhost:9090",
		"127.0.0.1:9090",
		"::1",
		"localhost",
		"127.0.0.1",
		"192.168.1.50:9090",
		"192.168.101.2:9090",
		"10.0.0.1:9090",
		"100.64.0.1:9090",
	}

	for _, host := range allowedHosts {
		addr, creds, err := BuildDialOptions(host, GRPCTLSConfig{Insecure: true})
		if err != nil {
			t.Errorf("expected insecure private/loopback %q to succeed, got error: %v", host, err)
		}
		if creds == nil {
			t.Errorf("expected transport credentials for %q, got nil", host)
		}
		if addr == "" {
			t.Errorf("expected resolved address for %q, got empty", host)
		}
	}
}

func TestBuildDialOptions_EnforcesTLSByDefault(t *testing.T) {
	hosts := []string{
		"grpc.autohost.cloud:9090",
		"controlplane.autohost.io",
		"https://grpc.autohost.cloud:443",
		"54.210.10.2:9090",
	}

	for _, host := range hosts {
		addr, creds, err := BuildDialOptions(host, GRPCTLSConfig{Insecure: false})
		if err != nil {
			t.Fatalf("expected TLS build to succeed for %q, got error: %v", host, err)
		}
		if creds == nil {
			t.Fatalf("expected TLS credentials for %q, got nil", host)
		}
		if !strings.Contains(addr, ":") {
			t.Fatalf("expected host:port format, got: %s", addr)
		}
	}
}

func TestBuildDialOptions_CustomCACert(t *testing.T) {
	tempDir := t.TempDir()
	caCertPath := filepath.Join(tempDir, "ca.crt")

	// Generate a self-signed CA cert for testing
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Autohost Test CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create CA cert: %v", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	if err := os.WriteFile(caCertPath, pemBytes, 0644); err != nil {
		t.Fatalf("failed to write CA cert: %v", err)
	}

	// Test valid CA file
	_, creds, err := BuildDialOptions("grpc.autohost.cloud:9090", GRPCTLSConfig{
		CACertPath: caCertPath,
	})
	if err != nil {
		t.Fatalf("expected success loading custom CA, got error: %v", err)
	}
	if creds == nil {
		t.Fatalf("expected credentials, got nil")
	}

	// Test non-existent CA file
	_, _, err = BuildDialOptions("grpc.autohost.cloud:9090", GRPCTLSConfig{
		CACertPath: filepath.Join(tempDir, "non_existent.crt"),
	})
	if err == nil {
		t.Fatalf("expected error for non-existent CA file, got nil")
	}
}

func TestBuildDialOptions_CertPinning(t *testing.T) {
	// Generate dummy certificate bytes
	dummyCert := []byte("dummy certificate content for pinning test")
	hash := sha256.Sum256(dummyCert)
	pinHex := hex.EncodeToString(hash[:])

	// Test correct pin
	_, _, err := BuildDialOptions("grpc.autohost.cloud:443", GRPCTLSConfig{
		CertPin: "sha256:" + pinHex,
	})
	if err != nil {
		t.Fatalf("expected BuildDialOptions to succeed with CertPin, got: %v", err)
	}
}
