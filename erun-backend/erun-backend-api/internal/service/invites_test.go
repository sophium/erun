package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

type stubInviteConsumer struct {
	result   repository.ConsumedInvite
	err      error
	gotToken string
	calls    int
}

func (s *stubInviteConsumer) ConsumeByToken(_ context.Context, token string) (repository.ConsumedInvite, error) {
	s.gotToken = token
	s.calls++
	return s.result, s.err
}

type stubInviteZitadelAdmin struct {
	user      zitadel.User
	err       error
	gotParams zitadel.CreateHumanUserParams
	calls     int
}

func (s *stubInviteZitadelAdmin) CreateHumanUser(_ context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error) {
	s.gotParams = params
	s.calls++
	return s.user, s.err
}

type stubInviteUserCreator struct {
	user               model.User
	err                error
	gotParams          repository.CreateUserParams
	gotSecurityContext security.Context
	calls              int
}

func (s *stubInviteUserCreator) Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error) {
	s.gotParams = params
	s.calls++
	if sc, ok := security.FromContext(ctx); ok {
		s.gotSecurityContext = sc
	}
	return s.user, false, s.err
}

func testConsumedInvite() repository.ConsumedInvite {
	return repository.ConsumedInvite{
		Invite: model.Invite{
			InviteID: "invite-1",
			TenantID: "tenant-1",
			Issuer:   "https://auth.example.com",
		},
		TenantType: "COMPANY",
	}
}

// TestAcceptInviteHappyPath locks the core of #1483's accept flow: a
// consumed invite's tenant and issuer drive the IdP identity and the erun
// user mapping, bound to the invite's own target tenant rather than any
// caller session (there is none).
func TestAcceptInviteHappyPath(t *testing.T) {
	invites := &stubInviteConsumer{result: testConsumedInvite()}
	admin := &stubInviteZitadelAdmin{user: zitadel.User{ID: "idp-1", Username: "newbie"}}
	users := &stubInviteUserCreator{user: model.User{UserID: "erun-1", Username: "newbie"}}
	svc := NewInviteService(invites, admin, users)

	result, err := svc.Accept(context.Background(), AcceptInviteParams{
		Token: "tok-abc", Username: "newbie", Email: "newbie@example.com", Password: "S3cret!Pass",
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if invites.gotToken != "tok-abc" {
		t.Fatalf("gotToken = %q, want tok-abc", invites.gotToken)
	}
	if admin.gotParams.InitialPassword != "S3cret!Pass" {
		t.Fatalf("InitialPassword = %q, want the invitee's own chosen password", admin.gotParams.InitialPassword)
	}
	if users.gotParams.Issuer != "https://auth.example.com" || users.gotParams.Subject != "idp-1" {
		t.Fatalf("CreateUserParams = %+v, want the invite's issuer and the new IdP subject", users.gotParams)
	}
	if users.gotSecurityContext.TenantID != "tenant-1" || users.gotSecurityContext.TenantType != "COMPANY" {
		t.Fatalf("security context = %+v, want bound to the invite's own target tenant", users.gotSecurityContext)
	}
	if result.IdPUser.ID != "idp-1" || result.ErunUser.UserID != "erun-1" {
		t.Fatalf("result = %+v, want both halves landed", result)
	}
}

// TestAcceptInviteReportsHalfLandedFailure mirrors Enroll's own contract:
// an erun-side mapping failure after the IdP identity was created is
// reported with the orphaned IdP user still attached, not swallowed.
func TestAcceptInviteReportsHalfLandedFailure(t *testing.T) {
	invites := &stubInviteConsumer{result: testConsumedInvite()}
	admin := &stubInviteZitadelAdmin{user: zitadel.User{ID: "idp-2", Username: "orphan"}}
	users := &stubInviteUserCreator{err: errors.New("db unavailable")}
	svc := NewInviteService(invites, admin, users)

	result, err := svc.Accept(context.Background(), AcceptInviteParams{Token: "tok", Username: "orphan", Password: "S3cret!Pass"})
	if !errors.Is(err, ErrIdentityMappingFailed) {
		t.Fatalf("err = %v, want ErrIdentityMappingFailed", err)
	}
	if result.IdPUser.ID != "idp-2" {
		t.Fatalf("result.IdPUser = %+v, want the orphaned IdP identity reported", result.IdPUser)
	}
}

// TestAcceptInviteRefusesEmailMismatch locks the pinned-email option (#1483
// item 2): a request that supplies a different email than the invite was
// pinned to is refused before ever reaching the identity provider.
func TestAcceptInviteRefusesEmailMismatch(t *testing.T) {
	pinned := testConsumedInvite()
	pinned.Invite.Email = "expected@example.com"
	invites := &stubInviteConsumer{result: pinned}
	admin := &stubInviteZitadelAdmin{}
	users := &stubInviteUserCreator{}
	svc := NewInviteService(invites, admin, users)

	_, err := svc.Accept(context.Background(), AcceptInviteParams{
		Token: "tok", Username: "someone", Email: "different@example.com", Password: "S3cret!Pass",
	})
	if !errors.Is(err, ErrInviteEmailMismatch) {
		t.Fatalf("err = %v, want ErrInviteEmailMismatch", err)
	}
	if admin.calls != 0 {
		t.Fatalf("CreateHumanUser was called %d times, want 0 for a refused mismatch", admin.calls)
	}
}

// TestAcceptInviteUsesPinnedEmailWhenRequestOmitsIt proves the pinned email
// is not just a validation check — it is used when the accepting request
// leaves email blank.
func TestAcceptInviteUsesPinnedEmailWhenRequestOmitsIt(t *testing.T) {
	pinned := testConsumedInvite()
	pinned.Invite.Email = "expected@example.com"
	invites := &stubInviteConsumer{result: pinned}
	admin := &stubInviteZitadelAdmin{user: zitadel.User{ID: "idp-3"}}
	users := &stubInviteUserCreator{user: model.User{UserID: "erun-3"}}
	svc := NewInviteService(invites, admin, users)

	if _, err := svc.Accept(context.Background(), AcceptInviteParams{Token: "tok", Username: "someone", Password: "S3cret!Pass"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if admin.gotParams.Email != "expected@example.com" {
		t.Fatalf("Email = %q, want the invite's pinned email", admin.gotParams.Email)
	}
}

// TestAcceptInvitePropagatesConsumeFailureWithoutCallingZitadel locks that a
// bad token (expired, consumed, unknown) never reaches the identity
// provider at all.
func TestAcceptInvitePropagatesConsumeFailureWithoutCallingZitadel(t *testing.T) {
	invites := &stubInviteConsumer{err: repository.ErrInviteExpired}
	admin := &stubInviteZitadelAdmin{}
	users := &stubInviteUserCreator{}
	svc := NewInviteService(invites, admin, users)

	_, err := svc.Accept(context.Background(), AcceptInviteParams{Token: "tok", Username: "someone", Password: "S3cret!Pass"})
	if !errors.Is(err, repository.ErrInviteExpired) {
		t.Fatalf("err = %v, want ErrInviteExpired", err)
	}
	if admin.calls != 0 {
		t.Fatalf("CreateHumanUser was called %d times, want 0 for an expired token", admin.calls)
	}
}
