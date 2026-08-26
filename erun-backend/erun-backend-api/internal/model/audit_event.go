package model

import (
	"time"

	"github.com/uptrace/bun"
)

type AuditEventType string

const (
	AuditEventTypeAPI AuditEventType = "API"
	AuditEventTypeMCP AuditEventType = "MCP"
	AuditEventTypeCLI AuditEventType = "CLI"
)

type AuditEvent struct {
	bun.BaseModel `bun:"table:audit_events,alias:ae"`
	// AuditEventID is populated only by List; the write path inserts by raw SQL
	// and never reads it back.
	AuditEventID     string `json:"auditEventId,omitempty" bun:"audit_event_id,pk,scanonly"`
	TenantID         string `json:"tenantId" bun:"tenant_id,scanonly"`
	ErunUserID       string `json:"erunUserId" bun:"erun_user_id,scanonly"`
	ExternalUserID   string `json:"externalUserId" bun:"external_user_id,scanonly"`
	ExternalIssuerID string `json:"externalIssuerId" bun:"external_issuer_id,scanonly"`
	// ExternalOrgID is the org claim value that resolved the tenant for an
	// org-scoped issuer; empty/omitted for single-tenant issuers.
	ExternalOrgID string         `json:"externalOrgId,omitempty" bun:"external_org_id,scanonly"`
	Type          AuditEventType `json:"type" bun:"type,scanonly"`
	APIMethod     string         `json:"apiMethod,omitempty" bun:"api_method,scanonly"`
	APIPath       string         `json:"apiPath,omitempty" bun:"api_path,scanonly"`
	CLICommand    string         `json:"cliCommand,omitempty" bun:"cli_command,scanonly"`
	// CLIParameters, MCPToolParameters, and APIParameters are write-only and
	// deliberately never tagged for Bun to populate: a tool such as
	// cloud_inject_aws_credentials takes credentials as arguments, and an API
	// caller's request body can carry secrets the same way, so this is where
	// that kind of caller-supplied text would be serialized (a merge-queue
	// override's reason, for example). The read API must not become the way
	// those secrets leak back out, so no query against this model may select
	// any of the three, ever.
	CLIParameters     string    `json:"-" bun:"-"`
	MCPTool           string    `json:"mcpTool,omitempty" bun:"mcp_tool,scanonly"`
	MCPToolParameters string    `json:"-" bun:"-"`
	APIParameters     string    `json:"-" bun:"-"`
	CreatedAt         time.Time `json:"createdAt" bun:"created_at,scanonly"`
}
