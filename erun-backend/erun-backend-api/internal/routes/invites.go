package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// inviteTTL is the fixed lifetime every invite is minted with (#1483 item 7:
// invites expire by default). Not yet operator-configurable — a fixed,
// honest default is simpler than a per-invite input nobody has asked for.
const inviteTTL = 7 * 24 * time.Hour

// InviteStore is the persistence dependency for POST/GET/DELETE /v1/invites.
// *repository.InviteRepository satisfies it.
type InviteStore interface {
	Create(ctx context.Context, params repository.CreateInviteParams) (model.Invite, error)
	List(ctx context.Context, filter repository.InviteFilter) ([]model.Invite, error)
	Revoke(ctx context.Context, inviteID string) error
}

type InviteRoutes struct {
	invites InviteStore
}

// RegisterInviteRoutes registers the authenticated half of #1483's
// invite-only registration model: an enrolled user creates, lists, and
// revokes invites for a tenant they can access. Unlike identity
// administration (which is restricted to an OPERATIONS tenant, since it
// manages the platform's shared IdP org settings), invite creation is open
// to every tenant — a COMPANY tenant needs its own way to add members, and
// resolveTargetTenant already gates the one case that needs to stay
// OPERATIONS-only: minting an invite for a tenant other than the caller's
// own, which is how an invite to an OPERATIONS tenant (a cross-tenant grant
// by URL) gets its own explicit gate today, ahead of #1481's finer-grained
// permission.
func RegisterInviteRoutes(register ProtectedRouteRegistrar, invites InviteStore) {
	routes := InviteRoutes{invites: invites}
	register(http.MethodPost, "/v1/invites", http.HandlerFunc(routes.createInvite))
	register(http.MethodGet, "/v1/invites", http.HandlerFunc(routes.listInvites))
	register(http.MethodDelete, "/v1/invites/{invite_id}", http.HandlerFunc(routes.revokeInvite))
}

type createInviteRequest struct {
	Email    string `json:"email,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
}

func (r InviteRoutes) createInvite(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	var body createInviteRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	targetTenantID, err := resolveTargetTenant(securityContext, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// TenantID is only passed through when it differs from the caller's own
	// session tenant, the same convention CreateUserParams uses.
	overrideTenantID := ""
	if targetTenantID != securityContext.TenantID {
		overrideTenantID = targetTenantID
	}

	invite, err := r.invites.Create(req.Context(), repository.CreateInviteParams{
		TenantID: overrideTenantID,
		Issuer:   securityContext.ExternalIssuer,
		Email:    strings.TrimSpace(body.Email),
		TTL:      inviteTTL,
	})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, invite)
}

func (r InviteRoutes) listInvites(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	targetTenantID, err := resolveTargetTenant(securityContext, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	invites, err := r.invites.List(req.Context(), repository.InviteFilter{TenantID: targetTenantID})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, invites)
}

func (r InviteRoutes) revokeInvite(w http.ResponseWriter, req *http.Request) {
	if err := r.invites.Revoke(req.Context(), req.PathValue("invite_id")); err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InviteAccepter accepts an invite token and enrolls the invitee.
// *service.InviteService satisfies it.
type InviteAccepter interface {
	Accept(ctx context.Context, params service.AcceptInviteParams) (service.AcceptInviteResult, error)
}

type acceptInviteRequest struct {
	Token     string `json:"token"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Password  string `json:"password"`
}

// acceptInviteResponse mirrors enrollIdentityUserResponse's half-landed
// shape: ErunUser absent with Error set means the IdP identity was created
// but the erun-side mapping failed, reported rather than swallowed.
type acceptInviteResponse struct {
	IdPUser  zitadel.User `json:"idpUser"`
	ErunUser *model.User  `json:"erunUser,omitempty"`
	Error    string       `json:"error,omitempty"`
}

// RegisterInviteAcceptRoute registers POST /v1/invites/accept directly on
// the mux, unauthenticated: an invitee accepting an invite has no bearer
// token yet — the token in the request body is the credential, the same
// shape the DNS-01 broker and registry token routes already use for a
// caller that authenticates some other way than the user-OIDC bearer token.
func RegisterInviteAcceptRoute(mux *http.ServeMux, accepter InviteAccepter) {
	mux.HandleFunc("POST /v1/invites/accept", func(w http.ResponseWriter, req *http.Request) {
		var body acceptInviteRequest
		if err := decodeJSON(req, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		token := strings.TrimSpace(body.Token)
		username := strings.TrimSpace(body.Username)
		password := strings.TrimSpace(body.Password)
		if token == "" || username == "" || password == "" {
			writeError(w, http.StatusBadRequest, "token, username, and password are required")
			return
		}
		result, err := accepter.Accept(req.Context(), service.AcceptInviteParams{
			Token:     token,
			Username:  username,
			Email:     strings.TrimSpace(body.Email),
			FirstName: strings.TrimSpace(body.FirstName),
			LastName:  strings.TrimSpace(body.LastName),
			Password:  password,
		})
		if err != nil {
			if errors.Is(err, service.ErrIdentityMappingFailed) {
				writeJSON(w, http.StatusCreated, acceptInviteResponse{IdPUser: result.IdPUser, Error: err.Error()})
				return
			}
			writeInviteAcceptError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, acceptInviteResponse{IdPUser: result.IdPUser, ErunUser: &result.ErunUser})
	})
}

// writeInviteAcceptError distinguishes the token-shaped failures #1483 asks
// to report plainly (not found, expired, already consumed) from a downstream
// Zitadel failure, which forwards the IdP's own real status and message the
// same way writeIdentityAdminError does.
func writeInviteAcceptError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "invite link is invalid")
	case errors.Is(err, repository.ErrInviteExpired):
		writeError(w, http.StatusGone, "invite link has expired")
	case errors.Is(err, repository.ErrInviteConsumed):
		writeError(w, http.StatusGone, "invite link has already been used")
	case errors.Is(err, service.ErrInviteEmailMismatch):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeIdentityAdminError(w, err)
	}
}
