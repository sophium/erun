package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

func (a *App) LoadTenantDashboard(input uiTenantDashboardInput) (uiTenantDashboard, error) {
	tenant, err := requireTenant("loading the tenant dashboard", input.Tenant)
	if err != nil {
		return uiTenantDashboard{}, err
	}
	dashboard := uiTenantDashboard{
		Tenant:      tenant,
		Environment: strings.TrimSpace(input.Environment),
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(input.MCPURL) != "" || strings.TrimSpace(input.KubernetesContext) != "" {
		input.mcpBearer = a.mcpBearer(tenant, strings.TrimSpace(input.Environment))
		log, err := a.deps.loadAPILog(ctx, input)
		if err != nil {
			dashboard.APILogError = err.Error()
		} else {
			dashboard.APILog = log
		}
	}

	resolution, err := a.resolveTenantPlatform(tenant, strings.TrimSpace(input.PlatformAlias))
	if err != nil {
		return uiTenantDashboard{}, err
	}
	dashboard.PlatformState = resolution.state
	dashboard.PlatformAliasChoices = resolution.aliasChoices
	dashboard.PlatformAlias = resolution.alias
	dashboard.PlatformURL = resolution.apiURL
	dashboard.PlatformIssuer = resolution.issuer
	dashboard.PlatformSubject = resolution.subject
	if resolution.state != tenantPlatformStateReady {
		return dashboard, nil
	}
	dashboard.APIURL = resolution.apiURL

	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	defer cancel()
	loadTenantDashboardData(requestCtx, resolution.client, &dashboard, input)
	// The caller's own invite-request status needs only the bearer this
	// resolution already minted, never tenant membership — read it even when
	// loadTenantDashboardData above downgraded PlatformState to not-enrolled/
	// no-permission, since that is exactly the caller this status is for.
	loadTenantDashboardMyInviteRequest(requestCtx, resolution.client, &dashboard)
	return dashboard, nil
}

const tenantDashboardTimeout = 10 * time.Second

// Panel tab ids, shared with the frontend's tab strip.
const (
	tenantDashboardTabUsers        = "users"
	tenantDashboardTabReviews      = "reviews"
	tenantDashboardTabQueue        = "queue"
	tenantDashboardTabBuilds       = "builds"
	tenantDashboardTabAudit        = "audit"
	tenantDashboardTabRegistration = "registration"
	tenantDashboardTabRequests     = "requests"
)

// The API reads each panel is made of, in canonical route-template form — the
// same form the platform reports capabilities in.
const (
	tenantDashboardReadWhoami         = "GET /v1/whoami"
	tenantDashboardReadReviews        = "GET /v1/reviews"
	tenantDashboardReadReview         = "GET /v1/reviews/{review_id}"
	tenantDashboardReadMergeQueue     = "GET /v1/reviews/merge-queue"
	tenantDashboardReadBuilds         = "GET /v1/reviews/{review_id}/builds"
	tenantDashboardReadComments       = "GET /v1/reviews/{review_id}/comments"
	tenantDashboardWriteComment       = "POST /v1/reviews/{review_id}/comments"
	tenantDashboardReadReviewers      = "GET /v1/reviews/{review_id}/reviewers"
	tenantDashboardWriteReviewers     = "POST /v1/reviews/{review_id}/reviewers"
	tenantDashboardRemoveReviewers    = "DELETE /v1/reviews/{review_id}/reviewers/{user_id}"
	tenantDashboardReadAuditEvents    = "GET /v1/audit-events"
	tenantDashboardReadUsers          = "GET /v1/users"
	tenantDashboardReadContexts       = "GET /v1/contexts"
	tenantDashboardReadEnvironments   = "GET /v1/environments"
	tenantDashboardReadInviteRequests = "GET /v1/invite-requests"
	tenantDashboardWriteApproveInvite = "POST /v1/invite-requests/{invite_request_id}/approve"
	tenantDashboardWriteDeclineInvite = "POST /v1/invite-requests/{invite_request_id}/decline"
)

// loadTenantDashboardData resolves every panel independently. One panel the
// caller may not read, or one call that fails, must not blank the panels that
// worked — and a panel the caller may not read must not read as an empty one.
func loadTenantDashboardData(ctx context.Context, client *eruncommon.PlatformClient, dashboard *uiTenantDashboard, input uiTenantDashboardInput) {
	whoami, err := client.Whoami(ctx)
	if err != nil {
		// Identity is the dashboard's own precondition: without it there is no
		// capability set to gate the remaining panels honestly.
		dashboard.PlatformState, dashboard.APIError = tenantDashboardIdentityFailure(err)
		return
	}
	dashboard.User = &uiTenantDashboardUser{
		TenantID: whoami.TenantID,
		UserID:   whoami.UserID,
		Username: whoami.Username,
		Roles:    whoami.Roles,
		Issuer:   whoami.Issuer,
		Subject:  whoami.Subject,
	}
	dashboard.Panels = []uiTenantDashboardPanel{{Tab: tenantDashboardTabUsers}}
	capabilities := whoami.Capabilities
	usernames := tenantDashboardUsernames(ctx, client, capabilities)
	reviewFilter := eruncommon.PlatformReviewFilter{}
	if input.ReviewFilterMine {
		reviewFilter.AuthorUserID = whoami.UserID
	}
	if input.ReviewFilterWaitingOnMe {
		reviewFilter.ReviewerUserID = whoami.UserID
	}
	reviewsOutcome := loadTenantDashboardReviews(ctx, client, capabilities, dashboard, reviewFilter, usernames)
	loadTenantDashboardMergeQueue(ctx, client, capabilities, dashboard, usernames)
	loadTenantDashboardBuilds(ctx, client, capabilities, dashboard, reviewsOutcome)
	loadTenantDashboardReviewThreadCounts(ctx, client, capabilities, dashboard, reviewsOutcome)
	loadTenantDashboardReviewFilterCounts(ctx, client, capabilities, dashboard, whoami.UserID)
	loadTenantDashboardAuditEvents(ctx, client, capabilities, dashboard)
	loadTenantDashboardRegistration(ctx, client, capabilities, dashboard)
	loadTenantDashboardInviteRequests(ctx, client, capabilities, dashboard)
	dashboard.CanCreateReview = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteCreateReview) == ""
	dashboard.CanAdvanceMergeQueue = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteAdvanceMergeQueue) == ""
	dashboard.CanOverrideMergeQueue = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteOverrideAdvanceMergeQueue) == ""
	dashboard.CanApproveInviteRequests = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteApproveInvite) == ""
	dashboard.CanDeclineInviteRequests = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteDeclineInvite) == ""
}

// loadTenantDashboardInviteRequests loads the operator/admin queue: every
// pending request the caller may see (their own tenant's JOIN_TENANT
// requests, or every request for an operations-scoped caller). Degrades like
// every other panel here — a caller who cannot read the queue gets a named
// restriction, never a false "nothing pending".
func loadTenantDashboardInviteRequests(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabRequests}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadInviteRequests); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	requests, err := client.ListInviteRequests(ctx, eruncommon.PlatformListInviteRequestsParams{
		Status: eruncommon.PlatformInviteRequestStatusPending,
	})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadInviteRequests, err)
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	dashboard.InviteRequests = make([]uiInviteRequest, 0, len(requests))
	for _, request := range requests {
		dashboard.InviteRequests = append(dashboard.InviteRequests, inviteRequestToUI(request))
	}
	count := len(dashboard.InviteRequests)
	dashboard.PendingInviteRequestCount = &count
	dashboard.Panels = append(dashboard.Panels, panel)
}

// tenantDashboardUsernames resolves every tenant user id to its display
// username, best effort: a caller who cannot read /v1/users, or a read that
// fails, gets back an empty map rather than failing the dashboard load — every
// caller of the map already falls back to the raw id it was given (#1378).
func tenantDashboardUsernames(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities) map[string]string {
	names := make(map[string]string)
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadUsers) != "" {
		return names
	}
	users, err := client.ListUsers(ctx, eruncommon.PlatformListUsersParams{})
	if err != nil {
		return names
	}
	for _, user := range users {
		userID := strings.TrimSpace(user.UserID)
		username := strings.TrimSpace(user.Username)
		if userID == "" || username == "" {
			continue
		}
		names[userID] = username
	}
	return names
}

// loadTenantDashboardReviewFilterCounts reports how many reviews are Mine and
// how many are Waiting on me, independent of whichever filter the caller
// currently has applied: the Reviews tab's filter buttons need the
// distribution visible before the caller clicks either one (#1378), not only
// after. Best effort like the other row-enrichment reads: a restricted or
// failing count simply leaves the corresponding field unset rather than
// reporting a false zero.
func loadTenantDashboardReviewFilterCounts(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard, userID string) {
	if strings.TrimSpace(userID) == "" || restrictedTenantDashboardRead(capabilities, tenantDashboardReadReviews) != "" {
		return
	}
	if mine, err := client.ListReviews(ctx, eruncommon.PlatformReviewFilter{AuthorUserID: userID}); err == nil {
		count := len(mine)
		dashboard.MineReviewCount = &count
	}
	if waiting, err := client.ListReviews(ctx, eruncommon.PlatformReviewFilter{ReviewerUserID: userID}); err == nil {
		count := len(waiting)
		dashboard.WaitingOnMeReviewCount = &count
	}
}

// reviewsLoadOutcome carries the Reviews panel's own result forward to the
// Builds panel, which needs the same review list but must not blank on a
// restriction or failure the Reviews panel already reported for a different
// reason than its own.
type reviewsLoadOutcome struct {
	reviews    []eruncommon.PlatformReview
	err        error
	restricted string
}

func loadTenantDashboardReviews(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard, filter eruncommon.PlatformReviewFilter, usernames map[string]string) reviewsLoadOutcome {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabReviews}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadReviews); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return reviewsLoadOutcome{restricted: restricted}
	}
	reviews, err := client.ListReviews(ctx, filter)
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadReviews, err)
		dashboard.Panels = append(dashboard.Panels, panel)
		return reviewsLoadOutcome{err: err}
	}
	dashboard.Reviews = tenantDashboardReviews(reviews, usernames)
	dashboard.Panels = append(dashboard.Panels, panel)
	return reviewsLoadOutcome{reviews: reviews}
}

func loadTenantDashboardMergeQueue(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard, usernames map[string]string) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabQueue}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadMergeQueue); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	// The merge queue is per target branch; the dashboard shows the default one
	// the API picks for an unspecified branch, as it always has.
	reviews, err := client.ListMergeQueue(ctx, "")
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadMergeQueue, err)
	} else {
		dashboard.MergeQueue = tenantDashboardReviews(reviews, usernames)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

// loadTenantDashboardBuilds reuses the review list the Reviews panel already
// fetched rather than re-listing it, and reads each review's builds
// concurrently: a tenant with many reviews used to pay one round trip per
// review under this panel's own share of the whole dashboard's single
// request timeout, so a big tenant read out as a timed-out API error instead
// of a slow-but-successful load.
func loadTenantDashboardBuilds(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard, reviewsOutcome reviewsLoadOutcome) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabBuilds}
	switch {
	case reviewsOutcome.restricted != "":
		// Builds are read per review, so this panel needs the review list too.
		panel.Restricted = reviewsOutcome.restricted
	case reviewsOutcome.err != nil:
		panel.Error = tenantDashboardReadError(tenantDashboardReadReviews, reviewsOutcome.err)
	default:
		if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadBuilds); restricted != "" {
			panel.Restricted = restricted
		} else {
			builds, err := fetchReviewBuildsConcurrently(ctx, client, reviewsOutcome.reviews)
			if err != nil {
				panel.Error = tenantDashboardReadError(tenantDashboardReadBuilds, err)
			}
			dashboard.Builds = builds
		}
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

// loadTenantDashboardReviewThreadCounts enriches each review row with its
// unresolved-thread count, so "is this review actually finished" is readable
// from the list itself, not only inside the detail dialog. A review is left
// without a count (rather than a false zero) when the caller cannot read
// comments at all, or when its own comment read failed — supplemental detail
// on a row, not worth a panel error the way the Reviews/Builds panels get.
func loadTenantDashboardReviewThreadCounts(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard, reviewsOutcome reviewsLoadOutcome) {
	if reviewsOutcome.restricted != "" || reviewsOutcome.err != nil {
		return
	}
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadComments) != "" {
		return
	}
	counts := fetchReviewThreadCountsConcurrently(ctx, client, reviewsOutcome.reviews)
	for i := range dashboard.Reviews {
		count, ok := counts[dashboard.Reviews[i].ReviewID]
		if !ok {
			continue
		}
		dashboard.Reviews[i].UnresolvedThreads = &count
	}
}

// maxConcurrentReviewCommentReads mirrors maxConcurrentReviewBuildReads for
// the same reason: bound the per-review /comments reads a big tenant's
// listing pays for, inside the one dashboard load's shared timeout.
const maxConcurrentReviewCommentReads = 8

// fetchReviewThreadCountsConcurrently reads every review's comments in
// parallel, bounded by maxConcurrentReviewCommentReads, mirroring
// fetchReviewBuildsConcurrently's shape. A review whose comment read fails is
// simply absent from the returned map rather than reported as zero.
func fetchReviewThreadCountsConcurrently(ctx context.Context, client *eruncommon.PlatformClient, reviews []eruncommon.PlatformReview) map[string]int {
	counts := make(map[string]int, len(reviews))
	semaphore := make(chan struct{}, maxConcurrentReviewCommentReads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, review := range reviews {
		reviewID := strings.TrimSpace(review.ReviewID)
		if reviewID == "" {
			continue
		}
		wg.Add(1)
		semaphore <- struct{}{}
		go func(reviewID string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			comments, err := client.ListComments(ctx, reviewID)
			if err != nil {
				return
			}
			count := eruncommon.CountUnresolvedThreads(comments)
			mu.Lock()
			counts[reviewID] = count
			mu.Unlock()
		}(reviewID)
	}
	wg.Wait()
	return counts
}

// maxConcurrentReviewBuildReads bounds how many /builds requests run at once,
// so a tenant with many reviews still fits comfortably inside one request's
// share of tenantDashboardTimeout instead of paying N sequential round trips.
const maxConcurrentReviewBuildReads = 8

// fetchReviewBuildsConcurrently reads every review's builds in parallel,
// bounded by maxConcurrentReviewBuildReads, and flattens the results in the
// same review order the old serial loop used to produce. Every read runs to
// completion regardless of another review's failure — the old serial loop
// dropped every review's builds *after* the point it broke on an error, an
// order-dependent partial result. This keeps every review that did succeed
// and still reports the first failure, which is both more complete and no
// longer dependent on where in the list the failure happened to land.
func fetchReviewBuildsConcurrently(ctx context.Context, client *eruncommon.PlatformClient, reviews []eruncommon.PlatformReview) ([]uiTenantDashboardBuild, error) {
	perReview := make([][]eruncommon.PlatformBuild, len(reviews))
	semaphore := make(chan struct{}, maxConcurrentReviewBuildReads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, review := range reviews {
		reviewID := strings.TrimSpace(review.ReviewID)
		if reviewID == "" {
			continue
		}
		wg.Add(1)
		semaphore <- struct{}{}
		go func(index int, reviewID string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			reviewBuilds, err := client.ListBuilds(ctx, reviewID)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			perReview[index] = reviewBuilds
		}(i, reviewID)
	}
	wg.Wait()

	builds := make([]uiTenantDashboardBuild, 0, len(reviews))
	for i, review := range reviews {
		for _, build := range perReview[i] {
			builds = append(builds, uiTenantDashboardBuild{
				BuildID:    build.BuildID,
				TenantID:   build.TenantID,
				ReviewID:   build.ReviewID,
				ReviewName: strings.TrimSpace(review.Name),
				Successful: build.Successful,
				CommitID:   build.CommitID,
				Version:    build.Version,
				CreatedAt:  tenantDashboardTime(build.CreatedAt),
				UpdatedAt:  tenantDashboardTime(build.UpdatedAt),
			})
		}
	}
	return builds, firstErr
}

func loadTenantDashboardAuditEvents(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabAudit}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadAuditEvents); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	page, err := client.ListAuditEvents(ctx, eruncommon.PlatformAuditEventFilter{})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadAuditEvents, err)
	} else {
		dashboard.AuditEvents = tenantDashboardAuditEvents(page.Events)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

// restrictedTenantDashboardRead reports the read the caller lacks, or "" when
// the panel may be attempted. A platform that reported no capability set at all
// leaves every read attemptable: the call then reports its own refusal, which is
// still better than hiding a surface the caller can use.
func restrictedTenantDashboardRead(capabilities eruncommon.PlatformCapabilities, read string) string {
	if !capabilities.Known() {
		return ""
	}
	method, apiPath, found := strings.Cut(read, " ")
	if !found || capabilities.Allows(method, apiPath) {
		return ""
	}
	return read
}

func tenantDashboardReadError(read string, err error) string {
	return fmt.Sprintf("load tenant dashboard %s: %v", read, err)
}

// tenantDashboardIdentityFailure classifies a refused identity read into the
// state that names the operator's actual next action, plus the message the
// dashboard shows before that action. By the time this runs, the bearer
// itself minted successfully (resolveTenantPlatform already reports
// tenantPlatformStateNotSignedIn when it does not) — so a 401 here is the
// platform's own auth middleware rejecting a token whose subject it does not
// recognize, i.e. an identity that is not enrolled in this tenant, never a
// stale session signing in again could fix. A 403 means the identity is
// enrolled but the whoami read itself is refused, which happens only for a
// caller with no permissions at all.
func tenantDashboardIdentityFailure(err error) (state, message string) {
	switch {
	case errors.Is(err, eruncommon.ErrPlatformForbidden):
		return tenantPlatformStateNoPermission, "You do not have access to this tenant's dashboard. Ask an administrator for access."
	case errors.Is(err, eruncommon.ErrPlatformUnauthorized):
		return tenantPlatformStateNotEnrolled, "Your signed-in identity is not enrolled in this tenant yet. Ask an administrator to enroll it, or request access below."
	default:
		return "", tenantDashboardReadError(tenantDashboardReadWhoami, err)
	}
}

func tenantDashboardReviews(reviews []eruncommon.PlatformReview, usernames map[string]string) []uiTenantDashboardReview {
	converted := make([]uiTenantDashboardReview, 0, len(reviews))
	for _, review := range reviews {
		converted = append(converted, tenantDashboardReviewWithUsername(review, usernames))
	}
	return converted
}

// tenantDashboardReviewWithUsername mirrors tenantDashboardCommentWithUsername:
// the read paths (listings, single-review load) have a resolved user
// directory to enrich with; the write paths in tenant_review_write.go return
// the review the caller themselves just acted on and use the base converter
// directly, unresolved (#1378).
func tenantDashboardReviewWithUsername(review eruncommon.PlatformReview, usernames map[string]string) uiTenantDashboardReview {
	converted := tenantDashboardReview(review)
	converted.AuthorUsername = usernames[review.AuthorUserID]
	return converted
}

func tenantDashboardReview(review eruncommon.PlatformReview) uiTenantDashboardReview {
	return uiTenantDashboardReview{
		ReviewID:          review.ReviewID,
		TenantID:          review.TenantID,
		AuthorUserID:      review.AuthorUserID,
		Name:              review.Name,
		TargetBranch:      review.TargetBranch,
		SourceBranch:      review.SourceBranch,
		Status:            review.Status,
		LastFailedBuildID: review.LastFailedBuildID,
		LastReadyBuildID:  review.LastReadyBuildID,
		LastMergedBuildID: review.LastMergedBuildID,
		CreatedAt:         tenantDashboardTime(review.CreatedAt),
		UpdatedAt:         tenantDashboardTime(review.UpdatedAt),
	}
}

func tenantDashboardAuditEvents(events []eruncommon.PlatformAuditEvent) []uiTenantDashboardAudit {
	converted := make([]uiTenantDashboardAudit, 0, len(events))
	for _, event := range events {
		converted = append(converted, uiTenantDashboardAudit{
			Type:      event.Type,
			Actor:     event.ExternalUserID,
			Action:    tenantDashboardAuditAction(event),
			CreatedAt: tenantDashboardTime(event.CreatedAt),
		})
	}
	return converted
}

func tenantDashboardAuditAction(event eruncommon.PlatformAuditEvent) string {
	switch event.Type {
	case "API":
		return strings.TrimSpace(event.APIMethod + " " + event.APIPath)
	case "CLI":
		return event.CLICommand
	case "MCP":
		return event.MCPTool
	default:
		return ""
	}
}

func tenantDashboardTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (a *App) tenantDashboardUsernameHint(alias string) string {
	provider, err := eruncommon.ResolveCloudProvider(a.deps.store, strings.TrimSpace(alias))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(provider.Username)
}
