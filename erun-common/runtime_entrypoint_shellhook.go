package eruncommon

import (
	"os"
	"strings"
)

// runtimeShellHook is sourced by every shell in the pod. It records CLI activity
// at each prompt, so an operator typing in a shell keeps the environment awake,
// and signals a dark terminal so libraries that honour COLORFGBG skip the OSC 11
// background-color query, whose reply would otherwise leak into the shell's stdin
// through the desktop's PTY.
const runtimeShellHook = `# Written by erun runtime-init at every container boot; edits are replaced.
export COLORFGBG='15;0'

__erun_record_cli_activity() {
    if [ -n "${ERUN_TENANT:-}" ] && [ -n "${ERUN_ENVIRONMENT:-}" ]; then
        command erun activity touch --tenant "${ERUN_TENANT}" --environment "${ERUN_ENVIRONMENT}" --kind cli >/dev/null 2>&1 || true
    fi
}

case ";${PROMPT_COMMAND:-};" in
    *";__erun_record_cli_activity;"*) ;;
    *) PROMPT_COMMAND="__erun_record_cli_activity${PROMPT_COMMAND:+;${PROMPT_COMMAND}}" ;;
esac
`

const (
	runtimeShellHookBegin = "# >>> erun shell hook >>>"
	runtimeShellHookEnd   = "# <<< erun shell hook <<<"
)

func describeRuntimeShellHook(boot runtimeBoot) string {
	return "write " + boot.path(".erun-shell-hook.bashrc") + " and source it from " + strings.Join(boot.runtimeShellProfiles(), ", ")
}

// runtimeShellProfiles are the startup files that source the hook: ~/.bashrc for
// interactive shells, ~/.profile for login shells (every `sh -lc` remote exec),
// and ~/.bash_profile only when one exists, since creating it would stop bash
// reading ~/.profile.
func (b runtimeBoot) runtimeShellProfiles() []string {
	profiles := []string{b.path(".bashrc"), b.path(".profile")}
	if _, err := os.Stat(b.path(".bash_profile")); err == nil {
		profiles = append(profiles, b.path(".bash_profile"))
	}
	return profiles
}

func installRuntimeShellHook(_ Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	hookFile := boot.path(".erun-shell-hook.bashrc")
	if err := os.WriteFile(hookFile, []byte(runtimeShellHook), 0o644); err != nil {
		return err
	}
	block := runtimeShellHookBegin + "\nif [ -r \"" + hookFile + "\" ]; then\n    . \"" + hookFile + "\"\nfi\n" + runtimeShellHookEnd + "\n"
	for _, profile := range boot.runtimeShellProfiles() {
		if err := upsertRuntimeShellHookBlock(profile, block); err != nil {
			return err
		}
	}
	return nil
}

// upsertRuntimeShellHookBlock replaces erun's managed block in a startup file,
// keeping every other line, and appends it when absent.
func upsertRuntimeShellHookBlock(path, block string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var kept []string
	inBlock := false
	for _, line := range strings.SplitAfter(string(existing), "\n") {
		switch strings.TrimRight(line, "\n") {
		case runtimeShellHookBegin:
			inBlock = true
			continue
		case runtimeShellHookEnd:
			inBlock = false
			continue
		}
		if !inBlock && line != "" {
			kept = append(kept, line)
		}
	}
	content := strings.Join(kept, "")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content+block), 0o644)
}
