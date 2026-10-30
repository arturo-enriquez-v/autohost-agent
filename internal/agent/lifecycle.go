// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"context"
	"runtime"
	"runtime/debug"
	"time"
)

// Este archivo contiene funciones relacionadas con el ciclo de vida del agente.
// Por ahora es un placeholder para futuras funcionalidades como:
// - Inicialización del agente
// - Limpieza de recursos
// - Manejo de señales de sistema
// - Reinicio automático

// startMemoryTrimLoop runs a background goroutine that periodically triggers
// garbage collection and returns unused memory pages to the OS.  This prevents
// the agent's RSS from growing indefinitely on long-running deployments where
// Go's GC would otherwise keep freed pages in its own heap pool.
func startMemoryTrimLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runtime.GC()
				debug.FreeOSMemory()
			}
		}
	}()
}
