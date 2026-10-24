package erunmcp

import (
	"context"
	"errors"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/activity"
	activitypkg "github.com/sophium/erun/erun-mcp/tools/activity"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// The lease tools are how an orchestrator driving detached work in this pod
// tells the environment what it is doing. What matters here is the transport
// contract: the server's own tenant/environment context is used when the caller
// omits it, and every call reports the whole claim set rather than only the
// lease it just moved.

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
	// Record ssh activity directly to simulate an operator session, and
	// confirm the *renewal* by the same id still succeeds - only a fresh
	// claim is gated on operator presence.
	if err := activity.RecordEnvironmentActivity(activity.EnvironmentActivityParams{
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
	if err := activity.RecordEnvironmentActivity(activity.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "job-fix-1245", ID: "job-fix-1245", Exclusive: true,
	})
	var operatorPresent *activity.EnvironmentOperatorPresentError
	if !errors.As(err, &operatorPresent) {
		t.Fatalf("expected a fresh exclusive claim to be refused while an ssh session is active, got %v", err)
	}
}

// The lease id defaults to the name and is never sanitized by the caller,
// but the store sanitizes it before persisting. A renewal must normalize the
// raw id the same way before comparing against the stored lease, or a name
// needing sanitization (a space, here) always misreads as a fresh claim.
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

	if err := activity.RecordEnvironmentActivity(activity.EnvironmentActivityParams{
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

// The presence gate must still catch a genuinely fresh claim even when its id
// happens to need sanitization - the fix must normalize both sides of the
// comparison, not simply stop comparing.
func TestExclusiveLeaseFreshClaimWithASanitizedIDStillRefusedWhileOperatorPresent(t *testing.T) {
	isolateLeaseCache(t)
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := activity.RecordEnvironmentActivity(activity.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: config.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := activitypkg.ActivityLeaseTakeTool(runtime)(context.Background(), nil, activitypkg.ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	})
	var operatorPresent *activity.EnvironmentOperatorPresentError
	if !errors.As(err, &operatorPresent) {
		t.Fatalf("expected a fresh exclusive claim with a sanitized id to still be refused while an ssh session is active, got %v", err)
	}
}

// The lease's persisted Scope is only trimmed and defaulted, never run
// through the id's filename sanitisation, so an untrimmed scope - not a
// sanitized one - is the analogous mismatch on this field: the renewal check
// must trim it the same way the store does before comparing.
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

	if err := activity.RecordEnvironmentActivity(activity.EnvironmentActivityParams{
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
