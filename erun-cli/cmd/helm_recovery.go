package cmd

import (
	"errors"
	"fmt"
	"io"
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
		if err := recover(pending.RecoveryParams(params.Verbosity, params.Stdout, params.Stderr)); err != nil {
			return err
		}
		return deploy(params)
	}
}

// confirmHelmReleaseRecovery answers the one prompt a locked release raises.
//
// A reader that went away is not an answer, and it is not a diagnosis either:
// a caller with no terminal (an orchestrator, a CI job, an agent, the desktop's
// piped shell) reads stdin at EOF, so letting the prompt's own EOF error
// replace the pending-release error turned "this release is locked, here is how
// to clear it" into a bare "EOF" -- a deploy that could not proceed reporting
// nothing an operator can act on. The pending error is returned instead (the
// caller re-reports it), with one line naming the step that did not run and how
// to run it without a prompt. `erun doctor` draws the same distinction for its
// own recovery prompts (see doctorConfirm); a real prompt failure still
// propagates.
func confirmHelmReleaseRecovery(run PromptRunner, pending *common.HelmReleasePendingOperationError, stderr io.Writer) (bool, error) {
	prompt := promptui.Prompt{
		Label:     helmReleaseRecoveryPromptLabel(pending),
		IsConfirm: true,
		Default:   "y",
	}

	// Non-interactive / CI callers auto-accept recovery without a TTY.
	if isTrueishEnv("ERUN_AUTO_RECOVER_HELM") {
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

// reportHelmRecoveryPromptUnanswered names the step that did not run and the
// command that runs it without a prompt. The pending error the caller re-reports
// carries the same remedy; this line is what tells a reader *why* the recovery
// they were not asked about is not happening.
func reportHelmRecoveryPromptUnanswered(stderr io.Writer, pending *common.HelmReleasePendingOperationError) {
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
