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

func (s *stubIdentityUserCreator) Create(_ context.Context, params repository.CreateUserParams) (model.User, error) {
	s.createCalls++
	s.gotParams = params
	if s.err != nil {
		return model.User{}, s.err
	}
	return s.created, nil
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

// TestIdentityServiceEnrollFallsBackToATemporaryPasswordWithNoMail locks the
// no-SMTP fallback (issue #1168): the platform's IdP cannot send the usual
// invite email, so Enroll must not create the usual passwordless account --
// stuck in USER_STATE_INITIAL forever, waiting on a link nothing will ever
// send -- and must instead report a temporary password the caller can hand
// to the enrollee directly.
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

// TestIdentityServiceEnrollTreatsAnUncheckableSMTPStatusAsUnconfigured locks
// that a failed status check defaults to the safe fallback rather than
// assuming mail works: claiming success on an invite email that was never
// actually confirmed deliverable is the exact dead end this issue is about.
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
