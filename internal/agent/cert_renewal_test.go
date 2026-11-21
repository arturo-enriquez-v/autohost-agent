// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	httpclient "autohost-agent/internal/transport/http"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// sign issues a client certificate for pub with the given CN and validity window.
func (ca *testCA) sign(t *testing.T, pub any, cn string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: notBefore, NotAfter: notAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// fakeAPI signs CSRs like the API would, optionally misbehaving.
type fakeAPI struct {
	t        *testing.T
	ca       *testCA
	cn       string        // CN to issue (normally the node ID)
	lifetime time.Duration // lifetime of issued certs
	foreign  bool          // sign a different key than the CSR's
	calls    int
}

func (f *fakeAPI) RenewClientCert(_ context.Context, csrPEM string) (*httpclient.RenewedCertificate, error) {
	f.calls++
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub := csr.PublicKey
	if f.foreign {
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		pub = &other.PublicKey
	}
	now := time.Now()
	return &httpclient.RenewedCertificate{
		ClientCertPEM: string(f.ca.sign(f.t, pub, f.cn, now.Add(-5*time.Minute), now.Add(f.lifetime))),
		CACertPEM:     string(f.ca.pem),
		ExpiresAt:     now.Add(f.lifetime).Format(time.RFC3339),
	}, nil
}

// writeCurrentCert writes an initial key/cert pair valid between notBefore and notAfter.
func writeCurrentCert(t *testing.T, ca *testCA, dir, nodeID string, notBefore, notAfter time.Time) certFiles {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	files := certFiles{
		certPath: filepath.Join(dir, "client.crt"),
		keyPath:  filepath.Join(dir, "client.key"),
		caPath:   filepath.Join(dir, "ca.crt"),
	}
	_ = os.WriteFile(files.keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600)
	_ = os.WriteFile(files.certPath, ca.sign(t, &key.PublicKey, nodeID, notBefore, notAfter), 0600)
	_ = os.WriteFile(files.caPath, ca.pem, 0644)
	return files
}

func TestNeedsRenewal(t *testing.T) {
	ca := newTestCA(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now()
	start := now.Add(-60 * 24 * time.Hour)

	fresh := ca.sign(t, &key.PublicKey, "n", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if renew, _ := needsRenewal(fresh, now); renew {
		t.Error("a fresh 90-day certificate should not be renewed")
	}
	// 90-day cert with 29 days left (< 1/3 of lifetime).
	aging := ca.sign(t, &key.PublicKey, "n", start, start.Add(89*24*time.Hour))
	if renew, _ := needsRenewal(aging, now); !renew {
		t.Error("a certificate with less than a third of its lifetime left should be renewed")
	}
	if renew, _ := needsRenewal([]byte("garbage"), now); !renew {
		t.Error("an unreadable certificate should be renewed")
	}
}

func TestRenewClientCertIfNeeded(t *testing.T) {
	ca := newTestCA(t)
	now := time.Now()
	const nodeID = "node-123"

	t.Run("renews an expiring certificate", func(t *testing.T) {
		dir := t.TempDir()
		files := writeCurrentCert(t, ca, dir, nodeID, now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour))
		api := &fakeAPI{t: t, ca: ca, cn: nodeID, lifetime: 90 * 24 * time.Hour}

		renewed, err := renewClientCertIfNeeded(context.Background(), api, nodeID, files, now)
		if err != nil || !renewed {
			t.Fatalf("expected renewal, got renewed=%v err=%v", renewed, err)
		}
		if _, err := tls.LoadX509KeyPair(files.certPath, files.keyPath); err != nil {
			t.Fatalf("written key/cert do not form a valid pair: %v", err)
		}
		certPEM, _ := os.ReadFile(files.certPath)
		if renew, notAfter := needsRenewal(certPEM, now); renew || notAfter.Before(now.Add(89*24*time.Hour)) {
			t.Fatalf("expected a fresh certificate on disk, notAfter=%s", notAfter)
		}
		info, _ := os.Stat(files.keyPath)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("key file mode = %v, want 0600", info.Mode().Perm())
		}
	})

	t.Run("does nothing while the certificate is fresh", func(t *testing.T) {
		dir := t.TempDir()
		files := writeCurrentCert(t, ca, dir, nodeID, now.Add(-time.Hour), now.Add(90*24*time.Hour))
		api := &fakeAPI{t: t, ca: ca, cn: nodeID, lifetime: 90 * 24 * time.Hour}
		if renewed, err := renewClientCertIfNeeded(context.Background(), api, nodeID, files, now); err != nil || renewed || api.calls != 0 {
			t.Fatalf("expected no renewal, got renewed=%v err=%v calls=%d", renewed, err, api.calls)
		}
	})

	rejections := []struct {
		name string
		api  func(t *testing.T) *fakeAPI
	}{
		{"certificate for another node", func(t *testing.T) *fakeAPI {
			return &fakeAPI{t: t, ca: ca, cn: "other-node", lifetime: 90 * 24 * time.Hour}
		}},
		{"certificate for a different key", func(t *testing.T) *fakeAPI {
			return &fakeAPI{t: t, ca: ca, cn: nodeID, lifetime: 90 * 24 * time.Hour, foreign: true}
		}},
		{"certificate that does not extend validity", func(t *testing.T) *fakeAPI {
			return &fakeAPI{t: t, ca: ca, cn: nodeID, lifetime: 24 * time.Hour}
		}},
	}
	for _, tc := range rejections {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := writeCurrentCert(t, ca, dir, nodeID, now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour))
			beforeCert, _ := os.ReadFile(files.certPath)
			beforeKey, _ := os.ReadFile(files.keyPath)

			if renewed, err := renewClientCertIfNeeded(context.Background(), tc.api(t), nodeID, files, now); err == nil || renewed {
				t.Fatalf("expected rejection, got renewed=%v err=%v", renewed, err)
			}
			afterCert, _ := os.ReadFile(files.certPath)
			afterKey, _ := os.ReadFile(files.keyPath)
			if string(afterCert) != string(beforeCert) || string(afterKey) != string(beforeKey) {
				t.Fatal("files must be left untouched when the renewed certificate is rejected")
			}
		})
	}
}
