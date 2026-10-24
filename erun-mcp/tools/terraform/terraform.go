package terraform

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/terraform"
	"github.com/sophium/erun/erun-mcp/tools/shared"
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
	shared.JobEnvelopeInput
}

func TerraformTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, TerraformInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input TerraformInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		tenant, environment, err := shared.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, shared.JobEnvelopeOutput{}, err
		}
		projectRoot := shared.FirstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		terraformStore, ok := any(runtime.Store).(terraform.TerraformStore)
		if !ok {
			terraformStore = config.ConfigStore{}
		}

		operation := terraform.TerraformOperation(strings.TrimSpace(input.Operation))
		// MCP is non-interactive: the confirmation is the explicit Confirm input,
		// which must equal the environment name to apply/destroy.
		confirm := func(_ core.Context, env string) error {
			if strings.TrimSpace(input.Confirm) != env {
				return fmt.Errorf("confirm must equal the environment name %q to %s (got %q)", env, operation, input.Confirm)
			}
			return nil
		}

		execute := shared.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, _ string) error {
			_, err := terraform.RunTerraform(runCtx, terraform.TerraformParams{
				Tenant:      tenant,
				Environment: environment,
				Operation:   operation,
				ProjectRoot: projectRoot,
				ExtraArgs:   input.ExtraArgs,
			}, terraformStore, confirm, nil)
			return err
		})
		envelope, err := shared.RunJobEnvelope(runtime, "terraform", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
