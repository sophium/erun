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
	bun.BaseModel     `bun:"table:audit_events,alias:ae"`
	TenantID          string         `json:"tenantId" bun:"tenant_id"`
	ErunUserID        string         `json:"erunUserId" bun:"erun_user_id"`
	ExternalUserID    string         `json:"externalUserId" bun:"external_user_id"`
	ExternalIssuerID  string         `json:"externalIssuerId" bun:"external_issuer_id"`
	Type              AuditEventType `json:"type" bun:"type"`
	APIMethod         string         `json:"apiMethod,omitempty" bun:"api_method,nullzero"`
	APIPath           string         `json:"apiPath,omitempty" bun:"api_path,nullzero"`
	CLICommand        string         `json:"cliCommand,omitempty" bun:"cli_command,nullzero"`
	CLIParameters     string         `json:"cliParameters,omitempty" bun:"cli_parameters,nullzero"`
	MCPTool           string         `json:"mcpTool,omitempty" bun:"mcp_tool,nullzero"`
	MCPToolParameters string         `json:"mcpToolParameters,omitempty" bun:"mcp_tool_parameters,nullzero"`
	CreatedAt         time.Time      `json:"createdAt" bun:"created_at"`
}
