package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DoctorInput struct {
	Tenant          string `json:"tenant,omitempty" jsonschema:"optional explicit tenant override"`
	Environment     string `json:"environment,omitempty" jsonschema:"optional explicit environment override"`
	PruneImages     bool   `json:"pruneImages,omitempty" jsonschema:"when true, prune unused Docker images"`
	PruneBuildCache bool   `json:"pruneBuildCache,omitempty" jsonschema:"when true, prune unused BuildKit cache"`
	PruneContainers bool   `json:"pruneContainers,omitempty" jsonschema:"when true, prune stopped Docker containers"`
	Preview         bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity       int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func doctorTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DoctorInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DoctorInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			target, err := resolveDoctorOpenResult(runtime, input)
			if err != nil {
				return err
			}
			req := eruncommon.ShellLaunchParamsFromResult(target)
			inspection, err := eruncommon.RunDoctorInspection(runCtx, nil, req)
			if err != nil {
				return err
			}
			if !runCtx.DryRun {
				if _, err := fmt.Fprintf(runCtx.Stdout, "Target: %s/%s\n", target.Tenant, target.Environment); err != nil {
					return err
				}
				if stdout := strings.TrimSpace(inspection.Stdout); stdout != "" {
					if _, err := fmt.Fprintln(runCtx.Stdout, stdout); err != nil {
						return err
					}
				}
				if stderr := strings.TrimSpace(inspection.Stderr); stderr != "" {
					if _, err := fmt.Fprintln(runCtx.Stderr, stderr); err != nil {
						return err
					}
				}
			}

			for _, action := range doctorActionsFromInput(input) {
				if !runCtx.DryRun {
					if _, err := fmt.Fprintf(runCtx.Stdout, "Running: %s\n", eruncommon.DoctorActionDescription(action)); err != nil {
						return err
					}
				}
				output, err := eruncommon.RunDoctorAction(runCtx, nil, req, action)
				if err != nil {
					return err
				}
				if runCtx.DryRun {
					continue
				}
				if stdout := strings.TrimSpace(output.Stdout); stdout != "" {
					if _, err := fmt.Fprintln(runCtx.Stdout, stdout); err != nil {
						return err
					}
				}
				if stderr := strings.TrimSpace(output.Stderr); stderr != "" {
					if _, err := fmt.Fprintln(runCtx.Stderr, stderr); err != nil {
						return err
					}
				}
			}
			return nil
		})
		return nil, output, err
	}
}

func resolveDoctorOpenResult(runtime RuntimeConfig, input DoctorInput) (eruncommon.OpenResult, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	switch {
	case tenant != "" && environment != "":
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Tenant:      tenant,
			Environment: environment,
		})
	case tenant != "":
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Tenant:                tenant,
			UseDefaultEnvironment: true,
		})
	case environment != "":
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Environment:      environment,
			UseDefaultTenant: true,
		})
	}

	runtimeTenant := strings.TrimSpace(runtime.Context.Tenant)
	runtimeEnvironment := strings.TrimSpace(runtime.Context.Environment)
	if runtimeTenant != "" && runtimeEnvironment != "" {
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Tenant:      runtimeTenant,
			Environment: runtimeEnvironment,
		})
	}

	return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
}

func doctorActionsFromInput(input DoctorInput) []eruncommon.DoctorAction {
	actions := make([]eruncommon.DoctorAction, 0, 3)
	if input.PruneImages {
		actions = append(actions, eruncommon.DoctorActionPruneImages)
	}
	if input.PruneBuildCache {
		actions = append(actions, eruncommon.DoctorActionPruneBuildCache)
	}
	if input.PruneContainers {
		actions = append(actions, eruncommon.DoctorActionPruneContainers)
	}
	return actions
}
