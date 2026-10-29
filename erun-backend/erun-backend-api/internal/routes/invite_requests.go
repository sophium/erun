package routes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/ratelimit"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
)

// InviteRequestStore reads invite requests for the protected routes.
type InviteRequestStore interface {
	Get(ctx context.Context, id string) (model.InviteRequest, error)
	List(ctx context.Context, filter repository.InviteRequestFilter) ([]model.InviteRequest, error)
}

// InviteRequestApprover runs the approve/decline workflow.
type InviteRequestApprover interface {
	ApproveJoin(ctx context.Context, request model.InviteRequest, decidedByUserID string) (model.InviteRequest, error)
	ApproveCreateTenant(ctx context.Context, request model.InviteRequest, decidedByUserID string) (model.InviteRequest, error)
	Decline(ctx context.Context, request model.InviteRequest, decidedByUserID string, reason string) (model.InviteRequest, error)
}

type inviteRequestRoutes struct {
	requests  InviteRequestStore
	tenants   TenantRepository
	decisions InviteRequestApprover
}

// RegisterInviteRequestRoutes registers the request/approve queue. Authority
// (own-tenant JOIN_TENANT, operations otherwise) is enforced in the handlers.
func RegisterInviteRequestRoutes(register ProtectedRouteRegistrar, requests InviteRequestStore, tenants TenantRepository, decisions InviteRequestApprover) {
	routes := inviteRequestRoutes{requests: requests, tenants: tenants, decisions: decisions}
	register(http.MethodGet, "/v1/invite-requests", http.HandlerFunc(routes.list))
	register(http.MethodPost, "/v1/invite-requests/{invite_request_id}/approve", http.HandlerFunc(routes.approve))
	register(http.MethodPost, "/v1/invite-requests/{invite_request_id}/decline", http.HandlerFunc(routes.decline))
}

// list scopes a non-operations caller to JOIN_TENANT requests naming its own
// tenant; invite_requests has no tenant_id for RLS to filter by.
func (r inviteRequestRoutes) list(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	filter := repository.InviteRequestFilter{
		Status: model.InviteRequestStatus(strings.ToUpper(strings.TrimSpace(req.URL.Query().Get("status")))),
	}
	if securityContext.TenantType == string(model.TenantTypeOperations) {
		filter.Kind = model.InviteRequestKind(strings.ToUpper(strings.TrimSpace(req.URL.Query().Get("kind"))))
	} else {
		tenant, err := r.tenants.Current(req.Context())
		if err != nil {
			writeRepositoryError(w, req, err)
			return
		}
		filter.Kind = model.InviteRequestKindJoinTenant
		filter.TenantName = tenant.Name
	}
	requests, err := r.requests.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, requests)
}

// approve checks authority before the service runs: JOIN_TENANT needs the
// caller's own tenant name, CREATE_TENANT an operations tenant.
func (r inviteRequestRoutes) approve(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	request, ok := r.loadPendingRequest(w, req)
	if !ok {
		return
	}

	var decided model.InviteRequest
	var err error
	switch request.Kind {
	case model.InviteRequestKindJoinTenant:
		if !r.callerOwnsTenantName(w, req, request.TenantName) {
			return
		}
		decided, err = r.decisions.ApproveJoin(req.Context(), request, securityContext.ErunUserID)
	case model.InviteRequestKindCreateTenant:
		if securityContext.TenantType != string(model.TenantTypeOperations) {
			writeError(w, http.StatusForbidden, "approving a tenant-creation request requires an operations tenant")
			return
		}
		decided, err = r.decisions.ApproveCreateTenant(req.Context(), request, securityContext.ErunUserID)
	default:
		writeInternalError(w, req, "unknown invite request kind", fmt.Errorf("invite request %s has unrecognized kind %q", request.InviteRequestID, request.Kind))
		return
	}
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, decided)
}

type declineInviteRequestBody struct {
	Reason string `json:"reason"`
}

// decline requires a reason, since it reaches the requester's status read.
func (r inviteRequestRoutes) decline(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	var body declineInviteRequestBody
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}

	request, ok := r.loadPendingRequest(w, req)
	if !ok {
		return
	}
	switch request.Kind {
	case model.InviteRequestKindJoinTenant:
		if !r.callerOwnsTenantName(w, req, request.TenantName) {
			return
		}
	case model.InviteRequestKindCreateTenant:
		if securityContext.TenantType != string(model.TenantTypeOperations) {
			writeError(w, http.StatusForbidden, "declining a tenant-creation request requires an operations tenant")
			return
		}
	}

	declined, err := r.decisions.Decline(req.Context(), request, securityContext.ErunUserID, reason)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, declined)
}

// loadPendingRequest refuses a request that is already decided, writing the response.
func (r inviteRequestRoutes) loadPendingRequest(w http.ResponseWriter, req *http.Request) (model.InviteRequest, bool) {
	request, err := r.requests.Get(req.Context(), req.PathValue("invite_request_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return model.InviteRequest{}, false
	}
	if request.Status != model.InviteRequestStatusPending {
		writeError(w, http.StatusConflict, "invite request has already been decided")
		return model.InviteRequest{}, false
	}
	return request, true
}

// callerOwnsTenantName writes the 403 itself on a mismatch.
func (r inviteRequestRoutes) callerOwnsTenantName(w http.ResponseWriter, req *http.Request, tenantName string) bool {
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return false
	}
	if !strings.EqualFold(tenant.Name, tenantName) {
		writeError(w, http.StatusForbidden, "this request does not name your tenant")
		return false
	}
	return true
}

// InviteRequestTokenVerifier verifies a token without requiring a tenant. Its
// method set matches backendapi.TokenVerifier, which routes must not import.
type InviteRequestTokenVerifier interface {
	VerifyBearerToken(ctx context.Context, token string) (security.Claims, error)
}

// InviteRequestSubmitter persists submissions for the unauthenticated routes.
type InviteRequestSubmitter interface {
	Submit(ctx context.Context, params repository.SubmitInviteRequestParams) (model.InviteRequest, error)
	GetByIdentity(ctx context.Context, issuer string, subject string) (model.InviteRequest, error)
}

// InviteRequestWindowReader reads the current invite-request rate-limit window.
type InviteRequestWindowReader interface {
	Get(ctx context.Context) (model.PlatformRateLimit, error)
}

// The pre-verification tier bounds the cost of verifying a flood of invalid
// tokens, so it runs before verification and is not operator-tunable.
const (
	preVerificationLimit  = 20
	preVerificationWindow = time.Minute
)

// InviteRequestRateLimiter bounds abuse per source address before verification and
// per verified identity after; the identity window is read fresh on every call.
type InviteRequestRateLimiter struct {
	limiter *ratelimit.Limiter
	windows InviteRequestWindowReader
}

func NewInviteRequestRateLimiter(windows InviteRequestWindowReader) *InviteRequestRateLimiter {
	return &InviteRequestRateLimiter{limiter: ratelimit.NewLimiter(), windows: windows}
}

func (l *InviteRequestRateLimiter) allowSourceAddress(address string) ratelimit.Result {
	return l.limiter.Allow("addr:"+address, preVerificationLimit, preVerificationWindow, time.Now())
}

func (l *InviteRequestRateLimiter) allowIdentity(ctx context.Context, issuer string, subject string) (ratelimit.Result, error) {
	config, err := l.windows.Get(ctx)
	if err != nil {
		return ratelimit.Result{}, err
	}
	window := time.Duration(config.InviteRequestWindowSeconds) * time.Second
	return l.limiter.Allow("identity:"+issuer+"|"+subject, 1, window, time.Now()), nil
}

type inviteRequestPublicRoutes struct {
	verifier InviteRequestTokenVerifier
	requests InviteRequestSubmitter
	limiter  *InviteRequestRateLimiter
}

// RegisterInviteRequestPublicRoutes registers routes for a caller enrolled nowhere,
// directly on the mux: the protected chain's tenant resolution rejects this caller.
func RegisterInviteRequestPublicRoutes(mux *http.ServeMux, verifier InviteRequestTokenVerifier, requests InviteRequestSubmitter, limiter *InviteRequestRateLimiter) {
	routes := inviteRequestPublicRoutes{verifier: verifier, requests: requests, limiter: limiter}
	mux.HandleFunc("POST /v1/invite-requests", routes.submit)
	mux.HandleFunc("GET /v1/invite-requests/mine", routes.mine)
}

type submitInviteRequestBody struct {
	Email           string `json:"email,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// submit answers identically whether or not TenantName exists, so it is never a
// tenant-name oracle.
func (r inviteRequestPublicRoutes) submit(w http.ResponseWriter, req *http.Request) {
	if result := r.limiter.allowSourceAddress(sourceAddress(req)); !result.Allowed {
		writeRateLimited(w, result)
		return
	}

	claims, ok := verifyInviteRequestBearerToken(req, r.verifier)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}

	result, err := r.limiter.allowIdentity(req.Context(), claims.Issuer, claims.Subject)
	if err != nil {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), err)
		return
	}
	if !result.Allowed {
		writeRateLimited(w, result)
		return
	}

	params, status, msg := parseSubmitInviteRequestParams(req, claims)
	if msg != "" {
		writeError(w, status, msg)
		return
	}

	request, err := r.requests.Submit(req.Context(), params)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, request)
}

func parseSubmitInviteRequestParams(req *http.Request, claims security.Claims) (repository.SubmitInviteRequestParams, int, string) {
	var body submitInviteRequestBody
	if err := decodeJSON(req, &body); err != nil {
		return repository.SubmitInviteRequestParams{}, http.StatusBadRequest, "invalid request body"
	}
	kind := model.InviteRequestKind(strings.ToUpper(strings.TrimSpace(body.Kind)))
	tenantName := strings.TrimSpace(body.TenantName)
	switch kind {
	case model.InviteRequestKindJoinTenant:
		if tenantName == "" {
			return repository.SubmitInviteRequestParams{}, http.StatusBadRequest, "tenantName is required"
		}
	case model.InviteRequestKindCreateTenant:
		if err := runtimetarget.ValidateTenantName(tenantName); err != nil {
			return repository.SubmitInviteRequestParams{}, http.StatusBadRequest, err.Error()
		}
	default:
		return repository.SubmitInviteRequestParams{}, http.StatusBadRequest, "kind must be one of JOIN_TENANT, CREATE_TENANT"
	}
	return repository.SubmitInviteRequestParams{
		Issuer:          claims.Issuer,
		Subject:         claims.Subject,
		Email:           strings.TrimSpace(body.Email),
		DisplayName:     strings.TrimSpace(body.DisplayName),
		Kind:            kind,
		TenantName:      tenantName,
		EnvironmentName: strings.TrimSpace(body.EnvironmentName),
		Note:            strings.TrimSpace(body.Note),
	}, 0, ""
}

func (r inviteRequestPublicRoutes) mine(w http.ResponseWriter, req *http.Request) {
	claims, ok := verifyInviteRequestBearerToken(req, r.verifier)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}
	request, err := r.requests.GetByIdentity(req.Context(), claims.Issuer, claims.Subject)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, request)
}

// verifyInviteRequestBearerToken verifies the token fully but never resolves a
// tenant or user.
func verifyInviteRequestBearerToken(req *http.Request, verifier InviteRequestTokenVerifier) (security.Claims, bool) {
	token, ok := security.BearerToken(req.Header.Get("Authorization"))
	if !ok {
		return security.Claims{}, false
	}
	claims, err := verifier.VerifyBearerToken(req.Context(), token)
	if err != nil || strings.TrimSpace(claims.Issuer) == "" || strings.TrimSpace(claims.Subject) == "" {
		return security.Claims{}, false
	}
	return claims, true
}

func sourceAddress(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func writeRateLimited(w http.ResponseWriter, result ratelimit.Result) {
	w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Round(time.Second).Seconds())))
	w.Header().Set("RateLimit-Limit", strconv.Itoa(result.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	writeError(w, http.StatusTooManyRequests, "too many requests, try again later")
}
