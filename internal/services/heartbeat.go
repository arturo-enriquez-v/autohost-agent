// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package services

import (
	"context"
	"log"
	"os"
	"runtime"

	"autohost-agent/internal/domain"
	"autohost-agent/pkg/sysinfo"
)

// HeartbeatSender is the interface for sending heartbeats to the API.
type HeartbeatSender interface {
	SendHeartbeat(ctx context.Context, payload domain.HeartbeatPayload) error
}

// HeartbeatConfig provides the node identity for heartbeats.
type HeartbeatConfig interface {
	GetNodeID() string
	GetTags() []string
}

// HeartbeatService builds and sends heartbeat payloads.
type HeartbeatService struct {
	cfg    HeartbeatConfig
	sender HeartbeatSender
}

// NewHeartbeatService creates a new heartbeat service.
func NewHeartbeatService(cfg HeartbeatConfig, sender HeartbeatSender) *HeartbeatService {
	return &HeartbeatService{cfg: cfg, sender: sender}
}

// Send collects system info and sends a heartbeat.
func (s *HeartbeatService) Send(ctx context.Context) error {
	hostname, _ := os.Hostname()
	uptime, err := sysinfo.GetUptimeSeconds()
	if err != nil {
		log.Printf("warning: could not get uptime: %v", err)
		uptime = 0
	}

	payload := domain.HeartbeatPayload{
		NodeID:        s.cfg.GetNodeID(),
		Hostname:      hostname,
		Tags:          s.cfg.GetTags(),
		OS:            runtime.GOOS,
		UptimeSeconds: uptime,
	}

	return s.sender.SendHeartbeat(ctx, payload)
}
