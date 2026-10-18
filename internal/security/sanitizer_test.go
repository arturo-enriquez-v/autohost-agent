// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package security

import (
	"strings"
	"testing"
)

func TestSanitizeLogLine_PasswordEnvVar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // substring that must appear
		deny  string // substring that must NOT appear
	}{
		{
			name:  "PASSWORD= value",
			input: `2026-01-01 DB_PASS=supersecret123 starting`,
			want:  "[REDACTED]",
			deny:  "supersecret123",
		},
		{
			name:  "SECRET_KEY= value",
			input: `SECRET_KEY=abcdef0123456789`,
			want:  "[REDACTED]",
			deny:  "abcdef0123456789",
		},
		{
			name:  "API_KEY colon value",
			input: `api_key: my-secret-key-value`,
			want:  "[REDACTED]",
			deny:  "my-secret-key-value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeLogLine(tt.input)
			if !strings.Contains(got, tt.want) {
				t.Errorf("SanitizeLogLine(%q) = %q, want substring %q", tt.input, got, tt.want)
			}
			if strings.Contains(got, tt.deny) {
				t.Errorf("SanitizeLogLine(%q) = %q, still contains %q", tt.input, got, tt.deny)
			}
		})
	}
}

func TestSanitizeLogLine_BearerToken(t *testing.T) {
	input := `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature`
	got := SanitizeLogLine(input)
	if strings.Contains(got, "eyJhbGci") {
		t.Errorf("JWT not redacted: %q", got)
	}
}

func TestSanitizeLogLine_ConnectionString(t *testing.T) {
	input := `DATABASE_URL=postgres://admin:P@ssw0rd!@db.example.com:5432/mydb`
	got := SanitizeLogLine(input)
	if strings.Contains(got, "P@ssw0rd!") {
		t.Errorf("password in conn string not redacted: %q", got)
	}
	// Host should be preserved
	if !strings.Contains(got, "db.example.com") {
		t.Errorf("host was incorrectly redacted: %q", got)
	}
}

func TestSanitizeLogLine_AWSKey(t *testing.T) {
	input := `using key AKIAIOSFODNN7EXAMPLE for S3`
	got := SanitizeLogLine(input)
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("AWS key not redacted: %q", got)
	}
}

func TestSanitizeLogLine_NoFalsePositive(t *testing.T) {
	// Normal log lines should not be modified
	inputs := []string{
		"2026-01-01T12:00:00 INFO server started on port 8080",
		"error: connection refused to 10.0.0.1:5432",
		"CPU usage 85.3%, memory 2.1GB/4GB",
	}
	for _, input := range inputs {
		got := SanitizeLogLine(input)
		if got != input {
			t.Errorf("false positive: %q → %q", input, got)
		}
	}
}

func TestSanitizeLogLines_Batch(t *testing.T) {
	lines := []string{
		"PASSWORD=secret1",
		"normal log line",
		"TOKEN=abc123",
	}
	SanitizeLogLines(lines)
	if strings.Contains(lines[0], "secret1") {
		t.Errorf("line 0 not sanitized: %q", lines[0])
	}
	if lines[1] != "normal log line" {
		t.Errorf("line 1 incorrectly modified: %q", lines[1])
	}
	if strings.Contains(lines[2], "abc123") {
		t.Errorf("line 2 not sanitized: %q", lines[2])
	}
}

func TestSanitizeLogLine_PEMKey(t *testing.T) {
	input := `-----BEGIN RSA PRIVATE KEY----- MIIEowIBAAKCAQEA...`
	got := SanitizeLogLine(input)
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("PEM key not redacted: %q", got)
	}
}
