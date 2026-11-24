// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package autoheal

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type MockRunner struct {
	Commands []string
	Fail     bool
}

func (m *MockRunner) Exec(ctx context.Context, cmd string, args ...string) (string, error) {
	full := cmd + " " + strings.Join(args, " ")
	m.Commands = append(m.Commands, full)
	if m.Fail {
		return "failed", fmt.Errorf("mock error")
	}
	return "ok", nil
}

func TestAutoHealEngine(t *testing.T) {
	config := AutoHealConfig{
		Enabled:                      true,
		ConsecutiveFailuresThreshold: 3,
		GracePeriodSeconds:           30,
		Restart: RestartConfig{
			Enabled:       true,
			MaxAttempts:   2,
			WindowSeconds: 900,
		},
		Recreate: RecreateConfig{
			Enabled:         true,
			ComposeFilePath: "/opt/app/compose.yml",
			ServiceName:     "web-api",
			MaxAttempts:     1,
		},
	}

	runner := &MockRunner{}
	var logs []string
	criticalFired := false

	callbacks := &AutoHealCallbacks{
		OnLog: func(msg string, level string) {
			logs = append(logs, msg)
		},
		OnCriticalFailure: func(serviceName string, state *ServiceHealthState, err error) {
			criticalFired = true
		},
	}

	ctx := context.Background()
	state := CreateInitialState()

	// Test 1: Initial Healthy
	_ = HandleServiceCheck(ctx, config, state, true, "web-container", runner, callbacks)
	if state.Status != StatusHealthy {
		t.Errorf("Test 1 Failed: expected HEALTHY, got %s", state.Status)
	}

	// Test 2: 1st failure -> DEGRADED (1/3)
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusDegraded || state.ConsecutiveFailures != 1 {
		t.Errorf("Test 2 Failed: expected DEGRADED with 1 failure, got %s, %d", state.Status, state.ConsecutiveFailures)
	}

	// Test 3: 2nd failure -> DEGRADED (2/3)
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusDegraded || state.ConsecutiveFailures != 2 {
		t.Errorf("Test 3 Failed: expected DEGRADED with 2 failures, got %s, %d", state.Status, state.ConsecutiveFailures)
	}

	// Test 4: 3rd failure -> Threshold reached (3/3) -> Action: RESTART -> COOLING_DOWN
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusCoolingDown {
		t.Errorf("Test 4 Failed: expected COOLING_DOWN, got %s", state.Status)
	}
	if len(runner.Commands) != 1 || runner.Commands[0] != "docker restart web-container" {
		t.Errorf("Test 4 Failed: command not executed as expected: %v", runner.Commands)
	}

	// Test 5: Immediate check during grace period -> SKIPPED (COOLING_DOWN)
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusCoolingDown {
		t.Errorf("Test 5 Failed: expected COOLING_DOWN during grace period, got %s", state.Status)
	}
	if len(runner.Commands) != 1 {
		t.Errorf("Test 5 Failed: extra command executed during grace period!")
	}

	// Test 6: Past grace period -> 2nd Restart Attempt (2/2)
	past := time.Now().Unix() - 35
	state.LastActionTimestamp = &past
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusCoolingDown {
		t.Errorf("Test 6 Failed: expected COOLING_DOWN after 2nd restart, got %s", state.Status)
	}
	if len(runner.Commands) != 2 || runner.Commands[1] != "docker restart web-container" {
		t.Errorf("Test 6 Failed: 2nd restart command missing: %v", runner.Commands)
	}

	// Test 7: Past grace period -> Restarts exhausted (2/2) -> Action: RECREATE -> COOLING_DOWN
	past = time.Now().Unix() - 35
	state.LastActionTimestamp = &past
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusCoolingDown {
		t.Errorf("Test 7 Failed: expected COOLING_DOWN after recreate, got %s", state.Status)
	}
	expectedCompose := "docker compose -f /opt/app/compose.yml up -d --force-recreate web-api"
	if len(runner.Commands) != 3 || runner.Commands[2] != expectedCompose {
		t.Errorf("Test 7 Failed: compose command mismatch: %v", runner.Commands)
	}

	// Test 8: Past grace period -> Recreates exhausted (1/1) -> CRITICAL_FAILED
	past = time.Now().Unix() - 35
	state.LastActionTimestamp = &past
	_ = HandleServiceCheck(ctx, config, state, false, "web-container", runner, callbacks)
	if state.Status != StatusCriticalFailed {
		t.Errorf("Test 8 Failed: expected CRITICAL_FAILED, got %s", state.Status)
	}
	if !criticalFired {
		t.Errorf("Test 8 Failed: OnCriticalFailure callback not fired")
	}

	// Test 9: Recovery -> HEALTHY
	_ = HandleServiceCheck(ctx, config, state, true, "web-container", runner, callbacks)
	if state.Status != StatusHealthy || state.ConsecutiveFailures != 0 {
		t.Errorf("Test 9 Failed: expected HEALTHY with 0 failures, got %s, %d", state.Status, state.ConsecutiveFailures)
	}

	t.Log("All 9 Go Agent AutoHeal tests passed!")
}

// labelRunner answers "docker inspect" with the given labels JSON and records every command.
type labelRunner struct {
	labels   string
	commands []string
}

func (r *labelRunner) Exec(_ context.Context, cmd string, args ...string) (string, error) {
	r.commands = append(r.commands, cmd+" "+strings.Join(args, " "))
	if len(args) > 0 && args[0] == "inspect" {
		return r.labels + "\n", nil
	}
	return "ok", nil
}

func TestRecreateArgs(t *testing.T) {
	composeLabels := `{"com.docker.compose.project":"shop","com.docker.compose.project.working_dir":"/opt/shop",` +
		`"com.docker.compose.project.config_files":"/opt/shop/compose.yml,/opt/shop/compose.prod.yml",` +
		`"com.docker.compose.service":"web"}`
	tests := []struct {
		name    string
		rc      RecreateConfig
		labels  string
		want    string
		wantErr string
	}{
		{"configured file", RecreateConfig{ComposeFilePath: "/srv/app/compose.yml", ServiceName: "api"}, "",
			"compose -f /srv/app/compose.yml up -d --force-recreate api", ""},
		{"from labels", RecreateConfig{}, composeLabels,
			"compose -p shop --project-directory /opt/shop -f /opt/shop/compose.yml -f /opt/shop/compose.prod.yml up -d --force-recreate web", ""},
		{"labels with service override", RecreateConfig{ServiceName: "worker"}, composeLabels,
			"compose -p shop --project-directory /opt/shop -f /opt/shop/compose.yml -f /opt/shop/compose.prod.yml up -d --force-recreate worker", ""},
		{"service name filled with the container name", RecreateConfig{ServiceName: "shop-web-1"}, composeLabels,
			"compose -p shop --project-directory /opt/shop -f /opt/shop/compose.yml -f /opt/shop/compose.prod.yml up -d --force-recreate web", ""},
		{"not a compose container", RecreateConfig{}, `{"maintainer":"someone"}`, "", "not created by Docker Compose"},
		{"no labels", RecreateConfig{}, `null`, "", "not created by Docker Compose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &labelRunner{labels: tt.labels}
			args, err := recreateArgs(context.Background(), runner, tt.rc, "shop-web-1", "shop-web-1")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(args, " "); got != tt.want {
				t.Errorf("args = %q\n want %q", got, tt.want)
			}
		})
	}
}

// Without a compose file, the recreate step runs the command built from the container's labels.
func TestAutoHealRecreateFromLabels(t *testing.T) {
	config := AutoHealConfig{
		Enabled: true, ConsecutiveFailuresThreshold: 1,
		Recreate: RecreateConfig{Enabled: true, MaxAttempts: 1},
	}
	runner := &labelRunner{labels: `{"com.docker.compose.project":"shop","com.docker.compose.project.config_files":"/opt/shop/compose.yml","com.docker.compose.service":"web"}`}
	state := CreateInitialState()
	if err := HandleServiceCheck(context.Background(), config, state, false, "shop-web-1", runner, nil); err != nil {
		t.Fatal(err)
	}
	want := "docker compose -p shop -f /opt/shop/compose.yml up -d --force-recreate web"
	if last := runner.commands[len(runner.commands)-1]; last != want {
		t.Errorf("ran %q, want %q", last, want)
	}
	if state.RecreateAttempts != 1 {
		t.Errorf("recreate attempts = %d, want 1", state.RecreateAttempts)
	}
}
