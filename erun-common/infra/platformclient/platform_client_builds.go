package platformclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// PlatformCreateUnattachedBuildParams records a build with no review; EnvironmentID is required.
type PlatformCreateUnattachedBuildParams struct {
	EnvironmentID string                    `json:"environmentId"`
	CommitID      string                    `json:"commitId"`
	Version       string                    `json:"version"`
	Successful    bool                      `json:"successful"`
	FailureDetail string                    `json:"failureDetail,omitempty"`
	Profile       *core.BuildProfileSummary `json:"profile,omitempty"`
}

// CreateUnattachedBuild records a build against an environment with no review.
func (c *PlatformClient) CreateUnattachedBuild(ctx context.Context, params PlatformCreateUnattachedBuildParams) (PlatformBuild, error) {
	var build PlatformBuild
	err := c.do(ctx, http.MethodPost, "/v1/builds", params, true, &build)
	return build, err
}

// PlatformBuildListFilter narrows GET /v1/builds, the tenant-wide history.
type PlatformBuildListFilter struct {
	EnvironmentID string
	Kind          string
	CommitID      string
	// Successful is nil for "either".
	Successful *bool
	Since      time.Time
	Until      time.Time
	Cursor     string
	// Limit zero or out of range uses the server's default.
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
	if strings.TrimSpace(f.CommitID) != "" {
		values.Set("commit", f.CommitID)
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

// PlatformBuildPage is one newest-first page; NextCursor is empty at the end.
type PlatformBuildPage struct {
	Builds     []PlatformBuild `json:"builds"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// ListAllBuilds lists the tenant's builds, newest first, across all reviews.
func (c *PlatformClient) ListAllBuilds(ctx context.Context, filter PlatformBuildListFilter) (PlatformBuildPage, error) {
	path := "/v1/builds"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var page PlatformBuildPage
	err := c.do(ctx, http.MethodGet, path, nil, true, &page)
	return page, err
}
