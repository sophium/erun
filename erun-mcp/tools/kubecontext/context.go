package kubecontext

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
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

type ContextStartInput struct {
	Name      string `json:"name" jsonschema:"managed cloud context name"`
	Force     bool   `json:"force,omitempty" jsonschema:"when true, override the working-hours start gate"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, return the planned operation without changing cloud resources"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// ContextListResult aliases the shared envelope so the CLI and the MCP tool
// cannot drift into two spellings of the same listing.
type ContextListResult = model.CloudContextListResult

type ContextActionResult struct {
	Preview bool                     `json:"preview"`
	Context model.CloudContextStatus `json:"context,omitempty"`
	Trace   []string                 `json:"trace,omitempty"`
}

func ContextListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextListInput) (*mcp.CallToolResult, ContextListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextListInput) (*mcp.CallToolResult, ContextListResult, error) {
		contexts, err := cloud.ListCloudContextStatuses(runtime.Store)
		if err != nil {
			return nil, ContextListResult{}, err
		}
		return nil, ContextListResult{CloudContexts: contexts}, nil
	}
}

func ContextInitTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextInitInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextInitInput) (*mcp.CallToolResult, ContextActionResult, error) {
		if strings.TrimSpace(input.CloudProviderAlias) == "" {
			return nil, ContextActionResult{}, fmt.Errorf("cloud provider alias is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		status, err := cloud.InitCloudContext(ctx, runtime.Store, model.InitCloudContextParams{
			Name:               input.Name,
			CloudProviderAlias: input.CloudProviderAlias,
			Region:             input.Region,
			InstanceType:       input.InstanceType,
			DiskType:           input.DiskType,
			DiskSizeGB:         input.DiskSizeGB,
			SubnetID:           input.SubnetID,
			SecurityGroupID:    input.SecurityGroupID,
			KeyName:            input.KeyName,
		}, cloud.CloudContextDependencies{})
		if err != nil {
			return nil, ContextActionResult{}, err
		}
		return nil, ContextActionResult{Preview: input.Preview, Context: status, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

func ContextStopTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return contextPowerTool(runtime, cloud.StopCloudContext)
}

func ContextStartTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContextStartInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextStartInput) (*mcp.CallToolResult, ContextActionResult, error) {
		if strings.TrimSpace(input.Name) == "" {
			return nil, ContextActionResult{}, fmt.Errorf("cloud context name is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		status, err := cloud.StartCloudContext(ctx, runtime.Store, model.CloudContextParams{Name: input.Name, Force: input.Force}, cloud.CloudContextDependencies{})
		if err != nil {
			return nil, ContextActionResult{}, err
		}
		return nil, ContextActionResult{Preview: input.Preview, Context: status, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

func contextPowerTool(runtime runtimecfg.RuntimeConfig, run func(core.Context, seam.CloudContextStore, model.CloudContextParams, cloud.CloudContextDependencies) (model.CloudContextStatus, error)) func(context.Context, *mcp.CallToolRequest, ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContextActionInput) (*mcp.CallToolResult, ContextActionResult, error) {
		if strings.TrimSpace(input.Name) == "" {
			return nil, ContextActionResult{}, fmt.Errorf("cloud context name is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		status, err := run(ctx, runtime.Store, model.CloudContextParams{Name: input.Name}, cloud.CloudContextDependencies{})
		if err != nil {
			return nil, ContextActionResult{}, err
		}
		return nil, ContextActionResult{Preview: input.Preview, Context: status, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}
