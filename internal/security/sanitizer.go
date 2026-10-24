// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package security

import (
	"regexp"
	"strings"
)

// sanitizerRule defines a single secret-redaction pattern.
type sanitizerRule struct {
	pattern     *regexp.Regexp
	replacement string
}

// defaultRules contains the compiled rules used by SanitizeLogLines.
// Order matters: more specific patterns should come before generic ones.
var defaultRules = []sanitizerRule{
	// PEM private keys (multi-line, but we redact the header/footer lines)
	{regexp.MustCompile(`(?i)(-----BEGIN\s+(?:RSA|EC|DSA|OPENSSH|ENCRYPTED)?\s*PRIVATE\s+KEY-----)`), "$1 [REDACTED]"},

	// AWS access keys (AKIA...)
	{regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`), "[AWS_KEY_REDACTED]"},

	// Bearer / token authorization headers
	{regexp.MustCompile(`(?i)((?:Bearer|Token)\s+)[A-Za-z0-9\-._~+/]+=*`), "${1}[REDACTED]"},

	// Connection strings with embedded credentials: postgres://, mysql://, redis://, mongodb://
	{regexp.MustCompile(`(?i)((?:postgres|postgresql|mysql|redis|mongodb|amqp)://)([^:]+):([^@]+)@`), "${1}${2}:[REDACTED]@"},

	// Generic env-var style secrets: KEY=value (on a single line)
	// Matches: PASSWORD=xxx, SECRET_KEY=xxx, API_KEY=xxx, TOKEN=xxx, etc.
	{regexp.MustCompile(`(?i)((?:PASSWORD|PASSWD|SECRET|API_KEY|APIKEY|ACCESS_KEY|SECRET_KEY|PRIVATE_KEY|TOKEN|CREDENTIALS?|AUTH|DB_PASS|DATABASE_PASSWORD)\s*[=:]\s*)(\S+)`), "${1}[REDACTED]"},

	// JWT tokens (three dot-separated base64 segments)
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "[JWT_REDACTED]"},

	// Hex secrets that look like API keys or hashes (32+ hex chars following a known prefix)
	{regexp.MustCompile(`(?i)((?:key|secret|token|hash|signature)\s*[=:]\s*)[0-9a-f]{32,}`), "${1}[REDACTED]"},
}

// SanitizeLogLine applies all sanitizer rules to a single log line.
func SanitizeLogLine(line string) string {
	for _, rule := range defaultRules {
		line = rule.pattern.ReplaceAllString(line, rule.replacement)
	}
	return line
}

// SanitizeLogLines sanitizes a batch of log lines in-place and returns the slice.
func SanitizeLogLines(lines []string) []string {
	for i, line := range lines {
		lines[i] = SanitizeLogLine(line)
	}
	return lines
}

// SanitizeLogBlock sanitizes a multi-line log block (newline-separated string).
func SanitizeLogBlock(block string) string {
	lines := strings.Split(block, "\n")
	SanitizeLogLines(lines)
	return strings.Join(lines, "\n")
}
