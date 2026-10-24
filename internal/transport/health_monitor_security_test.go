// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckHTTP_BlocksCloudMetadata(t *testing.T) {
	cfg := healthCheckConfig{
		MonitorID:   "test-mon-1",
		ServiceName: "evil-probe",
		CheckType:   "http",
		HTTPURL:     "http://169.254.169.254/latest/meta-data/",
	}

	status, _, msg := checkHTTP(context.Background(), cfg, time.Now())
	if status != "down" {
		t.Fatalf("expected status 'down', got %q", status)
	}
	if !strings.Contains(msg, "forbidden") && !strings.Contains(msg, "security violation") {
		t.Fatalf("expected security error in message, got: %s", msg)
	}
}

func TestCheckHTTP_BlocksUnsupportedSchemes(t *testing.T) {
	schemes := []string{
		"gopher://127.0.0.1:6379/_flushall",
		"file:///etc/passwd",
		"ftp://example.com/file",
	}

	for _, urlStr := range schemes {
		cfg := healthCheckConfig{
			MonitorID:   "test-mon-scheme",
			ServiceName: "evil-probe",
			CheckType:   "http",
			HTTPURL:     urlStr,
		}

		status, _, msg := checkHTTP(context.Background(), cfg, time.Now())
		if status != "down" {
			t.Fatalf("expected status 'down' for scheme %s, got %q", urlStr, status)
		}
		if !strings.Contains(msg, "unsupported") && !strings.Contains(msg, "invalid") {
			t.Fatalf("expected invalid/unsupported message for %s, got: %s", urlStr, msg)
		}
	}
}

func TestCheckHTTP_BlocksRedirectToMetadata(t *testing.T) {
	// Start a test server that redirects to 169.254.169.254
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer ts.Close()

	cfg := healthCheckConfig{
		MonitorID:   "test-mon-redir",
		ServiceName: "evil-redir-probe",
		CheckType:   "http",
		HTTPURL:     ts.URL,
	}

	status, _, msg := checkHTTP(context.Background(), cfg, time.Now())
	if status != "down" {
		t.Fatalf("expected status 'down' on malicious redirect, got %q", status)
	}
	if !strings.Contains(msg, "security violation") && !strings.Contains(msg, "forbidden") {
		t.Fatalf("expected redirect security violation in message, got: %s", msg)
	}
}

func TestCheckTCP_BlocksCloudMetadata(t *testing.T) {
	cfg := healthCheckConfig{
		MonitorID:   "test-mon-tcp-meta",
		ServiceName: "evil-tcp-probe",
		CheckType:   "tcp",
		TCPHost:     "169.254.169.254",
		TCPPort:     80,
	}

	status, _, msg := checkTCP(context.Background(), cfg, time.Now())
	if status != "down" {
		t.Fatalf("expected status 'down' for metadata TCP probe, got %q", status)
	}
	if !strings.Contains(msg, "security validation failed") && !strings.Contains(msg, "forbidden") {
		t.Fatalf("expected security failure message, got: %s", msg)
	}
}

func TestCheckHTTP_LegitimateServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer ts.Close()

	cfg := healthCheckConfig{
		MonitorID:          "test-mon-legit",
		ServiceName:        "web-service",
		CheckType:          "http",
		HTTPURL:            ts.URL,
		HTTPExpectedStatus: 200,
	}

	status, _, msg := checkHTTP(context.Background(), cfg, time.Now())
	if status != "up" {
		t.Fatalf("expected status 'up' for legitimate server, got %q (msg: %s)", status, msg)
	}
}
