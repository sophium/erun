package terraform

import (
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
)

// terraformLockPlatforms match erun's deploy targets so one committed lock
// works with -lockfile=readonly on any pod architecture.
var terraformLockPlatforms = []string{"linux_amd64", "linux_arm64"}

type terraformStep struct {
	args    []string
	confirm bool
}

func executeTerraformSteps(ctx core.Context, result model.TerraformResult, steps []terraformStep, confirm seam.TerraformConfirmFunc, rfc2136Secret string) error {
	if err := os.MkdirAll(result.WorkDir, 0o755); err != nil {
		return fmt.Errorf("creating terraform state dir %s: %w", result.WorkDir, err)
	}
	// Fail with a recovery path when a lock-generating init meets a read-only tree.
	if result.Operation == string(model.TerraformInit) && !result.LockReadonly {
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

// buildTerraformSteps does not run init; the operator runs `erun terraform init`
// so plan/apply/destroy never write the lock into a read-only tree.
func buildTerraformSteps(op model.TerraformOperation, varFile, stateFile, planFile string, lockReadonly bool, extraArgs []string) []terraformStep {
	switch op {
	case model.TerraformInit:
		return buildTerraformInitSteps(stateFile, lockReadonly)
	case model.TerraformPlan:
		plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		return []terraformStep{{args: plan}}
	case model.TerraformDestroy:
		plan := append([]string{"plan", "-destroy", "-input=false"}, terraformVarFileArgs(varFile)...)
		plan = append(plan, extraArgs...)
		plan = append(plan, "-out", planFile)
		apply := terraformStep{args: []string{"apply", "-input=false", planFile}, confirm: true}
		return []terraformStep{{args: plan}, apply}
	case model.TerraformApply:
	}
	return buildTerraformApplySteps(varFile, planFile, extraArgs)
}

func buildTerraformApplySteps(varFile, planFile string, extraArgs []string) []terraformStep {
	// -check: the playbook tree is read-only in the release.
	fmtStep := terraformStep{args: []string{"fmt", "-check", "-recursive", ".."}}
	plan := append([]string{"plan", "-input=false"}, terraformVarFileArgs(varFile)...)
	plan = append(plan, extraArgs...)
	plan = append(plan, "-out", planFile)
	apply := terraformStep{args: []string{"apply", "-input=false", planFile}, confirm: true}
	return []terraformStep{fmtStep, {args: plan}, apply}
}

// buildTerraformInitSteps uses -lockfile=readonly when a lock is committed;
// otherwise it generates one with hashes for every deploy platform.
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

// terraformExtraEnv passes secrets via the environment so they never land in the trace.
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

// ensureTerraformTreeWritable reports a recovery path when the tree is read-only
// (a sourceless runtime env symlinks it into the image layer).
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
