// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"encoding/json"
	"strings"
	"testing"

	pb "autohost-agent/internal/transport/nodepb"
)

func TestAnalyzeLogBundles_Healthy(t *testing.T) {
	bundles := []*pb.LogSourceBundle{
		{
			Source:    "journalctl:autohost-agent",
			RawLogs:  "2026-08-18T10:00:00Z INFO starting agent\n2026-08-18T10:00:05Z INFO connected to gRPC",
			LineCount: 2,
		},
		{
			Source:    "docker:nginx",
			RawLogs:  "2026-08-18T10:00:00Z [notice] 1#1: using the \"epoll\" event method\n2026-08-18T10:00:01Z 127.0.0.1 - GET / HTTP/1.1 200",
			LineCount: 2,
		},
	}

	rawJSON := analyzeLogBundles(bundles)

	var report diagnosticReport
	if err := json.Unmarshal([]byte(rawJSON), &report); err != nil {
		t.Fatalf("failed to parse diagnostic JSON: %v", err)
	}

	if report.SystemHealth != "HEALTHY" {
		t.Errorf("expected SystemHealth=HEALTHY, got %s", report.SystemHealth)
	}
	if len(report.Issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(report.Issues))
	}
	if len(report.SecurityInsights) != 0 {
		t.Errorf("expected 0 security insights, got %d", len(report.SecurityInsights))
	}
}

func TestAnalyzeLogBundles_CriticalIssue(t *testing.T) {
	bundles := []*pb.LogSourceBundle{
		{
			Source:    "journalctl:api-service",
			RawLogs:  "2026-08-18T10:00:00Z panic: runtime error: invalid memory address or nil pointer dereference\ngoroutine 1 [running]:",
			LineCount: 2,
		},
		{
			Source:    "docker:postgres",
			RawLogs:  "2026-08-18T10:00:00Z FATAL: could not connect to database: connection refused",
			LineCount: 1,
		},
	}

	rawJSON := analyzeLogBundles(bundles)

	var report diagnosticReport
	if err := json.Unmarshal([]byte(rawJSON), &report); err != nil {
		t.Fatalf("failed to parse diagnostic JSON: %v", err)
	}

	if report.SystemHealth != "CRITICAL" {
		t.Errorf("expected SystemHealth=CRITICAL, got %s", report.SystemHealth)
	}
	if len(report.Issues) < 2 {
		t.Errorf("expected at least 2 issues, got %d", len(report.Issues))
	}

	// Verify suggested_fix action is populated
	for _, issue := range report.Issues {
		if issue.SuggestedFix.Action == "" {
			t.Errorf("issue %s missing suggested_fix action", issue.ID)
		}
	}
}

func TestAnalyzeLogBundles_SecurityBruteForce(t *testing.T) {
	sshLogs := strings.Join([]string{
		"2026-08-18T10:00:01Z Failed password for root from 192.168.1.50 port 45678 ssh2",
		"2026-08-18T10:00:02Z Failed password for root from 192.168.1.50 port 45679 ssh2",
		"2026-08-18T10:00:03Z Failed password for root from 192.168.1.50 port 45680 ssh2",
		"2026-08-18T10:00:04Z Failed password for root from 192.168.1.50 port 45681 ssh2",
	}, "\n")

	bundles := []*pb.LogSourceBundle{
		{
			Source:    "journalctl:sshd",
			RawLogs:  sshLogs,
			LineCount: 4,
		},
	}

	rawJSON := analyzeLogBundles(bundles)

	var report diagnosticReport
	if err := json.Unmarshal([]byte(rawJSON), &report); err != nil {
		t.Fatalf("failed to parse diagnostic JSON: %v", err)
	}

	if len(report.SecurityInsights) == 0 {
		t.Fatalf("expected security insight for SSH brute-force, got 0")
	}

	insight := report.SecurityInsights[0]
	if insight.RiskLevel != "HIGH" {
		t.Errorf("expected RiskLevel=HIGH, got %s", insight.RiskLevel)
	}
	if !strings.Contains(insight.Finding, "SSH brute-force") {
		t.Errorf("unexpected finding text: %s", insight.Finding)
	}
}

func TestAnalyzeLogBundles_AnomalyPattern(t *testing.T) {
	bundles := []*pb.LogSourceBundle{
		{
			Source:    "docker:worker",
			RawLogs:  "2026-08-18T10:00:01Z container back-off restarting failed container",
			LineCount: 1,
		},
	}

	rawJSON := analyzeLogBundles(bundles)

	var report diagnosticReport
	if err := json.Unmarshal([]byte(rawJSON), &report); err != nil {
		t.Fatalf("failed to parse diagnostic JSON: %v", err)
	}

	if len(report.DetectedPatterns) == 0 {
		t.Fatalf("expected detected patterns, got 0")
	}
	if report.SystemHealth != "DEGRADED" {
		t.Errorf("expected SystemHealth=DEGRADED, got %s", report.SystemHealth)
	}
}
