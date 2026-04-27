package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ContextListInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type ContextInitInput struct {
	Name               string `json:"name,omitempty" jsonschema:"Kubernetes context name to create"`
	CloudProviderAlias string `json:"cloudProviderAlias" jsonschema:"cloud provider alias to use"`
	Region             string `json:"region" jsonschema:"cloud region for the context"`
	InstanceType       string `json:"instanceType,omitempty" jsonschema:"EC2 instance type; defaults to c8gd.2xlarge"`
	DiskType           string `json:"diskType,omitempty" jsonschema:"root disk type; defaults to gp3"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty" jsonschema:"root disk size in GB; supported values are 100 and 200"`
	SubnetID           string `json:"subnetId,omitempty" jsonschema:"optional EC2 subnet ID"`
	SecurityGroupID    string `json:"securityGroupId,omitempty" jsonschema:"optional EC2 security group ID"`
	KeyName            string `json:"keyName,omitempty" jsonschema:"optional EC2 key pair name"`
	Preview            bool   `json:"preview,omitempty" jsonschema:"when true, return the planned operation without creating cloud resources"`
	Verbosity          int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type ContextActionInput struct {
	Name      string `json:"name" jsonschema:"managed cloud context name"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, return the planned operation without changing cloud resources"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type ContextListResult struct {
	CloudContexts []eruncommon.CloudContextStatus `json:"cloudContexts,omitempty"`
}

type ContextActionResult struct {
	Preview bool                          `json:"preview"`
	Context eruncommon.CloudContextStatus `json:"context,omitempty"`
	Trace   []string                      `json:"trace,omitempty"`
}

func contextListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextListInput) (*mcp.CallToolResult, ContextListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextListInput) (*mcp.CallToolResult, ContextListResult, error) {
		contexts, err := eruncommon.ListCloudContextStatuses(runtime.Store)
		if err != nil {
			return nil, ContextListResult{}, err
		}
		return nil, ContextListResult{CloudContexts: contexts}, nil
	}
}

func contextInitTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextInitInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextInitInput) (*mcp.CallToolResult, ContextActionResult, error) {
		if strings.TrimSpace(input.CloudProviderAlias) == "" {
			return nil, ContextActionResult{}, fmt.Errorf("cloud provider alias is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		status, err := eruncommon.InitCloudContext(ctx, runtime.Store, eruncommon.InitCloudContextParams{
			Name:               input.Name,
			CloudProviderAlias: input.CloudProviderAlias,
			Region:             input.Region,
			InstanceType:       input.InstanceType,
			DiskType:           input.DiskType,
			DiskSizeGB:         input.DiskSizeGB,
			SubnetID:           input.SubnetID,
			SecurityGroupID:    input.SecurityGroupID,
			KeyName:            input.KeyName,
		}, eruncommon.CloudContextDependencies{})
		if err != nil {
			return nil, ContextActionResult{}, err
		}
		return nil, ContextActionResult{Preview: input.Preview, Context: status, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

func contextStopTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return contextPowerTool(runtime, eruncommon.StopCloudContext)
}

func contextStartTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return contextPowerTool(runtime, eruncommon.StartCloudContext)
}

func contextPowerTool(runtime RuntimeConfig, run func(eruncommon.Context, eruncommon.CloudContextStore, eruncommon.CloudContextParams, eruncommon.CloudContextDependencies) (eruncommon.CloudContextStatus, error)) func(context.Context, *mcp.CallToolRequest, ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
		if strings.TrimSpace(input.Name) == "" {
			return nil, ContextActionResult{}, fmt.Errorf("cloud context name is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		status, err := run(ctx, runtime.Store, eruncommon.CloudContextParams{Name: input.Name}, eruncommon.CloudContextDependencies{})
		if err != nil {
			return nil, ContextActionResult{}, err
		}
		return nil, ContextActionResult{Preview: input.Preview, Context: status, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
