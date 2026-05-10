package model

import (
	"time"
)

type AuditEventType string

const (
	AuditEventTypeAPI AuditEventType = "API"
	AuditEventTypeMCP AuditEventType = "MCP"
	AuditEventTypeCLI AuditEventType = "CLI"
)

type AuditEvent struct {
	TenantID          string         `json:"tenantId"`
	ErunUserID        string         `json:"erunUserId"`
	ExternalUserID    string         `json:"externalUserId"`
	ExternalIssuerID  string         `json:"externalIssuerId"`
	Type              AuditEventType `json:"type"`
	APIMethod         string         `json:"apiMethod,omitempty"`
	APIPath           string         `json:"apiPath,omitempty"`
	CLICommand        string         `json:"cliCommand,omitempty"`
	CLIParameters     string         `json:"cliParameters,omitempty"`
	MCPTool           string         `json:"mcpTool,omitempty"`
	MCPToolParameters string         `json:"mcpToolParameters,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
}
