package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

type stubIdentityAdmin struct {
	created       zitadel.User
	err           error
	gotParams     zitadel.CreateHumanUserParams
	smtpStatus    zitadel.SMTPStatus
	smtpStatusErr error
}

func (s *stubIdentityAdmin) CreateHumanUser(_ context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error) {
	s.gotParams = params
	if s.err != nil {
		return zitadel.User{}, s.err
	}
	return s.created, nil
}

func (s *stubIdentityAdmin) GetSMTPStatus(context.Context) (zitadel.SMTPStatus, error) {
	return s.smtpStatus, s.smtpStatusErr
}

type stubIdentityUserCreator struct {
	created     model.User
	err         error
	gotParams   repository.CreateUserParams
	createCalls int
}

func (s *stubIdentityUserCreator) Create(_ context.Context, params repository.CreateUserParams) (model.User, bool, error) {
	s.createCalls++
	s.gotParams = params
	if s.err != nil {
		return model.User{}, false, s.err
	}
	return s.created, false, nil
}

func TestIdentityServiceEnrollHappyPath(t *testing.T) {
	admin := &stubIdentityAdmin{
		created:    zitadel.User{ID: "idp-1", Username: "alice", Email: "alice@example.com"},
		smtpStatus: zitadel.SMTPStatus{Configured: true},
	}
	users := &stubIdentityUserCreator{created: model.User{UserID: "erun-1", Username: "alice"}}
	svc := NewIdentityService(admin, users)

	result, err := svc.Enroll(context.Background(), EnrollIdentityParams{
		Username: "alice", Email: "alice@example.com", Issuer: "https://auth.example.com",
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if result.IdPUser.ID != "idp-1" || result.ErunUser.UserID != "erun-1" {
		t.Fatalf("result = %+v", result)
	}
	if !result.MailDeliveryConfigured || result.TemporaryPassword != "" {
		t.Fatalf("result = %+v, want mail delivery reported configured and no temporary password", result)
	}
	if admin.gotParams.InitialPassword != "" {
		t.Fatalf("gotParams.InitialPassword = %q, want empty when mail delivery is configured (the normal invite-email flow)", admin.gotParams.InitialPassword)
	}
	if users.gotParams.Subject != "idp-1" || users.gotParams.Issuer != "https://auth.example.com" {
		t.Fatalf("erun user create params = %+v, want the IdP's own id as subject and the caller's issuer", users.gotParams)
	}
}

func TestIdentityServiceEnrollFallsBackToATemporaryPasswordWithNoMail(t *testing.T) {
	admin := &stubIdentityAdmin{
		created:    zitadel.User{ID: "idp-3", Username: "carol", Email: "carol@example.com"},
		smtpStatus: zitadel.SMTPStatus{Configured: false},
	}
	users := &stubIdentityUserCreator{created: model.User{UserID: "erun-3", Username: "carol"}}
	svc := NewIdentityService(admin, users)

	result, err := svc.Enroll(context.Background(), EnrollIdentityParams{Username: "carol", Email: "carol@example.com"})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if result.MailDeliveryConfigured {
		t.Fatalf("result.MailDeliveryConfigured = true, want false when SMTP is unconfigured")
	}
	if result.TemporaryPassword == "" {
		t.Fatal("result.TemporaryPassword is empty, want a generated password to hand to the enrollee")
	}
	if admin.gotParams.InitialPassword != result.TemporaryPassword {
		t.Fatalf("gotParams.InitialPassword = %q, want it to match the password reported to the caller (%q)", admin.gotParams.InitialPassword, result.TemporaryPassword)
	}
}

func TestIdentityServiceEnrollTreatsAnUncheckableSMTPStatusAsUnconfigured(t *testing.T) {
	admin := &stubIdentityAdmin{
		created:       zitadel.User{ID: "idp-4", Username: "dave", Email: "dave@example.com"},
		smtpStatusErr: errors.New("zitadel admin api unreachable"),
	}
	users := &stubIdentityUserCreator{created: model.User{UserID: "erun-4", Username: "dave"}}
	svc := NewIdentityService(admin, users)

	result, err := svc.Enroll(context.Background(), EnrollIdentityParams{Username: "dave", Email: "dave@example.com"})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if result.TemporaryPassword == "" {
		t.Fatal("result.TemporaryPassword is empty, want the safe fallback when SMTP status could not be checked")
	}
}

func TestIdentityServiceEnrollFailsClosedWhenIdPCreateFails(t *testing.T) {
	admin := &stubIdentityAdmin{err: errors.New("zitadel unavailable")}
	users := &stubIdentityUserCreator{}
	svc := NewIdentityService(admin, users)

	if _, err := svc.Enroll(context.Background(), EnrollIdentityParams{Username: "alice", Email: "alice@example.com"}); err == nil {
		t.Fatal("want an error when the IdP create fails")
	}
	if users.createCalls != 0 {
		t.Fatalf("erun user create must not run when the IdP half never landed, got %+v", users.gotParams)
	}
}

func TestIdentityServiceEnrollReportsOrphanedIdPUserOnMappingFailure(t *testing.T) {
	admin := &stubIdentityAdmin{created: zitadel.User{ID: "idp-2", Username: "bob"}}
	users := &stubIdentityUserCreator{err: repository.ErrConflict}
	svc := NewIdentityService(admin, users)

	result, err := svc.Enroll(context.Background(), EnrollIdentityParams{Username: "bob", Email: "bob@example.com"})
	if !errors.Is(err, ErrIdentityMappingFailed) {
		t.Fatalf("err = %v, want ErrIdentityMappingFailed", err)
	}
	if result.IdPUser.ID != "idp-2" {
		t.Fatalf("result.IdPUser = %+v, want the created IdP user reported even though the mapping failed", result.IdPUser)
	}
	if result.ErunUser.UserID != "" {
		t.Fatalf("result.ErunUser = %+v, want the zero value on a mapping failure", result.ErunUser)
	}
}

func TestIdentityServiceEnrollTargetsAnotherOrg(t *testing.T) {
	admin := &stubIdentityAdmin{
		created:    zitadel.User{ID: "idp-9", Username: "bob", Email: "bob@example.com"},
		smtpStatus: zitadel.SMTPStatus{Configured: true},
	}
	users := &stubIdentityUserCreator{}
	svc := NewIdentityService(admin, users)

	result, err := svc.Enroll(context.Background(), EnrollIdentityParams{
		Username: "bob", Email: "bob@example.com", Issuer: "https://auth.example.com",
		OrgID: "388520359030161586",
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if admin.gotParams.OrgID != "388520359030161586" {
		t.Fatalf("OrgID reached the IdP as %q", admin.gotParams.OrgID)
	}
	// Deliberately not written: RLS would file the row under the caller's tenant.
	if users.createCalls != 0 {
		t.Fatalf("createCalls = %d, want no erun user written for a cross-org enrollment", users.createCalls)
	}
	if !result.MappingDeferred {
		t.Fatal("MappingDeferred must say the zero ErunUser is by design, not a failure")
	}
	if result.IdPUser.ID != "idp-9" {
		t.Fatalf("IdPUser = %+v", result.IdPUser)
	}
}

func TestIdentityServiceEnrollWithoutOrgStillWritesTheMapping(t *testing.T) {
	admin := &stubIdentityAdmin{
		created:    zitadel.User{ID: "idp-10", Username: "carol"},
		smtpStatus: zitadel.SMTPStatus{Configured: true},
	}
	users := &stubIdentityUserCreator{created: model.User{UserID: "erun-10", Username: "carol"}}
	svc := NewIdentityService(admin, users)

	result, err := svc.Enroll(context.Background(), EnrollIdentityParams{
		Username: "carol", Email: "carol@example.com", Issuer: "https://auth.example.com",
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if admin.gotParams.OrgID != "" {
		t.Fatalf("OrgID = %q, want the credential's own org", admin.gotParams.OrgID)
	}
	if users.createCalls != 1 || result.MappingDeferred {
		t.Fatalf("createCalls = %d deferred = %v, want the mapping written", users.createCalls, result.MappingDeferred)
	}
}
