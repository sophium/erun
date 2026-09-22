package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type AISessionsInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be inspected; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to inspect; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Session     string `json:"session,omitempty" jsonschema:"AI session id to resolve; omit to list every session recorded for the environment"`
}

// AISessionsResult echoes the resolved tenant/environment alongside the
// sessions, matching idle_stop_history's shape, so an empty Sessions list
// cannot be misread as answering for a different target than the caller
// intended.
type AISessionsResult struct {
	Tenant      string                       `json:"tenant"`
	Environment string                       `json:"environment"`
	Sessions    []eruncommon.AISessionStatus `json:"sessions"`
}

// aiSessionsTool is the read side of the structured AI-session status model:
// idle/busy/awaiting-input/exited/oom-killed resolved from each session's own
// last reported turn-boundary event, never from PTY output volume or silence.
// The write side (erun activity ai-session report) is what a tool's own hook
// invokes; this tool only reads what has already been reported.
func aiSessionsTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, AISessionsInput) (*mcp.CallToolResult, AISessionsResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input AISessionsInput) (*mcp.CallToolResult, AISessionsResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, AISessionsResult{}, err
		}
		if input.Session != "" {
			status, err := eruncommon.LoadAISessionStatus(tenant, environment, input.Session)
			if err != nil {
				return nil, AISessionsResult{}, err
			}
			return nil, AISessionsResult{Tenant: tenant, Environment: environment, Sessions: []eruncommon.AISessionStatus{status}}, nil
		}
		statuses, err := eruncommon.LoadAISessionStatuses(tenant, environment)
		if err != nil {
			return nil, AISessionsResult{}, err
		}
		return nil, AISessionsResult{Tenant: tenant, Environment: environment, Sessions: statuses}, nil
	}
}
