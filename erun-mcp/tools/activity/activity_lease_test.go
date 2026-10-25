package activity

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/infra/activity/records"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func isolateLeaseCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func TestActivityLeaseToolsRoundTripAgainstTheServerContext(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	// A real pid: a made-up one is reclaimed as an orphan on the next read.
	holder := os.Getpid()
	_, taken, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "agent-run", PID: holder, TTLSeconds: 600,
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if taken.Tenant != "tenant-a" || taken.Environment != "dev" {
		t.Errorf("expected the server context to fill the target, got %s/%s", taken.Tenant, taken.Environment)
	}
	if taken.Lease == nil || taken.Lease.ID != "agent-run" || taken.Lease.PID != holder {
		t.Fatalf("unexpected lease %+v", taken.Lease)
	}
	if len(taken.Held) != 1 {
		t.Fatalf("expected the take to report the whole claim set, got %+v", taken.Held)
	}

	_, released, err := ActivityLeaseReleaseTool(runtime)(context.Background(), nil, ActivityLeaseReleaseInput{ID: "agent-run"})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(released.Held) != 0 {
		t.Fatalf("expected no leases after release, got %+v", released.Held)
	}
}

func TestExclusiveLeaseToolRefusesASecondHolderAndNamesIt(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "erun", Environment: "ux"}}

	_, first, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true, Orchestrator: "petios",
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || !first.Lease.Exclusive || first.Lease.Scope != "worktree" {
		t.Fatalf("expected an exclusive worktree-scoped lease, got %+v", first.Lease)
	}

	_, _, err = ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1245", ID: "job-fix-1245", Exclusive: true, Orchestrator: "erun",
	})
	var conflict *activitylease.EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected the second exclusive take to be refused with a conflict, got %v", err)
	}
	if conflict.Holder.ID != "job-fix-1201" || conflict.Holder.Holder.Orchestrator != "petios" {
		t.Fatalf("refusal must name the actual holder, got %+v", conflict.Holder)
	}
}

func TestExclusiveLeaseToolAllowsDifferentScopesInOnePod(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "erun", Environment: "pod"}}

	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-a", ID: "clone-a", Exclusive: true, Scope: "/git/clone-a",
	}); err != nil {
		t.Fatalf("take scope a: %v", err)
	}
	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-b", ID: "clone-b", Exclusive: true, Scope: "/git/clone-b",
	}); err != nil {
		t.Fatalf("expected a second clone's own scope to succeed, got %v", err)
	}
}

func TestActivityLeaseToolsRejectIncompleteInput(t *testing.T) {
	isolateLeaseCache(t)
	if _, _, err := ActivityLeaseTakeTool(runtimecfg.RuntimeConfig{})(context.Background(), nil, ActivityLeaseTakeInput{Name: "x"}); err == nil {
		t.Error("expected an error without a resolvable tenant and environment")
	}
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{}); err == nil {
		t.Error("expected an error when the lease has no name")
	}
	if _, _, err := ActivityLeaseReleaseTool(runtime)(context.Background(), nil, ActivityLeaseReleaseInput{}); err == nil {
		t.Error("expected an error when the release names no lease")
	}
}

func TestExclusiveLeaseToolRenewalSkipsTheOperatorPresenceGate(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true,
	}); err != nil {
		t.Fatalf("first take: %v", err)
	}
	// Only a fresh claim is gated on operator presence; a renewal still succeeds.
	if err := records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true,
	}); err != nil {
		t.Fatalf("expected the same holder's renewal to proceed despite ssh activity, got %v", err)
	}
}

func TestExclusiveLeaseToolRefusesAFreshClaimWhileAnOperatorSSHSessionIsActive(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1245", ID: "job-fix-1245", Exclusive: true,
	})
	var operatorPresent *model.EnvironmentOperatorPresentError
	if !errors.As(err, &operatorPresent) {
		t.Fatalf("expected a fresh exclusive claim to be refused while an ssh session is active, got %v", err)
	}
}

// The store sanitizes the id, so a renewal must normalize the raw id before comparing.
func TestExclusiveLeaseRenewalNormalizesASanitizedIDForTheSameCaller(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	_, first, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || first.Lease.ID != "release_1.4.2" {
		t.Fatalf("expected the stored lease id to be sanitized, got %+v", first.Lease)
	}

	if err := records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	}); err != nil {
		t.Fatalf("expected the same caller's renewal of a sanitized id to proceed despite ssh activity, got %v", err)
	}
}

// Normalization must apply to both sides, not disable the comparison.
func TestExclusiveLeaseFreshClaimWithASanitizedIDStillRefusedWhileOperatorPresent(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	})
	var operatorPresent *model.EnvironmentOperatorPresentError
	if !errors.As(err, &operatorPresent) {
		t.Fatalf("expected a fresh exclusive claim with a sanitized id to still be refused while an ssh session is active, got %v", err)
	}
}

// Scope is trimmed, not sanitized, so the renewal check must trim it too.
func TestExclusiveLeaseRenewalNormalizesAnUntrimmedScopeForTheSameCaller(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	_, first, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-a", Exclusive: true, Scope: " clone-a ",
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || first.Lease.Scope != "clone-a" {
		t.Fatalf("expected the stored lease scope to be trimmed, got %+v", first.Lease)
	}

	if err := records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-a", Exclusive: true, Scope: " clone-a ",
	}); err != nil {
		t.Fatalf("expected the same caller's renewal of an untrimmed scope to proceed despite ssh activity, got %v", err)
	}
}
