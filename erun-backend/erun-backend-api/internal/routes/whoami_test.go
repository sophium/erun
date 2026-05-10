package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubWhoamiUserRepository struct {
	user  model.User
	roles []string
}

func (r stubWhoamiUserRepository) Get(context.Context, string) (model.User, error) {
	return r.user, nil
}

func (r stubWhoamiUserRepository) RoleNames(context.Context, string) ([]string, error) {
	return r.roles, nil
}

func TestWhoamiReturnsUsernameAndRoles(t *testing.T) {
	repo := stubWhoamiUserRepository{
		user: model.User{Username: "Rihards.Freimanis"},
		roles: []string{
			"ReadAll",
			"WriteAll",
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:       "tenant-1",
		ErunUserID:     "user-1",
		ExternalIssuer: "https://issuer.example",
		ExternalUserID: "external-user-1",
	}))
	rec := httptest.NewRecorder()

	WhoamiRoutes{users: repo}.handleWhoami(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var response whoamiResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Username != "Rihards.Freimanis" {
		t.Fatalf("unexpected username: %q", response.Username)
	}
	if len(response.Roles) != 2 || response.Roles[0] != "ReadAll" || response.Roles[1] != "WriteAll" {
		t.Fatalf("unexpected roles: %+v", response.Roles)
	}
}
