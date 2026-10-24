package erunmcp

import (
	"context"
	"errors"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	activitypkg "github.com/sophium/erun/erun-mcp/tools/activity"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

func isolateLeaseCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func TestExclusiveLeaseToolRenewalSkipsTheOperatorPresenceGate(t *testing.T) {
	isolateLeaseCache(t)
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	if _, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true,
	}); err != nil {
		t.Fatalf("first take: %v", err)
	}
	// Only a fresh claim is gated on operator presence; a renewal still succeeds.
	if err := activity.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true,
	}); err != nil {
		t.Fatalf("expected the same holder's renewal to proceed despite ssh activity, got %v", err)
	}
}

func TestExclusiveLeaseToolRefusesAFreshClaimWhileAnOperatorSSHSessionIsActive(t *testing.T) {
	isolateLeaseCache(t)
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := activity.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
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
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	_, first, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || first.Lease.ID != "release_1.4.2" {
		t.Fatalf("expected the stored lease id to be sanitized, got %+v", first.Lease)
	}

	if err := activity.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	}); err != nil {
		t.Fatalf("expected the same caller's renewal of a sanitized id to proceed despite ssh activity, got %v", err)
	}
}

// Normalization must apply to both sides, not disable the comparison.
func TestExclusiveLeaseFreshClaimWithASanitizedIDStillRefusedWhileOperatorPresent(t *testing.T) {
	isolateLeaseCache(t)
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := activity.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
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
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	_, first, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "clone-a", Exclusive: true, Scope: " clone-a ",
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || first.Lease.Scope != "clone-a" {
		t.Fatalf("expected the stored lease scope to be trimmed, got %+v", first.Lease)
	}

	if err := activity.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "clone-a", Exclusive: true, Scope: " clone-a ",
	}); err != nil {
		t.Fatalf("expected the same caller's renewal of an untrimmed scope to proceed despite ssh activity, got %v", err)
	}
}
