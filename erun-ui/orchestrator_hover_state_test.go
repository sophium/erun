package main

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// These cover the join orchestratorInfoFor performs against the
// environment-activity poller's own state (environment_activity.go) and
// against an orchestrator session's pacing state (orchestrator_pacing.go) —
// the two sources the hover card renders without collecting anything new.

func TestEnvInfosJoinsActivityByTenantAndEnvironment(t *testing.T) {
	envs := []eruncommon.OrchestratorEnvConfig{
		{Tenant: "petios", Environment: "rihards-review"},
		{Tenant: "erun", Environment: "local-ideas"},
	}
	activity := map[string]environmentActivityState{
		selectionKey(uiSelection{Tenant: "petios", Environment: "rihards-review"}): {
			reachable: true, observed: true, busy: true, detail: "holding: gradle-build",
		},
	}
	out := envInfos(envs, activity, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 envs, got %d", len(out))
	}
	if out[0].Activity == nil {
		t.Fatalf("expected petios/rihards-review to carry a joined activity snapshot")
	}
	if !out[0].Activity.Busy || out[0].Activity.Detail != "holding: gradle-build" {
		t.Fatalf("expected the busy detail to survive the join, got %+v", out[0].Activity)
	}
	if out[1].Activity != nil {
		t.Fatalf("expected erun/local-ideas to have no observation yet, got %+v", out[1].Activity)
	}
}

func TestEnvInfosDistinguishesOutageFromIdleAndUnobserved(t *testing.T) {
	key := selectionKey(uiSelection{Tenant: "frs", Environment: "dev"})
	envs := []eruncommon.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}}

	outage := envInfos(envs, map[string]environmentActivityState{key: {outage: true}}, nil)
	if outage[0].Activity == nil || !outage[0].Activity.Outage {
		t.Fatalf("expected an outage observation to render as outage, got %+v", outage[0].Activity)
	}

	idle := envInfos(envs, map[string]environmentActivityState{key: {reachable: true, observed: true, busy: false}}, nil)
	if idle[0].Activity == nil || idle[0].Activity.Outage || idle[0].Activity.Busy {
		t.Fatalf("expected a reachable, observed, non-busy env to render idle, got %+v", idle[0].Activity)
	}

	unobserved := envInfos(envs, map[string]environmentActivityState{key: {reachable: true, observed: false}}, nil)
	if unobserved[0].Activity == nil || unobserved[0].Activity.Observed {
		t.Fatalf("expected reachable-but-unobserved to stay distinct from idle, got %+v", unobserved[0].Activity)
	}
}

func TestEnvInfosJoinsUsageByTenantAndEnvironment(t *testing.T) {
	envs := []eruncommon.OrchestratorEnvConfig{
		{Tenant: "petios", Environment: "rihards-review"},
		{Tenant: "erun", Environment: "local-ideas"},
	}
	observedAt := time.Unix(1000, 0)
	usage := map[string]environmentUsageReading{
		selectionKey(uiSelection{Tenant: "petios", Environment: "rihards-review"}): {
			usage:      uiRuntimeUsage{Available: true, CPU: uiRuntimeCPUUsage{Available: true, Utilization: "12.0%"}},
			observedAt: observedAt,
		},
	}
	out := envInfos(envs, nil, usage)
	if out[0].Usage == nil {
		t.Fatalf("expected petios/rihards-review to carry a joined usage snapshot")
	}
	if out[0].Usage.Usage.CPU.Utilization != "12.0%" || out[0].Usage.ObservedAtUnix != observedAt.Unix() {
		t.Fatalf("expected the usage reading and its timestamp to survive the join, got %+v", out[0].Usage)
	}
	if out[1].Usage != nil {
		t.Fatalf("expected erun/local-ideas to have no usage observation yet, got %+v", out[1].Usage)
	}
}

func TestOrchestratorInfoForCarriesThePacingSnapshotVerbatim(t *testing.T) {
	info := orchestratorInfoFor("id", "name", nil, "running", 1, orchestratorBusySnapshot{}, false,
		orchestratorShellSnapshot{}, orchestratorPacingSnapshot{NudgeCount: 3, Capped: true, LastNudgeAtUnix: 42}, nil, nil, false)
	if info.NudgeCount != 3 || !info.NudgeCapped || info.LastNudgeAtUnix != 42 {
		t.Fatalf("expected the pacing snapshot to pass through unchanged, got %+v", info)
	}
}

func TestOrchestratorInfoForStoppedOrchestratorReportsNeverNudged(t *testing.T) {
	info := orchestratorInfoFor("id", "name", nil, "stopped", 0, orchestratorBusySnapshot{}, false,
		orchestratorShellSnapshot{}, orchestratorPacingSnapshot{}, nil, nil, false)
	if info.NudgeCount != 0 || info.NudgeCapped {
		t.Fatalf("expected a stopped orchestrator to report never-nudged, got %+v", info)
	}
}
