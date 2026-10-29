package helmrecovery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/manifoldco/promptui"
	promptpkg "github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	recoverypkg "github.com/sophium/erun/erun-common/primitive/deploy/recovery"
)

func WrapHelmDeployWithReleaseRecovery(promptRunner promptpkg.PromptRunner, deploy helmdeploy.HelmChartDeployerFunc, recoverRelease helmdeploy.HelmReleaseRecovererFunc) helmdeploy.HelmChartDeployerFunc {
	if deploy == nil {
		return nil
	}
	if recoverRelease == nil {
		recoverRelease = recoverypkg.ClearHelmReleasePendingOperation
	}

	return func(params helmdeploy.HelmDeployParams) error {
		err := deploy(params)
		if err == nil {
			return nil
		}

		var pending *recoverypkg.HelmReleasePendingOperationError
		if !errors.As(err, &pending) || promptRunner == nil {
			return err
		}

		ok, promptErr := confirmHelmReleaseRecovery(promptRunner, pending, params.Stderr)
		if promptErr != nil {
			return promptErr
		}
		if !ok {
			return err
		}

		if params.Stderr != nil {
			_, _ = fmt.Fprintf(params.Stderr, "clearing pending helm metadata: %s\n", pending.RecoveryCommand())
		}
		if err := recoverRelease(pending.RecoveryParams(params.Verbosity, params.Stdout, params.Stderr)); err != nil {
			return err
		}
		return deploy(params)
	}
}

// confirmHelmReleaseRecovery answers the locked-release prompt. On EOF it returns the
// pending error plus a hint, not a bare "EOF"; real prompt failures still propagate.
func confirmHelmReleaseRecovery(run promptpkg.PromptRunner, pending *recoverypkg.HelmReleasePendingOperationError, stderr io.Writer) (bool, error) {
	prompt := promptui.Prompt{
		Label:     helmReleaseRecoveryPromptLabel(pending),
		IsConfirm: true,
		Default:   "y",
	}

	if IsTrueishEnv("ERUN_AUTO_RECOVER_HELM") {
		return true, nil
	}

	result, err := run(prompt)
	if err != nil {
		switch {
		case errors.Is(err, promptui.ErrInterrupt):
			return false, fmt.Errorf("helm release recovery interrupted")
		case errors.Is(err, promptui.ErrAbort):
			return false, nil
		case errors.Is(err, promptui.ErrEOF):
			reportHelmRecoveryPromptUnanswered(stderr, pending)
			return false, nil
		}
		return false, err
	}
	if strings.TrimSpace(result) == "" {
		return true, nil
	}
	return strings.EqualFold(strings.TrimSpace(result), "y"), nil
}

// reportHelmRecoveryPromptUnanswered explains why recovery was skipped and how to run it without a prompt.
func reportHelmRecoveryPromptUnanswered(stderr io.Writer, pending *recoverypkg.HelmReleasePendingOperationError) {
	if stderr == nil {
		return
	}
	target := ""
	if pending != nil {
		target = strings.TrimSpace(pending.Tenant) + " " + strings.TrimSpace(pending.Environment)
		target = strings.TrimSpace(target)
	}
	recovery := "`erun doctor --clear-pending-helm <tenant> <environment>`"
	if target != "" {
		recovery = "`erun doctor --clear-pending-helm " + target + "`"
	}
	_, _ = fmt.Fprintf(stderr, "helm release recovery not run: stdin reached EOF before the prompt could be answered. %s\n", recovery)
}

func IsTrueishEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func helmReleaseRecoveryPromptLabel(pending *recoverypkg.HelmReleasePendingOperationError) string {
	if pending == nil {
		return "clear pending Helm release metadata and retry deploy"
	}
	label := fmt.Sprintf("clear pending Helm metadata for release %s", pending.ReleaseName)
	if strings.TrimSpace(pending.Namespace) != "" {
		label += " from namespace " + strings.TrimSpace(pending.Namespace)
	}
	if strings.TrimSpace(pending.KubernetesContext) != "" {
		label += " in context " + strings.TrimSpace(pending.KubernetesContext)
	}
	return label + " and retry deploy"
}
