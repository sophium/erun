package provision

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type claimCall struct {
	environmentID string
	staleAfter    time.Duration
}

type fakeReconcilerEnvironments struct {
	environments []model.Environment
	listErr      error
	claims       []claimCall
	// claimResults maps environmentID -> whether ClaimDelete should report a
	// successful claim; missing entries default to true (claimed).
	claimResults map[string]bool
	claimErr     error
	// blocked records MarkDeleteBlocked calls: environmentID -> reason. This
	// is what proves a claimed row is never left stranded in `deleting`.
	blocked    map[string]string
	blockedErr error
}

func (f *fakeReconcilerEnvironments) MarkDeleteBlocked(_ context.Context, environmentID, reason string) error {
	if f.blocked == nil {
		f.blocked = map[string]string{}
	}
	f.blocked[environmentID] = reason
	return f.blockedErr
}

func (f *fakeReconcilerEnvironments) ListByStatuses(_ context.Context, _ []model.EnvironmentStatus) ([]model.Environment, error) {
	return f.environments, f.listErr
}

func (f *fakeReconcilerEnvironments) ClaimDelete(_ context.Context, environmentID string, staleAfter time.Duration) (bool, error) {
	f.claims = append(f.claims, claimCall{environmentID: environmentID, staleAfter: staleAfter})
	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimResults == nil {
		return true, nil
	}
	claimed, ok := f.claimResults[environmentID]
	if !ok {
		return true, nil
	}
	return claimed, nil
}

type fakeReconcilerTenants struct {
	tenants []model.Tenant
	err     error
}

func (f *fakeReconcilerTenants) List(_ context.Context) ([]model.Tenant, error) {
	return f.tenants, f.err
}

type fakeReconcilerContexts struct {
	contexts map[string]model.Context
	err      error
}

func (f *fakeReconcilerContexts) Get(_ context.Context, contextID string) (model.Context, error) {
	if f.err != nil {
		return model.Context{}, f.err
	}
	return f.contexts[contextID], nil
}

type fakeDeleteStarter struct {
	started []EnvDeleteInput
	err     error
}

func (f *fakeDeleteStarter) Start(input EnvDeleteInput) error {
	f.started = append(f.started, input)
	return f.err
}

func testReconciler(environments *fakeReconcilerEnvironments, tenants *fakeReconcilerTenants, contexts *fakeReconcilerContexts, deleter *fakeDeleteStarter) *EnvDeleteReconciler {
	return &EnvDeleteReconciler{environments: environments, tenants: tenants, contexts: contexts, deleter: deleter}
}

// TestReconcileRestartsAMidTeardownEnvironment pins the core contract of
// #1140: an environment stuck deleting or deletion-blocked converges on its
// own, without an operator noticing and re-issuing the delete.
func TestReconcileRestartsAMidTeardownEnvironment(t *testing.T) {
	environments := &fakeReconcilerEnvironments{environments: []model.Environment{
		{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "prod", Status: model.EnvironmentStatusDeletionBlocked, DeployedVersion: "1.2.3"},
	}}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}}
	contexts := &fakeReconcilerContexts{}
	deleter := &fakeDeleteStarter{}
	r := testReconciler(environments, tenants, contexts, deleter)

	restarted, err := r.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restarted = %d, want 1", restarted)
	}
	if len(deleter.started) != 1 {
		t.Fatalf("delete attempts started = %d, want 1", len(deleter.started))
	}
	got := deleter.started[0]
	if got.EnvironmentID != "env-1" || got.Tenant != "acme" || got.TenantID != "tenant-1" || got.TenantType != string(model.TenantTypeCompany) || got.RunningVersion != "1.2.3" {
		t.Fatalf("delete input = %+v", got)
	}
	if got.DeleteID == "" {
		t.Fatal("a reconciler-driven attempt must get its own fresh delete id, or it would replay the previous attempt's cached workflow result")
	}
}

// TestReconcileLeavesAFreshInFlightDeleteAlone: a `deleting` row whose own
// attempt has not had time to finish yet must not be raced by the reconciler.
func TestReconcileLeavesAFreshInFlightDeleteAlone(t *testing.T) {
	environments := &fakeReconcilerEnvironments{
		environments: []model.Environment{{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "prod", Status: model.EnvironmentStatusDeleting}},
		claimResults: map[string]bool{"env-1": false},
	}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme"}}}
	deleter := &fakeDeleteStarter{}
	r := testReconciler(environments, tenants, &fakeReconcilerContexts{}, deleter)

	restarted, err := r.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 0 {
		t.Fatalf("restarted = %d, want 0", restarted)
	}
	if len(deleter.started) != 0 {
		t.Fatal("a fresh in-flight delete must not be restarted")
	}
	if len(environments.claims) != 1 || environments.claims[0].staleAfter != DeleteClaimStaleAfter {
		t.Fatalf("claims = %+v, want one claim at DeleteClaimStaleAfter", environments.claims)
	}
}

// TestReconcileResolvesPlacementForARemoteContext: an environment placed on a
// registered context must have its delete retried against that same cluster,
// not the platform's own (#1112 interaction).
func TestReconcileResolvesPlacementForARemoteContext(t *testing.T) {
	environments := &fakeReconcilerEnvironments{environments: []model.Environment{
		{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "prod", ContextID: "ctx-1", Status: model.EnvironmentStatusDeletionBlocked, RuntimeVersion: "1.0.0"},
	}}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme"}}}
	contexts := &fakeReconcilerContexts{contexts: map[string]model.Context{
		"ctx-1": {ContextID: "ctx-1", Name: "prod-cluster", KubernetesContext: "prod-cluster", PublicIP: "203.0.113.10"},
	}}
	deleter := &fakeDeleteStarter{}
	r := testReconciler(environments, tenants, contexts, deleter)

	if _, err := r.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := deleter.started[0]
	if got.ContextID != "ctx-1" || got.PlacementKubernetesContext != "prod-cluster" || got.PlacementServerURL != "https://203.0.113.10:6443" {
		t.Fatalf("placement = %+v", got)
	}
}

// TestReconcileSkipsAnEnvironmentWithNoTenantMatch: a lookup failure for one
// environment must not stop the reconciler from restarting every other one.
func TestReconcileContinuesPastOneFailure(t *testing.T) {
	environments := &fakeReconcilerEnvironments{environments: []model.Environment{
		{EnvironmentID: "env-1", TenantID: "missing-tenant", Name: "prod", Status: model.EnvironmentStatusDeletionBlocked},
		{EnvironmentID: "env-2", TenantID: "tenant-1", Name: "staging", Status: model.EnvironmentStatusDeletionBlocked},
	}}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme"}}}
	deleter := &fakeDeleteStarter{}
	r := testReconciler(environments, tenants, &fakeReconcilerContexts{}, deleter)

	restarted, err := r.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restarted = %d, want 1 (only env-2 resolves)", restarted)
	}
	if len(deleter.started) != 1 || deleter.started[0].EnvironmentID != "env-2" {
		t.Fatalf("started = %+v, want only env-2", deleter.started)
	}
}

func TestReconcileNoopWhenNothingIsMidTeardown(t *testing.T) {
	environments := &fakeReconcilerEnvironments{}
	deleter := &fakeDeleteStarter{}
	r := testReconciler(environments, &fakeReconcilerTenants{}, &fakeReconcilerContexts{}, deleter)

	restarted, err := r.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 0 {
		t.Fatalf("restarted = %d, want 0", restarted)
	}
	if len(deleter.started) != 0 {
		t.Fatal("nothing to reconcile must start no delete attempts")
	}
}

func TestReconcilePropagatesAListError(t *testing.T) {
	environments := &fakeReconcilerEnvironments{listErr: errors.New("db unavailable")}
	r := testReconciler(environments, &fakeReconcilerTenants{}, &fakeReconcilerContexts{}, &fakeDeleteStarter{})

	if _, err := r.reconcile(context.Background()); err == nil {
		t.Fatal("expected the list error to propagate")
	}
}

// TestReconcileRecordsAReasonWhenAClaimedAttemptCannotStart is the regression
// test for #1166's worst path. The claim has already moved the row to
// `deleting`, so a failure between the claim and the workflow starting used to
// leave the row claiming an in-flight delete that did not exist -- strictly
// less information than before the tick ran, which is exactly the misreporting
// #1140 was about.
func TestReconcileRecordsAReasonWhenAClaimedAttemptCannotStart(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tenants     *fakeReconcilerTenants
		deleter     *fakeDeleteStarter
		wantInError string
	}{
		{
			name:        "tenant cannot be resolved",
			tenants:     &fakeReconcilerTenants{}, // no tenants, so the lookup misses
			deleter:     &fakeDeleteStarter{},
			wantInError: "tenant",
		},
		{
			name:        "the workflow will not enqueue",
			tenants:     &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}},
			deleter:     &fakeDeleteStarter{err: errors.New("dbos unavailable")},
			wantInError: "start delete workflow",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			environments := &fakeReconcilerEnvironments{environments: []model.Environment{
				{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "prod", Status: model.EnvironmentStatusDeletionBlocked},
			}}
			restarted, err := testReconciler(environments, tc.tenants, &fakeReconcilerContexts{}, tc.deleter).reconcile(context.Background())
			if err != nil {
				t.Fatalf("reconcile returned %v; one environment failing must not fail the scan", err)
			}
			if restarted != 0 {
				t.Fatalf("restarted = %d, want 0", restarted)
			}
			reason, ok := environments.blocked["env-1"]
			if !ok {
				t.Fatal("the claimed row was left in `deleting` with no reason recorded")
			}
			if !strings.Contains(reason, tc.wantInError) {
				t.Fatalf("recorded reason %q does not name the cause (%q)", reason, tc.wantInError)
			}
		})
	}
}

// TestReconcileStopsReAttemptingPastTheAttemptCap: a teardown that has failed
// MaxDeleteAttempts times is not going to succeed on its own, and re-attempting
// it forever buries that under identical ticks instead of surfacing it.
func TestReconcileStopsReAttemptingPastTheAttemptCap(t *testing.T) {
	environments := &fakeReconcilerEnvironments{environments: []model.Environment{
		{
			EnvironmentID: "env-capped", TenantID: "tenant-1", Status: model.EnvironmentStatusDeletionBlocked,
			DeleteAttempts: MaxDeleteAttempts, DeleteError: "namespace did not finish terminating",
		},
		{EnvironmentID: "env-fresh", TenantID: "tenant-1", Status: model.EnvironmentStatusDeletionBlocked},
	}}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}}
	deleter := &fakeDeleteStarter{}

	restarted, err := testReconciler(environments, tenants, &fakeReconcilerContexts{}, deleter).reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restarted = %d, want 1 (only the uncapped environment)", restarted)
	}
	for _, claim := range environments.claims {
		if claim.environmentID == "env-capped" {
			t.Fatal("an environment past the attempt cap must not be claimed again")
		}
	}
	if len(deleter.started) != 1 || deleter.started[0].EnvironmentID != "env-fresh" {
		t.Fatalf("started = %+v, want only env-fresh", deleter.started)
	}
}

// TestReconcileHoldsABlockedRowThroughItsBackoff: without a backoff every
// blocked row was re-attempted on every tick, which for a namespace wedged on
// an unsatisfiable finalizer is pure load with no chance of progress.
func TestReconcileHoldsABlockedRowThroughItsBackoff(t *testing.T) {
	// One attempt already made, updated a moment ago: its 5-minute backoff has
	// not elapsed.
	environments := &fakeReconcilerEnvironments{environments: []model.Environment{
		{
			EnvironmentID: "env-waiting", TenantID: "tenant-1", Status: model.EnvironmentStatusDeletionBlocked,
			DeleteAttempts: 1, UpdatedAt: time.Now().Add(-30 * time.Second),
		},
	}}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}}
	deleter := &fakeDeleteStarter{}

	restarted, err := testReconciler(environments, tenants, &fakeReconcilerContexts{}, deleter).reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 0 || len(environments.claims) != 0 {
		t.Fatalf("a row inside its backoff was re-attempted: restarted=%d claims=%v", restarted, environments.claims)
	}

	// Past the backoff it is attempted again.
	environments.environments[0].UpdatedAt = time.Now().Add(-10 * time.Minute)
	environments.claims = nil
	restarted, err = testReconciler(environments, tenants, &fakeReconcilerContexts{}, deleter).reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restarted = %d, want 1 once the backoff has elapsed", restarted)
	}
}

// TestReconcileDoesNotBackOffAStaleDeletingRow: a stale `deleting` row means the
// attempt behind it can no longer be live, so holding it back would just leave a
// dead attempt sitting. Only `deletion-blocked` rows wait.
func TestReconcileDoesNotBackOffAStaleDeletingRow(t *testing.T) {
	environments := &fakeReconcilerEnvironments{environments: []model.Environment{
		{
			EnvironmentID: "env-stale", TenantID: "tenant-1", Status: model.EnvironmentStatusDeleting,
			DeleteAttempts: 3, UpdatedAt: time.Now(),
		},
	}}
	tenants := &fakeReconcilerTenants{tenants: []model.Tenant{{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}}
	deleter := &fakeDeleteStarter{}

	restarted, err := testReconciler(environments, tenants, &fakeReconcilerContexts{}, deleter).reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restarted = %d, want 1: a stale deleting row is claimed without waiting on a backoff", restarted)
	}
}

// TestDeleteRetryBackoffGrowsAndCaps pins the schedule itself, so a change to
// it is a deliberate edit rather than an accident.
func TestDeleteRetryBackoffGrowsAndCaps(t *testing.T) {
	if got := deleteRetryBackoff(0); got != 0 {
		t.Fatalf("backoff(0) = %v, want 0 — a never-attempted row waits for nothing", got)
	}
	if got := deleteRetryBackoff(1); got != deleteRetryFirstBackoff {
		t.Fatalf("backoff(1) = %v, want %v", got, deleteRetryFirstBackoff)
	}
	if got := deleteRetryBackoff(2); got != 2*deleteRetryFirstBackoff {
		t.Fatalf("backoff(2) = %v, want %v", got, 2*deleteRetryFirstBackoff)
	}
	previous := time.Duration(0)
	for attempts := 1; attempts <= MaxDeleteAttempts+4; attempts++ {
		got := deleteRetryBackoff(attempts)
		if got > deleteRetryMaxBackoff {
			t.Fatalf("backoff(%d) = %v, over the %v cap", attempts, got, deleteRetryMaxBackoff)
		}
		if got < previous {
			t.Fatalf("backoff(%d) = %v, went backwards from %v", attempts, got, previous)
		}
		previous = got
	}
	if deleteRetryBackoff(MaxDeleteAttempts+4) != deleteRetryMaxBackoff {
		t.Fatal("backoff must reach and stay at its cap")
	}
}
