// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	pb "autohost-agent/internal/grpc/nodepb"
)

// ─── Diagnostic JSON structures ──────────────────────────────────────────────
// These mirror the schema required by the AutoHost diagnostic engine.

type diagnosticReport struct {
	Summary          string             `json:"summary"`
	SystemHealth     string             `json:"system_health"` // HEALTHY | DEGRADED | CRITICAL
	Issues           []diagnosticIssue  `json:"issues"`
	SecurityInsights []securityInsight   `json:"security_insights"`
	DetectedPatterns []string           `json:"detected_patterns"`
}

type diagnosticIssue struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`     // RUNTIME_ERROR | CONFIG_ERROR | NETWORK_TIMEOUT | DATABASE_FAILURE | RESOURCE_LIMIT | UNKNOWN
	Severity     string         `json:"severity"` // LOW | MEDIUM | CRITICAL
	Component    string         `json:"component"`
	RootCause    string         `json:"root_cause"`
	SuggestedFix suggestedFix   `json:"suggested_fix"`
}

type suggestedFix struct {
	Action  string  `json:"action"`
	Command *string `json:"command"`
}

type securityInsight struct {
	Finding        string `json:"finding"`
	RiskLevel      string `json:"risk_level"` // INFO | WARNING | HIGH
	Recommendation string `json:"recommendation"`
}

// ─── Pattern rules ───────────────────────────────────────────────────────────

type issuePattern struct {
	regex     *regexp.Regexp
	issueType string
	severity  string
	rootCause string
	fixAction string
	fixCmd    *string
}

type securityPattern struct {
	regex          *regexp.Regexp
	finding        string
	riskLevel      string
	recommendation string
	minMatches     int // how many matches before flagging (e.g. brute-force)
}

type anomalyPattern struct {
	regex   *regexp.Regexp
	message string
}

func strPtr(s string) *string { return &s }

var issuePatterns = []issuePattern{
	{
		regex:     regexp.MustCompile(`(?i)panic:`),
		issueType: "RUNTIME_ERROR", severity: "CRITICAL",
		rootCause: "Go runtime panic detected — unrecovered nil pointer or index out of range",
		fixAction: "Check stack trace for the offending goroutine and add nil/bounds checks",
	},
	{
		regex:     regexp.MustCompile(`(?i)fatal\s+error:`),
		issueType: "RUNTIME_ERROR", severity: "CRITICAL",
		rootCause: "Fatal runtime error (e.g. concurrent map write, stack overflow)",
		fixAction: "Review goroutine safety and resource limits in the affected process",
	},
	{
		regex:     regexp.MustCompile(`(?i)out\s*of\s*memory|OOM|oom-kill`),
		issueType: "RESOURCE_LIMIT", severity: "CRITICAL",
		rootCause: "Process killed by OOM killer — memory usage exceeded available RAM",
		fixAction: "Increase memory limit or optimize memory consumption",
		fixCmd:    strPtr("dmesg | grep -i 'oom\\|killed process' | tail -5"),
	},
	{
		regex:     regexp.MustCompile(`(?i)connection\s+refused`),
		issueType: "NETWORK_TIMEOUT", severity: "MEDIUM",
		rootCause: "Target service is not listening on the expected port",
		fixAction: "Verify the target service is running and bound to the correct address",
		fixCmd:    strPtr("ss -tlnp | grep <port>"),
	},
	{
		regex:     regexp.MustCompile(`(?i)(?:connection\s+timed?\s*out|i/o\s+timeout)`),
		issueType: "NETWORK_TIMEOUT", severity: "MEDIUM",
		rootCause: "Network request timed out — possible firewall, DNS, or routing issue",
		fixAction: "Check network connectivity, DNS resolution, and firewall rules",
	},
	{
		regex:     regexp.MustCompile(`(?i)permission\s+denied`),
		issueType: "CONFIG_ERROR", severity: "MEDIUM",
		rootCause: "Process lacks file-system or network permissions",
		fixAction: "Check file ownership, SELinux/AppArmor policies, and process user",
	},
	{
		regex:     regexp.MustCompile(`(?i)(?:ENOSPC|no\s+space\s+left\s+on\s+device)`),
		issueType: "RESOURCE_LIMIT", severity: "CRITICAL",
		rootCause: "Disk is full — no space left on the device",
		fixAction: "Free disk space or expand the volume",
		fixCmd:    strPtr("df -h && du -sh /var/log/* | sort -rh | head -10"),
	},
	{
		regex:     regexp.MustCompile(`(?i)(?:ECONNRESET|broken\s+pipe|reset\s+by\s+peer)`),
		issueType: "NETWORK_TIMEOUT", severity: "LOW",
		rootCause: "Connection reset by remote peer — transient network issue or client disconnect",
		fixAction: "Usually transient; check if it's repeated for the same endpoint",
	},
	{
		regex:     regexp.MustCompile(`(?i)(?:could\s+not\s+connect\s+to\s+(?:database|postgres|mysql|redis|mongo)|SQLSTATE|pq:\s+)`),
		issueType: "DATABASE_FAILURE", severity: "CRITICAL",
		rootCause: "Database connection failed — service may be down or credentials invalid",
		fixAction: "Verify database service status and connection credentials",
		fixCmd:    strPtr("systemctl status postgresql || docker ps | grep postgres"),
	},
	{
		regex:     regexp.MustCompile(`(?i)address\s+already\s+in\s+use|bind\s+failed.*port`),
		issueType: "CONFIG_ERROR", severity: "MEDIUM",
		rootCause: "Port conflict — another process is already bound to the same port",
		fixAction: "Identify the conflicting process and stop it or change the port",
		fixCmd:    strPtr("ss -tlnp | grep <port>"),
	},
}

var securityPatterns = []securityPattern{
	{
		regex:          regexp.MustCompile(`(?i)(?:Failed\s+password|authentication\s+failure|invalid\s+user).*ssh`),
		finding:        "SSH brute-force attempt detected — multiple failed authentication attempts",
		riskLevel:      "HIGH",
		recommendation: "Enable fail2ban, restrict SSH to key-only auth, and consider changing the SSH port",
		minMatches:     3,
	},
	{
		regex:          regexp.MustCompile(`(?i)(?:Failed\s+password|invalid\s+password|bad\s+credentials|unauthorized)`),
		finding:        "Repeated authentication failures detected",
		riskLevel:      "WARNING",
		recommendation: "Review access logs for brute-force patterns; consider rate-limiting authentication endpoints",
		minMatches:     5,
	},
	{
		regex:          regexp.MustCompile(`(?i)(?:root|admin|administrator).*(?:login|auth|session)`),
		finding:        "Login attempts using default/privileged usernames",
		riskLevel:      "WARNING",
		recommendation: "Disable root/admin direct login; use named user accounts with sudo",
		minMatches:     1,
	},
	{
		regex:          regexp.MustCompile(`(?i)(?:SQL\s+injection|UNION\s+SELECT|OR\s+1\s*=\s*1|DROP\s+TABLE|xp_cmdshell)`),
		finding:        "Potential SQL injection pattern detected in logs",
		riskLevel:      "HIGH",
		recommendation: "Ensure all database queries use parameterized statements; deploy a WAF",
		minMatches:     1,
	},
	{
		regex:          regexp.MustCompile(`(?i)(?:<script|javascript:|onerror\s*=|onload\s*=)`),
		finding:        "Potential XSS/script injection pattern detected in request logs",
		riskLevel:      "WARNING",
		recommendation: "Sanitize all user input; implement Content-Security-Policy headers",
		minMatches:     1,
	},
}

var anomalyPatterns = []anomalyPattern{
	{
		regex:   regexp.MustCompile(`(?i)(?:restarting|restart\s+count|back-off\s+restarting)`),
		message: "Container/service restart loop detected — possible crash-loop",
	},
	{
		regex:   regexp.MustCompile(`(?i)(?:too\s+many\s+open\s+files|EMFILE|ENFILE)`),
		message: "File descriptor exhaustion — process is hitting ulimit for open files",
	},
	{
		regex:   regexp.MustCompile(`(?i)(?:slow\s+query|query\s+took|exceeded\s+timeout)`),
		message: "Slow query / timeout pattern — potential database performance degradation",
	},
	{
		regex:   regexp.MustCompile(`(?i)(?:disk\s+I/O|await|iowait)\s*(?:high|slow|>)`),
		message: "High disk I/O wait — possible storage bottleneck",
	},
	{
		regex:   regexp.MustCompile(`(?i)(?:worker|goroutine|thread)\s+(?:pool\s+)?(?:exhausted|limit|full)`),
		message: "Worker/goroutine pool saturation — processing capacity at limit",
	},
}

// ─── Analyzer ────────────────────────────────────────────────────────────────

// analyzeLogBundles runs rules-based pattern detection on collected log bundles
// and produces a diagnostic JSON report.
func analyzeLogBundles(bundles []*pb.LogSourceBundle) string {
	report := diagnosticReport{
		Issues:           []diagnosticIssue{},
		SecurityInsights: []securityInsight{},
		DetectedPatterns: []string{},
	}

	issueID := 0
	// Track deduplication
	seenIssues := make(map[string]bool)   // issueType+rootCause
	seenSecurity := make(map[string]bool) // finding
	seenPatterns := make(map[string]bool) // message

	for _, bundle := range bundles {
		lines := strings.Split(bundle.GetRawLogs(), "\n")

		// --- Issue detection ---
		for _, pat := range issuePatterns {
			for _, line := range lines {
				if pat.regex.MatchString(line) {
					key := pat.issueType + ":" + pat.rootCause
					if seenIssues[key] {
						break
					}
					seenIssues[key] = true
					issueID++
					issue := diagnosticIssue{
						ID:        fmt.Sprintf("err-%d", issueID),
						Type:      pat.issueType,
						Severity:  pat.severity,
						Component: bundle.GetSource(),
						RootCause: pat.rootCause,
						SuggestedFix: suggestedFix{
							Action:  pat.fixAction,
							Command: pat.fixCmd,
						},
					}
					report.Issues = append(report.Issues, issue)
					break // one match per pattern per source is enough
				}
			}
		}

		// --- Security detection ---
		for _, pat := range securityPatterns {
			matchCount := 0
			for _, line := range lines {
				if pat.regex.MatchString(line) {
					matchCount++
				}
			}
			if matchCount >= pat.minMatches {
				key := pat.finding
				if !seenSecurity[key] {
					seenSecurity[key] = true
					report.SecurityInsights = append(report.SecurityInsights, securityInsight{
						Finding:        fmt.Sprintf("%s (%d occurrences in %s)", pat.finding, matchCount, bundle.GetSource()),
						RiskLevel:      pat.riskLevel,
						Recommendation: pat.recommendation,
					})
				}
			}
		}

		// --- Anomaly detection ---
		for _, pat := range anomalyPatterns {
			for _, line := range lines {
				if pat.regex.MatchString(line) {
					if !seenPatterns[pat.message] {
						seenPatterns[pat.message] = true
						report.DetectedPatterns = append(report.DetectedPatterns, fmt.Sprintf("[%s] %s", bundle.GetSource(), pat.message))
					}
					break
				}
			}
		}
	}

	// --- Determine overall health ---
	report.SystemHealth = "HEALTHY"
	hasCritical := false
	hasMedium := false
	for _, issue := range report.Issues {
		if issue.Severity == "CRITICAL" {
			hasCritical = true
		}
		if issue.Severity == "MEDIUM" {
			hasMedium = true
		}
	}
	for _, si := range report.SecurityInsights {
		if si.RiskLevel == "HIGH" {
			hasCritical = true
		}
	}

	if hasCritical {
		report.SystemHealth = "CRITICAL"
	} else if hasMedium || len(report.DetectedPatterns) > 0 {
		report.SystemHealth = "DEGRADED"
	}

	// --- Summary ---
	if report.SystemHealth == "HEALTHY" {
		report.Summary = "No anomalies or errors detected in the analyzed log sources."
	} else {
		report.Summary = fmt.Sprintf(
			"Detected %d issue(s), %d security finding(s), and %d anomalous pattern(s) across %d log source(s).",
			len(report.Issues), len(report.SecurityInsights), len(report.DetectedPatterns), len(bundles),
		)
	}

	data, _ := json.Marshal(report)
	return string(data)
}
