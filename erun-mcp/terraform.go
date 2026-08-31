package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type TerraformInput struct {
	Operation   string   `json:"operation" jsonschema:"required terraform operation: init (download providers, wire state, record the provider lock — run once before the others), apply (fmt/plan/apply), plan (read-only), or destroy"`
	Tenant      string   `json:"tenant,omitempty" jsonschema:"tenant name; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string   `json:"environment,omitempty" jsonschema:"environment name; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ProjectRoot string   `json:"projectRoot,omitempty" jsonschema:"project root holding terraform-<tenant>/ or <tenant>-devops/terraform-<tenant>/ (or the paths.terraform base from .erun/config.yaml); defaults to the runtime repo path"`
	Confirm     string   `json:"confirm,omitempty" jsonschema:"for apply/destroy: restate the environment name to confirm the mutation; required to apply, ignored for plan and preview"`
	ExtraArgs   []string `json:"extraArgs,omitempty" jsonschema:"extra args passed through to terraform plan"`
	Preview     bool     `json:"preview,omitempty" jsonschema:"when true, resolve and print the terraform commands without executing them"`
	Verbosity   int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait        *bool    `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func terraformTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, TerraformInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input TerraformInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobEnvelopeOutput{}, err
		}
		projectRoot := firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		terraformStore, ok := any(runtime.Store).(eruncommon.TerraformStore)
		if !ok {
			terraformStore = eruncommon.ConfigStore{}
		}

		operation := eruncommon.TerraformOperation(strings.TrimSpace(input.Operation))
		// MCP is non-interactive: the confirmation is the explicit Confirm input,
		// which must equal the environment name to apply/destroy.
		confirm := func(_ eruncommon.Context, env string) error {
			if strings.TrimSpace(input.Confirm) != env {
				return fmt.Errorf("confirm must equal the environment name %q to %s (got %q)", env, operation, input.Confirm)
			}
			return nil
		}

		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			_, err := eruncommon.RunTerraform(runCtx, eruncommon.TerraformParams{
				Tenant:      tenant,
				Environment: environment,
				Operation:   operation,
				ProjectRoot: projectRoot,
				ExtraArgs:   input.ExtraArgs,
			}, terraformStore, confirm)
			return err
		})
		envelope, err := runJobEnvelope(runtime, "terraform", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}
