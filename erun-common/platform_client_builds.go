package eruncommon

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// platform_client_builds.go extends PlatformClient with the tenant-wide
// build history (erun#1954): GET/POST /v1/builds, the same resource
// platform_client_reviews.go's review-nested ListBuilds/CreateBuild cover,
// but for a build with no review attached.

// PlatformCreateUnattachedBuildParams is the input for a build with no
// review -- an ordinary `erun build` self-reporting. EnvironmentID is
// required; Kind is always RECORDED, since a GATE build always gates a
// review's merge.
type PlatformCreateUnattachedBuildParams struct {
	EnvironmentID string `json:"environmentId"`
	CommitID      string `json:"commitId"`
	Version       string `json:"version"`
	Successful    bool   `json:"successful"`
	FailureDetail string `json:"failureDetail,omitempty"`
}

// CreateUnattachedBuild records a build against an environment with no
// review. See PlatformClient.CreateBuild for the review-linked counterpart.
func (c *PlatformClient) CreateUnattachedBuild(ctx context.Context, params PlatformCreateUnattachedBuildParams) (PlatformBuild, error) {
	var build PlatformBuild
	err := c.do(ctx, http.MethodPost, "/v1/builds", params, true, &build)
	return build, err
}

// PlatformBuildListFilter narrows a page of GET /v1/builds -- the tenant-wide
// build history, review-linked and unattached alike.
type PlatformBuildListFilter struct {
	EnvironmentID string
	Kind          string
	// Successful is nil for "either".
	Successful *bool
	Since      time.Time
	Until      time.Time
	Cursor     string
	// Limit caps the page size; the server applies its own default/max when
	// zero or out of range.
	Limit int
}

func (f PlatformBuildListFilter) queryString() string {
	values := url.Values{}
	if strings.TrimSpace(f.EnvironmentID) != "" {
		values.Set("environmentId", f.EnvironmentID)
	}
	if strings.TrimSpace(f.Kind) != "" {
		values.Set("kind", f.Kind)
	}
	if f.Successful != nil {
		values.Set("successful", strconv.FormatBool(*f.Successful))
	}
	if !f.Since.IsZero() {
		values.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		values.Set("until", f.Until.UTC().Format(time.RFC3339))
	}
	if f.Cursor != "" {
		values.Set("cursor", f.Cursor)
	}
	if f.Limit > 0 {
		values.Set("limit", strconv.Itoa(f.Limit))
	}
	return values.Encode()
}

// PlatformBuildPage is one newest-first page of a tenant's build history.
// NextCursor is empty when the page reached the end of the history.
type PlatformBuildPage struct {
	Builds     []PlatformBuild `json:"builds"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// ListAllBuilds lists the caller's tenant's whole build history, newest
// first, narrowed by filter -- unlike ListBuilds, which is scoped to one
// review.
func (c *PlatformClient) ListAllBuilds(ctx context.Context, filter PlatformBuildListFilter) (PlatformBuildPage, error) {
	path := "/v1/builds"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var page PlatformBuildPage
	err := c.do(ctx, http.MethodGet, path, nil, true, &page)
	return page, err
}
