package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// fakeEnvironmentCreator is the narrow EnvironmentCreator dependency,
// recording what it was asked to create and which tenant the context it
// received was bound to, so a test can prove CreateForTenant persists
// against the scoped context rather than the caller's own.
type fakeEnvironmentCreator struct {
	created  model.Environment
	err      error
	calls    int
	sawInput model.Environment
	sawTenID string
}

func (f *fakeEnvironmentCreator) Create(ctx context.Context, environment model.Environment) (model.Environment, error) {
	f.calls++
	f.sawInput = environment
	if securityContext, ok := security.FromContext(ctx); ok {
		f.sawTenID = securityContext.TenantID
	}
	if f.err != nil {
		return model.Environment{}, f.err
	}
	created := f.created
	if created.EnvironmentID == "" {
		created = environment
		created.EnvironmentID = "env-1"
	}
	return created, nil
}

// fakeEnvironmentAdminAudit is the narrow EnvironmentAdminAuditLogger
// dependency: tests that want "no audit logger configured" pass a literal
// nil for the interface parameter, not a typed *fakeEnvironmentAdminAudit
// nil (which would not compare equal to nil through the interface) —
// mirroring fakeReviewAudit's own doc comment in reviews_test.go.
type fakeEnvironmentAdminAudit struct {
	events []model.AuditEvent
	err    error
}

func (f *fakeEnvironmentAdminAudit) LogAuditEvent(_ context.Context, event model.AuditEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

func homeAndScopedContexts(homeTenantID, targetTenantID string) (homeCtx, scopedCtx context.Context) {
	homeCtx = security.WithContext(context.Background(), security.Context{
		TenantID:       homeTenantID,
		TenantType:     string(model.TenantTypeOperations),
		ErunUserID:     "operator-1",
		ExternalIssuer: "https://issuer.example",
		ExternalUserID: "sub-1",
	})
	scopedCtx = security.WithContext(homeCtx, security.Context{
		TenantID:       targetTenantID,
		TenantType:     string(model.TenantTypeOperations),
		ErunUserID:     "operator-1",
		ExternalIssuer: "https://issuer.example",
		ExternalUserID: "sub-1",
	})
	return homeCtx, scopedCtx
}

func TestCreateForTenantPersistsAgainstTheScopedTenant(t *testing.T) {
	environments := &fakeEnvironmentCreator{}
	audit := &fakeEnvironmentAdminAudit{}
	svc := NewEnvironmentAdminService(environments, audit)
	homeCtx, scopedCtx := homeAndScopedContexts("ops-tenant", "target-tenant")

	created, err := svc.CreateForTenant(scopedCtx, homeCtx, "target-tenant", model.Environment{Name: "prod", Type: model.EnvironmentTypeRemoteAgent})
	if err != nil {
		t.Fatalf("CreateForTenant: %v", err)
	}
	if created.EnvironmentID == "" {
		t.Fatalf("expected a persisted environment, got %+v", created)
	}
	if environments.calls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", environments.calls)
	}
	if environments.sawTenID != "target-tenant" {
		t.Fatalf("Create ran against tenant %q, want target-tenant", environments.sawTenID)
	}
}

func TestCreateForTenantRecordsWhichOperatorActedOnWhichTenantFromWhichHomeTenant(t *testing.T) {
	environments := &fakeEnvironmentCreator{}
	audit := &fakeEnvironmentAdminAudit{}
	svc := NewEnvironmentAdminService(environments, audit)
	homeCtx, scopedCtx := homeAndScopedContexts("ops-tenant", "target-tenant")

	if _, err := svc.CreateForTenant(scopedCtx, homeCtx, "target-tenant", model.Environment{Name: "prod", Type: model.EnvironmentTypeRemoteAgent}); err != nil {
		t.Fatalf("CreateForTenant: %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("expected exactly one audit event, got %d", len(audit.events))
	}
	event := audit.events[0]
	// The audit row's own tenant_id is the operator's home tenant, not the
	// tenant the write landed in — that fact lives in APIParameters instead,
	// since the write itself only ever records the target.
	if event.TenantID != "ops-tenant" {
		t.Fatalf("audit TenantID = %q, want ops-tenant (the operator's home tenant)", event.TenantID)
	}
	if event.ErunUserID != "operator-1" {
		t.Fatalf("audit ErunUserID = %q, want operator-1", event.ErunUserID)
	}
	var parameters auditCrossTenantEnvironmentCreateParameters
	if err := json.Unmarshal([]byte(event.APIParameters), &parameters); err != nil {
		t.Fatalf("decode api_parameters: %v", err)
	}
	if parameters.TargetTenantID != "target-tenant" {
		t.Fatalf("api_parameters targetTenantId = %q, want target-tenant", parameters.TargetTenantID)
	}
	if parameters.Name != "prod" {
		t.Fatalf("api_parameters name = %q, want prod", parameters.Name)
	}
}

// TestCreateForTenantFailsClosedWithNoAuditLogger mirrors
// OverrideAdvanceMergeQueue's own "a missing audit logger fails closed rather
// than silently promoting anyway" contract in reviews.go: an unattributable
// cross-tenant write must never happen quietly.
func TestCreateForTenantFailsClosedWithNoAuditLogger(t *testing.T) {
	environments := &fakeEnvironmentCreator{}
	svc := NewEnvironmentAdminService(environments, nil)
	homeCtx, scopedCtx := homeAndScopedContexts("ops-tenant", "target-tenant")

	if _, err := svc.CreateForTenant(scopedCtx, homeCtx, "target-tenant", model.Environment{Name: "prod", Type: model.EnvironmentTypeRemoteAgent}); err == nil {
		t.Fatal("expected an error with no audit logger configured, got nil")
	}
	if environments.calls != 0 {
		t.Fatalf("Create must not run when the write cannot be audited, got %d calls", environments.calls)
	}
}

// TestCreateForTenantFailsClosedWhenAuditWriteFails proves the audit-before-
// create ordering actually holds: a failing audit write must leave no
// environment row behind in the target tenant.
func TestCreateForTenantFailsClosedWhenAuditWriteFails(t *testing.T) {
	environments := &fakeEnvironmentCreator{}
	audit := &fakeEnvironmentAdminAudit{err: context.DeadlineExceeded}
	svc := NewEnvironmentAdminService(environments, audit)
	homeCtx, scopedCtx := homeAndScopedContexts("ops-tenant", "target-tenant")

	if _, err := svc.CreateForTenant(scopedCtx, homeCtx, "target-tenant", model.Environment{Name: "prod", Type: model.EnvironmentTypeRemoteAgent}); err == nil {
		t.Fatal("expected the audit write's error to surface, got nil")
	}
	if environments.calls != 0 {
		t.Fatalf("Create must not run when the audit write failed, got %d calls", environments.calls)
	}
}
