package erunmcp

import (
	"context"
	"os"
	"testing"

	"github.com/adrg/xdg"
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
