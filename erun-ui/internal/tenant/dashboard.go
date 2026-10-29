package tenant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) LoadTenantDashboard(input DashboardInput) (Dashboard, error) {
	tenant, err := uimodel.RequireTenant("loading the tenant dashboard", input.Tenant)
	if err != nil {
		return Dashboard{}, err
	}
	dashboard := Dashboard{
		Tenant:      tenant,
		Environment: strings.TrimSpace(input.Environment),
	}
	ctx := s.d.Context()
	if strings.TrimSpace(input.MCPURL) != "" || strings.TrimSpace(input.KubernetesContext) != "" {
		input.MCPBearer = s.d.MCPBearer(tenant, strings.TrimSpace(input.Environment))
		log, err := s.d.LoadAPILog(ctx, input)
		if err != nil {
			dashboard.APILogError = "Could not read this environment's API log. " + err.Error()
		} else {
			dashboard.APILog = log
		}
	}

	resolution, err := s.Platform(tenant, strings.TrimSpace(input.PlatformAlias))
	if err != nil {
		return Dashboard{}, err
	}
	dashboard.PlatformState = resolution.State
	dashboard.PlatformAliasChoices = resolution.AliasChoices
	dashboard.PlatformAlias = resolution.Alias
	dashboard.PlatformURL = resolution.APIURL
	dashboard.PlatformIssuer = resolution.Issuer
	dashboard.PlatformSubject = resolution.Subject
	if resolution.State != StateReady {
		return dashboard, nil
	}
	dashboard.APIURL = resolution.APIURL

	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	capabilities := loadTenantDashboardData(requestCtx, resolution.Client, &dashboard, input)
	if dashboard.PlatformState == tenantPlatformStateTenantMismatch {
		// Every remaining read would be the other tenant's rows.
		return dashboard, nil
	}
	// Needs only the bearer, not membership, so read it even for a not-enrolled caller.
	loadTenantDashboardMyInviteRequest(requestCtx, resolution.Client, &dashboard)
	loadTenantDashboardAccessRemedies(requestCtx, resolution.Client, capabilities, &dashboard)
	return dashboard, nil
}

func loadTenantDashboardAccessRemedies(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) {
	var userID string
	if dashboard.User != nil {
		userID = dashboard.User.UserID
	}
	reads := make([]string, 0, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		reads = append(reads, panel.Restricted)
	}
	dashboard.AccessRemedies = loadAccessRemedies(ctx, client, capabilities, userID, reads...)
}

const RequestTimeout = 10 * time.Second

// Panel tab ids, shared with the frontend's tab strip.
const (
	tenantDashboardTabUsers        = "users"
	tenantDashboardTabReviews      = "reviews"
	tenantDashboardTabQueue        = "queue"
	tenantDashboardTabPipeline     = "pipeline"
	tenantDashboardTabBuilds       = "builds"
	tenantDashboardTabAudit        = "audit"
	tenantDashboardTabRegistration = "registration"
	tenantDashboardTabRequests     = "requests"
)

// API reads per panel, in the route-template form the platform reports capabilities in.
const (
	tenantDashboardReadWhoami         = "GET /v1/whoami"
	ReadReviews                       = "GET /v1/reviews"
	tenantDashboardReadReview         = "GET /v1/reviews/{review_id}"
	ReadMergeQueue                    = "GET /v1/reviews/merge-queue"
	tenantDashboardReadPipeline       = "GET /v1/pipeline"
	tenantDashboardReadBuilds         = "GET /v1/reviews/{review_id}/builds"
	tenantDashboardReadAllBuilds      = "GET /v1/builds"
	ReadComments                      = "GET /v1/reviews/{review_id}/comments"
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

// loadTenantDashboardData resolves each panel independently; a restricted panel must not read as empty.
func loadTenantDashboardData(ctx context.Context, client *platformclient.PlatformClient, dashboard *Dashboard, input DashboardInput) model.PlatformCapabilities {
	whoami, err := client.Whoami(ctx)
	if err != nil {
		dashboard.PlatformState, dashboard.APIError = tenantDashboardIdentityFailure(err)
		return nil
	}
	dashboard.User = &DashboardUser{
		TenantID: whoami.TenantID,
		// The local tenant is a different namespace; only this says whose rows the panels show.
		TenantName: whoami.TenantName,
		UserID:     whoami.UserID,
		Username:   whoami.Username,
		Roles:      whoami.Roles,
		Issuer:     whoami.Issuer,
		Subject:    whoami.Subject,
	}
	// A different platform tenant would render its rows, and write controls, under the
	// local tenant's name, so refuse before reading. No reported name skips the check.
	if mismatched := tenantDashboardTenantMismatch(dashboard.Tenant, whoami.TenantName); mismatched != "" {
		dashboard.PlatformState = tenantPlatformStateTenantMismatch
		return nil
	}
	capabilities := whoami.Capabilities
	roster := loadTenantDashboardUsers(ctx, client, capabilities, dashboard)
	usernames := tenantDashboardUsernamesFromRoster(roster)
	reviewFilter := platformclient.PlatformReviewFilter{}
	if input.ReviewFilterMine {
		reviewFilter.AuthorUserID = whoami.UserID
	}
	if input.ReviewFilterWaitingOnMe {
		reviewFilter.ReviewerUserID = whoami.UserID
	}
	reviewsOutcome := loadTenantDashboardReviews(ctx, client, capabilities, dashboard, reviewFilter, usernames)
	loadTenantDashboardMergeQueue(ctx, client, capabilities, dashboard, usernames)
	loadTenantDashboardPipeline(ctx, client, capabilities, dashboard)
	loadTenantDashboardBuilds(ctx, client, capabilities, dashboard, reviewsOutcome)
	loadTenantDashboardReviewThreadCounts(ctx, client, capabilities, dashboard, reviewsOutcome)
	loadTenantDashboardReviewFilterCounts(ctx, client, capabilities, dashboard, whoami.UserID)
	loadTenantDashboardAuditEvents(ctx, client, capabilities, dashboard)
	loadTenantDashboardRegistration(ctx, client, capabilities, dashboard)
	loadTenantDashboardInviteRequests(ctx, client, capabilities, dashboard)
	dashboard.CanCreateReview = RestrictedRead(capabilities, tenantDashboardWriteCreateReview) == ""
	dashboard.CanAdvanceMergeQueue = RestrictedRead(capabilities, WriteAdvanceMergeQueue) == ""
	dashboard.CanOverrideMergeQueue = RestrictedRead(capabilities, tenantDashboardWriteOverrideAdvanceMergeQueue) == ""
	dashboard.CanApproveInviteRequests = RestrictedRead(capabilities, tenantDashboardWriteApproveInvite) == ""
	dashboard.CanDeclineInviteRequests = RestrictedRead(capabilities, tenantDashboardWriteDeclineInvite) == ""
	return capabilities
}

// tenantDashboardTenantMismatch returns the mismatching platform name, or "" when
// they match or either is empty. Compared case-insensitively.
func tenantDashboardTenantMismatch(localTenant, platformTenantName string) string {
	platformTenantName = strings.TrimSpace(platformTenantName)
	localTenant = strings.TrimSpace(localTenant)
	if platformTenantName == "" || localTenant == "" {
		return ""
	}
	if strings.EqualFold(localTenant, platformTenantName) {
		return ""
	}
	return platformTenantName
}

func loadTenantDashboardInviteRequests(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) {
	panel := DashboardPanel{Tab: tenantDashboardTabRequests}
	if restricted := RestrictedRead(capabilities, tenantDashboardReadInviteRequests); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	requests, err := client.ListInviteRequests(ctx, platformclient.PlatformListInviteRequestsParams{
		Status: platformclient.PlatformInviteRequestStatusPending,
	})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadInviteRequests, err)
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	dashboard.InviteRequests = make([]InviteRequest, 0, len(requests))
	for _, request := range requests {
		dashboard.InviteRequests = append(dashboard.InviteRequests, inviteRequestToUI(request))
	}
	count := len(dashboard.InviteRequests)
	dashboard.PendingInviteRequestCount = &count
	dashboard.Panels = append(dashboard.Panels, panel)
}

// loadTenantDashboardUsers also returns the roster for resolving review authors,
// so GET /v1/users is read once.
func loadTenantDashboardUsers(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) []model.PlatformUser {
	panel := DashboardPanel{Tab: tenantDashboardTabUsers}
	if restricted := RestrictedRead(capabilities, tenantDashboardReadUsers); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return nil
	}
	users, err := client.ListUsers(ctx, model.PlatformListUsersParams{})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadUsers, err)
		dashboard.Panels = append(dashboard.Panels, panel)
		return nil
	}
	selfUserID := ""
	if dashboard.User != nil {
		selfUserID = strings.TrimSpace(dashboard.User.UserID)
	}
	dashboard.Users = make([]DashboardUser, 0, len(users))
	for _, user := range users {
		row := DashboardUser{
			TenantID:  user.TenantID,
			UserID:    user.UserID,
			Username:  user.Username,
			Issuer:    user.Issuer,
			Subject:   user.Subject,
			CreatedAt: tenantDashboardTime(user.CreatedAt),
			UpdatedAt: tenantDashboardTime(user.UpdatedAt),
		}
		// The roster has no roles; only the caller's own row is filled in, from whoami.
		if selfUserID != "" && strings.TrimSpace(user.UserID) == selfUserID {
			row.Roles = dashboard.User.Roles
		}
		dashboard.Users = append(dashboard.Users, row)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
	return users
}

func tenantDashboardUsernamesFromRoster(users []model.PlatformUser) map[string]string {
	names := make(map[string]string)
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

// tenantDashboardUsernames is best-effort and returns an empty map on failure.
func tenantDashboardUsernames(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities) map[string]string {
	if RestrictedRead(capabilities, tenantDashboardReadUsers) != "" {
		return map[string]string{}
	}
	users, err := client.ListUsers(ctx, model.PlatformListUsersParams{})
	if err != nil {
		return map[string]string{}
	}
	return tenantDashboardUsernamesFromRoster(users)
}

func loadTenantDashboardMergeQueue(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard, usernames map[string]string) {
	panel := DashboardPanel{Tab: tenantDashboardTabQueue}
	if restricted := RestrictedRead(capabilities, ReadMergeQueue); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	// Shows every repository's queue; each row names its repository.
	reviews, err := client.ListMergeQueue(ctx, model.PlatformMergeQueueParams{})
	if err != nil {
		panel.Error = tenantDashboardReadError(ReadMergeQueue, err)
	} else {
		dashboard.MergeQueue = tenantDashboardReviews(reviews, usernames)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

// Builds are read concurrently per review to stay within the dashboard's single request timeout.
func loadTenantDashboardBuilds(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard, reviewsOutcome reviewsLoadOutcome) {
	panel := DashboardPanel{Tab: tenantDashboardTabBuilds}
	switch {
	case reviewsOutcome.restricted != "":
		panel.Restricted = reviewsOutcome.restricted
	case reviewsOutcome.err != nil:
		panel.Error = tenantDashboardReadError(ReadReviews, reviewsOutcome.err)
	default:
		if restricted := RestrictedRead(capabilities, tenantDashboardReadBuilds); restricted != "" {
			panel.Restricted = restricted
		} else {
			builds, err := fetchReviewBuildsConcurrently(ctx, client, reviewsOutcome.reviews)
			if err != nil {
				panel.Error = tenantDashboardReadError(tenantDashboardReadBuilds, err)
			}
			dashboard.Builds = builds
		}
	}
	appendUnattachedTenantDashboardBuilds(ctx, client, capabilities, dashboard, &panel)
	dashboard.Panels = append(dashboard.Panels, panel)
}

// appendUnattachedTenantDashboardBuilds merges no-review builds best-effort; it
// only sets the panel's restriction/error when the review-side read had none.
func appendUnattachedTenantDashboardBuilds(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard, panel *DashboardPanel) {
	if restricted := RestrictedRead(capabilities, tenantDashboardReadAllBuilds); restricted != "" {
		if panel.Restricted == "" && panel.Error == "" {
			panel.Restricted = restricted
		}
		return
	}
	page, err := client.ListAllBuilds(ctx, platformclient.PlatformBuildListFilter{})
	if err != nil {
		if panel.Restricted == "" && panel.Error == "" {
			panel.Error = tenantDashboardReadError(tenantDashboardReadAllBuilds, err)
		}
		return
	}
	for _, build := range page.Builds {
		if strings.TrimSpace(build.ReviewID) != "" {
			continue
		}
		dashboard.Builds = append(dashboard.Builds, DashboardBuild{
			BuildID:         build.BuildID,
			TenantID:        build.TenantID,
			EnvironmentID:   build.EnvironmentID,
			EnvironmentName: build.EnvironmentName,
			Successful:      build.Successful,
			CommitID:        build.CommitID,
			Version:         build.Version,
			Profile:         build.Profile,
			CreatedAt:       tenantDashboardTime(build.CreatedAt),
			UpdatedAt:       tenantDashboardTime(build.UpdatedAt),
		})
	}
	sort.Slice(dashboard.Builds, func(i, j int) bool {
		return dashboard.Builds[i].CreatedAt > dashboard.Builds[j].CreatedAt
	})
}

func loadTenantDashboardAuditEvents(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) {
	panel := DashboardPanel{Tab: tenantDashboardTabAudit}
	if restricted := RestrictedRead(capabilities, tenantDashboardReadAuditEvents); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	page, err := client.ListAuditEvents(ctx, platformclient.PlatformAuditEventFilter{})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadAuditEvents, err)
	} else {
		dashboard.AuditEvents = tenantDashboardAuditEvents(page.Events)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

// No reported capability set leaves every read attemptable.
func RestrictedRead(capabilities model.PlatformCapabilities, read string) string {
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

// A 401 here means the platform rejected a minted token. Only NOT_ENROLLED (or an
// older platform's code-less 401) means not enrolled; TENANT_UNRESOLVED and
// RESOLUTION_FAILED get the generic message. A 403 means enrolled with no permissions.
func tenantDashboardIdentityFailure(err error) (state, message string) {
	switch {
	case errors.Is(err, platformclient.ErrPlatformForbidden):
		return StateNoPermission, "You do not have access to this tenant's dashboard. Ask an administrator for access."
	case errors.Is(err, platformclient.ErrPlatformUnauthorized):
		switch platformclient.PlatformAuthErrorCode(err) {
		case "TENANT_UNRESOLVED", "RESOLUTION_FAILED":
			return "", tenantDashboardReadError(tenantDashboardReadWhoami, err)
		}
		return tenantPlatformStateNotEnrolled, "Your signed-in identity is not enrolled in this tenant yet. Ask an administrator to enroll it, or request access below."
	default:
		return "", tenantDashboardReadError(tenantDashboardReadWhoami, err)
	}
}

func tenantDashboardAuditEvents(events []platformclient.PlatformAuditEvent) []DashboardAudit {
	converted := make([]DashboardAudit, 0, len(events))
	for _, event := range events {
		converted = append(converted, DashboardAudit{
			Type:      event.Type,
			Actor:     event.ExternalUserID,
			Action:    tenantDashboardAuditAction(event),
			CreatedAt: tenantDashboardTime(event.CreatedAt),
		})
	}
	return converted
}

func tenantDashboardAuditAction(event platformclient.PlatformAuditEvent) string {
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

func (s *Service) tenantDashboardUsernameHint(alias string) string {
	provider, err := providerconfig.ResolveCloudProvider(s.d.Store, strings.TrimSpace(alias))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(provider.Username)
}
