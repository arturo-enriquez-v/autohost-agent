// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"log"

	pb "autohost-agent/internal/transport/nodepb"
)

// handleCollectLogs is invoked when the server sends a CollectLogsPayload.
// It collects logs from the requested sources, sanitizes them, runs the local
// analyzer, and sends back a LogDiagnosticPayload through the results channel.
func (c *GRPCClient) handleCollectLogs(ctx context.Context, req *pb.CollectLogsPayload, results chan<- *pb.NodeMessage) {
	requestID := req.GetRequestId()
	log.Printf("📋 log diagnostic: collecting logs for request %s (%d sources, include_containers=%v)",
		requestID, len(req.GetSources()), req.GetIncludeContainers())

	// 1. Collect and sanitize logs from all requested sources.
	bundles, err := collectAllSources(ctx, req)
	if err != nil {
		log.Printf("❌ log diagnostic %s: collection failed: %v", requestID, err)
		sendDiagnosticError(requestID, err.Error(), results)
		return
	}

	if len(bundles) == 0 {
		log.Printf("⚠️  log diagnostic %s: no sources collected", requestID)
		sendDiagnosticError(requestID, "no log sources could be collected", results)
		return
	}

	// 2. Run rules-based analysis on the collected (already sanitized) logs.
	diagnosticJSON := analyzeLogBundles(bundles)

	// 3. Send the result back to the server.
	msg := &pb.NodeMessage{
		Payload: &pb.NodeMessage_LogDiagnostic{
			LogDiagnostic: &pb.LogDiagnosticPayload{
				RequestId:      requestID,
				DiagnosticJson: diagnosticJSON,
				Sources:        bundles,
			},
		},
	}

	select {
	case results <- msg:
		log.Printf("✅ log diagnostic %s: sent %d bundles to server", requestID, len(bundles))
	case <-ctx.Done():
		log.Printf("⚠️  log diagnostic %s: context cancelled before sending", requestID)
	default:
		log.Printf("⚠️  log diagnostic %s: results buffer full, dropping", requestID)
	}
}

// sendDiagnosticError sends a LogDiagnosticPayload with only the error field set.
func sendDiagnosticError(requestID, errMsg string, results chan<- *pb.NodeMessage) {
	msg := &pb.NodeMessage{
		Payload: &pb.NodeMessage_LogDiagnostic{
			LogDiagnostic: &pb.LogDiagnosticPayload{
				RequestId: requestID,
				Error:     errMsg,
			},
		},
	}
	select {
	case results <- msg:
	default:
	}
}
