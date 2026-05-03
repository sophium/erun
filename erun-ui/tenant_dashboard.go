package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
		Tenant:          tenant,
		APIURL:          apiURL,
		AuditLogMessage: "Audit log listing is not exposed by the ERun API yet.",
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(input.MCPURL) != "" || strings.TrimSpace(input.KubernetesContext) != "" {
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
	client := &http.Client{Timeout: 10 * time.Second}

	user, err := loadTenantDashboardJSON[uiTenantDashboardUser](ctx, client, apiURL, "/v1/whoami", bearer)
	if err != nil {
		dashboard.APIError = err.Error()
		return dashboard, nil
	}
	dashboard.User = &user
	reviews, err := loadTenantDashboardJSON[[]uiTenantDashboardReview](ctx, client, apiURL, "/v1/reviews", bearer)
	if err != nil {
		dashboard.APIError = err.Error()
		return dashboard, nil
	}
	dashboard.Reviews = reviews
	mergeQueue, err := loadTenantDashboardJSON[[]uiTenantDashboardReview](ctx, client, apiURL, "/v1/reviews/merge-queue", bearer)
	if err != nil {
		dashboard.APIError = err.Error()
		return dashboard, nil
	}
	dashboard.MergeQueue = mergeQueue
	builds, err := loadTenantDashboardBuilds(ctx, client, apiURL, bearer, reviews)
	if err != nil {
		dashboard.APIError = err.Error()
		return dashboard, nil
	}
	dashboard.Builds = builds
	return dashboard, nil
}

func loadTenantDashboardBuilds(ctx context.Context, client *http.Client, apiURL, bearer string, reviews []uiTenantDashboardReview) ([]uiTenantDashboardBuild, error) {
	builds := make([]uiTenantDashboardBuild, 0)
	reviewNames := make(map[string]string, len(reviews))
	for _, review := range reviews {
		reviewID := strings.TrimSpace(review.ReviewID)
		if reviewID == "" {
			continue
		}
		reviewNames[reviewID] = strings.TrimSpace(review.Name)
		reviewBuilds, err := loadTenantDashboardJSON[[]uiTenantDashboardBuild](ctx, client, apiURL, "/v1/reviews/"+url.PathEscape(reviewID)+"/builds", bearer)
		if err != nil {
			return nil, err
		}
		for _, build := range reviewBuilds {
			build.ReviewName = reviewNames[build.ReviewID]
			builds = append(builds, build)
		}
	}
	return builds, nil
}

func loadTenantDashboardJSON[T any](ctx context.Context, client *http.Client, apiURL, apiPath, bearer string) (T, error) {
	var result T
	endpoint, err := tenantDashboardURL(apiURL, apiPath)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("load tenant dashboard %s: %s", apiPath, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("parse tenant dashboard %s: %w", apiPath, err)
	}
	return result, nil
}

func tenantDashboardURL(apiURL, apiPath string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil {
		return "", err
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("tenant API URL is invalid: %s", apiURL)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(apiPath, "/")
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}
