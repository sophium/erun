package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func wrapHelmDeployWithReleaseRecovery(promptRunner PromptRunner, deploy common.HelmChartDeployerFunc, recover common.HelmReleaseRecovererFunc) common.HelmChartDeployerFunc {
	if deploy == nil {
		return nil
	}
	if recover == nil {
		recover = common.ClearHelmReleasePendingOperation
	}

	return func(params common.HelmDeployParams) error {
		err := deploy(params)
		if err == nil {
			return nil
		}

		var pending *common.HelmReleasePendingOperationError
		if !errors.As(err, &pending) || promptRunner == nil {
			return err
		}

		ok, promptErr := confirmHelmReleaseRecovery(promptRunner, pending)
		if promptErr != nil {
			return promptErr
		}
		if !ok {
			return err
		}

		if params.Stderr != nil {
			_, _ = fmt.Fprintf(params.Stderr, "clearing pending helm metadata: %s\n", pending.RecoveryCommand())
		}
		if err := recover(pending.RecoveryParams(params.Verbosity, params.Stdout, params.Stderr)); err != nil {
			return err
		}
		return deploy(params)
	}
}

func confirmHelmReleaseRecovery(run PromptRunner, pending *common.HelmReleasePendingOperationError) (bool, error) {
	prompt := promptui.Prompt{
		Label:     helmReleaseRecoveryPromptLabel(pending),
		IsConfirm: true,
		Default:   "y",
	}

	// CI / non-interactive callers can opt in to the default-accept path
	// by setting ERUN_AUTO_RECOVER_HELM=1, which mirrors the prompt's
	// own Default="y" behavior without requiring a TTY.
	if isTrueishEnv("ERUN_AUTO_RECOVER_HELM") {
		return true, nil
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("helm release recovery interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return false, nil
		}
		return false, err
	}
	if strings.TrimSpace(result) == "" {
		return true, nil
	}
	return strings.EqualFold(strings.TrimSpace(result), "y"), nil
}

// isTrueishEnv returns true when the named env var is set to a value that
// callers commonly use to mean "yes": 1, true, yes, on (case-insensitive).
// Empty / unset / 0 / false / no / off all return false.
func isTrueishEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func helmReleaseRecoveryPromptLabel(pending *common.HelmReleasePendingOperationError) string {
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
