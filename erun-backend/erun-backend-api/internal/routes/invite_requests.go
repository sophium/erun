package routes

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/ratelimit"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// InviteRequestStore is the read/decision-lookup dependency for the
// protected invite-request routes. *repository.InviteRequestRepository
// satisfies it.
type InviteRequestStore interface {
	Get(ctx context.Context, id string) (model.InviteRequest, error)
	List(ctx context.Context, filter repository.InviteRequestFilter) ([]model.InviteRequest, error)
}

// InviteRequestApprover runs the approve/decline workflow.
// *service.InviteRequestService satisfies it.
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

// RegisterInviteRequestRoutes registers the operator half of the request/
// approve queue: listing pending requests and deciding them. A tenant admin
// reaches these for a JOIN_TENANT request naming their own tenant; only an
// operations caller reaches them for a CREATE_TENANT request, or a
// JOIN_TENANT request naming a tenant other than their own — both enforced
// in the handlers below, not by the route registration itself.
func RegisterInviteRequestRoutes(register ProtectedRouteRegistrar, requests InviteRequestStore, tenants TenantRepository, decisions InviteRequestApprover) {
	routes := inviteRequestRoutes{requests: requests, tenants: tenants, decisions: decisions}
	register(http.MethodGet, "/v1/invite-requests", http.HandlerFunc(routes.list))
	register(http.MethodPost, "/v1/invite-requests/{invite_request_id}/approve", http.HandlerFunc(routes.approve))
	register(http.MethodPost, "/v1/invite-requests/{invite_request_id}/decline", http.HandlerFunc(routes.decline))
}

// list returns every request an operations caller can see (both kinds,
// optionally narrowed by ?kind=), or, for a non-operations caller, only
// PENDING/decided JOIN_TENANT requests naming their own tenant — invite_requests
// carries no tenant_id to filter by, so the caller's own tenant name is read
// and applied explicitly, the same pattern TenantRepository.List already
// uses for this root table.
func (r inviteRequestRoutes) list(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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
			writeRepositoryError(w, err)
			return
		}
		filter.Kind = model.InviteRequestKindJoinTenant
		filter.TenantName = tenant.Name
	}
	requests, err := r.requests.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, requests)
}

// approve decides a pending request in the caller's favor: enrolling the
// requester (JOIN_TENANT) or registering the tenant and enrolling the
// requester as its first user (CREATE_TENANT), then minting an invite either
// way. Authority is checked here, before the service ever runs: a
// JOIN_TENANT approval requires the caller's own tenant name to match the
// request's; a CREATE_TENANT approval requires an operations tenant.
func (r inviteRequestRoutes) approve(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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
		writeError(w, http.StatusInternalServerError, "unknown invite request kind")
		return
	}
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decided)
}

type declineInviteRequestBody struct {
	Reason string `json:"reason"`
}

// decline refuses a pending request with a reason, which reaches the
// requester's own unauthenticated status read. A decline with no reason is a
// dead end (root AGENTS.md § "Smooth, Seamless, No Dead Ends"), refused here
// before the schema's own CHECK constraint ever has to.
func (r inviteRequestRoutes) decline(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, declined)
}

// loadPendingRequest fetches the path's request and refuses (writing the
// response itself) unless it is still PENDING — a request already decided
// cannot be decided again.
func (r inviteRequestRoutes) loadPendingRequest(w http.ResponseWriter, req *http.Request) (model.InviteRequest, bool) {
	request, err := r.requests.Get(req.Context(), req.PathValue("invite_request_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return model.InviteRequest{}, false
	}
	if request.Status != model.InviteRequestStatusPending {
		writeError(w, http.StatusConflict, "invite request has already been decided")
		return model.InviteRequest{}, false
	}
	return request, true
}

// callerOwnsTenantName reports whether the caller's own current tenant name
// matches tenantName — the "authority over that tenant" a JOIN_TENANT
// decision requires — writing a 403 itself when it does not.
func (r inviteRequestRoutes) callerOwnsTenantName(w http.ResponseWriter, req *http.Request, tenantName string) bool {
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return false
	}
	if !strings.EqualFold(tenant.Name, tenantName) {
		writeError(w, http.StatusForbidden, "this request does not name your tenant")
		return false
	}
	return true
}

// InviteRequestTokenVerifier verifies a bearer token and returns its claims,
// tolerating a caller resolved to no tenant at all — the case the normal
// protected registrar's auth middleware rejects (erun-backend-api/AGENTS.md's
// Authentication section: "an unknown external subject is unauthorized for a
// tenant that already has a user"). Its method set matches
// backendapi.TokenVerifier exactly (a type alias over the same
// security.Claims), so the same verifier instance satisfies both without an
// adapter — this package just cannot name that type directly, since routes
// must not import the root backendapi package.
type InviteRequestTokenVerifier interface {
	VerifyBearerToken(ctx context.Context, token string) (security.Claims, error)
}

// InviteRequestSubmitter is the persistence dependency for the unauthenticated
// submit/status routes. *repository.InviteRequestRepository satisfies it.
type InviteRequestSubmitter interface {
	Submit(ctx context.Context, params repository.SubmitInviteRequestParams) (model.InviteRequest, error)
	GetByIdentity(ctx context.Context, issuer string, subject string) (model.InviteRequest, error)
}

// InviteRequestWindowReader reads the platform's current invite-request
// rate-limit window. *repository.PlatformRateLimitRepository satisfies it.
type InviteRequestWindowReader interface {
	Get(ctx context.Context) (model.PlatformRateLimit, error)
}

// preVerificationLimit/Window is the cheap burst ceiling ahead of token
// verification (issue §9): not operator-tunable, unlike the post-verification
// window below — it exists purely to bound the cost of verifying (fetching
// JWKS, checking a signature) a flood of tokens that will mostly turn out
// invalid, so it has to run before verification, not after.
const (
	preVerificationLimit  = 20
	preVerificationWindow = time.Minute
)

// InviteRequestRateLimiter is POST /v1/invite-requests' two-tier abuse bound:
// a cheap pre-verification tier keyed on source address, and a
// post-verification tier keyed on the verified (issuer, subject) whose
// window is read fresh from configuration on every call, so an operator's
// change takes effect on the very next request with no redeploy. Built on
// internal/ratelimit.Limiter, a general fixed-window bucket, so the
// documented per-token/per-tenant limiter (erun-docs' api-protocol.md "Rate
// limits") can adopt the same abstraction later rather than replace it.
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

// RegisterInviteRequestPublicRoutes registers the two routes reachable by a
// caller who has proven who they are at their own IdP but is enrolled
// nowhere: submitting a request, and reading back its own status. Neither
// can run behind the normal protected registrar — that chain's tenant/user
// resolution rejects exactly this caller — so, like POST /v1/invites/accept,
// they are registered directly on the mux instead. Unlike that route, the
// caller here does present a bearer token; verifyInviteRequestBearerToken
// verifies its signature in full and stops there, never resolving a tenant
// or user.
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

// submit records a new (or updates the caller's existing pending) invite
// request. The response is identical whether or not TenantName names a real
// tenant — Submit itself never looks that up — so this can never become a
// tenant-name oracle (issue §4).
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
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	if !result.Allowed {
		writeRateLimited(w, result)
		return
	}

	var body submitInviteRequestBody
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	kind := model.InviteRequestKind(strings.ToUpper(strings.TrimSpace(body.Kind)))
	tenantName := strings.TrimSpace(body.TenantName)
	switch kind {
	case model.InviteRequestKindJoinTenant:
		if tenantName == "" {
			writeError(w, http.StatusBadRequest, "tenantName is required")
			return
		}
	case model.InviteRequestKindCreateTenant:
		if err := eruncommon.ValidateTenantName(tenantName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "kind must be one of JOIN_TENANT, CREATE_TENANT")
		return
	}

	request, err := r.requests.Submit(req.Context(), repository.SubmitInviteRequestParams{
		Issuer:          claims.Issuer,
		Subject:         claims.Subject,
		Email:           strings.TrimSpace(body.Email),
		DisplayName:     strings.TrimSpace(body.DisplayName),
		Kind:            kind,
		TenantName:      tenantName,
		EnvironmentName: strings.TrimSpace(body.EnvironmentName),
		Note:            strings.TrimSpace(body.Note),
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, request)
}

// mine answers "what is the state of my own request" — the one thing a
// requester can check while waiting, without an account on the platform
// (issue §5/§7).
func (r inviteRequestPublicRoutes) mine(w http.ResponseWriter, req *http.Request) {
	claims, ok := verifyInviteRequestBearerToken(req, r.verifier)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}
	request, err := r.requests.GetByIdentity(req.Context(), claims.Issuer, claims.Subject)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, request)
}

// verifyInviteRequestBearerToken verifies the request's bearer token in
// full — the same signature/issuer check every protected route requires —
// but stops there: it does not resolve a tenant or user, which is the whole
// point of this registrar.
func verifyInviteRequestBearerToken(req *http.Request, verifier InviteRequestTokenVerifier) (security.Claims, bool) {
	token, ok := bearerTokenFromHeader(req.Header.Get("Authorization"))
	if !ok {
		return security.Claims{}, false
	}
	claims, err := verifier.VerifyBearerToken(req.Context(), token)
	if err != nil || strings.TrimSpace(claims.Issuer) == "" || strings.TrimSpace(claims.Subject) == "" {
		return security.Claims{}, false
	}
	return claims, true
}

func bearerTokenFromHeader(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", false
	}
	return fields[1], true
}

func sourceAddress(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// writeRateLimited refuses with 429 and the documented Retry-After/
// RateLimit-* headers (erun-docs/docs/agent-reference/api-protocol.md's Rate
// limits section) — the shape this endpoint's limiter is the first
// implementation of.
func writeRateLimited(w http.ResponseWriter, result ratelimit.Result) {
	w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Round(time.Second).Seconds())))
	w.Header().Set("RateLimit-Limit", strconv.Itoa(result.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	writeError(w, http.StatusTooManyRequests, "too many requests, try again later")
}
