package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// IdentityAdminClient is the Zitadel Management API surface these routes
// call directly (list, deactivate, reactivate, org settings); *zitadel.Client
// satisfies it. Enrollment goes through IdentityEnroller instead, since it
// coordinates the IdP call with the erun-side user mapping.
type IdentityAdminClient interface {
	ListUsers(ctx context.Context, orgID string) ([]zitadel.User, error)
	DeactivateUser(ctx context.Context, orgID string, userID string) error
	ReactivateUser(ctx context.Context, orgID string, userID string) error
	GetOrgSettings(ctx context.Context) (zitadel.OrgSettings, error)
	UpdateOrgSettings(ctx context.Context, params zitadel.UpdateOrgSettingsParams) (zitadel.OrgSettings, error)
	GetSMTPStatus(ctx context.Context) (zitadel.SMTPStatus, error)
	UpdateSMTPConfig(ctx context.Context, params zitadel.SetSMTPConfigParams) (zitadel.SMTPStatus, error)
	CreateOrg(ctx context.Context, name string) (zitadel.Org, error)
}

// IdentityEnroller creates an IdP identity and its erun user mapping as one
// enrollment action. *service.IdentityService satisfies it.
type IdentityEnroller interface {
	Enroll(ctx context.Context, params service.EnrollIdentityParams) (service.EnrollIdentityResult, error)
}

// EnrolledUserLister is the erun-side half of the Users list: which of the
// IdP's identities are also enrolled erun users of the caller's own tenant.
// *repository.UserRepository satisfies it.
type EnrolledUserLister interface {
	List(ctx context.Context, filter repository.UserFilter) ([]model.User, error)
}

type IdentityRoutes struct {
	admin     IdentityAdminClient
	enroller  IdentityEnroller
	erunUsers EnrolledUserLister
}

// RegisterIdentityRoutes registers the identity-administration surface
// : the erun-zitadel chart provisions an org-owner Management
// API credential on every deployment, and until this, nothing consumed it —
// enrolling a colleague required a second browser tab on Zitadel's own admin
// console. Every operation here is named individually; this is deliberately
// not a generic proxy over the Management API (see the internal/zitadel
// package doc for the least-privilege reasoning). Restricted to an
// OPERATIONS tenant by every handler below: administering the platform's IdP
// is not a company tenant's business, and effective-permission gating still
// applies on top via the normal role_permissions mechanism every registered
// route already gets.
func RegisterIdentityRoutes(register ProtectedRouteRegistrar, admin IdentityAdminClient, enroller IdentityEnroller, erunUsers EnrolledUserLister) {
	routes := IdentityRoutes{admin: admin, enroller: enroller, erunUsers: erunUsers}
	register(http.MethodGet, "/v1/identity/users", http.HandlerFunc(routes.listUsers))
	register(http.MethodPost, "/v1/identity/users", http.HandlerFunc(routes.createUser))
	register(http.MethodPost, "/v1/identity/users/{external_id}/deactivate", http.HandlerFunc(routes.deactivateUser))
	register(http.MethodPost, "/v1/identity/users/{external_id}/reactivate", http.HandlerFunc(routes.reactivateUser))
	// Creating an org is what makes a *second* tenant possible on the
	// platform's own IdP: an org-scoped issuer resolves tenants by the org
	// claim, so a new tenant needs an org for its mapping to point at. Until
	// this, that was a hand-made org in Zitadel's own console — erun could
	// register the tenant and the issuer mapping and then had nowhere to
	// point them.
	register(http.MethodPost, "/v1/identity/orgs", http.HandlerFunc(routes.createOrg))
	register(http.MethodGet, "/v1/identity/org-settings", http.HandlerFunc(routes.getOrgSettings))
	register(http.MethodPatch, "/v1/identity/org-settings", http.HandlerFunc(routes.updateOrgSettings))
	// The platform's honest answer to "can this instance send mail at all"
	//: every flow that reaches a user out of band -- signup
	// verification, password reset, invitation -- depends on it, and until
	// this the only signal was Zitadel's own unhandled 404.
	register(http.MethodGet, "/v1/identity/smtp-settings", http.HandlerFunc(routes.getSMTPSettings))
	register(http.MethodPatch, "/v1/identity/smtp-settings", http.HandlerFunc(routes.updateSMTPSettings))
}

// listUsers, deactivateUser, and reactivateUser all accept an optional
// ?orgId= query parameter, addressing the same Zitadel org
// enrollIdentityUserRequest.OrgID can already create a user in: without it,
// a user created in another org was permanent, invisible residue --
// listable and deactivatable only in the credential's own org, no matter
// which org actually held it. Omitting orgId keeps that original behaviour,
// so an existing caller sees no change.
//
// orgId is taken as-is, never derived from a tenantId: tenant_issuers maps a
// tenant to zero, one, or several org_field_value rows (one per issuer), so
// "the org for tenant X" is not a well-defined single answer the way "the
// org for this explicit id" is. Guessing one of several candidate orgs would
// reintroduce the same silent wrong-org resolution this parameter exists to
// remove; refusing to guess and requiring the caller to name the org
// directly is the default-closed choice. No new permission check is added
// for it either: every handler here already requires an OPERATIONS-tenant
// caller (requireOperationsTenant below) plus the matching ReadAll/WriteAll
// permission, which is already the platform's strictest tier -- addressing a
// different org through an already-fully-gated action is not the same kind
// of entitlement decision as letting a caller reach a route it could not
// reach at all before.
var errIdentityAdminForbidden = errors.New("identity administration is restricted to an operations tenant")

// requireOperationsTenant is the shared gate every handler below applies
// first. Unlike resolveTargetTenant in users.go (which lets an operations
// caller opt into acting on another tenant), this surface has no
// company-tenant case at all: the IdP behind it is the platform's own, not a
// per-tenant resource an operations caller crosses into.
func requireOperationsTenant(securityContext security.Context) error {
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		return errIdentityAdminForbidden
	}
	return nil
}

func (r IdentityRoutes) securityContext(w http.ResponseWriter, req *http.Request) (security.Context, bool) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return security.Context{}, false
	}
	if err := requireOperationsTenant(securityContext); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return security.Context{}, false
	}
	return securityContext, true
}

// identityUserView reports one identity of the platform's IdP org alongside
// whether it is also an enrolled erun user of the caller's own tenant. The
// IdP's user list (zitadel.Client.ListUsers) and erun's own users table are
// two separate systems — see that method's doc comment — so a row present
// here with Enrolled=false is a self-registered or otherwise unmapped IdP
// account that cannot use erun, not a tenant member, and must not render as
// one.
type identityUserView struct {
	zitadel.User
	Enrolled   bool   `json:"enrolled"`
	ErunUserID string `json:"erunUserId,omitempty"`
}

func (r IdentityRoutes) listUsers(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := r.securityContext(w, req)
	if !ok {
		return
	}
	orgID := strings.TrimSpace(req.URL.Query().Get("orgId"))
	idpUsers, err := r.admin.ListUsers(req.Context(), orgID)
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	erunUsers, err := r.erunUsers.List(req.Context(), repository.UserFilter{TenantID: securityContext.TenantID})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	views := mergeIdentityUsers(idpUsers, erunUsers)
	writeJSON(w, http.StatusOK, views)
}

// mergeIdentityUsers cross-references the IdP's own identities against
// erun's enrolled users of the caller's tenant by external subject
// (zitadel.User.ID == model.User.ExternalUserID), so the console can tell a
// tenant member apart from an IdP-only account instead of rendering every
// row in the org identically.
func mergeIdentityUsers(idpUsers []zitadel.User, erunUsers []model.User) []identityUserView {
	enrolledBySubject := make(map[string]model.User, len(erunUsers))
	for _, u := range erunUsers {
		if u.ExternalUserID != "" {
			enrolledBySubject[u.ExternalUserID] = u
		}
	}
	views := make([]identityUserView, 0, len(idpUsers))
	for _, u := range idpUsers {
		view := identityUserView{User: u}
		if erunUser, ok := enrolledBySubject[u.ID]; ok {
			view.Enrolled = true
			view.ErunUserID = erunUser.UserID
		}
		views = append(views, view)
	}
	return views
}

type enrollIdentityUserRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	// OrgID enrolls into another organization rather than the platform's own
	// -- the identity boundary another tenant resolves by, and the only way a
	// newly created tenant can receive its first user. No erun user row is
	// written in that case; the target tenant's own first-user bootstrap
	// enrols them, with full access, on their first sign-in.
	OrgID string `json:"orgId"`
}

// enrollIdentityUserResponse always carries IdPUser once the IdP half
// succeeded. ErunUser is nil and Error is set when the erun-side mapping
// failed after the IdP user was created — the "which half landed" report
// the enrollment flow must give rather than either silently swallowing the
// failure or claiming full success.
//
// MailDeliveryConfigured/TemporaryPassword/Warning report the other half of
// what actually landed: a caller cannot tell "invited, check
// your inbox" apart from "invited, but nothing could ever be sent" from
// IdPUser alone, and the difference is exactly which action the operator
// needs to take next.
type enrollIdentityUserResponse struct {
	IdPUser                zitadel.User `json:"idpUser"`
	ErunUser               *model.User  `json:"erunUser,omitempty"`
	Error                  string       `json:"error,omitempty"`
	MailDeliveryConfigured bool         `json:"mailDeliveryConfigured"`
	TemporaryPassword      string       `json:"temporaryPassword,omitempty"`
	Warning                string       `json:"warning,omitempty"`
}

// newEnrollIdentityUserResponse fills the mail-delivery half of the
// response from an EnrollIdentityResult. When mail delivery was not
// configured, Enroll already minted a temporary password instead of the
// usual invite email; this is what tells the caller the invite link they
// might otherwise expect will never arrive, and gives them the credential
// to hand over instead.
func newEnrollIdentityUserResponse(result service.EnrollIdentityResult) enrollIdentityUserResponse {
	resp := enrollIdentityUserResponse{
		IdPUser:                result.IdPUser,
		MailDeliveryConfigured: result.MailDeliveryConfigured,
		TemporaryPassword:      result.TemporaryPassword,
	}
	if !result.MailDeliveryConfigured {
		resp.Warning = "This platform's identity provider has no SMTP configured, so no invitation email was sent. Share temporaryPassword with " + result.IdPUser.Username + " directly; they must sign in and change it."
	}
	return resp
}

func (r IdentityRoutes) createUser(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := r.securityContext(w, req)
	if !ok {
		return
	}
	var body enrollIdentityUserRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(body.Username)
	email := strings.TrimSpace(body.Email)
	if username == "" || email == "" {
		writeError(w, http.StatusBadRequest, "username and email are required")
		return
	}

	result, err := r.enroller.Enroll(req.Context(), service.EnrollIdentityParams{
		Username:  username,
		Email:     email,
		FirstName: strings.TrimSpace(body.FirstName),
		LastName:  strings.TrimSpace(body.LastName),
		Issuer:    securityContext.ExternalIssuer,
		OrgID:     strings.TrimSpace(body.OrgID),
	})
	if err != nil {
		if errors.Is(err, service.ErrIdentityMappingFailed) {
			// The IdP identity is real; only the erun mapping failed. Report
			// both halves rather than a bare 500, so the operator can see the
			// orphaned IdP user id and retry the mapping (POST /v1/users with
			// that id as subject) instead of enrolling a duplicate.
			resp := newEnrollIdentityUserResponse(result)
			resp.Error = err.Error()
			writeJSON(w, http.StatusCreated, resp)
			return
		}
		writeIdentityAdminError(w, err)
		return
	}
	resp := newEnrollIdentityUserResponse(result)
	resp.ErunUser = &result.ErunUser
	writeJSON(w, http.StatusCreated, resp)
}

func (r IdentityRoutes) deactivateUser(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	orgID := strings.TrimSpace(req.URL.Query().Get("orgId"))
	if err := r.admin.DeactivateUser(req.Context(), orgID, req.PathValue("external_id")); err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r IdentityRoutes) reactivateUser(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	orgID := strings.TrimSpace(req.URL.Query().Get("orgId"))
	if err := r.admin.ReactivateUser(req.Context(), orgID, req.PathValue("external_id")); err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createOrgRequest is the org-creation input. Name only: everything else
// about a Zitadel org is policy this surface already administers separately.
type createOrgRequest struct {
	Name string `json:"name"`
}

// createOrg creates a Zitadel organization to hold a tenant's own identities.
// It deliberately stops there rather than also registering the erun tenant:
// the two are separate resources with separate gates, and an operator may be
// creating an org for a tenant that already exists. Pair the returned id with
// POST /v1/tenants orgFieldValue, or with PATCH /v1/tenant-issuers to convert
// a single-tenant issuer first.
func (r IdentityRoutes) createOrg(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	var input createOrgRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	org, err := r.admin.CreateOrg(req.Context(), input.Name)
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

func (r IdentityRoutes) getOrgSettings(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	settings, err := r.admin.GetOrgSettings(req.Context())
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// updateOrgSettingsRequest mirrors zitadel.UpdateOrgSettingsParams field for
// field: a route-local request struct (per this module's convention for
// partial-update inputs) rather than decoding straight into the client
// package's params type, keeping the HTTP contract owned by this layer.
type updateOrgSettingsRequest struct {
	ForceMFA                  *bool   `json:"forceMfa,omitempty"`
	AllowRegister             *bool   `json:"allowRegister,omitempty"`
	MinPasswordLength         *uint64 `json:"minPasswordLength,omitempty"`
	PasswordRequiresUppercase *bool   `json:"passwordRequiresUppercase,omitempty"`
	PasswordRequiresLowercase *bool   `json:"passwordRequiresLowercase,omitempty"`
	PasswordRequiresNumber    *bool   `json:"passwordRequiresNumber,omitempty"`
	PasswordRequiresSymbol    *bool   `json:"passwordRequiresSymbol,omitempty"`
}

func (r IdentityRoutes) updateOrgSettings(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	var body updateOrgSettingsRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	settings, err := r.admin.UpdateOrgSettings(req.Context(), zitadel.UpdateOrgSettingsParams{
		ForceMFA:                  body.ForceMFA,
		AllowRegister:             body.AllowRegister,
		MinPasswordLength:         body.MinPasswordLength,
		PasswordRequiresUppercase: body.PasswordRequiresUppercase,
		PasswordRequiresLowercase: body.PasswordRequiresLowercase,
		PasswordRequiresNumber:    body.PasswordRequiresNumber,
		PasswordRequiresSymbol:    body.PasswordRequiresSymbol,
	})
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// getSMTPSettings answers "can this instance send mail at all" (issue
// #1168) directly, rather than leaving Zitadel's own unhandled 404 as the
// only signal.
func (r IdentityRoutes) getSMTPSettings(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	status, err := r.admin.GetSMTPStatus(req.Context())
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// updateSMTPSettingsRequest is the declarative desired state for the
// platform's outbound mail, provider-agnostic and sourced
// from wherever the operator holds the credential out of band; Password is
// omitted on an update that only changes non-secret fields.
type updateSMTPSettingsRequest struct {
	Host           string `json:"host"`
	Username       string `json:"username"`
	Password       string `json:"password,omitempty"`
	SenderAddress  string `json:"senderAddress"`
	SenderName     string `json:"senderName,omitempty"`
	ReplyToAddress string `json:"replyToAddress,omitempty"`
	TLS            bool   `json:"tls"`
}

func (r IdentityRoutes) updateSMTPSettings(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	var body updateSMTPSettingsRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	host := strings.TrimSpace(body.Host)
	senderAddress := strings.TrimSpace(body.SenderAddress)
	if host == "" || senderAddress == "" {
		writeError(w, http.StatusBadRequest, "host and senderAddress are required")
		return
	}
	status, err := r.admin.UpdateSMTPConfig(req.Context(), zitadel.SetSMTPConfigParams{
		Host:           host,
		User:           strings.TrimSpace(body.Username),
		Password:       body.Password,
		SenderAddress:  senderAddress,
		SenderName:     strings.TrimSpace(body.SenderName),
		ReplyToAddress: strings.TrimSpace(body.ReplyToAddress),
		TLS:            body.TLS,
	})
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// writeIdentityAdminError forwards a Zitadel Management API error's real
// status and message when there is one (identity-state text like "User with
// state initial can only be deleted not deactivated" is actionable for an
// operator), falling back to 502 for a transport-level failure that never
// got a Zitadel response at all.
func writeIdentityAdminError(w http.ResponseWriter, err error) {
	var apiErr *zitadel.APIError
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		writeError(w, status, apiErr.Body)
		return
	}
	writeError(w, http.StatusBadGateway, "identity provider request failed")
}
