// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	APIURL       string   `yaml:"api_url"`
	WSURL        string   `yaml:"ws_url"`
	GRPCAddress  string   `yaml:"grpc_address"`
	AgentToken   string   `yaml:"agent_token"`
	RefreshToken string   `yaml:"refresh_token"`
	NodeID       string   `yaml:"node_id"`
	Tags         []string `yaml:"tags"`
	GRPCInsecure bool     `yaml:"grpc_insecure"`
	GRPCCACert   string   `yaml:"grpc_ca_cert"`
	GRPCCertPin  string   `yaml:"grpc_cert_pin"`
}

func Load(path string) (*Config, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(bs, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetNodeID returns the node ID.
func (c *Config) GetNodeID() string {
	return c.NodeID
}

// GetTags returns the tags.
func (c *Config) GetTags() []string {
	return c.Tags
}
