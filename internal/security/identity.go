// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package security

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

// Identity represents the agent's identity information.
type Identity struct {
	NodeID      string
	Fingerprint string
}

// GetOrCreateIdentity loads or generates a unique identity for the agent.
func GetOrCreateIdentity(configPath string) (*Identity, error) {
	// TODO: Implementar persistencia de identidad
	// - Leer desde archivo si existe
	// - Generar nueva identidad si no existe
	// - Guardar en archivo

	fingerprint, err := generateFingerprint()
	if err != nil {
		return nil, err
	}

	hostname, _ := os.Hostname()

	return &Identity{
		NodeID:      hostname,
		Fingerprint: fingerprint,
	}, nil
}

// generateFingerprint generates a random fingerprint for the agent.
func generateFingerprint() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
