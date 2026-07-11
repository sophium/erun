package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	TerraformInit    TerraformOperation = "init"
	TerraformApply   TerraformOperation = "apply"
	TerraformPlan    TerraformOperation = "plan"
	TerraformDestroy TerraformOperation = "destroy"
)

// terraformLockPlatforms are the provider platforms `erun terraform init`
// records in a generated .terraform.lock.hcl. They match erun's deploy targets
// (build/deploy always produce linux/amd64 + linux/arm64), so one committed lock
// lets init run -lockfile=readonly on every env regardless of the pod's arch — a
// lock generated for only the local platform would fail a read-only init on a
// pod of the other architecture.
var terraformLockPlatforms = []string{"linux_amd64", "linux_arm64"}

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
	// RootSource records which candidate the per-env root resolved from:
	// "configured" (paths.terraform), "repo-root" (terraform-<tenant>/), or
	// "devops" (<tenant>-devops/terraform-<tenant>/).
	RootSource string `json:"rootSource,omitempty"`
	// ConfiguredTerraformBase is the relative paths.terraform override from the
	// project's .erun/config.yaml when set; empty when the default
	// terraform-<tenant> base is used. Surfaced so the resolved base is auditable.
	ConfiguredTerraformBase string `json:"configuredTerraformBase,omitempty"`
	VarFile                 string `json:"varFile,omitempty"`
	// WorkDir, StateFile, and DataDir are the durable, per-env home-PVC locations
	// for Terraform's mutable artifacts — the local-backend state file, the plan
	// file, and TF_DATA_DIR — kept off the read-only image-baked playbook tree so
	// state and the init cache survive a runtime pod restart.
	WorkDir   string `json:"workDir,omitempty"`
	StateFile string `json:"stateFile,omitempty"`
	DataDir   string `json:"dataDir,omitempty"`
	// LockReadonly is set when a baked .terraform.lock.hcl pins providers, so init
	// runs -lockfile=readonly and never rewrites the read-only playbook tree.
	LockReadonly bool `json:"lockReadonly,omitempty"`
	// LegacyStateInTree flags a pre-relocation terraform.tfstate left in the root;
	// it is ignored (state now lives at StateFile) and surfaced as a warning.
	LegacyStateInTree    bool       `json:"legacyStateInTree,omitempty"`
	Commands             [][]string `json:"commands"`
	RequiresConfirmation bool       `json:"requiresConfirmation"`
}

// terraformRootSource records which candidate location the per-env Terraform
// root resolved from, so the decision is auditable in the trace and JSON.
type terraformRootSource string

const (
	terraformRootConfigured terraformRootSource = "configured"
	terraformRootRepo       terraformRootSource = "repo-root"
	terraformRootDevops     terraformRootSource = "devops"
)

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
	if result.RootSource == string(terraformRootDevops) {
		ctx.Trace(fmt.Sprintf("terraform: root under %s-devops/terraform-%s/ (devops-convention layout)", result.Tenant, result.Tenant))
	}
	if result.ConfiguredTerraformBase != "" {
		ctx.Trace("terraform: base " + result.ConfiguredTerraformBase + " from .erun/config.yaml paths.terraform")
	}
	if result.Operation != string(TerraformInit) {
		if result.VarFile != "" {
			ctx.Trace("terraform: var file " + result.VarFile)
		} else {
			ctx.Trace("terraform: no " + result.Environment + ".tfvars found; planning without -var-file")
		}
	}
	// State, the plan file, and TF_DATA_DIR live on the durable home PVC, off the
	// read-only playbook tree, so they survive a runtime pod restart.
	ctx.Trace("terraform: state " + result.StateFile + " (local backend on the home PVC — survives pod restarts)")
	ctx.Trace("terraform: TF_DATA_DIR " + result.DataDir)
	traceTerraformStateBackend(ctx, result)
	if result.LegacyStateInTree {
		ctx.Trace("terraform: warning: legacy in-tree state " + filepath.Join(result.Directory, "terraform.tfstate") + " is ignored; state now lives at " + result.StateFile)
	}
	traceTerraformInitLock(ctx, result)
	if cloudflareTokenPresent() {
		ctx.Trace("terraform: injecting TF_VAR_cloudflare_api_token from CLOUDFLARE_API_TOKEN")
	}
	ctx.TraceCommand("", "mkdir", "-p", result.WorkDir)
	for _, step := range steps {
		if step.confirm {
			ctx.Trace(fmt.Sprintf("terraform: confirmation gate — the %s below runs only after you type the environment name %q; a mismatch or empty entry aborts before anything is applied", result.Operation, result.Environment))
		}
		ctx.TraceCommand(result.Directory, "terraform", step.args...)
	}
}

// terraformBackendBlockRe matches a `backend "…" {` declaration in a Terraform
// config file, used to warn when the tree has none.
var terraformBackendBlockRe = regexp.MustCompile(`(?m)^\s*backend\s+"`)

// traceTerraformStateBackend warns when the tree declares no backend block. Only
// then does `terraform init -backend-config=path=` persist to plan/apply; without
// one, Terraform silently keeps state in ./terraform.tfstate inside the (often
// read-only) playbook tree instead of on the durable home PVC.
func traceTerraformStateBackend(ctx Context, result TerraformResult) {
	if terraformTreeHasBackend(result.Directory) {
		return
	}
	ctx.Trace("terraform: warning: no `backend \"local\"` block in the tree — Terraform keeps state in ./terraform.tfstate inside the playbook tree, not on the PVC; add `backend \"local\" {}` so state persists to " + result.StateFile + " and apply works on a read-only tree")
}

// terraformTreeHasBackend reports whether any .tf file in the env dir or its base
// declares a backend block. It follows the env dir's symlinked common.tf and
// also scans the base directly, matching how the playbook tree is laid out.
func terraformTreeHasBackend(dir string) bool {
	for _, scan := range []string{dir, filepath.Dir(dir)} {
		entries, err := os.ReadDir(scan)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tf") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(scan, entry.Name()))
			if err != nil {
				continue
			}
			if terraformBackendBlockRe.Match(data) {
				return true
			}
		}
	}
	return false
}

// traceTerraformInitLock records how init will treat the provider lock: a
// committed lock keeps init read-only (never rewrites the tree), while its
// absence means init generates one covering every deploy platform for the
// operator to commit.
func traceTerraformInitLock(ctx Context, result TerraformResult) {
	if result.Operation != string(TerraformInit) {
		return
	}
	if result.LockReadonly {
		ctx.Trace("terraform: committed .terraform.lock.hcl found — init runs -lockfile=readonly and never rewrites the tree")
		return
	}
	ctx.Trace("terraform: no .terraform.lock.hcl yet — init will generate it and record provider hashes for " + strings.Join(terraformLockPlatforms, ", ") + "; commit it so read-only envs can init with -lockfile=readonly")
}

func executeTerraformSteps(ctx Context, result TerraformResult, steps []terraformStep, confirm TerraformConfirmFunc) error {
	// The durable state + data dir live on the home PVC; create it before init so
	// terraform can write the local-backend state and TF_DATA_DIR there.
	if err := os.MkdirAll(result.WorkDir, 0o755); err != nil {
		return fmt.Errorf("creating terraform state dir %s: %w", result.WorkDir, err)
	}
	// A lock-generating init needs to write .terraform.lock.hcl into the tree.
	// Fail early with a recovery path rather than a raw permission error when the
	// tree is read-only (e.g. a baked runtime release with no committed lock).
	if result.Operation == string(TerraformInit) && !result.LockReadonly {
		if err := ensureTerraformTreeWritable(result.Directory); err != nil {
			return err
		}
	}
	extraEnv := terraformExtraEnv(result.DataDir)
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

	dir, varFile, source, err := resolveTerraformDir(projectRoot, tenant, environment, configuredBase)
	if err != nil {
		return TerraformResult{}, nil, err
	}

	workDir, err := resolveTerraformWorkDir(tenant, environment)
	if err != nil {
		return TerraformResult{}, nil, err
	}
	stateFile := filepath.Join(workDir, "terraform.tfstate")
	dataDir := filepath.Join(workDir, "data")
	planFile := filepath.Join(workDir, terraformPlanFile)
	lockReadonly := fileExists(filepath.Join(dir, ".terraform.lock.hcl"))

	steps := buildTerraformSteps(op, varFile, stateFile, planFile, lockReadonly, params.ExtraArgs)
	result := TerraformResult{
		Tenant:                  tenant,
		Environment:             environment,
		Operation:               string(op),
		Namespace:               KubernetesNamespaceName(tenant, environment),
		Directory:               dir,
		RootSource:              string(source),
		ConfiguredTerraformBase: configuredBase,
		VarFile:                 varFile,
		WorkDir:                 workDir,
		StateFile:               stateFile,
		DataDir:                 dataDir,
		LockReadonly:            lockReadonly,
		LegacyStateInTree:       fileExists(filepath.Join(dir, "terraform.tfstate")),
		Commands:                terraformStepCommands(steps),
		RequiresConfirmation:    op == TerraformApply || op == TerraformDestroy,
	}
	return result, steps, nil
}

func validateTerraformOperation(op TerraformOperation) error {
	switch op {
	case TerraformInit, TerraformApply, TerraformPlan, TerraformDestroy:
		return nil
	default:
		return fmt.Errorf("unsupported terraform operation %q (want init, apply, plan, or destroy)", op)
	}
}

// resolveTerraformDir resolves the per-env Terraform root. A configured
// paths.terraform override wins; otherwise erun looks for terraform-<tenant>/ at
// the project root, then under <tenant>-devops/ — where a tenant that keeps its
// whole devops footprint together (docker/, k8s/, terraform-<tenant>/) holds it,
// mirroring how deploy/build discover the -devops dir. Either way erun appends
// /<environment>.
func resolveTerraformDir(projectRoot, tenant, environment, configuredBase string) (dir, varFile string, source terraformRootSource, err error) {
	if base := resolveProjectPath(projectRoot, configuredBase); base != "" {
		dir = filepath.Join(base, environment)
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			return "", "", "", terraformRootNotFoundError(tenant, environment, configuredBase, dir)
		}
		return dir, detectTerraformVarFile(dir, environment), terraformRootConfigured, nil
	}

	candidates := []struct {
		base   string
		source terraformRootSource
	}{
		{filepath.Join(projectRoot, "terraform-"+tenant), terraformRootRepo},
		{filepath.Join(projectRoot, tenant+"-devops", "terraform-"+tenant), terraformRootDevops},
	}
	for _, c := range candidates {
		d := filepath.Join(c.base, environment)
		if info, statErr := os.Stat(d); statErr == nil && info.IsDir() {
			return d, detectTerraformVarFile(d, environment), c.source, nil
		}
	}
	return "", "", "", terraformRootNotFoundError(tenant, environment, "", "")
}

// resolveTerraformWorkDir is the durable, per-env home-PVC location for
// Terraform's mutable artifacts — the local-backend state file, the plan file,
// and TF_DATA_DIR (providers/modules/backend record). It sits beside
// ~/.erun/outputs, on the /home/erun PVC in a runtime pod, so state and the init
// cache survive a pod restart while the playbooks stay read-only in the release.
func resolveTerraformWorkDir(tenant, environment string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir for terraform state: %w", err)
	}
	return filepath.Join(home, ".erun", "terraform", tenant, environment), nil
}

func detectTerraformVarFile(dir, environment string) string {
	candidate := environment + ".tfvars"
	if fileExists(filepath.Join(dir, candidate)) {
		return candidate
	}
	return ""
}

// terraformRootNotFoundError explains where erun looked and how to scaffold it,
// tailoring the hint to whether a paths.terraform override is in effect.
func terraformRootNotFoundError(tenant, environment, configuredBase, configuredDir string) error {
	if strings.TrimSpace(configuredBase) != "" {
		return fmt.Errorf("no Terraform root at %s for %s/%s — the .erun/config.yaml paths.terraform base %q must contain a %s/ dir with its main.tf and %s.tfvars", configuredDir, tenant, environment, strings.TrimSpace(configuredBase), environment, environment)
	}
	return fmt.Errorf("no Terraform root for %s/%s — looked under terraform-%s/%s/ and %s-devops/terraform-%s/%s/; scaffold it with the erun-blueprint-platform skill, or create one with its main.tf and %s.tfvars",
		tenant, environment, tenant, environment, tenant, tenant, environment, environment)
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

// buildTerraformSteps resolves the terraform command sequence for op. init is
// its own operation, so plan/apply/destroy no longer run it implicitly — the
// operator runs `erun terraform init` once (and after changing providers), which
// keeps apply/plan/destroy from ever trying to write the lock file into a
// read-only playbook tree.
func buildTerraformSteps(op TerraformOperation, varFile, stateFile, planFile string, lockReadonly bool, extraArgs []string) []terraformStep {
	switch op {
	case TerraformInit:
		return buildTerraformInitSteps(stateFile, lockReadonly)
	case TerraformPlan:
		plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		return []terraformStep{{args: plan}}
	case TerraformDestroy:
		plan := append([]string{"plan", "-destroy", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		plan = append(plan, "-out", planFile)
		apply := terraformStep{args: []string{"apply", "-input=false", planFile}, confirm: true}
		return []terraformStep{{args: plan}, apply}
	default: // apply
		// -check, not a rewrite: the playbook tree is read-only in the release.
		fmtStep := terraformStep{args: []string{"fmt", "-check", "-recursive", ".."}}
		plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		plan = append(plan, "-out", planFile)
		apply := terraformStep{args: []string{"apply", "-input=false", planFile}, confirm: true}
		return []terraformStep{fmtStep, {args: plan}, apply}
	}
}

// buildTerraformInitSteps sets up the local backend at the durable state file and
// populates the provider cache (TF_DATA_DIR) on the PVC. When the tree already
// carries a committed .terraform.lock.hcl (e.g. a baked read-only release), init
// runs -lockfile=readonly so it never rewrites the tree. When no lock exists yet,
// init generates one and a follow-up `providers lock` records hashes for every
// deploy platform, so the single committed lock initializes on any env's arch.
func buildTerraformInitSteps(stateFile string, lockReadonly bool) []terraformStep {
	initArgs := []string{"init", "-input=false", "-backend-config=path=" + stateFile}
	if lockReadonly {
		initArgs = append(initArgs, "-lockfile=readonly")
		return []terraformStep{{args: initArgs}}
	}
	lockArgs := []string{"providers", "lock"}
	for _, platform := range terraformLockPlatforms {
		lockArgs = append(lockArgs, "-platform="+platform)
	}
	return []terraformStep{{args: initArgs}, {args: lockArgs}}
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

// terraformExtraEnv sets TF_DATA_DIR to the durable home-PVC data dir (so
// providers/modules/backend record survive a pod restart) and supplies the
// Cloudflare API token the edge module's cert-manager DNS-01 solver needs. The
// token rides in the environment, never in argv, so it never lands in the trace.
func terraformExtraEnv(dataDir string) []string {
	env := []string{"TF_DATA_DIR=" + dataDir}
	if token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); token != "" {
		env = append(env, "TF_VAR_cloudflare_api_token="+token)
	}
	return env
}

// ensureTerraformTreeWritable confirms init can write .terraform.lock.hcl into
// the playbook tree, and otherwise returns a recovery path. A sourceless runtime
// env surfaces the tree as a symlink into the read-only, root-owned image layer
// (/opt/erun/release), so the erun user cannot create the lock there — the lock
// must be generated on a writable env and committed so it bakes into the image.
func ensureTerraformTreeWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".erun-tf-writeprobe-*")
	if err != nil {
		return fmt.Errorf("terraform init must generate .terraform.lock.hcl but the playbook tree %s is not writable — run `erun terraform init` on a writable env (e.g. a <tenant>-local checkout), commit the generated .terraform.lock.hcl, and rebuild/redeploy so it bakes into the image; then this env can init with -lockfile=readonly: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
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
