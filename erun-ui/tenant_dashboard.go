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
	tenant := strings.TrimSpace(input.Tenant)
	if tenant == "" {
		return uiTenantDashboard{}, fmt.Errorf("tenant is required")
	}
	apiURL := strings.TrimSpace(input.APIURL)
	if apiURL == "" {
		return uiTenantDashboard{}, fmt.Errorf("tenant API URL is required")
	}
	dashboard := uiTenantDashboard{
		Tenant:      tenant,
		Environment: strings.TrimSpace(input.Environment),
		APIURL:      apiURL,
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
	alias := strings.TrimSpace(input.CloudProviderAlias)
	if alias == "" {
		return uiTenantDashboard{}, fmt.Errorf("tenant primary cloud alias is required")
	}
	client, requestCtx, cancel, err := a.tenantDashboardBearerClient(ctx, apiURL, alias)
	if err != nil {
		dashboard.APIError = err.Error()
		return dashboard, nil
	}
	defer cancel()
	loadTenantDashboardData(requestCtx, client, &dashboard, input)
	return dashboard, nil
}

// tenantDashboardBearerClient mints a bearer token for cloudProviderAlias and
// wraps it in the shared platform client every dashboard-backed read or write
// uses, so wire shapes, error mapping, and header handling cannot drift from
// the CLI's. cloudProviderAlias must already be validated non-empty by the
// caller; a failure here is a runtime condition (bad or expired credentials),
// not a caller wiring error, so every caller surfaces it as its own error
// field rather than treating it as a Wails-level failure.
func (a *App) tenantDashboardBearerClient(ctx context.Context, apiURL, cloudProviderAlias string) (*eruncommon.PlatformClient, context.Context, context.CancelFunc, error) {
	token, err := eruncommon.CloudProviderBearerToken(eruncommon.Context{}, a.deps.store, eruncommon.CloudBearerParams{Alias: cloudProviderAlias}, a.deps.cloudDeps)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get cloud bearer token: %w", err)
	}
	bearer := strings.TrimSpace(token.Token)
	if bearer == "" {
		return nil, nil, nil, fmt.Errorf("get cloud bearer token: empty token")
	}
	client := eruncommon.NewPlatformClient(apiURL, func() (string, error) { return bearer, nil }).
		WithUsernameHint(a.tenantDashboardUsernameHint(token.Alias))
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	return client, requestCtx, cancel, nil
}

const tenantDashboardTimeout = 10 * time.Second

// Panel tab ids, shared with the frontend's tab strip.
const (
	tenantDashboardTabUsers   = "users"
	tenantDashboardTabReviews = "reviews"
	tenantDashboardTabQueue   = "queue"
	tenantDashboardTabBuilds  = "builds"
	tenantDashboardTabAudit   = "audit"
)

// The API reads each panel is made of, in canonical route-template form — the
// same form the platform reports capabilities in.
const (
	tenantDashboardReadWhoami      = "GET /v1/whoami"
	tenantDashboardReadReviews     = "GET /v1/reviews"
	tenantDashboardReadReview      = "GET /v1/reviews/{review_id}"
	tenantDashboardReadMergeQueue  = "GET /v1/reviews/merge-queue"
	tenantDashboardReadBuilds      = "GET /v1/reviews/{review_id}/builds"
	tenantDashboardReadComments    = "GET /v1/reviews/{review_id}/comments"
	tenantDashboardWriteComment    = "POST /v1/reviews/{review_id}/comments"
	tenantDashboardReadAuditEvents = "GET /v1/audit-events"
)

// loadTenantDashboardData resolves every panel independently. One panel the
// caller may not read, or one call that fails, must not blank the panels that
// worked — and a panel the caller may not read must not read as an empty one.
func loadTenantDashboardData(ctx context.Context, client *eruncommon.PlatformClient, dashboard *uiTenantDashboard, input uiTenantDashboardInput) {
	whoami, err := client.Whoami(ctx)
	if err != nil {
		// Identity is the dashboard's own precondition: without it there is no
		// capability set to gate the remaining panels honestly.
		dashboard.APIError = tenantDashboardIdentityError(err)
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
	reviewFilter := eruncommon.PlatformReviewFilter{}
	if input.ReviewFilterMine {
		reviewFilter.AuthorUserID = whoami.UserID
	}
	if input.ReviewFilterWaitingOnMe {
		reviewFilter.ReviewerUserID = whoami.UserID
	}
	reviewsOutcome := loadTenantDashboardReviews(ctx, client, capabilities, dashboard, reviewFilter)
	loadTenantDashboardMergeQueue(ctx, client, capabilities, dashboard)
	loadTenantDashboardBuilds(ctx, client, capabilities, dashboard, reviewsOutcome)
	loadTenantDashboardReviewThreadCounts(ctx, client, capabilities, dashboard, reviewsOutcome)
	loadTenantDashboardAuditEvents(ctx, client, capabilities, dashboard)
	dashboard.CanCreateReview = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteCreateReview) == ""
	dashboard.CanAdvanceMergeQueue = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteAdvanceMergeQueue) == ""
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

func loadTenantDashboardReviews(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard, filter eruncommon.PlatformReviewFilter) reviewsLoadOutcome {
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
	dashboard.Reviews = tenantDashboardReviews(reviews)
	dashboard.Panels = append(dashboard.Panels, panel)
	return reviewsLoadOutcome{reviews: reviews}
}

func loadTenantDashboardMergeQueue(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
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
		dashboard.MergeQueue = tenantDashboardReviews(reviews)
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

// tenantDashboardIdentityError says what a refused identity read means for the
// operator. The identity read is authorized like any other route, so a user with
// no permissions at all is refused it — and "http 403: Forbidden" is the state
// of their access, not a fault they can act on as written.
func tenantDashboardIdentityError(err error) string {
	switch {
	case errors.Is(err, eruncommon.ErrPlatformForbidden):
		return "You do not have access to this tenant's dashboard. Ask an administrator for access."
	case errors.Is(err, eruncommon.ErrPlatformUnauthorized):
		return "This tenant's platform did not accept the signed-in identity. Sign in to the tenant's cloud provider again."
	default:
		return tenantDashboardReadError(tenantDashboardReadWhoami, err)
	}
}

func tenantDashboardReviews(reviews []eruncommon.PlatformReview) []uiTenantDashboardReview {
	converted := make([]uiTenantDashboardReview, 0, len(reviews))
	for _, review := range reviews {
		converted = append(converted, tenantDashboardReview(review))
	}
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
