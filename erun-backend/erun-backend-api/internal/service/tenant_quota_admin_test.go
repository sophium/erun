package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// fakeTenantQuotaSetter is the narrow TenantQuotaSetter dependency, recording
// what it was asked to set and which tenant it was told to set it for, so a
// test can prove SetForTenant persists against the named target rather than
// whatever tenant the caller's own context carries.
type fakeTenantQuotaSetter struct {
	set       model.TenantQuota
	err       error
	calls     int
	sawTenant string
	sawQuota  model.TenantQuota
}

func (f *fakeTenantQuotaSetter) Set(_ context.Context, tenantID string, quota model.TenantQuota) (model.TenantQuota, error) {
	f.calls++
	f.sawTenant = tenantID
	f.sawQuota = quota
	if f.err != nil {
		return model.TenantQuota{}, f.err
	}
	set := f.set
	set.TenantID = tenantID
	return set, nil
}

// fakeTenantQuotaAdminAudit is the narrow TenantQuotaAdminAuditLogger
// dependency: tests that want "no audit logger configured" pass a literal nil
// for the interface parameter, not a typed *fakeTenantQuotaAdminAudit nil —
// mirroring fakeEnvironmentAdminAudit's own doc comment.
type fakeTenantQuotaAdminAudit struct {
	events []model.AuditEvent
	err    error
}

func (f *fakeTenantQuotaAdminAudit) LogAuditEvent(_ context.Context, event model.AuditEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

func operatorContext(homeTenantID string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID:       homeTenantID,
		TenantType:     string(model.TenantTypeOperations),
		ErunUserID:     "operator-1",
		ExternalIssuer: "https://issuer.example",
		ExternalUserID: "sub-1",
	})
}

func TestSetForTenantPersistsAgainstTheNamedTenant(t *testing.T) {
	quotas := &fakeTenantQuotaSetter{}
	audit := &fakeTenantQuotaAdminAudit{}
	svc := NewTenantQuotaAdminService(quotas, audit)

	quota := model.TenantQuota{
		MaxEnvironments: 50, MaxCPUMillicores: 4000, MaxMemoryMB: 9216, MaxStorageGB: 80,
		MaxTotalCPUMillicores: 40000, MaxTotalMemoryMB: 92160, MaxTotalStorageGB: 800,
	}
	if _, err := svc.SetForTenant(operatorContext("ops-tenant"), "target-tenant", quota); err != nil {
		t.Fatalf("SetForTenant: %v", err)
	}
	if quotas.calls != 1 {
		t.Fatalf("expected exactly one Set call, got %d", quotas.calls)
	}
	if quotas.sawTenant != "target-tenant" {
		t.Fatalf("Set ran against tenant %q, want target-tenant", quotas.sawTenant)
	}
	if quotas.sawQuota != quota {
		t.Fatalf("Set saw quota %+v, want %+v", quotas.sawQuota, quota)
	}
}

func TestSetForTenantRecordsWhichOperatorSetWhichTenantsQuotaFromWhichHomeTenant(t *testing.T) {
	quotas := &fakeTenantQuotaSetter{}
	audit := &fakeTenantQuotaAdminAudit{}
	svc := NewTenantQuotaAdminService(quotas, audit)

	quota := model.TenantQuota{
		MaxEnvironments: 50, MaxCPUMillicores: 4000, MaxMemoryMB: 9216, MaxStorageGB: 80,
		MaxTotalCPUMillicores: 40000, MaxTotalMemoryMB: 92160, MaxTotalStorageGB: 800,
	}
	if _, err := svc.SetForTenant(operatorContext("ops-tenant"), "target-tenant", quota); err != nil {
		t.Fatalf("SetForTenant: %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("expected exactly one audit event, got %d", len(audit.events))
	}
	event := audit.events[0]
	// The audit row's own tenant_id is the operator's home tenant, not the
	// tenant whose quota was set — that fact lives in APIParameters instead,
	// since the write itself only ever records the target.
	if event.TenantID != "ops-tenant" {
		t.Fatalf("audit TenantID = %q, want ops-tenant (the operator's home tenant)", event.TenantID)
	}
	if event.ErunUserID != "operator-1" {
		t.Fatalf("audit ErunUserID = %q, want operator-1", event.ErunUserID)
	}
	var parameters auditSetTenantQuotaParameters
	if err := json.Unmarshal([]byte(event.APIParameters), &parameters); err != nil {
		t.Fatalf("decode api_parameters: %v", err)
	}
	if parameters.TargetTenantID != "target-tenant" {
		t.Fatalf("api_parameters targetTenantId = %q, want target-tenant", parameters.TargetTenantID)
	}
	if parameters.MaxEnvironments != 50 {
		t.Fatalf("api_parameters maxEnvironments = %d, want 50", parameters.MaxEnvironments)
	}
}

// TestSetForTenantFailsClosedWithNoAuditLogger mirrors
// TestCreateForTenantFailsClosedWithNoAuditLogger: an unattributable
// cross-tenant write must never happen quietly.
func TestSetForTenantFailsClosedWithNoAuditLogger(t *testing.T) {
	quotas := &fakeTenantQuotaSetter{}
	svc := NewTenantQuotaAdminService(quotas, nil)

	if _, err := svc.SetForTenant(operatorContext("ops-tenant"), "target-tenant", model.TenantQuota{}); err == nil {
		t.Fatal("expected an error with no audit logger configured, got nil")
	}
	if quotas.calls != 0 {
		t.Fatalf("Set must not run when the write cannot be audited, got %d calls", quotas.calls)
	}
}

// TestSetForTenantFailsClosedWhenAuditWriteFails proves the audit-before-set
// ordering actually holds: a failing audit write must leave the target
// tenant's quota unchanged.
func TestSetForTenantFailsClosedWhenAuditWriteFails(t *testing.T) {
	quotas := &fakeTenantQuotaSetter{}
	audit := &fakeTenantQuotaAdminAudit{err: context.DeadlineExceeded}
	svc := NewTenantQuotaAdminService(quotas, audit)

	if _, err := svc.SetForTenant(operatorContext("ops-tenant"), "target-tenant", model.TenantQuota{}); err == nil {
		t.Fatal("expected the audit write's error to surface, got nil")
	}
	if quotas.calls != 0 {
		t.Fatalf("Set must not run when the audit write failed, got %d calls", quotas.calls)
	}
}
