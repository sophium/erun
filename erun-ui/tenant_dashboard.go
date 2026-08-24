package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	token, err := eruncommon.CloudProviderBearerToken(eruncommon.Context{}, a.deps.store, eruncommon.CloudBearerParams{Alias: alias}, a.deps.cloudDeps)
	if err != nil {
		dashboard.APIError = fmt.Sprintf("get cloud bearer token: %v", err)
		return dashboard, nil
	}
	bearer := strings.TrimSpace(token.Token)
	if bearer == "" {
		dashboard.APIError = "get cloud bearer token: empty token"
		return dashboard, nil
	}
	// The dashboard reaches the hosted platform through the shared client every
	// other transport uses, so its wire shapes, error mapping, and header
	// handling cannot drift from the CLI's.
	client := eruncommon.NewPlatformClient(apiURL, func() (string, error) { return bearer, nil }).
		WithUsernameHint(a.tenantDashboardUsernameHint(token.Alias))
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	defer cancel()
	loadTenantDashboardData(requestCtx, client, &dashboard)
	return dashboard, nil
}

const tenantDashboardTimeout = 10 * time.Second

// Panel tab ids, shared with the frontend's tab strip.
const (
	tenantDashboardTabUsers  = "users"
	tenantDashboardTabQueue  = "queue"
	tenantDashboardTabBuilds = "builds"
	tenantDashboardTabAudit  = "audit"
)

// The API reads each panel is made of, in canonical route-template form — the
// same form the platform reports capabilities in.
const (
	tenantDashboardReadWhoami      = "GET /v1/whoami"
	tenantDashboardReadReviews     = "GET /v1/reviews"
	tenantDashboardReadMergeQueue  = "GET /v1/reviews/merge-queue"
	tenantDashboardReadBuilds      = "GET /v1/reviews/{review_id}/builds"
	tenantDashboardReadAuditEvents = "GET /v1/audit-events"
)

// loadTenantDashboardData resolves every panel independently. One panel the
// caller may not read, or one call that fails, must not blank the panels that
// worked — and a panel the caller may not read must not read as an empty one.
func loadTenantDashboardData(ctx context.Context, client *eruncommon.PlatformClient, dashboard *uiTenantDashboard) {
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
	loadTenantDashboardMergeQueue(ctx, client, capabilities, dashboard)
	loadTenantDashboardBuilds(ctx, client, capabilities, dashboard)
	loadTenantDashboardAuditEvents(ctx, client, capabilities, dashboard)
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

func loadTenantDashboardBuilds(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabBuilds}
	// Builds are read per review, so the panel needs both reads.
	for _, read := range []string{tenantDashboardReadReviews, tenantDashboardReadBuilds} {
		if restricted := restrictedTenantDashboardRead(capabilities, read); restricted != "" {
			panel.Restricted = restricted
			dashboard.Panels = append(dashboard.Panels, panel)
			return
		}
	}
	reviews, err := client.ListReviews(ctx, eruncommon.PlatformReviewFilter{})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadReviews, err)
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	dashboard.Reviews = tenantDashboardReviews(reviews)
	builds := make([]uiTenantDashboardBuild, 0)
	for _, review := range reviews {
		reviewID := strings.TrimSpace(review.ReviewID)
		if reviewID == "" {
			continue
		}
		reviewBuilds, err := client.ListBuilds(ctx, reviewID)
		if err != nil {
			panel.Error = tenantDashboardReadError(tenantDashboardReadBuilds, err)
			break
		}
		for _, build := range reviewBuilds {
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
	dashboard.Builds = builds
	dashboard.Panels = append(dashboard.Panels, panel)
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
		converted = append(converted, uiTenantDashboardReview{
			ReviewID:          review.ReviewID,
			TenantID:          review.TenantID,
			Name:              review.Name,
			TargetBranch:      review.TargetBranch,
			SourceBranch:      review.SourceBranch,
			Status:            review.Status,
			LastFailedBuildID: review.LastFailedBuildID,
			LastReadyBuildID:  review.LastReadyBuildID,
			LastMergedBuildID: review.LastMergedBuildID,
			CreatedAt:         tenantDashboardTime(review.CreatedAt),
			UpdatedAt:         tenantDashboardTime(review.UpdatedAt),
		})
	}
	return converted
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
