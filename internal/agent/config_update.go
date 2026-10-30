// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UpdateTokensInConfig updates the agent_token and refresh_token in the config file.
// This is called when tokens are refreshed to persist them to disk atomically.
func (a *Agent) UpdateTokensInConfig(configPath string) error {
	var accessToken, refreshToken string
	if a.apiClient != nil {
		accessToken, refreshToken = a.apiClient.GetTokens()
	}
	return updateTokensInConfigFile(configPath, accessToken, refreshToken)
}

// updateTokensInConfigFile reads configPath, updates tokens via yaml.Node, and atomically writes it.
func updateTokensInConfigFile(configPath, accessToken, refreshToken string) error {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config %s: %w", configPath, err)
	}

	updatedYAML, err := updateYAMLTokens(content, accessToken, refreshToken)
	if err != nil {
		return fmt.Errorf("error updating YAML tokens: %w", err)
	}

	return atomicWriteFile(configPath, updatedYAML, 0640)
}

// updateYAMLTokens parses YAML AST, updates agent_token and refresh_token, and encodes back to YAML.
func updateYAMLTokens(content []byte, accessToken, refreshToken string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}

	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("invalid YAML root: expected mapping node")
	}

	mapping := root.Content[0]
	setMappingScalar(mapping, "agent_token", accessToken)
	setMappingScalar(mapping, "refresh_token", refreshToken)

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	return buf.Bytes(), nil
}

// setMappingScalar sets or updates a key-value pair in a YAML mapping node.
func setMappingScalar(mapping *yaml.Node, key, value string) {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = "!!str"
			mapping.Content[i+1].Style = yaml.DoubleQuotedStyle
			return
		}
	}

	// Key not found: append key and value nodes
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: key,
	}
	valNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: value,
		Style: yaml.DoubleQuotedStyle,
	}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}

// atomicWriteFile writes data to a temp file in the same directory and atomically renames it.
func atomicWriteFile(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("ensure config dir %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".config-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName) // Clean up if rename fails

	if err := tmpFile.Chmod(perm); err != nil {
		tmpFile.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomic rename %s -> %s: %w", tmpName, targetPath, err)
	}

	return nil
}

// checkAndUpdateTokens checks if tokens have changed and persists them to config.
func (a *Agent) checkAndUpdateTokens() error {
	if a.apiClient == nil {
		return nil
	}
	currentAccess, currentRefresh := a.apiClient.GetTokens()

	// Check if tokens have changed
	if currentAccess != a.lastKnownTokens.access || currentRefresh != a.lastKnownTokens.refresh {
		if err := a.UpdateTokensInConfig(a.configPath); err != nil {
			return fmt.Errorf("failed to persist updated tokens: %w", err)
		}

		// Update our tracking
		a.lastKnownTokens.access = currentAccess
		a.lastKnownTokens.refresh = currentRefresh

		log.Printf("✅ Tokens updated and persisted to config")
	}

	return nil
}