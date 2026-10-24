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

// IdentityAdminClient is the Zitadel Management API surface these routes call.
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

// IdentityEnroller creates an IdP identity and its erun user mapping together.
type IdentityEnroller interface {
	Enroll(ctx context.Context, params service.EnrollIdentityParams) (service.EnrollIdentityResult, error)
}

// EnrolledUserLister lists the erun users the IdP identities are joined against.
type EnrolledUserLister interface {
	List(ctx context.Context, filter repository.UserFilter) ([]model.User, error)
}

type IdentityRoutes struct {
	admin     IdentityAdminClient
	enroller  IdentityEnroller
	erunUsers EnrolledUserLister
}

// RegisterIdentityRoutes registers identity administration, restricted to an
// OPERATIONS tenant. Operations are named individually, never a generic proxy.
func RegisterIdentityRoutes(register ProtectedRouteRegistrar, admin IdentityAdminClient, enroller IdentityEnroller, erunUsers EnrolledUserLister) {
	routes := IdentityRoutes{admin: admin, enroller: enroller, erunUsers: erunUsers}
	register(http.MethodGet, "/v1/identity/users", http.HandlerFunc(routes.listUsers))
	register(http.MethodPost, "/v1/identity/users", http.HandlerFunc(routes.createUser))
	register(http.MethodPost, "/v1/identity/users/{external_id}/deactivate", http.HandlerFunc(routes.deactivateUser))
	register(http.MethodPost, "/v1/identity/users/{external_id}/reactivate", http.HandlerFunc(routes.reactivateUser))
	// An org-scoped issuer resolves tenants by org, so a new tenant needs an org.
	register(http.MethodPost, "/v1/identity/orgs", http.HandlerFunc(routes.createOrg))
	register(http.MethodGet, "/v1/identity/org-settings", http.HandlerFunc(routes.getOrgSettings))
	register(http.MethodPatch, "/v1/identity/org-settings", http.HandlerFunc(routes.updateOrgSettings))
	register(http.MethodGet, "/v1/identity/smtp-settings", http.HandlerFunc(routes.getSMTPSettings))
	register(http.MethodPatch, "/v1/identity/smtp-settings", http.HandlerFunc(routes.updateSMTPSettings))
}

// orgId is taken as given, never derived from a tenant: a tenant may map to
// several orgs, and guessing one would resolve the wrong org silently.
var errIdentityAdminForbidden = errors.New("identity administration is restricted to an operations tenant")

// requireOperationsTenant gates every handler: the IdP is the platform's own, so
// there is no company-tenant case.
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

// identityUserView marks whether an IdP identity is enrolled in erun; an unmapped
// IdP account cannot use erun. The embedded User.ID is the OIDC subject join key.
type identityUserView struct {
	zitadel.User
	Enrolled   bool   `json:"enrolled"`
	ErunUserID string `json:"erunUserId,omitempty"`
	// ErunUsername is chosen at enrolment, independent of the IdP username.
	ErunUsername string `json:"erunUsername,omitempty"`
}

func (r IdentityRoutes) listUsers(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := r.securityContext(w, req)
	if !ok {
		return
	}
	targetTenantID, err := resolveTargetTenant(securityContext, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	orgID := strings.TrimSpace(req.URL.Query().Get("orgId"))
	idpUsers, err := r.admin.ListUsers(req.Context(), orgID)
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	erunUsers, err := r.erunUsers.List(req.Context(), repository.UserFilter{TenantID: targetTenantID})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	views := mergeIdentityUsers(idpUsers, erunUsers)
	writeJSON(w, http.StatusOK, views)
}

// mergeIdentityUsers joins on zitadel.User.ID == model.User.ExternalUserID.
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
			view.ErunUsername = erunUser.Username
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
	// OrgID enrolls into another org without writing an erun user row; that tenant's
	// first-user bootstrap enrols them on first sign-in.
	OrgID string `json:"orgId"`
}

// enrollIdentityUserResponse reports which halves landed: Error is set when the
// erun mapping failed after the IdP user was created.
type enrollIdentityUserResponse struct {
	IdPUser                zitadel.User `json:"idpUser"`
	ErunUser               *model.User  `json:"erunUser,omitempty"`
	Error                  string       `json:"error,omitempty"`
	MailDeliveryConfigured bool         `json:"mailDeliveryConfigured"`
	TemporaryPassword      string       `json:"temporaryPassword,omitempty"`
	Warning                string       `json:"warning,omitempty"`
}

// newEnrollIdentityUserResponse warns when mail is unconfigured and no invite will arrive.
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
			// Report both halves so the operator can retry the mapping, not enroll a duplicate.
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

type createOrgRequest struct {
	Name string `json:"name"`
}

// createOrg does not register the erun tenant: the org may be for an existing tenant.
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

// updateSMTPSettingsRequest omits Password on an update of non-secret fields only.
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

// writeIdentityAdminError forwards Zitadel's status and message, or 502 on
// transport failure. A taken login name becomes USERNAME_TAKEN naming the chosen
// name, checked first because the APIError branch would otherwise swallow it.
func writeIdentityAdminError(w http.ResponseWriter, err error) {
	var usernameTaken *zitadel.UsernameTakenError
	if errors.As(err, &usernameTaken) {
		writeErrorCode(w, http.StatusConflict, "USERNAME_TAKEN", usernameTaken.Error())
		return
	}
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
