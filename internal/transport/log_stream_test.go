// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"strings"
	"testing"
)

func TestClampHistoryLines(t *testing.T) {
	cases := []struct {
		input    int32
		expected int32
	}{
		{-10, 0},
		{0, 0},
		{50, 50},
		{1000, 1000},
		{5000, 1000},
		{1000000, 1000},
	}

	for _, tc := range cases {
		actual := clampHistoryLines(tc.input)
		if actual != tc.expected {
			t.Errorf("clampHistoryLines(%d) = %d; expected %d", tc.input, actual, tc.expected)
		}
	}
}

func TestSanitizeLogLine(t *testing.T) {
	shortLine := "short log message"
	if sanitized := sanitizeLogLine(shortLine); sanitized != shortLine {
		t.Errorf("expected %q, got %q", shortLine, sanitized)
	}

	longLine := strings.Repeat("A", 5000)
	sanitized := sanitizeLogLine(longLine)
	if len(sanitized) > MaxLogLineBytes+len(" ... [truncated]") {
		t.Errorf("sanitized line length %d exceeds max allowed length", len(sanitized))
	}
	if !strings.HasSuffix(sanitized, " ... [truncated]") {
		t.Errorf("expected truncated suffix, got: %s", sanitized[len(sanitized)-20:])
	}
}
