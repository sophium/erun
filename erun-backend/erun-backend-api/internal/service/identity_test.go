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
	created   zitadel.User
	err       error
	gotParams zitadel.CreateHumanUserParams
}

func (s *stubIdentityAdmin) CreateHumanUser(_ context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error) {
	s.gotParams = params
	if s.err != nil {
		return zitadel.User{}, s.err
	}
	return s.created, nil
}

type stubIdentityUserCreator struct {
	created   model.User
	err       error
	gotParams repository.CreateUserParams
}

func (s *stubIdentityUserCreator) Create(_ context.Context, params repository.CreateUserParams) (model.User, error) {
	s.gotParams = params
	if s.err != nil {
		return model.User{}, s.err
	}
	return s.created, nil
}

func TestIdentityServiceEnrollHappyPath(t *testing.T) {
	admin := &stubIdentityAdmin{created: zitadel.User{ID: "idp-1", Username: "alice", Email: "alice@example.com"}}
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
	if users.gotParams.Subject != "idp-1" || users.gotParams.Issuer != "https://auth.example.com" {
		t.Fatalf("erun user create params = %+v, want the IdP's own id as subject and the caller's issuer", users.gotParams)
	}
}

func TestIdentityServiceEnrollFailsClosedWhenIdPCreateFails(t *testing.T) {
	admin := &stubIdentityAdmin{err: errors.New("zitadel unavailable")}
	users := &stubIdentityUserCreator{}
	svc := NewIdentityService(admin, users)

	if _, err := svc.Enroll(context.Background(), EnrollIdentityParams{Username: "alice", Email: "alice@example.com"}); err == nil {
		t.Fatal("want an error when the IdP create fails")
	}
	if users.gotParams != (repository.CreateUserParams{}) {
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
