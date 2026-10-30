// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"autohost-agent/internal/agent"
	"autohost-agent/internal/platform/paths"
)

// Inyectado en build time por goreleaser
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(os.Args) < 2 {
		log.Fatal("usage: autohost-agent <config-path>\n       autohost-agent --version")
	}

	if os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Printf("autohost-agent %s\n", Version)
		os.Exit(0)
	}

	if err := paths.EnsureAutohostDirs(); err != nil {
		log.Fatalf("creating autohost dirs: %v", err)
	}

	cfgPath := os.Args[1]

	log.Printf("Loading configuration from: %s", cfgPath)
	cfg, err := agent.Load(cfgPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	a := agent.New(cfg, Version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("Starting Autohost Agent...")
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent stopped: %v", err)
	}
	log.Println("Agent stopped gracefully")
}
