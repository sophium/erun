package terraform

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/operatorprompt"
	"github.com/sophium/erun/erun-common/base/seam/runtimepod"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

type terraformRootSource string

const (
	terraformRootConfigured terraformRootSource = "configured"
	terraformRootRepo       terraformRootSource = "repo-root"
	terraformRootDevops     terraformRootSource = "devops"
)

// terraformPlanFile makes apply/destroy apply exactly the reviewed plan.
const terraformPlanFile = "apply.tfplan"

// RunTerraform resolves and, unless dry-run, runs Terraform against a per-env
// root, tracing everything first. dispatch runs it inside a remote env's own
// pod; nil defaults to RunRemoteCommand.
func RunTerraform(ctx core.Context, params model.TerraformParams, store envstore.TerraformStore, confirm operatorprompt.TerraformConfirmFunc, dispatch runtimepod.RemoteCommandRunnerFunc) (model.TerraformResult, error) {
	if store == nil {
		store = configpkg.ConfigStore{}
	}

	target, err := resolveTerraformTargetEnvironment(params, store)
	if err != nil {
		return model.TerraformResult{}, err
	}
	if target.NeedsDispatch {
		return dispatchTerraform(ctx, target, params, confirm, dispatch)
	}

	result, steps, err := resolveTerraformPlan(params, target, store)
	if err != nil {
		return model.TerraformResult{}, err
	}

	rfc2136Secret, err := prepareTerraformRFC2136Secret(ctx, &result)
	if err != nil {
		return model.TerraformResult{}, err
	}

	traceTerraformPlan(ctx, result, steps)
	if ctx.DryRun {
		return result, nil
	}
	if err := executeTerraformSteps(ctx, result, steps, confirm, rfc2136Secret); err != nil {
		return result, err
	}
	return result, nil
}

// prepareTerraformRFC2136Secret reads the TSIG secret up front so a missing one
// is named before any plan output. The value is never stored on result, which
// --output json serializes.
func prepareTerraformRFC2136Secret(ctx core.Context, result *model.TerraformResult) (string, error) {
	if result.Operation == string(model.TerraformInit) {
		return "", nil
	}
	requirement, err := resolveTerraformRFC2136Requirement(result.Directory, result.VarFile)
	if err != nil {
		return "", err
	}
	if !requirement.Needed {
		return "", nil
	}
	requirement.KubernetesContext = result.KubernetesContext
	secret, err := readTerraformRFC2136Secret(ctx, requirement)
	if err != nil {
		return "", err
	}
	result.RFC2136SecretName = requirement.SecretName
	result.RFC2136SecretNamespace = requirement.Namespace
	return secret, nil
}

func traceTerraformPlan(ctx core.Context, result model.TerraformResult, steps []terraformStep) {
	ctx.Trace(fmt.Sprintf("terraform: %s in %s (env %s/%s, type %s, namespace %s)", result.Operation, result.Directory, result.Tenant, result.Environment, terraformEnvTypeLabel(result.EnvType), result.Namespace))
	if result.RootSource == string(terraformRootDevops) {
		ctx.Trace(fmt.Sprintf("terraform: root under %s-devops/terraform-%s/ (devops-convention layout)", result.Tenant, result.Tenant))
	}
	if result.ConfiguredTerraformBase != "" {
		ctx.Trace("terraform: base " + result.ConfiguredTerraformBase + " from .erun/config.yaml paths.terraform")
	}
	if result.Operation != string(model.TerraformInit) {
		if result.VarFile != "" {
			ctx.Trace("terraform: var file " + result.VarFile)
		} else {
			ctx.Trace("terraform: no " + result.Environment + ".tfvars found; planning without -var-file")
		}
	}
	ctx.Trace("terraform: state " + result.StateFile + " (" + result.StateLocus + ")")
	ctx.Trace("terraform: TF_DATA_DIR " + result.DataDir)
	traceTerraformStateBackend(ctx, result)
	if result.LegacyStateInTree {
		ctx.Trace("terraform: warning: legacy in-tree state " + filepath.Join(result.Directory, "terraform.tfstate") + " is ignored; state now lives at " + result.StateFile)
	}
	traceTerraformInitLock(ctx, result)
	if cloudflareTokenPresent() {
		ctx.Trace("terraform: injecting TF_VAR_cloudflare_api_token from CLOUDFLARE_API_TOKEN")
	}
	if result.RFC2136SecretName != "" {
		ctx.Trace(fmt.Sprintf("terraform: injecting TF_VAR_rfc2136_tsig_secret from Secret %s/%s (key %s), read via %s",
			result.RFC2136SecretNamespace, result.RFC2136SecretName, terraformRFC2136SecretKey, terraformKubernetesContextLabel(result.KubernetesContext)))
	}
	ctx.TraceCommand("", "mkdir", "-p", result.WorkDir)
	for _, step := range steps {
		if step.confirm {
			ctx.Trace(fmt.Sprintf("terraform: confirmation gate — the %s below runs only after you type the environment name %q; a mismatch or empty entry aborts before anything is applied", result.Operation, result.Environment))
		}
		ctx.TraceCommand(result.Directory, "terraform", step.args...)
	}
}

var terraformBackendBlockRe = regexp.MustCompile(`(?m)^\s*backend\s+"`)

// Without a backend block, -backend-config is ignored and state lands in the
// read-only playbook tree.
func traceTerraformStateBackend(ctx core.Context, result model.TerraformResult) {
	if terraformTreeHasBackend(result.Directory) {
		return
	}
	ctx.Trace("terraform: warning: no `backend \"local\"` block in the tree — Terraform keeps state in ./terraform.tfstate inside the playbook tree, not on the PVC; add `backend \"local\" {}` so state persists to " + result.StateFile + " and apply works on a read-only tree")
}

// terraformTreeHasBackend also scans the base, since common.tf is symlinked.
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

func traceTerraformInitLock(ctx core.Context, result model.TerraformResult) {
	if result.Operation != string(model.TerraformInit) {
		return
	}
	if result.LockReadonly {
		ctx.Trace("terraform: committed .terraform.lock.hcl found — init runs -lockfile=readonly and never rewrites the tree")
		return
	}
	ctx.Trace("terraform: no .terraform.lock.hcl yet — init will generate it and record provider hashes for " + strings.Join(terraformLockPlatforms, ", ") + "; commit it so read-only envs can init with -lockfile=readonly")
}

// terraformTarget is resolved once so local and dispatch paths cannot diverge.
type terraformTarget struct {
	Tenant         string
	Environment    string
	KubeContext    string
	EnvType        configpkg.EnvironmentType
	RemoteWorktree bool
	// NeedsDispatch is true when state lives on the target pod's PVC and this
	// process is not running in that pod.
	NeedsDispatch bool
}

// resolveTerraformTargetEnvironment confirms the env exists before deriving its
// root, so a typo is a named error rather than a missing directory.
func resolveTerraformTargetEnvironment(params model.TerraformParams, store envstore.TerraformStore) (terraformTarget, error) {
	tenant, environment, err := resolveTerraformScope(store, params)
	if err != nil {
		return terraformTarget{}, err
	}
	if err := configpkg.ValidateTenantName(tenant); err != nil {
		return terraformTarget{}, err
	}
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return terraformTarget{}, err
	}
	envType := envConfig.ResolvedType()
	remoteWorktree := envConfig.RemoteWorktree()
	// A host env has no cluster. ResolvedType, not !HasPod(), keeps legacy untyped
	// envs working.
	if envType == configpkg.EnvironmentTypeHost {
		return terraformTarget{}, fmt.Errorf("terraform %s/%s: %s is a host environment — it has no pod and no cluster to run terraform against", tenant, environment, environment)
	}
	// A remote env's state lives on its pod's PVC; outside that pod
	// (ERUN_TENANT/ERUN_ENVIRONMENT), dispatch rather than resolve a plausible but
	// wrong local path.
	needsDispatch := false
	if remoteWorktree {
		podTenant, podEnvironment, inPod := core.InjectedRuntimePodIdentity(os.Getenv)
		needsDispatch = !inPod || podTenant != tenant || podEnvironment != environment
	}
	kubeContext := store.ResolveEffectiveKubernetesContext(environment, envConfig.KubernetesContext)
	return terraformTarget{
		Tenant:         tenant,
		Environment:    environment,
		KubeContext:    kubeContext,
		EnvType:        envType,
		RemoteWorktree: remoteWorktree,
		NeedsDispatch:  needsDispatch,
	}, nil
}

func requiresTerraformConfirmation(op model.TerraformOperation) bool {
	return op == model.TerraformApply || op == model.TerraformDestroy
}

// dispatchTerraform runs `erun terraform <op>` in the target pod, behind the
// same confirm gate as a local run.
func dispatchTerraform(ctx core.Context, target terraformTarget, params model.TerraformParams, confirm operatorprompt.TerraformConfirmFunc, dispatch runtimepod.RemoteCommandRunnerFunc) (model.TerraformResult, error) {
	if len(params.ExtraArgs) > 0 {
		return model.TerraformResult{}, fmt.Errorf("terraform %s/%s: extra terraform args are not supported when dispatching %s into %s's own runtime pod from off-environment", target.Tenant, target.Environment, params.Operation, target.Environment)
	}

	req := configpkg.ShellLaunchParams{
		Tenant:            target.Tenant,
		Environment:       target.Environment,
		Namespace:         configpkg.KubernetesNamespaceName(target.Tenant, target.Environment),
		KubernetesContext: target.KubeContext,
	}
	script := terraformDispatchScript(target, params.Operation)

	ctx.Trace(fmt.Sprintf("terraform: %s/%s is a %s environment — dispatching %s non-interactively into its own runtime pod instead of running on this machine", target.Tenant, target.Environment, terraformEnvTypeLabel(string(target.EnvType)), params.Operation))
	kube.TraceRemoteCommand(ctx, req, "terraform-dispatch", script)

	dispatched := model.TerraformResult{
		Tenant:      target.Tenant,
		Environment: target.Environment,
		Operation:   string(params.Operation),
		Namespace:   req.Namespace,
		EnvType:     string(target.EnvType),
		Dispatched:  true,
	}
	if ctx.DryRun {
		return dispatched, nil
	}
	if requiresTerraformConfirmation(params.Operation) {
		if confirm == nil {
			return model.TerraformResult{}, fmt.Errorf("terraform %s requires confirmation but none was provided", params.Operation)
		}
		if err := confirm(ctx, target.Environment); err != nil {
			return model.TerraformResult{}, err
		}
	}
	if dispatch == nil {
		dispatch = kube.RunRemoteCommand
	}
	result, err := dispatch(req, script)
	dispatched.Stdout = result.Stdout
	dispatched.Stderr = result.Stderr
	if err != nil {
		return model.TerraformResult{}, fmt.Errorf("terraform %s: %w%s", params.Operation, err, kube.FormatRemoteCommandStderr(result.Stderr))
	}
	return dispatched, nil
}

// terraformDispatchScript embeds --confirm-environment only after the host-side
// gate approved: the in-pod process has no stdin.
func terraformDispatchScript(target terraformTarget, op model.TerraformOperation) string {
	args := []string{"erun", "terraform", string(op), "--tenant", core.ShellQuote(target.Tenant), "--environment", core.ShellQuote(target.Environment)}
	if requiresTerraformConfirmation(op) {
		args = append(args, "--confirm-environment", core.ShellQuote(target.Environment))
	}
	return strings.Join(args, " ")
}

func terraformEnvTypeLabel(envType string) string {
	if strings.TrimSpace(envType) == "" {
		return "unresolved"
	}
	return envType
}

// Remote envs reach here only from inside their own pod, so home is the PVC.
func terraformStateLocus(remoteWorktree bool) string {
	if remoteWorktree {
		return "this environment's own home PVC, resolved from inside its runtime pod — survives pod restarts"
	}
	return "this host's home directory"
}

func resolveTerraformPlan(params model.TerraformParams, target terraformTarget, store envstore.TerraformStore) (model.TerraformResult, []terraformStep, error) {
	op := params.Operation
	if err := validateTerraformOperation(op); err != nil {
		return model.TerraformResult{}, nil, err
	}
	projectRoot := strings.TrimSpace(params.ProjectRoot)
	if projectRoot == "" {
		return model.TerraformResult{}, nil, fmt.Errorf("project root is required")
	}

	tenant, environment, kubeContext, envType, remoteWorktree := target.Tenant, target.Environment, target.KubeContext, target.EnvType, target.RemoteWorktree

	paths, err := configpkg.LoadProjectPaths(projectRoot)
	if err != nil {
		return model.TerraformResult{}, nil, err
	}
	configuredBase := strings.TrimSpace(paths.Terraform)

	dir, varFile, source, err := resolveTerraformDir(projectRoot, tenant, environment, configuredBase)
	if err != nil {
		return model.TerraformResult{}, nil, err
	}

	workDir, err := resolveTerraformWorkDir(tenant, environment)
	if err != nil {
		return model.TerraformResult{}, nil, err
	}
	stateFile := filepath.Join(workDir, "terraform.tfstate")
	dataDir := filepath.Join(workDir, "data")
	planFile := filepath.Join(workDir, terraformPlanFile)
	lockReadonly := projectseam.FileExists(filepath.Join(dir, ".terraform.lock.hcl"))

	steps := buildTerraformSteps(op, varFile, stateFile, planFile, lockReadonly, params.ExtraArgs)
	result := model.TerraformResult{
		Tenant:                  tenant,
		Environment:             environment,
		Operation:               string(op),
		Namespace:               configpkg.KubernetesNamespaceName(tenant, environment),
		Directory:               dir,
		RootSource:              string(source),
		ConfiguredTerraformBase: configuredBase,
		VarFile:                 varFile,
		WorkDir:                 workDir,
		StateFile:               stateFile,
		DataDir:                 dataDir,
		EnvType:                 string(envType),
		StateLocus:              terraformStateLocus(remoteWorktree),
		KubernetesContext:       kubeContext,
		LockReadonly:            lockReadonly,
		LegacyStateInTree:       projectseam.FileExists(filepath.Join(dir, "terraform.tfstate")),
		Commands:                terraformStepCommands(steps),
		RequiresConfirmation:    requiresTerraformConfirmation(op),
	}
	return result, steps, nil
}

func validateTerraformOperation(op model.TerraformOperation) error {
	switch op {
	case model.TerraformInit, model.TerraformApply, model.TerraformPlan, model.TerraformDestroy:
		return nil
	default:
		return fmt.Errorf("unsupported terraform operation %q (want init, apply, plan, or destroy)", op)
	}
}

// resolveTerraformDir prefers paths.terraform, then terraform-<tenant>/, then
// <tenant>-devops/terraform-<tenant>/, and appends /<environment>.
func resolveTerraformDir(projectRoot, tenant, environment, configuredBase string) (dir, varFile string, source terraformRootSource, err error) {
	if base := configpkg.ResolveProjectPath(projectRoot, configuredBase); base != "" {
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

// resolveTerraformWorkDir sits beside ~/.erun/outputs on the /home/erun PVC.
func resolveTerraformWorkDir(tenant, environment string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir for terraform state: %w", err)
	}
	return filepath.Join(home, ".erun", "terraform", tenant, environment), nil
}

func detectTerraformVarFile(dir, environment string) string {
	candidate := environment + ".tfvars"
	if projectseam.FileExists(filepath.Join(dir, candidate)) {
		return candidate
	}
	return ""
}

func terraformRootNotFoundError(tenant, environment, configuredBase, configuredDir string) error {
	if strings.TrimSpace(configuredBase) != "" {
		return fmt.Errorf("no Terraform root at %s for %s/%s — the .erun/config.yaml paths.terraform base %q must contain a %s/ dir with its main.tf and %s.tfvars", configuredDir, tenant, environment, strings.TrimSpace(configuredBase), environment, environment)
	}
	return fmt.Errorf("no Terraform root for %s/%s — looked under terraform-%s/%s/ and %s-devops/terraform-%s/%s/; scaffold it with the erun-blueprint-platform skill, or create one with its main.tf and %s.tfvars",
		tenant, environment, tenant, environment, tenant, tenant, environment, environment)
}

func resolveTerraformScope(store envstore.TerraformStore, params model.TerraformParams) (tenant, environment string, err error) {
	tenant = strings.TrimSpace(params.Tenant)
	environment = strings.TrimSpace(params.Environment)
	if tenant == "" {
		config, _, err := store.LoadERunConfig()
		if err != nil {
			return "", "", err
		}
		tenant = strings.TrimSpace(config.DefaultTenant)
		if tenant == "" {
			return "", "", opentarget.ErrDefaultTenantNotConfigured
		}
	}
	if environment == "" {
		tenantConfig, _, err := store.LoadTenantConfig(tenant)
		if err != nil {
			return "", "", err
		}
		environment = strings.TrimSpace(tenantConfig.DefaultEnvironment)
		if environment == "" {
			return "", "", opentarget.ErrDefaultEnvironmentNotConfigured
		}
	}
	return tenant, environment, nil
}
