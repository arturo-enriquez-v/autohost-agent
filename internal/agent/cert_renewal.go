// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	httpclient "autohost-agent/internal/transport/http"
)

// certRenewalInterval is how often the agent checks its mTLS client certificate.
const certRenewalInterval = 6 * time.Hour

// certRenewer is the API call used to obtain a new certificate (implemented by httpclient.Client).
type certRenewer interface {
	RenewClientCert(ctx context.Context, csrPEM string) (*httpclient.RenewedCertificate, error)
}

// certFiles are the on-disk locations of the agent's mTLS material.
type certFiles struct {
	certPath string
	keyPath  string
	caPath   string // optional; updated when the API returns a different (valid) CA
}

// needsRenewal reports whether the certificate should be renewed: when a third or less of its lifetime
// remains (at least one day before expiry), or when it cannot be read or parsed.
func needsRenewal(certPEM []byte, now time.Time) (bool, time.Time) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return true, time.Time{}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true, time.Time{}
	}
	lifetime := cert.NotAfter.Sub(cert.NotBefore)
	threshold := lifetime / 3
	if threshold < 24*time.Hour {
		threshold = 24 * time.Hour
	}
	return cert.NotAfter.Sub(now) <= threshold, cert.NotAfter
}

// renewClientCertIfNeeded renews the client certificate when needsRenewal says so. A new key pair is generated
// for every renewal; files are only replaced after the returned certificate has been validated against it.
func renewClientCertIfNeeded(ctx context.Context, api certRenewer, nodeID string, files certFiles, now time.Time) (bool, error) {
	current, err := os.ReadFile(files.certPath)
	if err != nil {
		return false, fmt.Errorf("read client certificate: %w", err)
	}
	renew, notAfter := needsRenewal(current, now)
	if !renew {
		return false, nil
	}
	log.Printf("🔐 mTLS client certificate expires %s — renewing", notAfter.Format(time.RFC3339))

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generate key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: nodeID, Organization: []string{"Autohost Node"}},
	}, key)
	if err != nil {
		return false, fmt.Errorf("create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	resp, err := api.RenewClientCert(ctx, string(csrPEM))
	if err != nil {
		return false, fmt.Errorf("renewal request: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return false, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM := []byte(resp.ClientCertPEM)

	if err := validateRenewedCert(certPEM, keyPEM, nodeID, notAfter); err != nil {
		return false, fmt.Errorf("rejecting renewed certificate: %w", err)
	}

	// Write the key first, then the certificate: if the process dies in between, the next check sees the
	// old (expiring) certificate and renews again with a fresh key.
	if err := writeFileAtomic(files.keyPath, keyPEM, 0600); err != nil {
		return false, fmt.Errorf("write key: %w", err)
	}
	if err := writeFileAtomic(files.certPath, certPEM, 0600); err != nil {
		return false, fmt.Errorf("write certificate: %w", err)
	}
	if files.caPath != "" && resp.CACertPEM != "" {
		if err := maybeUpdateCA(files.caPath, []byte(resp.CACertPEM)); err != nil {
			log.Printf("⚠️  could not update CA certificate: %v", err)
		}
	}
	log.Printf("✅ mTLS client certificate renewed (expires %s); it is used on the next gRPC reconnect", resp.ExpiresAt)
	return true, nil
}

// validateRenewedCert checks the certificate parses, matches the new private key, is bound to this node,
// and extends the current certificate's validity.
func validateRenewedCert(certPEM, keyPEM []byte, nodeID string, previousNotAfter time.Time) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("certificate does not match the generated key: %w", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return errors.New("no PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	if cert.Subject.CommonName != nodeID {
		return fmt.Errorf("certificate is for %q, expected %q", cert.Subject.CommonName, nodeID)
	}
	if !previousNotAfter.IsZero() && !cert.NotAfter.After(previousNotAfter) {
		return fmt.Errorf("new certificate expires %s, not after the current one (%s)", cert.NotAfter, previousNotAfter)
	}
	return nil
}

// maybeUpdateCA replaces the CA file only if the API returned a different, parseable CA certificate.
func maybeUpdateCA(caPath string, caPEM []byte) error {
	current, err := os.ReadFile(caPath)
	if err == nil && bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(caPEM)) {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("returned CA is not a valid PEM certificate")
	}
	return writeFileAtomic(caPath, caPEM, 0644)
}

// writeFileAtomic writes data to a temporary file in the same directory and renames it over path.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// certRenewalLoop checks the client certificate shortly after startup and then every certRenewalInterval.
func (a *Agent) certRenewalLoop(ctx context.Context) {
	files := certFiles{certPath: a.cfg.GRPCClientCert, keyPath: a.cfg.GRPCClientKey, caPath: a.cfg.GRPCCACert}
	check := func() {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := renewClientCertIfNeeded(cctx, a.apiClient, a.cfg.NodeID, files, time.Now()); err != nil {
			log.Printf("⚠️  mTLS certificate renewal: %v", err)
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
		check()
	}
	ticker := time.NewTicker(certRenewalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// certRenewalEnabled reports whether this agent uses file-based mTLS that it can renew.
func (a *Agent) certRenewalEnabled() bool {
	return !a.cfg.GRPCInsecure && a.cfg.GRPCClientCert != "" && a.cfg.GRPCClientKey != "" &&
		a.cfg.APIURL != "" && a.cfg.NodeID != "" && a.apiClient != nil
}
