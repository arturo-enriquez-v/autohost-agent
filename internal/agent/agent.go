// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"context"
	"log"

	"autohost-agent/internal/adapters/state"
	"autohost-agent/internal/dispatcher"
	"autohost-agent/internal/domain"
	"autohost-agent/internal/services"
	"autohost-agent/internal/transport"
	"autohost-agent/internal/transport/http"
)

// Agent is the top-level orchestrator that coordinates all subsystems:
// gRPC (heartbeats, metrics, command reception) and job execution.
type Agent struct {
	cfg             *Config
	configPath      string
	version         string
	apiClient       *httpclient.Client
	grpcClient      *transport.GRPCClient
	registry        *dispatcher.Registry
	stateStore      state.Store
	lastKnownTokens struct {
		access  string
		refresh string
	}
}

// New creates and wires all agent subsystems.
func New(cfg *Config, version string) *Agent {
	var apiClient *httpclient.Client
	if cfg.RefreshToken != "" {
		apiClient = httpclient.NewClientWithRefresh(cfg.APIURL, cfg.AgentToken, cfg.RefreshToken)
	} else {
		apiClient = httpclient.NewClient(cfg.APIURL, cfg.AgentToken)
	}

	stateStore, err := state.New()
	if err != nil {
		log.Printf("⚠️  could not initialize local state store: %v", err)
	}

	metricsService := services.NewMetricsService()

	registry := dispatcher.NewRegistry()
	dispatcher.RegisterAll(registry)
	if err := dispatcher.RegisterCustomScripts(registry, domain.CustomCommandsDir); err != nil {
		log.Printf("⚠️  could not load custom scripts: %v", err)
	}

	grpcClient := transport.NewGRPCClient(
		cfg.GRPCAddress,
		cfg.AgentToken,
		cfg.NodeID,
		cfg.Tags,
		transport.GRPCTLSConfig{
			Insecure:   cfg.GRPCInsecure,
			CACertPath: cfg.GRPCCACert,
			CertPin:    cfg.GRPCCertPin,
		},
		registry,
		metricsService,
	)
	if stateStore != nil {
		grpcClient.SetStateStore(stateStore)
	}

	a := &Agent{
		cfg:        cfg,
		configPath: "/etc/autohost/config.yaml",
		version:    version,
		apiClient:  apiClient,
		grpcClient: grpcClient,
		registry:   registry,
		stateStore: stateStore,
	}
	a.lastKnownTokens.access = cfg.AgentToken
	a.lastKnownTokens.refresh = cfg.RefreshToken
	return a
}

// Run starts the agent: establishes gRPC connection which handles heartbeats,
// metrics, and command reception internally.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("Agent starting — NodeID: %s, gRPC: %s", a.cfg.NodeID, a.cfg.GRPCAddress)

	// Periodically return unused memory pages to the OS to keep RSS stable
	// on long-running VPS deployments.
	startMemoryTrimLoop(ctx)

	// Report version to the API on every startup so the frontend stays up to date.
	if a.version != "" {
		if err := a.apiClient.ReportVersion(ctx, a.version); err != nil {
			log.Printf("⚠️  could not report version to API: %v", err)
		} else {
			log.Printf("✅ Version %s reported to API", a.version)
		}
	}

	if a.cfg.GRPCAddress == "" {
		log.Println("ℹ️  gRPC address not configured — agent is idle")
		<-ctx.Done()
		return ctx.Err()
	}

	if err := a.grpcClient.Run(ctx); err != nil {
		log.Printf("gRPC client stopped: %v", err)
	}
	return ctx.Err()
}

