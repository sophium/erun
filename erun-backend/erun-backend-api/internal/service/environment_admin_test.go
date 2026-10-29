package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

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

// Tests wanting no audit logger pass a literal nil; a typed nil pointer is not nil through the interface.
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
	// The audit row is filed under the operator's home tenant.
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
