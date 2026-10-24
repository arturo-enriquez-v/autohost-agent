// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Signer handles cryptographic signing of payloads.
type Signer struct {
	secret []byte
}

// NewSigner creates a new signer with the given secret.
func NewSigner(secret string) *Signer {
	return &Signer{
		secret: []byte(secret),
	}
}

// Sign signs a payload using HMAC-SHA256.
func (s *Signer) Sign(payload []byte) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Verify verifies that a signature matches the payload.
func (s *Signer) Verify(payload []byte, signature string) bool {
	expected := s.Sign(payload)
	return hmac.Equal([]byte(expected), []byte(signature))
}
