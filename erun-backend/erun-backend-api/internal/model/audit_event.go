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
	// AuditEventID is populated only by List.
	AuditEventID     string `json:"auditEventId,omitempty" bun:"audit_event_id,pk,scanonly"`
	TenantID         string `json:"tenantId" bun:"tenant_id,scanonly"`
	ErunUserID       string `json:"erunUserId" bun:"erun_user_id,scanonly"`
	ExternalUserID   string `json:"externalUserId" bun:"external_user_id,scanonly"`
	ExternalIssuerID string `json:"externalIssuerId" bun:"external_issuer_id,scanonly"`
	// ExternalOrgID is the org claim that resolved the tenant; empty for single-tenant issuers.
	ExternalOrgID string         `json:"externalOrgId,omitempty" bun:"external_org_id,scanonly"`
	Type          AuditEventType `json:"type" bun:"type,scanonly"`
	APIMethod     string         `json:"apiMethod,omitempty" bun:"api_method,scanonly"`
	APIPath       string         `json:"apiPath,omitempty" bun:"api_path,scanonly"`
	CLICommand    string         `json:"cliCommand,omitempty" bun:"cli_command,scanonly"`
	// These parameters can carry caller-supplied secrets, so they are write-only:
	// no query may ever select them.
	CLIParameters     string    `json:"-" bun:"-"`
	MCPTool           string    `json:"mcpTool,omitempty" bun:"mcp_tool,scanonly"`
	MCPToolParameters string    `json:"-" bun:"-"`
	APIParameters     string    `json:"-" bun:"-"`
	CreatedAt         time.Time `json:"createdAt" bun:"created_at,scanonly"`
}
