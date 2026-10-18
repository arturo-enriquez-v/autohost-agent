// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"
)

// HTTPRequestLog represents a single incoming HTTP request processed by the web server.
type HTTPRequestLog struct {
	Timestamp  string  `json:"timestamp"`
	ClientIP   string  `json:"client_ip"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	StatusCode int     `json:"status_code"`
	DurationMs float64 `json:"duration_ms"`
	UserAgent  string  `json:"user_agent,omitempty"`
}

// caddyLogRecord matches Caddy 2 structured JSON access logs.
type caddyLogRecord struct {
	TS      float64 `json:"ts"`
	Request struct {
		RemoteAddr string `json:"remote_ip"`
		ClientIP   string `json:"client_ip"`
		Method     string `json:"method"`
		URI        string `json:"uri"`
		Headers    map[string][]string `json:"headers"`
	} `json:"request"`
	Status   int     `json:"status"`
	Duration float64 `json:"duration"`
}

// GetRecentHTTPRequests reads recent access log entries from Caddy/Nginx log files or journalctl.
func GetRecentHTTPRequests() ([]HTTPRequestLog, float64, error) {
	// Try reading /var/log/caddy/access.log or journalctl -u caddy
	records, err := readCaddyLogFile("/var/log/caddy/access.log")
	if err == nil && len(records) > 0 {
		rps := calculateRPS(records)
		return records, rps, nil
	}

	records, err = readCaddyJournal()
	if err == nil && len(records) > 0 {
		rps := calculateRPS(records)
		return records, rps, nil
	}

	return nil, 0, nil
}

func readCaddyLogFile(path string) ([]HTTPRequestLog, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read last 50 lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > 50 {
			lines = lines[1:]
		}
	}

	var results []HTTPRequestLog
	for i := len(lines) - 1; i >= 0; i-- {
		rec := parseCaddyJSONLine(lines[i])
		if rec != nil {
			results = append(results, *rec)
		}
	}
	return results, nil
}

func readCaddyJournal() ([]HTTPRequestLog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "journalctl", "-u", "caddy", "-n", "30", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var results []HTTPRequestLog
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		var entry struct {
			Message string `json:"MESSAGE"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil && entry.Message != "" {
			rec := parseCaddyJSONLine(entry.Message)
			if rec != nil {
				results = append(results, *rec)
			}
		}
	}
	return results, nil
}

func parseCaddyJSONLine(line string) *HTTPRequestLog {
	var c caddyLogRecord
	if err := json.Unmarshal([]byte(line), &c); err != nil || c.Request.URI == "" {
		return nil
	}

	clientIP := c.Request.ClientIP
	if clientIP == "" {
		parts := strings.Split(c.Request.RemoteAddr, ":")
		clientIP = parts[0]
	}

	tsStr := time.Unix(int64(c.TS), 0).Format("15:04:05")

	return &HTTPRequestLog{
		Timestamp:  tsStr,
		ClientIP:   clientIP,
		Method:     c.Request.Method,
		Path:       c.Request.URI,
		StatusCode: c.Status,
		DurationMs: c.Duration * 1000,
	}
}

func calculateRPS(reqs []HTTPRequestLog) float64 {
	if len(reqs) == 0 {
		return 0
	}
	return float64(len(reqs)) / 30.0 // Requests in last 30 seconds
}
