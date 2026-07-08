package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TerraformStore is the read surface RunTerraform needs to resolve the target
// scope (default tenant/environment) and confirm the environment is configured.
type TerraformStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	LoadTenantConfig(string) (TenantConfig, string, error)
	LoadEnvConfig(string, string) (EnvConfig, string, error)
}

// TerraformOperation is the verb RunTerraform performs against a per-env root.
type TerraformOperation string

const (
	TerraformApply   TerraformOperation = "apply"
	TerraformPlan    TerraformOperation = "plan"
	TerraformDestroy TerraformOperation = "destroy"
)

// TerraformConfirmFunc gates a mutating operation (apply/destroy) after the plan
// is produced and before it is applied. The operator confirms by restating the
// environment name — the guard against applying to the wrong env. It is never
// called for a read-only plan or under dry-run.
type TerraformConfirmFunc func(ctx Context, environment string) error

// TerraformParams are the inputs to running Terraform against a platform's
// per-env root. An empty Tenant/Environment resolves the configured default
// scope.
type TerraformParams struct {
	Tenant      string
	Environment string
	Operation   TerraformOperation
	ProjectRoot string
	ExtraArgs   []string
}

// TerraformResult is the resolved Terraform plan a run would execute.
type TerraformResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Operation   string `json:"operation"`
	Namespace   string `json:"namespace"`
	Directory   string `json:"directory"`
	// ConfiguredTerraformBase is the relative paths.terraform override from the
	// project's .erun/config.yaml when set; empty when the default
	// terraform-<tenant> base is used. Surfaced so the resolved base is auditable.
	ConfiguredTerraformBase string     `json:"configuredTerraformBase,omitempty"`
	VarFile                 string     `json:"varFile,omitempty"`
	Commands                [][]string `json:"commands"`
	RequiresConfirmation    bool       `json:"requiresConfirmation"`
}

// terraformPlanFile holds the plan apply/destroy write and then apply, so the
// applied changes are exactly the reviewed ones.
const terraformPlanFile = "apply.tfplan"

type terraformStep struct {
	args    []string
	confirm bool
}

// RunTerraform resolves and (unless dry-run) runs Terraform against a platform's
// per-env root. Every command and decision is traced before execution, so a
// dry-run is a complete, side-effect-free plan of what a real run would do.
func RunTerraform(ctx Context, params TerraformParams, store TerraformStore, confirm TerraformConfirmFunc) (TerraformResult, error) {
	if store == nil {
		store = ConfigStore{}
	}

	result, steps, err := resolveTerraformPlan(params, store)
	if err != nil {
		return TerraformResult{}, err
	}

	traceTerraformPlan(ctx, result, steps)
	if ctx.DryRun {
		return result, nil
	}
	if err := executeTerraformSteps(ctx, result, steps, confirm); err != nil {
		return result, err
	}
	return result, nil
}

func traceTerraformPlan(ctx Context, result TerraformResult, steps []terraformStep) {
	ctx.Trace(fmt.Sprintf("terraform: %s in %s (env %s/%s, namespace %s)", result.Operation, result.Directory, result.Tenant, result.Environment, result.Namespace))
	if result.ConfiguredTerraformBase != "" {
		ctx.Trace("terraform: base " + result.ConfiguredTerraformBase + " from .erun/config.yaml paths.terraform")
	}
	if result.VarFile != "" {
		ctx.Trace("terraform: var file " + result.VarFile)
	} else {
		ctx.Trace("terraform: no " + result.Environment + ".tfvars found; planning without -var-file")
	}
	if cloudflareTokenPresent() {
		ctx.Trace("terraform: injecting TF_VAR_cloudflare_api_token from CLOUDFLARE_API_TOKEN")
	}
	for _, step := range steps {
		if step.confirm {
			ctx.Trace(fmt.Sprintf("terraform: %s requires typing the environment name (%s) to confirm before apply", result.Operation, result.Environment))
		}
		ctx.TraceCommand(result.Directory, "terraform", step.args...)
	}
}

func executeTerraformSteps(ctx Context, result TerraformResult, steps []terraformStep, confirm TerraformConfirmFunc) error {
	extraEnv := terraformExtraEnv()
	for _, step := range steps {
		if step.confirm {
			if confirm == nil {
				return fmt.Errorf("terraform %s requires confirmation but none was provided", result.Operation)
			}
			if err := confirm(ctx, result.Environment); err != nil {
				return err
			}
		}
		if err := execTerraformStep(ctx, result.Directory, extraEnv, step.args); err != nil {
			return fmt.Errorf("terraform %s: %w", step.args[0], err)
		}
	}
	return nil
}

func resolveTerraformPlan(params TerraformParams, store TerraformStore) (TerraformResult, []terraformStep, error) {
	op := params.Operation
	if err := validateTerraformOperation(op); err != nil {
		return TerraformResult{}, nil, err
	}
	projectRoot := strings.TrimSpace(params.ProjectRoot)
	if projectRoot == "" {
		return TerraformResult{}, nil, fmt.Errorf("project root is required")
	}

	tenant, environment, err := resolveTerraformScope(store, params)
	if err != nil {
		return TerraformResult{}, nil, err
	}
	if err := ValidateTenantName(tenant); err != nil {
		return TerraformResult{}, nil, err
	}
	// Confirm the env is configured before deriving its root, so a typo fails
	// here rather than with an opaque "no such directory".
	if _, _, err := store.LoadEnvConfig(tenant, environment); err != nil {
		return TerraformResult{}, nil, err
	}

	paths, err := loadProjectPaths(projectRoot)
	if err != nil {
		return TerraformResult{}, nil, err
	}
	configuredBase := strings.TrimSpace(paths.Terraform)

	dir, varFile, err := resolveTerraformDir(projectRoot, tenant, environment, configuredBase)
	if err != nil {
		return TerraformResult{}, nil, err
	}

	steps := buildTerraformSteps(op, varFile, params.ExtraArgs)
	result := TerraformResult{
		Tenant:                  tenant,
		Environment:             environment,
		Operation:               string(op),
		Namespace:               KubernetesNamespaceName(tenant, environment),
		Directory:               dir,
		ConfiguredTerraformBase: configuredBase,
		VarFile:                 varFile,
		Commands:                terraformStepCommands(steps),
		RequiresConfirmation:    op == TerraformApply || op == TerraformDestroy,
	}
	return result, steps, nil
}

func validateTerraformOperation(op TerraformOperation) error {
	switch op {
	case TerraformApply, TerraformPlan, TerraformDestroy:
		return nil
	default:
		return fmt.Errorf("unsupported terraform operation %q (want apply, plan, or destroy)", op)
	}
}

// resolveTerraformDir resolves the per-env Terraform root. The base defaults to
// terraform-<tenant> under the project root but is replaced by a configured
// paths.terraform override; either way erun still appends /<environment>.
func resolveTerraformDir(projectRoot, tenant, environment, configuredBase string) (dir, varFile string, err error) {
	base := resolveProjectPath(projectRoot, configuredBase)
	if base == "" {
		base = filepath.Join(projectRoot, "terraform-"+tenant)
	}
	dir = filepath.Join(base, environment)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return "", "", terraformRootNotFoundError(dir, tenant, environment, configuredBase)
	}
	candidate := environment + ".tfvars"
	if st, statErr := os.Stat(filepath.Join(dir, candidate)); statErr == nil && !st.IsDir() {
		varFile = candidate
	}
	return dir, varFile, nil
}

// terraformRootNotFoundError explains where erun looked and how to scaffold it,
// tailoring the hint to whether a paths.terraform override is in effect.
func terraformRootNotFoundError(dir, tenant, environment, configuredBase string) error {
	if strings.TrimSpace(configuredBase) != "" {
		return fmt.Errorf("no Terraform root at %s for %s/%s — the .erun/config.yaml paths.terraform base %q must contain a %s/ dir with its main.tf and %s.tfvars", dir, tenant, environment, strings.TrimSpace(configuredBase), environment, environment)
	}
	return fmt.Errorf("no Terraform root at %s for %s/%s — scaffold it with the erun-blueprint-platform skill, or create terraform-%s/%s/ with its main.tf and %s.tfvars", dir, tenant, environment, tenant, environment, environment)
}

func resolveTerraformScope(store TerraformStore, params TerraformParams) (tenant, environment string, err error) {
	tenant = strings.TrimSpace(params.Tenant)
	environment = strings.TrimSpace(params.Environment)
	if tenant == "" {
		config, _, err := store.LoadERunConfig()
		if err != nil {
			return "", "", err
		}
		tenant = strings.TrimSpace(config.DefaultTenant)
		if tenant == "" {
			return "", "", ErrDefaultTenantNotConfigured
		}
	}
	if environment == "" {
		tenantConfig, _, err := store.LoadTenantConfig(tenant)
		if err != nil {
			return "", "", err
		}
		environment = strings.TrimSpace(tenantConfig.DefaultEnvironment)
		if environment == "" {
			return "", "", ErrDefaultEnvironmentNotConfigured
		}
	}
	return tenant, environment, nil
}

func buildTerraformSteps(op TerraformOperation, varFile string, extraArgs []string) []terraformStep {
	initStep := terraformStep{args: []string{"init", "-input=false"}}
	switch op {
	case TerraformPlan:
		plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		return []terraformStep{initStep, {args: plan}}
	case TerraformDestroy:
		plan := append([]string{"plan", "-destroy", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		plan = append(plan, "-out", terraformPlanFile)
		apply := terraformStep{args: []string{"apply", "-input=false", terraformPlanFile}, confirm: true}
		return []terraformStep{initStep, {args: plan}, apply}
	default: // apply
		fmtStep := terraformStep{args: []string{"fmt", "-recursive", ".."}}
		plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		plan = append(plan, "-out", terraformPlanFile)
		apply := terraformStep{args: []string{"apply", "-input=false", terraformPlanFile}, confirm: true}
		return []terraformStep{initStep, fmtStep, {args: plan}, apply}
	}
}

func terraformVarFileArgs(varFile string) []string {
	if strings.TrimSpace(varFile) == "" {
		return nil
	}
	return []string{"-var-file=" + varFile}
}

func terraformStepCommands(steps []terraformStep) [][]string {
	commands := make([][]string, 0, len(steps))
	for _, step := range steps {
		commands = append(commands, append([]string{"terraform"}, step.args...))
	}
	return commands
}

func cloudflareTokenPresent() bool {
	return strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")) != ""
}

// terraformExtraEnv supplies the Cloudflare API token the edge module's
// cert-manager DNS-01 solver needs. It rides in the environment, never in argv,
// so the token never lands in the trace.
func terraformExtraEnv() []string {
	token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if token == "" {
		return nil
	}
	return []string{"TF_VAR_cloudflare_api_token=" + token}
}

func execTerraformStep(ctx Context, dir string, extraEnv, args []string) error {
	cmd := Command("terraform", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	capture := ctx.ToolCapture()
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}
