package erunmcp

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
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

func TestActivityLeaseToolsRoundTripAgainstTheServerContext(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	// A real pid, because the lease is reconciled against its holder on every
	// read — a made-up one would be reclaimed as an orphan before the call
	// returns, which is the behaviour, not a test artefact.
	holder := os.Getpid()
	_, taken, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
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

	_, released, err := activityLeaseReleaseTool(runtime)(context.Background(), nil, ActivityLeaseReleaseInput{ID: "agent-run"})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(released.Held) != 0 {
		t.Fatalf("expected no leases after release, got %+v", released.Held)
	}
}

// erun#1245: a second orchestrator or agent job must be refused, named, and
// pointed at who holds the worktree - not silently allowed to collide.

func TestExclusiveLeaseToolRefusesASecondHolderAndNamesIt(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "erun", Environment: "ux"}}

	_, first, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true, Orchestrator: "petios",
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || !first.Lease.Exclusive || first.Lease.Scope != "worktree" {
		t.Fatalf("expected an exclusive worktree-scoped lease, got %+v", first.Lease)
	}

	_, _, err = activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1245", ID: "job-fix-1245", Exclusive: true, Orchestrator: "erun",
	})
	var conflict *eruncommon.EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected the second exclusive take to be refused with a conflict, got %v", err)
	}
	if conflict.Holder.ID != "job-fix-1201" || conflict.Holder.Holder.Orchestrator != "petios" {
		t.Fatalf("refusal must name the actual holder, got %+v", conflict.Holder)
	}
}

func TestExclusiveLeaseToolAllowsDifferentScopesInOnePod(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "erun", Environment: "pod"}}

	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-a", ID: "clone-a", Exclusive: true, Scope: "/git/clone-a",
	}); err != nil {
		t.Fatalf("take scope a: %v", err)
	}
	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-b", ID: "clone-b", Exclusive: true, Scope: "/git/clone-b",
	}); err != nil {
		t.Fatalf("expected a second clone's own scope to succeed, got %v", err)
	}
}

func TestExclusiveLeaseToolRenewalSkipsTheOperatorPresenceGate(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true,
	}); err != nil {
		t.Fatalf("first take: %v", err)
	}
	// Record ssh activity directly to simulate an operator session, and
	// confirm the *renewal* by the same id still succeeds - only a fresh
	// claim is gated on operator presence.
	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: eruncommon.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1201", ID: "job-fix-1201", Exclusive: true,
	}); err != nil {
		t.Fatalf("expected the same holder's renewal to proceed despite ssh activity, got %v", err)
	}
}

func TestExclusiveLeaseToolRefusesAFreshClaimWhileAnOperatorSSHSessionIsActive(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: eruncommon.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "job-fix-1245", ID: "job-fix-1245", Exclusive: true,
	})
	var operatorPresent *eruncommon.EnvironmentOperatorPresentError
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
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	_, first, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || first.Lease.ID != "release_1.4.2" {
		t.Fatalf("expected the stored lease id to be sanitized, got %+v", first.Lease)
	}

	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: eruncommon.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
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
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}
	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: eruncommon.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	_, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "release 1.4.2", Exclusive: true,
	})
	var operatorPresent *eruncommon.EnvironmentOperatorPresentError
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
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "erun", Environment: "ux"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{"erun/ux": {Name: "ux"}},
		},
	}

	_, first, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-a", Exclusive: true, Scope: " clone-a ",
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Lease == nil || first.Lease.Scope != "clone-a" {
		t.Fatalf("expected the stored lease scope to be trimmed, got %+v", first.Lease)
	}

	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "erun", Environment: "ux", Kind: eruncommon.ActivityKindSSH,
	}); err != nil {
		t.Fatalf("seed ssh activity: %v", err)
	}

	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "clone-a", Exclusive: true, Scope: " clone-a ",
	}); err != nil {
		t.Fatalf("expected the same caller's renewal of an untrimmed scope to proceed despite ssh activity, got %v", err)
	}
}

func TestActivityLeaseToolsRejectIncompleteInput(t *testing.T) {
	isolateLeaseCache(t)
	// No server context and none supplied: an MCP path must fail clearly rather
	// than writing a lease somewhere unintended.
	if _, _, err := activityLeaseTakeTool(RuntimeConfig{})(context.Background(), nil, ActivityLeaseTakeInput{Name: "x"}); err == nil {
		t.Error("expected an error without a resolvable tenant and environment")
	}
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	// A lease with no name would report the env busy without saying why.
	if _, _, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{}); err == nil {
		t.Error("expected an error when the lease has no name")
	}
	if _, _, err := activityLeaseReleaseTool(runtime)(context.Background(), nil, ActivityLeaseReleaseInput{}); err == nil {
		t.Error("expected an error when the release names no lease")
	}
}
