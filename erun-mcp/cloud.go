package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type CloudListInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type CloudInitAWSInput struct {
	SSOStartURL string `json:"ssoStartUrl" jsonschema:"AWS IAM Identity Center start URL"`
	SSORegion   string `json:"ssoRegion" jsonschema:"AWS IAM Identity Center region"`
	AccountID   string `json:"accountId" jsonschema:"AWS account ID to use for SSO login"`
	RoleName    string `json:"roleName" jsonschema:"AWS role name to use for SSO login"`
	Region      string `json:"region,omitempty" jsonschema:"default AWS region for the generated configuration; defaults to ssoRegion"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, return the planned operation without executing login or saving config"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type CloudLoginInput struct {
	Alias     string `json:"alias" jsonschema:"configured cloud provider alias to login"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, return the planned operation without executing login"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type CloudSetInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be updated; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to update; defaults to the server environment context"`
	Alias       string `json:"alias" jsonschema:"cloud provider alias to assign to the environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, return the planned operation without saving config"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type CloudListResult struct {
	CloudProviders []eruncommon.CloudProviderStatus `json:"cloudProviders,omitempty"`
}

type CloudActionResult struct {
	Preview  bool                           `json:"preview"`
	Alias    string                         `json:"alias,omitempty"`
	Provider eruncommon.CloudProviderStatus `json:"provider,omitempty"`
	Trace    []string                       `json:"trace,omitempty"`
	Plan     []string                       `json:"plan,omitempty"`
}

type CloudSetResult struct {
	Preview     bool                 `json:"preview"`
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	EnvConfig   eruncommon.EnvConfig `json:"envConfig"`
	Trace       []string             `json:"trace,omitempty"`
}

func cloudListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, CloudListInput) (*mcp.CallToolResult, CloudListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input CloudListInput) (*mcp.CallToolResult, CloudListResult, error) {
		statuses, err := eruncommon.ListCloudProviderStatuses(runtime.Store, eruncommon.CloudDependencies{})
		if err != nil {
			return nil, CloudListResult{}, err
		}
		return nil, CloudListResult{CloudProviders: statuses}, nil
	}
}

func cloudInitAWSTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, CloudInitAWSInput) (*mcp.CallToolResult, CloudActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input CloudInitAWSInput) (*mcp.CallToolResult, CloudActionResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		params := eruncommon.InitAWSCloudProviderParams{
			SSOStartURL: input.SSOStartURL,
			SSORegion:   input.SSORegion,
			AccountID:   input.AccountID,
			RoleName:    input.RoleName,
			Region:      input.Region,
		}
		if input.Preview {
			plan := []string{
				"aws configure set sso_start_url " + strings.TrimSpace(input.SSOStartURL) + " --profile erun-sso-<timestamp>",
				"aws configure set sso_region " + strings.TrimSpace(input.SSORegion) + " --profile erun-sso-<timestamp>",
				"aws configure set sso_account_id " + strings.TrimSpace(input.AccountID) + " --profile erun-sso-<timestamp>",
				"aws configure set sso_role_name " + strings.TrimSpace(input.RoleName) + " --profile erun-sso-<timestamp>",
				"aws sso login --profile erun-sso-<timestamp>",
				"aws sts get-caller-identity --profile erun-sso-<timestamp>",
				"save root cloud provider alias resolved from AWS identity",
			}
			return nil, CloudActionResult{Preview: true, Plan: plan}, nil
		}
		provider, err := eruncommon.InitAWSCloudProvider(ctx, runtime.Store, params, eruncommon.CloudDependencies{})
		if err != nil {
			return nil, CloudActionResult{}, err
		}
		status := eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{})
		return nil, CloudActionResult{Alias: provider.Alias, Provider: status, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

func cloudLoginTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, CloudLoginInput) (*mcp.CallToolResult, CloudActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input CloudLoginInput) (*mcp.CallToolResult, CloudActionResult, error) {
		alias := strings.TrimSpace(input.Alias)
		if alias == "" {
			return nil, CloudActionResult{}, fmt.Errorf("cloud provider alias is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		if input.Preview {
			return nil, CloudActionResult{Preview: true, Alias: alias, Plan: []string{"check cloud provider token status", "run provider login if token is expired"}}, nil
		}
		status, err := eruncommon.LoginCloudProviderAlias(ctx, runtime.Store, eruncommon.CloudLoginParams{Alias: alias}, eruncommon.CloudDependencies{})
		if err != nil {
			return nil, CloudActionResult{}, err
		}
		return nil, CloudActionResult{Alias: alias, Provider: status, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

func cloudSetTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, CloudSetInput) (*mcp.CallToolResult, CloudSetResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input CloudSetInput) (*mcp.CallToolResult, CloudSetResult, error) {
		tenant := firstNonEmpty(strings.TrimSpace(input.Tenant), strings.TrimSpace(runtime.Context.Tenant))
		environment := firstNonEmpty(strings.TrimSpace(input.Environment), strings.TrimSpace(runtime.Context.Environment))
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		config, err := eruncommon.SetEnvironmentCloudProviderAlias(ctx, runtime.Store, eruncommon.SetEnvironmentCloudAliasParams{
			Tenant:      tenant,
			Environment: environment,
			Alias:       input.Alias,
		})
		if err != nil {
			return nil, CloudSetResult{}, err
		}
		return nil, CloudSetResult{
			Preview:     input.Preview,
			Tenant:      tenant,
			Environment: environment,
			EnvConfig:   config,
			Trace:       normalizeTraceLines(traceOutput.String()),
		}, nil
	}
}
