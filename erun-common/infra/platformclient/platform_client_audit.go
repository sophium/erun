package platformclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PlatformAuditEvent mirrors model.AuditEvent. Parameter payloads are never
// returned because some tools take credentials as arguments.
type PlatformAuditEvent struct {
	AuditEventID     string    `json:"auditEventId,omitempty"`
	TenantID         string    `json:"tenantId"`
	ErunUserID       string    `json:"erunUserId"`
	ExternalUserID   string    `json:"externalUserId"`
	ExternalIssuerID string    `json:"externalIssuerId"`
	ExternalOrgID    string    `json:"externalOrgId,omitempty"`
	Type             string    `json:"type"`
	APIMethod        string    `json:"apiMethod,omitempty"`
	APIPath          string    `json:"apiPath,omitempty"`
	CLICommand       string    `json:"cliCommand,omitempty"`
	MCPTool          string    `json:"mcpTool,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// PlatformAuditEventFilter narrows GET /v1/audit-events; Cursor is a prior NextCursor.
type PlatformAuditEventFilter struct {
	ErunUserID string
	Type       string
	APIMethod  string
	APIPath    string
	Limit      int
	Cursor     string
}

func (f PlatformAuditEventFilter) queryString() string {
	values := url.Values{}
	for key, value := range map[string]string{
		"erunUserId": f.ErunUserID,
		"type":       f.Type,
		"apiMethod":  f.APIMethod,
		"apiPath":    f.APIPath,
		"cursor":     f.Cursor,
	} {
		if strings.TrimSpace(value) != "" {
			values.Set(key, value)
		}
	}
	if f.Limit > 0 {
		values.Set("limit", strconv.Itoa(f.Limit))
	}
	return values.Encode()
}

// PlatformAuditEventPage is one audit page; NextCursor is empty on the last.
type PlatformAuditEventPage struct {
	Events     []PlatformAuditEvent `json:"events"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

// ListAuditEvents reads the caller's tenant audit trail, newest first.
func (c *PlatformClient) ListAuditEvents(ctx context.Context, filter PlatformAuditEventFilter) (PlatformAuditEventPage, error) {
	path := "/v1/audit-events"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var page PlatformAuditEventPage
	err := c.do(ctx, http.MethodGet, path, nil, true, &page)
	return page, err
}
