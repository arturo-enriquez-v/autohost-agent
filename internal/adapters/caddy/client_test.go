// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package caddy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type mockCaddyServer struct {
	mu          sync.Mutex
	servers     map[string]serverConfig
	tlsPolicies []map[string]any
	objectsByID map[string]map[string]any
}

func newMockCaddyServer() *mockCaddyServer {
	return &mockCaddyServer{
		servers:     make(map[string]serverConfig),
		objectsByID: make(map[string]map[string]any),
	}
}

func (m *mockCaddyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := r.URL.Path
	switch {
	case path == "/config/" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
		return

	case path == "/config/apps/http/servers" && r.Method == http.MethodGet:
		if len(m.servers) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(m.servers)
		return

	case strings.HasPrefix(path, "/config/apps/http/servers/") && strings.HasSuffix(path, "/automatic_https"):
		srvName := strings.TrimSuffix(strings.TrimPrefix(path, "/config/apps/http/servers/"), "/automatic_https")
		srv, ok := m.servers[srvName]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet {
			if srv.AutomaticHTTPS == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(srv.AutomaticHTTPS)
			return
		}
		if r.Method == http.MethodPost {
			var cfg autoHTTPSConfig
			_ = json.NewDecoder(r.Body).Decode(&cfg)
			srv.AutomaticHTTPS = &cfg
			m.servers[srvName] = srv
			w.WriteHeader(http.StatusOK)
			return
		}

	case strings.HasPrefix(path, "/config/apps/http/servers/") && strings.HasSuffix(path, "/routes") && r.Method == http.MethodPost:
		srvName := strings.TrimSuffix(strings.TrimPrefix(path, "/config/apps/http/servers/"), "/routes")
		srv := m.servers[srvName]
		body, _ := io.ReadAll(r.Body)
		var route caddyRoute
		_ = json.Unmarshal(body, &route)
		srv.Routes = append(srv.Routes, route)
		m.servers[srvName] = srv
		if route.ID != "" {
			var raw map[string]any
			_ = json.Unmarshal(body, &raw)
			m.objectsByID[route.ID] = raw
		}
		w.WriteHeader(http.StatusOK)
		return

	case strings.HasPrefix(path, "/config/apps/http/servers/") && r.Method == http.MethodPost:
		srvName := strings.TrimPrefix(path, "/config/apps/http/servers/")
		var srv serverConfig
		_ = json.NewDecoder(r.Body).Decode(&srv)
		m.servers[srvName] = srv
		w.WriteHeader(http.StatusOK)
		return

	case path == "/config/apps/tls/automation/policies" && r.Method == http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		var pol map[string]any
		_ = json.Unmarshal(body, &pol)
		m.tlsPolicies = append(m.tlsPolicies, pol)
		if id, ok := pol["@id"].(string); ok && id != "" {
			m.objectsByID[id] = pol
		}
		w.WriteHeader(http.StatusOK)
		return

	case strings.HasPrefix(path, "/id/"):
		id := strings.TrimPrefix(path, "/id/")
		if r.Method == http.MethodPut {
			if _, exists := m.objectsByID[id]; !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var raw map[string]any
			_ = json.NewDecoder(r.Body).Decode(&raw)
			m.objectsByID[id] = raw
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			if _, exists := m.objectsByID[id]; !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(m.objectsByID, id)
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

func TestIsLocalOrPrivateDomain(t *testing.T) {
	locals := []string{
		"jellyfin.local",
		"nas.lan",
		"pi.home",
		"media.home.arpa",
		"api.internal",
		"app.localhost",
		"192.168.1.50",
		"100.64.0.10",
	}
	for _, d := range locals {
		if !IsLocalOrPrivateDomain(d) {
			t.Errorf("expected %q to be detected as local/private domain", d)
		}
	}

	if IsLocalOrPrivateDomain("app.example.com") {
		t.Errorf("did not expect app.example.com to be local domain")
	}
	if !IsTailscaleDomain("mynode.tailnet-123.ts.net") {
		t.Errorf("expected mynode.tailnet-123.ts.net to be detected as Tailscale domain")
	}
}

func TestUpsertRouteWithTLS_Modes(t *testing.T) {
	mock := newMockCaddyServer()
	srv := httptest.NewServer(mock)
	defer srv.Close()

	client := NewClient(srv.URL)

	// 1. Local .local domain with "auto" -> should configure internal TLS policy
	if err := client.UpsertRouteWithTLS("jellyfin.local", "localhost:8096", "auto"); err != nil {
		t.Fatalf("unexpected error for jellyfin.local: %v", err)
	}
	if _, ok := mock.objectsByID["autohost-tls-jellyfin-local"]; !ok {
		t.Fatalf("expected internal TLS policy autohost-tls-jellyfin-local to be created")
	}

	// 2. Tailscale *.ts.net domain with "auto" -> should configure tailscale get_certificate policy
	if err := client.UpsertRouteWithTLS("node1.tailnet.ts.net", "localhost:3000", "auto"); err != nil {
		t.Fatalf("unexpected error for node1.tailnet.ts.net: %v", err)
	}
	tsPol, ok := mock.objectsByID["autohost-tls-node1-tailnet-ts-net"]
	if !ok {
		t.Fatalf("expected tailscale TLS policy autohost-tls-node1-tailnet-ts-net to be created")
	}
	if _, hasGetCert := tsPol["get_certificate"]; !hasGetCert {
		t.Fatalf("expected get_certificate in tailscale TLS policy, got: %+v", tsPol)
	}

	// 3. HTTP-only mode for LAN IP -> should add to automatic_https.skip and not create TLS policy
	if err := client.UpsertRouteWithTLS("192.168.1.50", "localhost:9000", "http_only"); err != nil {
		t.Fatalf("unexpected error for 192.168.1.50 http_only: %v", err)
	}
	if _, ok := mock.objectsByID["autohost-tls-192-168-1-50"]; ok {
		t.Fatalf("did not expect TLS policy for http_only route")
	}
	httpSrv := mock.servers["autohost"]
	if httpSrv.AutomaticHTTPS == nil || len(httpSrv.AutomaticHTTPS.Skip) != 1 || httpSrv.AutomaticHTTPS.Skip[0] != "192.168.1.50" {
		t.Fatalf("expected 192.168.1.50 in automatic_https.skip, got: %+v", httpSrv.AutomaticHTTPS)
	}

	// 4. DeleteRoute cleans up TLS policy and route
	if err := client.DeleteRoute("jellyfin.local"); err != nil {
		t.Fatalf("unexpected error deleting jellyfin.local: %v", err)
	}
	if _, ok := mock.objectsByID["autohost-tls-jellyfin-local"]; ok {
		t.Fatalf("expected TLS policy to be deleted on DeleteRoute")
	}
}

