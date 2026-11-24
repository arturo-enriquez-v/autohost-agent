// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "autohost-agent/internal/transport/nodepb"
)

func nextFailures(t *testing.T, results <-chan *pb.NodeMessage) int32 {
	t.Helper()
	select {
	case msg := <-results:
		return msg.GetHealthResult().GetConsecutiveFailures()
	case <-time.After(5 * time.Second):
		t.Fatal("no health check result")
		return 0
	}
}

// Re-sending a monitor (an edit, a toggle or a reconnect) must not reset its failure count, or a service
// that is still down goes back to "degraded" and alerts again when it reaches the threshold.
func TestHealthMonitor_KeepsFailuresAcrossRestarts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hm := newHealthMonitor()
	defer hm.stopAll()
	results := make(chan *pb.NodeMessage, 8)
	cfg := healthCheckConfig{
		MonitorID: "mon-1", ServiceName: "web", CheckType: "http",
		HTTPURL: ts.URL, HTTPExpectedStatus: 200, IntervalSeconds: 1, Enabled: true,
	}

	hm.upsert(ctx, cfg, results)
	if got := nextFailures(t, results); got != 1 {
		t.Fatalf("first check: failures = %d, want 1", got)
	}

	hm.stopAll() // the stream dropped
	hm.upsert(ctx, cfg, results)
	if got := nextFailures(t, results); got != 2 {
		t.Fatalf("after re-sending the monitor: failures = %d, want 2", got)
	}

	hm.remove("mon-1")
	hm.upsert(ctx, cfg, results)
	if got := nextFailures(t, results); got != 1 {
		t.Fatalf("after deleting and re-creating the monitor: failures = %d, want 1", got)
	}
}
