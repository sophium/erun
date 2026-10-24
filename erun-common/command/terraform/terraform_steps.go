package terraform

import (
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// terraformLockPlatforms are the provider platforms `erun terraform init`
// records in a generated .terraform.lock.hcl. They match erun's deploy targets
// (build/deploy always produce linux/amd64 + linux/arm64), so one committed lock
// lets init run -lockfile=readonly on every env regardless of the pod's arch — a
// lock generated for only the local platform would fail a read-only init on a
// pod of the other architecture.
var terraformLockPlatforms = []string{"linux_amd64", "linux_arm64"}

type terraformStep struct {
	args    []string
	confirm bool
}

func executeTerraformSteps(ctx core.Context, result TerraformResult, steps []terraformStep, confirm TerraformConfirmFunc, rfc2136Secret string) error {
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
	extraEnv := terraformExtraEnv(result.DataDir, rfc2136Secret)
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
	case TerraformApply:
	}
	return buildTerraformApplySteps(varFile, planFile, extraArgs)
}

func buildTerraformApplySteps(varFile, planFile string, extraArgs []string) []terraformStep {
	// -check, not a rewrite: the playbook tree is read-only in the release.
	fmtStep := terraformStep{args: []string{"fmt", "-check", "-recursive", ".."}}
	plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
	plan = append(plan, extraArgs...)
	plan = append(plan, "-out", planFile)
	apply := terraformStep{args: []string{"apply", "-input=false", planFile}, confirm: true}
	return []terraformStep{fmtStep, {args: plan}, apply}
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
// Cloudflare API token and, when the resolved dns01_provider needs it, the
// RFC2136 TSIG secret the edge module's cert-manager DNS-01 solver needs.
// Both ride in the environment, never in argv, so neither lands in the trace.
func terraformExtraEnv(dataDir, rfc2136Secret string) []string {
	env := []string{"TF_DATA_DIR=" + dataDir}
	if token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); token != "" {
		env = append(env, "TF_VAR_cloudflare_api_token="+token)
	}
	if rfc2136Secret != "" {
		env = append(env, "TF_VAR_rfc2136_tsig_secret="+rfc2136Secret)
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

func execTerraformStep(ctx core.Context, dir string, extraEnv, args []string) error {
	cmd := core.Command("terraform", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	capture := ctx.ToolCapture()
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}
