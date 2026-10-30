// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	pb "autohost-agent/internal/transport/nodepb"
	"autohost-agent/internal/security"
	"autohost-agent/internal/services/autoheal"
)

// healthCheckConfig mirrors the proto payload for local use.
type healthCheckConfig struct {
	MonitorID          string
	ServiceName        string
	CheckType          string // "process" | "tcp" | "http"
	HTTPURL            string
	HTTPExpectedStatus int
	TCPHost            string
	TCPPort            int
	IntervalSeconds    int
	FailureThreshold   int
	Enabled            bool
	AutoHealConfig     string
	AutoHealState      string
}

// healthMonitor manages all active health check loops for a single agent session.
type healthMonitor struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc // monitorID -> cancel
}

func newHealthMonitor() *healthMonitor {
	return &healthMonitor{cancels: make(map[string]context.CancelFunc)}
}

// upsert starts or restarts the health check loop for the given config.
func (hm *healthMonitor) upsert(ctx context.Context, cfg healthCheckConfig, results chan<- *pb.NodeMessage) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Cancel existing loop for this monitor, if any.
	if cancel, ok := hm.cancels[cfg.MonitorID]; ok {
		cancel()
	}

	if !cfg.Enabled {
		delete(hm.cancels, cfg.MonitorID)
		log.Printf("🔍 health-check %s (%s): disabled", cfg.ServiceName, cfg.MonitorID)
		return
	}

	loopCtx, cancel := context.WithCancel(ctx)
	hm.cancels[cfg.MonitorID] = cancel

	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	// Parse AutoHeal configuration & state
	var autoHealCfg autoheal.AutoHealConfig
	if cfg.AutoHealConfig != "" && cfg.AutoHealConfig != "{}" && cfg.AutoHealConfig != "null" {
		if err := json.Unmarshal([]byte(cfg.AutoHealConfig), &autoHealCfg); err != nil {
			log.Printf("⚠️ [autoheal] failed to parse AutoHealConfig for %s: %v", cfg.ServiceName, err)
		}
	}

	var autoHealState *autoheal.ServiceHealthState
	if cfg.AutoHealState != "" && cfg.AutoHealState != "{}" && cfg.AutoHealState != "null" {
		var st autoheal.ServiceHealthState
		if err := json.Unmarshal([]byte(cfg.AutoHealState), &st); err == nil {
			autoHealState = &st
		}
	}
	if autoHealState == nil {
		autoHealState = autoheal.CreateInitialState()
	}

	go func() {
		log.Printf("🔍 health-check %s (%s): started every %s (auto-heal=%v)", cfg.ServiceName, cfg.MonitorID, interval, autoHealCfg.Enabled)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		consecutiveFailures := 0

		for {
			select {
			case <-loopCtx.Done():
				log.Printf("🔍 health-check %s (%s): stopped", cfg.ServiceName, cfg.MonitorID)
				return
			case <-ticker.C:
				status, latencyMs, msg := runCheck(loopCtx, cfg)
				isHealthy := (status == "up")
				if isHealthy {
					consecutiveFailures = 0
				} else {
					consecutiveFailures++
				}

				var autoHealStateJSON string
				if autoHealCfg.Enabled {
					if autoHealCfg.ConsecutiveFailuresThreshold <= 0 {
						if cfg.FailureThreshold > 0 {
							autoHealCfg.ConsecutiveFailuresThreshold = cfg.FailureThreshold
						} else {
							autoHealCfg.ConsecutiveFailuresThreshold = 3
						}
					}

					callbacks := &autoheal.AutoHealCallbacks{
						OnLog: func(message string, level string) {
							log.Printf("🩺 %s", message)
						},
						OnStatusChange: func(serviceName string, previousStatus, newStatus autoheal.ServiceStatus) {
							log.Printf("🩺 [autoheal] %s: status changed from %s to %s", serviceName, previousStatus, newStatus)
						},
						OnCriticalFailure: func(serviceName string, state *autoheal.ServiceHealthState, err error) {
							log.Printf("🚨 [autoheal] %s: critical failure: %v", serviceName, err)
						},
					}

					if err := autoheal.HandleServiceCheck(loopCtx, autoHealCfg, autoHealState, isHealthy, cfg.ServiceName, nil, callbacks); err != nil {
						log.Printf("🩺 [autoheal] error executing check for %s: %v", cfg.ServiceName, err)
					}

					if bytes, err := json.Marshal(autoHealState); err == nil {
						autoHealStateJSON = string(bytes)
					}
				}

				payload := &pb.NodeMessage{
					Payload: &pb.NodeMessage_HealthResult{
						HealthResult: &pb.HealthCheckResultPayload{
							MonitorId:           cfg.MonitorID,
							Status:              status,
							LatencyMs:           int32(latencyMs),
							Message:             msg,
							ConsecutiveFailures: int32(consecutiveFailures),
							AutoHealState:       autoHealStateJSON,
						},
					},
				}
				select {
				case results <- payload:
				case <-loopCtx.Done():
					return
				}
			}
		}
	}()
}

// remove stops the health check loop for the given monitor ID.
func (hm *healthMonitor) remove(monitorID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if cancel, ok := hm.cancels[monitorID]; ok {
		cancel()
		delete(hm.cancels, monitorID)
	}
}

// stopAll stops all running health check loops.
func (hm *healthMonitor) stopAll() {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	for id, cancel := range hm.cancels {
		cancel()
		delete(hm.cancels, id)
	}
}

// ─── Check implementations ───────────────────────────────────────────────────

// runCheck performs a single health check and returns (status, latencyMs, message).
func runCheck(ctx context.Context, cfg healthCheckConfig) (string, int, string) {
	start := time.Now()

	switch cfg.CheckType {
	case "http":
		return checkHTTP(ctx, cfg, start)
	case "tcp":
		return checkTCP(ctx, cfg, start)
	default: // "process" — check Docker container status
		return checkProcess(ctx, cfg, start)
	}
}

// safeHTTPClient is a hardened HTTP client with strict IP filtering and redirect protection against SSRF.
var safeHTTPClient = newSafeHTTPClient()

func newSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address %s: %w", addr, err)
			}

			// Validate and resolve destination IPs to prevent SSRF and DNS Rebinding
			safeIPs, err := security.ResolveAndValidateIPs(ctx, host)
			if err != nil {
				return nil, err
			}

			// Connect strictly to the validated safe IP
			var lastErr error
			for _, ip := range safeIPs {
				targetAddr := net.JoinHostPort(ip.String(), port)
				conn, err := dialer.DialContext(ctx, network, targetAddr)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}

	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			// Enforce HTTP / HTTPS scheme only
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("security violation: redirect to unsupported scheme %q", req.URL.Scheme)
			}
			// Validate redirect destination
			_, err := security.ResolveAndValidateIPs(req.Context(), req.URL.Hostname())
			if err != nil {
				return fmt.Errorf("security violation on redirect: %w", err)
			}
			return nil
		},
	}
}

func checkHTTP(ctx context.Context, cfg healthCheckConfig, start time.Time) (string, int, string) {
	if cfg.HTTPURL == "" {
		return "down", 0, "http_url not configured"
	}

	parsedURL, err := url.Parse(cfg.HTTPURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return "down", 0, fmt.Sprintf("invalid or unsupported URL scheme: %s", cfg.HTTPURL)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, cfg.HTTPURL, nil)
	if err != nil {
		return "down", 0, fmt.Sprintf("build request: %v", err)
	}
	req.Header.Set("User-Agent", "autohost-agent/healthcheck")

	resp, err := safeHTTPClient.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return "down", latencyMs, fmt.Sprintf("http error: %v", err)
	}
	resp.Body.Close()

	expected := cfg.HTTPExpectedStatus
	if expected == 0 {
		expected = 200
	}
	if resp.StatusCode != expected {
		return "down", latencyMs, fmt.Sprintf("status %d (expected %d)", resp.StatusCode, expected)
	}
	return "up", latencyMs, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

func checkTCP(ctx context.Context, cfg healthCheckConfig, start time.Time) (string, int, string) {
	if cfg.TCPHost == "" || cfg.TCPPort == 0 {
		return "down", 0, "tcp_host or tcp_port not configured"
	}
	if cfg.TCPPort < 1 || cfg.TCPPort > 65535 {
		return "down", 0, fmt.Sprintf("invalid TCP port: %d", cfg.TCPPort)
	}

	// Validate and resolve destination IPs
	safeIPs, err := security.ResolveAndValidateIPs(ctx, cfg.TCPHost)
	if err != nil {
		return "down", 0, fmt.Sprintf("tcp security validation failed: %v", err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	dialer := &net.Dialer{}
	var conn net.Conn
	var dialErr error

	for _, ip := range safeIPs {
		targetAddr := net.JoinHostPort(ip.String(), fmt.Sprintf("%d", cfg.TCPPort))
		conn, dialErr = dialer.DialContext(dialCtx, "tcp", targetAddr)
		if dialErr == nil {
			break
		}
	}

	latencyMs := int(time.Since(start).Milliseconds())
	if dialErr != nil {
		return "down", latencyMs, fmt.Sprintf("tcp connect failed: %v", dialErr)
	}
	conn.Close()
	return "up", latencyMs, fmt.Sprintf("TCP %s:%d reachable", cfg.TCPHost, cfg.TCPPort)
}

// dockerSocketClient is a package-level, reusable HTTP client that talks to the
// Docker Unix socket. Creating a new http.Transport on every checkProcess call
// leaks the underlying idle-connection goroutines (readLoop + writeLoop) because
// the abandoned transport keeps them alive indefinitely. One shared client with
// MaxIdleConns=1 and a finite IdleConnTimeout avoids any accumulation.
var dockerSocketClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
		},
		MaxIdleConns:    1,
		IdleConnTimeout: 90 * time.Second,
	},
}

func checkProcess(ctx context.Context, cfg healthCheckConfig, start time.Time) (string, int, string) {
	name := cfg.ServiceName
	if strings.HasPrefix(name, "/") {
		name = strings.TrimPrefix(name, "/")
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(reqCtx,
		http.MethodGet,
		"http://docker/containers/"+name+"/json",
		nil,
	)
	resp, err := dockerSocketClient.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return "down", latencyMs, fmt.Sprintf("docker inspect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "down", latencyMs, "container not found"
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "down", latencyMs, fmt.Sprintf("docker API status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the container inspect response to check if it is actually running.
	var inspect struct {
		State struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"` // "running", "exited", "paused", etc.
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return "down", latencyMs, fmt.Sprintf("parse docker response: %v", err)
	}
	if !inspect.State.Running {
		return "down", latencyMs, fmt.Sprintf("container state: %s", inspect.State.Status)
	}
	return "up", latencyMs, "container running"
}
