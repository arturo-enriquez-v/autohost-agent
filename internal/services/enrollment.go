// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package services

import "context"

// EnrollmentService handles agent enrollment with the API.
type EnrollmentService struct {
	apiURL string
}

// NewEnrollmentService creates a new enrollment service.
func NewEnrollmentService(apiURL string) *EnrollmentService {
	return &EnrollmentService{apiURL: apiURL}
}

// Enroll registers this agent with the backend using the given token.
func (s *EnrollmentService) Enroll(ctx context.Context, token string) error {
	// TODO: Implement enrollment logic
	return nil
}

// EnrollmentToken represents a time-limited enrollment token.
type EnrollmentToken struct {
	Value     string
	ExpiresAt int64
}

// ValidateToken checks an enrollment token with the backend.
func ValidateToken(ctx context.Context, token string) (*EnrollmentToken, error) {
	// TODO: Implement token validation with backend
	return &EnrollmentToken{Value: token}, nil
}
