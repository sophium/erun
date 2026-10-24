package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/doctor"
)

func runDoctorInRuntime(ctx core.Context, promptRunner shared.PromptRunner, options doctorOptions) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// Config drift is more fundamental than remote-init, so it short-circuits.
	if options.syncConfig {
		return runRuntimeConfigSync(ctx, promptRunner, options, doctor.ResolveRuntimeConfigHome(homeDir))
	}
	inspection, err := doctor.InspectRemoteInit(homeDir, os.Getenv)
	if err != nil {
		return err
	}
	if err := doctor.WriteRemoteInitInspectionReport(ctx, inspection); err != nil {
		return err
	}
	if handled, err := writeRemoteInitShortCircuit(ctx, inspection); handled || err != nil {
		return err
	}
	proceed, err := confirmRemoteInitFinish(ctx, promptRunner, options)
	if err != nil || !proceed {
		return err
	}
	updated, err := doctor.RunRemoteInitFinish(ctx, inspection, doctor.RemoteInitFinishParams{
		HomeDir:            homeDir,
		RepositoryURL:      options.remoteRepositoryURL,
		CodeCommitSSHKeyID: options.codeCommitSSHKeyID,
		Sleep:              time.Sleep,
	}, remoteInitPromptFunc(promptRunner))
	if err != nil {
		return err
	}
	return writeRemoteInitFinishReport(ctx, updated)
}

func writeRemoteInitShortCircuit(ctx core.Context, inspection model.RemoteInitInspection) (bool, error) {
	if inspection.Complete() {
		_, err := fmt.Fprintln(ctx.Stdout, "Remote init is complete; nothing to finish.")
		return true, err
	}
	if len(inspection.MissingItems()) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "No missing remote-init artifacts detected, but the bootstrap marker is incomplete. Re-run `erun init --remote` from your local machine to refresh the marker.")
		return true, err
	}
	return false, nil
}

func confirmRemoteInitFinish(ctx core.Context, promptRunner shared.PromptRunner, options doctorOptions) (bool, error) {
	if options.finishRemoteInit {
		return true, nil
	}
	if ctx.DryRun || promptRunner == nil {
		_, err := fmt.Fprintln(ctx.Stdout, "Run `erun doctor --finish-remote-init` inside this pod to finish the missing steps.")
		return false, err
	}
	return doctorConfirm(ctx, promptRunner, "Finish missing remote-init steps now",
		"Re-run with --finish-remote-init to run it without a prompt.")
}

func remoteInitPromptFunc(promptRunner shared.PromptRunner) doctor.RemoteInitFinishPrompt {
	return func(label string) (string, error) {
		if promptRunner == nil {
			return "", errors.New("interactive prompt is unavailable; pass --remote-repository-url and --codecommit-ssh-key-id when applicable")
		}
		return doctorRemoteInitPrompt(promptRunner, label)
	}
}

func writeRemoteInitFinishReport(ctx core.Context, updated model.RemoteInitInspection) error {
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry-run: would finish remote init by running the steps traced above.")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Remote init finished."); err != nil {
		return err
	}
	return doctor.WriteRemoteInitInspectionReport(ctx, updated)
}

func doctorRemoteInitPrompt(run shared.PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		},
	}
	result, err := run(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}
