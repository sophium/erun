package shell

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/ailaunch"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-common/primitive/open/podwait"
	"github.com/sophium/erun/erun-common/primitive/open/runtimeref"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
	"gopkg.in/yaml.v3"
)

var (
	ErrShellReattachDeploy   = errors.New("remote shell requested deploy handoff and reattach")
	ErrShellPodReplaced      = errors.New("remote shell pod was replaced; reattach")
	ErrShellSessionTakenOver = errors.New("remote session was re-attached in another ERun window")

	openUserHomeDir = os.UserHomeDir
)

type ShellLaunchPreview struct {
	// SeedArgs streams the SSH key on stdin; the key is never in these args.
	SeedArgs []string
	WaitArgs []string
	ExecArgs []string
	Script   string
}

// EnsureLocalPortRangePersisted freezes the port choice so unrelated tenants
// cannot reshuffle it.
func EnsureLocalPortRangePersisted(ctx core.Context, saveEnvConfig func(string, configpkg.EnvConfig) error, result configpkg.OpenResult) (configpkg.OpenResult, error) {
	if result.EnvConfig.LocalPortRangeStart > 0 {
		return result, nil
	}
	rangeStart := result.LocalPorts.RangeStart
	if rangeStart == 0 {
		return result, fmt.Errorf("local port range is not resolved for %s/%s", result.Tenant, result.Environment)
	}
	updated := result.EnvConfig
	updated.LocalPortRangeStart = rangeStart
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("config: would assign localportrangestart=%d to %s/%s", rangeStart, result.Tenant, result.Environment))
		result.EnvConfig = updated
		return result, nil
	}
	if saveEnvConfig == nil {
		return result, fmt.Errorf("persist local port range: env config storage is not wired")
	}
	if err := saveEnvConfig(result.Tenant, updated); err != nil {
		return result, fmt.Errorf("persist local port range: %w", err)
	}
	ctx.Trace(fmt.Sprintf("config: assigned localportrangestart=%d to %s/%s", rangeStart, result.Tenant, result.Environment))
	result.EnvConfig = updated
	return result, nil
}

func LocalShellSetupScript(result configpkg.OpenResult) string {
	commands := []string{
		fmt.Sprintf("kubectl config use-context %s >/dev/null", core.ShellQuote(strings.TrimSpace(result.EnvConfig.KubernetesContext))),
		fmt.Sprintf("kubectl config set-context --current --namespace=%s >/dev/null", core.ShellQuote(configpkg.KubernetesNamespaceName(result.Tenant, result.Environment))),
		fmt.Sprintf("cd %s", core.ShellQuote(result.RepoPath)),
	}
	return strings.Join(commands, " &&\n") + "\n"
}

func ExecShell(req configpkg.ShellLaunchParams) error {
	// Stdin keeps the key out of every argv and exec audit log.
	if err := seedRemoteSSHKey(req); err != nil {
		return err
	}

	script, err := buildRemoteShellScript(req)
	if err != nil {
		return err
	}

	// Not Command(): its CREATE_NO_WINDOW detaches the console, so `exec -it`
	// gets no TTY.
	cmd := core.NewExecCommand("kubectl", kubectlExecArgs(req, script)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if isShellReattachDeployExit(err) {
			return ErrShellReattachDeploy
		}
		if isShellTakenOverExit(err) {
			return ErrShellSessionTakenOver
		}
		if isShellReplacementExit(err) && podwait.ShellReplacementPodReady(req, opentarget.RunOpenKubectl) {
			return ErrShellPodReplaced
		}
		return podwait.EnrichShellDeploymentError(req, err, opentarget.RunOpenKubectl)
	}
	return nil
}

func PreviewShellLaunch(req configpkg.ShellLaunchParams) (ShellLaunchPreview, error) {
	script, err := buildRemoteShellScript(req)
	if err != nil {
		return ShellLaunchPreview{}, err
	}

	keyMaterial, err := remoteShellPrivateKeyMaterial(req)
	if err != nil {
		return ShellLaunchPreview{}, err
	}
	var seedArgs []string
	if strings.TrimSpace(keyMaterial) != "" {
		seedArgs = RemoteSSHKeySeedArgs(req)
	}

	return ShellLaunchPreview{
		SeedArgs: seedArgs,
		WaitArgs: cloud.KubectlDeploymentWaitArgs(req),
		ExecArgs: kubectlExecArgs(req, script),
		Script:   script,
	}, nil
}

func kubectlExecArgs(req configpkg.ShellLaunchParams, script string) []string {
	args := configpkg.KubectlTargetArgs(req)
	args = append(args, "exec", "-it")
	args = append(args, "deployment/"+configpkg.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

func buildRemoteShellScript(req configpkg.ShellLaunchParams) (string, error) {
	config, err := remoteShellConfigForRequest(req)
	if err != nil {
		return "", err
	}
	workdir := core.ShellQuote(config.Workdir)
	scriptLines := remoteShellBaseScriptLines(req, config, workdir, core.ShellQuote(req.Title))
	gitLines, err := remoteShellGitSeedLines(req, workdir)
	if err != nil {
		return "", err
	}
	if len(gitLines) > 0 {
		scriptLines = append(gitLines, scriptLines[1:]...)
	}
	return strings.Join(scriptLines, "\n"), nil
}

type remoteShellConfig struct {
	Workdir    string
	ToolYAML   string
	TenantYAML string
	EnvYAML    string
}

func remoteShellConfigForRequest(req configpkg.ShellLaunchParams) (remoteShellConfig, error) {
	remoteWorkdir := runtimeref.RemoteWorktreePath(req)

	tenantConfig, err := yaml.Marshal(model.TenantConfig{
		Name:               req.Tenant,
		DefaultEnvironment: req.Environment,
	})
	if err != nil {
		return remoteShellConfig{}, err
	}
	toolConfig, err := yaml.Marshal(configpkg.ERunConfig{
		DefaultTenant: req.Tenant,
	})
	if err != nil {
		return remoteShellConfig{}, err
	}
	envConfig, err := yaml.Marshal(configpkg.EnvConfig{
		Name:               req.Environment,
		LocalRepoPath:      remoteWorkdir,
		KubernetesContext:  req.KubernetesContext,
		Type:               req.Type,
		ManagedCloud:       req.ManagedCloud,
		CloudProviderAlias: req.CloudProviderAlias,
		Idle:               req.Idle,
	})
	if err != nil {
		return remoteShellConfig{}, err
	}

	return remoteShellConfig{
		Workdir:    remoteWorkdir,
		ToolYAML:   string(toolConfig),
		TenantYAML: string(tenantConfig),
		EnvYAML:    string(envConfig),
	}, nil
}

// seedRemoteConfigLines never overwrites the pod's own config, which carries
// chart-injected fields the host snapshot lacks.
func seedRemoteConfigLines(quotedPath, content string) string {
	return fmt.Sprintf("if [ ! -f %s ]; then\n  mkdir -p \"$(dirname %s)\"\n  cat > %s <<'EOF'\n%s\nEOF\nfi", quotedPath, quotedPath, quotedPath, content)
}

func remoteShellBaseScriptLines(req configpkg.ShellLaunchParams, config remoteShellConfig, workdir, title string) []string {
	markerDir := fmt.Sprintf("$HOME/.erun/%s/%s", req.Tenant, req.Environment)
	bashrcPath := markerDir + "/bashrc"
	requestPath := markerDir + "/shell-request"
	lines := []string{
		"set -eu",
		"export COLORTERM=truecolor",
		"export COLORFGBG='15;0'",
		fmt.Sprintf("mkdir -p %s", workdir),
		fmt.Sprintf("cd %s", workdir),
		"config_home=\"${XDG_CONFIG_HOME:-$HOME/.config}\"",
		seedRemoteConfigLines("\"$config_home/erun/config.yaml\"", config.ToolYAML),
		seedRemoteConfigLines(fmt.Sprintf("\"$config_home/erun/%s/config.yaml\"", req.Tenant), config.TenantYAML),
		seedRemoteConfigLines(fmt.Sprintf("\"$config_home/erun/%s/%s/config.yaml\"", req.Tenant, req.Environment), config.EnvYAML),
		fmt.Sprintf("mkdir -p \"%s\"", markerDir),
		fmt.Sprintf("cat > \"%s\" <<'EOF'\nexport ERUN_SHELL_HOST=%s\nerun() {\n  if [ \"${1:-}\" = \"deploy\" ] && [ \"$#\" -eq 1 ] && [ -n \"${ERUN_SHELL_REQUEST_FILE:-}\" ]; then\n    : > \"$ERUN_SHELL_REQUEST_FILE\"\n    exit 0\n  fi\n  command erun \"$@\"\n}\nEOF", bashrcPath, title),
		fmt.Sprintf("printf '\\033]0;%s\\007'", title),
		fmt.Sprintf("request_file=\"%s\"", requestPath),
		"rm -f \"$request_file\"",
		"export ERUN_SHELL_REQUEST_FILE=\"$request_file\"",
		"shell_status=0",
	}
	lines = append(lines, remoteShellLaunchLines(req, bashrcPath, markerDir)...)
	return append(lines,
		fmt.Sprintf("if [ -e \"$request_file\" ]; then rm -f \"$request_file\"; exit %d; fi", appsession.RemoteShellReattachDeployExitCode),
		"rm -f \"$request_file\"",
		"exit \"$shell_status\"",
	)
}

// remoteShellLaunchLines keeps the bare CLI path a single bash invocation, byte
// for byte; AppSession wraps it in dtach.
func remoteShellLaunchLines(req configpkg.ShellLaunchParams, bashrcPath, markerDir string) []string {
	if strings.TrimSpace(req.AppSession) == "" {
		return []string{fmt.Sprintf("/bin/bash --rcfile \"%s\" -i || shell_status=$?", bashrcPath)}
	}
	id := core.SanitizeForFilename(req.AppSession)
	socket := appsession.RemoteAppSessionSocketPath(req.Tenant, req.Environment, id)
	launchScript := fmt.Sprintf("%s/launch-%s.sh", markerDir, id)
	body := strings.Join(RemoteSessionLauncherBody(req, bashrcPath), "\n")
	redraw := "ctrl_l"
	if req.AI {
		redraw = "winch"
	}
	lines := []string{
		fmt.Sprintf("mkdir -p \"%s\"", activity.RemoteAppSessionSocketDir),
		fmt.Sprintf("cat > \"%s\" <<'EOF'\n%s\nEOF", launchScript, body),
	}
	return append(lines, appsession.RemoteAppSessionAttachLines(socket, redraw, fmt.Sprintf("/bin/bash \"%s\"", launchScript))...)
}

// RemoteSessionLauncherBody is the dtach create-time program, so its prelude
// never repeats on reattach.
func RemoteSessionLauncherBody(req configpkg.ShellLaunchParams, bashrcPath string) []string {
	var body []string
	if req.Contribute {
		body = append(body,
			"export PATH=\"$HOME/.erun/contribute/bin:$PATH\"",
			"cd \"$HOME/git/erun\"",
		)
	}
	if req.AI {
		body = append(body, ailaunch.AISessionLaunchLines(req.AITool, req.Claude, req.Gateway, req.Tenant, req.Environment)...)
	}
	return append(body, fmt.Sprintf("exec /bin/bash --rcfile \"%s\" -i", bashrcPath))
}

func isShellReattachDeployExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == appsession.RemoteShellReattachDeployExitCode
}

func isShellTakenOverExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == appsession.RemoteShellTakenOverExitCode
}

func isShellReplacementExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == 137
}
