package open

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"gopkg.in/yaml.v3"
)

var (
	ErrShellReattachDeploy   = errors.New("remote shell requested deploy handoff and reattach")
	ErrShellPodReplaced      = errors.New("remote shell pod was replaced; reattach")
	ErrShellSessionTakenOver = errors.New("remote session was re-attached in another ERun window")

	openUserHomeDir = os.UserHomeDir
)

const (
	RemoteShellReattachDeployExitCode = 75
	RemoteShellTakenOverExitCode      = 76
)

// ShellSessionTakenOverNotice is the stable line `erun open` prints when its
// persistent session is re-attached from another ERun window (dtach takeover
// semantics: the session keeps running, this viewer is detached). The desktop
// matches this exact line to stop its reconnect loop instead of stealing the
// session back, so treat the wording as a public contract.
const ShellSessionTakenOverNotice = "open: session re-attached in another ERun window"

// AISessionAttachOutcome is why an attach to a persistent session ended, from
// the attaching client's point of view — not merely that the underlying
// process exited, but which of the reattach contract's outcomes
// (RemoteAppSessionAttachLines) it exited for, or that no exit signal could be
// read at all. A WSS gateway bridging this contract to a remote client must
// report one of these explicitly rather than letting every non-success case
// read as a plain disconnect.
type AISessionAttachOutcome string

const (
	// AISessionAttachOutcomeTakenOver: a different attach claimed session
	// ownership while this one was connected (the owner-id check
	// RemoteAppSessionAttachLines writes). The session itself is still
	// running; only this viewer was evicted, and it must be told so plainly
	// — see ShellSessionTakenOverNotice — rather than reading as a network
	// drop.
	AISessionAttachOutcomeTakenOver AISessionAttachOutcome = "taken-over"
	// AISessionAttachOutcomeDeployReattach: the shell asked for a deploy
	// handoff (ErrShellReattachDeploy's exit code); the caller should
	// reattach once the new pod is ready.
	AISessionAttachOutcomeDeployReattach AISessionAttachOutcome = "deploy-reattach"
	// AISessionAttachOutcomeEnded: the session's own program exited on its
	// own (any exit code other than the two above) — there is nothing left
	// to reattach to.
	AISessionAttachOutcomeEnded AISessionAttachOutcome = "ended"
	// AISessionAttachOutcomeUnknown: the connection closed with no
	// interpretable exit status — a network drop, a proxy timeout, a killed
	// pod. Never collapse this into Ended or TakenOver: the caller genuinely
	// does not know which happened, and reporting a guess as fact is the
	// exact failure root AGENTS.md's "an unknown must not render as a
	// definite value" forbids.
	AISessionAttachOutcomeUnknown AISessionAttachOutcome = "unknown"
)

// ResolveAISessionAttachOutcome classifies an attach's own exit status — the
// underlying dtach-wrapping shell script's exit code, however the caller ran
// it (ssh, kubectl exec, or client-go's remotecommand) — into the outcome a
// client must show. exitCodeKnown distinguishes "the process exited with code
// 0" from "no exit status was observed at all"; a false exitCodeKnown always
// resolves to Unknown, never Ended, regardless of exitCode's value.
func ResolveAISessionAttachOutcome(exitCode int, exitCodeKnown bool) AISessionAttachOutcome {
	if !exitCodeKnown {
		return AISessionAttachOutcomeUnknown
	}
	switch exitCode {
	case RemoteShellTakenOverExitCode:
		return AISessionAttachOutcomeTakenOver
	case RemoteShellReattachDeployExitCode:
		return AISessionAttachOutcomeDeployReattach
	default:
		return AISessionAttachOutcomeEnded
	}
}

// ParseRemoteAppSessionIDs extracts this tenant+env's persistent desktop
// session ids so the desktop can rebuild tabs for sessions another ERun
// window created.
func ParseRemoteAppSessionIDs(tenant, environment, lsOutput string) []string {
	prefix := core.SanitizeForFilename(tenant) + "-" + core.SanitizeForFilename(environment) + "-"
	var ids []string
	for _, raw := range strings.Split(lsOutput, "\n") {
		name := strings.TrimSpace(raw)
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".dtach") {
			continue
		}
		if id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".dtach"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

type ShellLaunchPreview struct {
	// SeedArgs is the kubectl exec that streams the SSH private key to the pod
	// on stdin — the key is never in these args. Empty when the env has no key
	// to seed.
	SeedArgs []string
	WaitArgs []string
	ExecArgs []string
	Script   string
}

type ShellLauncherFunc func(configpkg.ShellLaunchParams) error

func OpenParamsForArgs(args []string) (shared.OpenParams, error) {
	switch len(args) {
	case 0:
		return shared.OpenParams{
			UseDefaultTenant:      true,
			UseDefaultEnvironment: true,
		}, nil
	case 1:
		return shared.OpenParams{
			Environment:      args[0],
			UseDefaultTenant: true,
		}, nil
	case 2:
		return shared.OpenParams{
			Tenant:      args[0],
			Environment: args[1],
		}, nil
	default:
		return shared.OpenParams{}, fmt.Errorf("accepts 0 to 2 arg(s), received %d", len(args))
	}
}

func loadOpenDefaultEnvironment(store configpkg.OpenStore, tenant string) (string, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return "", shared.ErrDefaultEnvironmentNotConfigured
	}
	if err != nil {
		return "", err
	}
	if tenantConfig.DefaultEnvironment == "" {
		return "", shared.ErrDefaultEnvironmentNotConfigured
	}
	return tenantConfig.DefaultEnvironment, nil
}

func InitParamsForOpenArgs(store configpkg.OpenStore, args []string) (BootstrapInitParams, error) {
	params, err := OpenParamsForArgs(args)
	if err != nil {
		return BootstrapInitParams{}, err
	}
	return InitParamsForOpenTarget(store, params)
}

func InitParamsForOpenTarget(store configpkg.OpenStore, params shared.OpenParams) (BootstrapInitParams, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)

	switch {
	case tenant != "" && environment != "":
		return BootstrapInitParams{Tenant: tenant, Environment: environment}, nil
	case tenant != "":
		return BootstrapInitParams{Tenant: tenant}, nil
	case environment != "":
		return initParamsForOpenEnvironmentOnly(store, environment)
	}

	return initParamsForOpenDefaults(store)
}

func initParamsForOpenEnvironmentOnly(store configpkg.OpenStore, environment string) (BootstrapInitParams, error) {
	resolvedTenant, err := shared.LoadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, shared.ErrDefaultTenantNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return BootstrapInitParams{Environment: environment, ResolveTenant: true}, nil
		}
		return BootstrapInitParams{}, err
	}
	return BootstrapInitParams{Tenant: resolvedTenant, Environment: environment}, nil
}

func initParamsForOpenDefaults(store configpkg.OpenStore) (BootstrapInitParams, error) {
	resolvedTenant, err := shared.LoadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, shared.ErrDefaultTenantNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return BootstrapInitParams{ResolveTenant: true}, nil
		}
		return BootstrapInitParams{}, err
	}

	defaultEnvironment, err := loadOpenDefaultEnvironment(store, resolvedTenant)
	if err != nil {
		if errors.Is(err, shared.ErrDefaultEnvironmentNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return BootstrapInitParams{Tenant: resolvedTenant}, nil
		}
		return BootstrapInitParams{}, err
	}

	return BootstrapInitParams{Tenant: resolvedTenant, Environment: defaultEnvironment}, nil
}

func ResolveOpen(store configpkg.OpenStore, params shared.OpenParams) (configpkg.OpenResult, error) {
	return shared.ResolveOpenWithFinder(store, configpkg.FindProjectRoot, params)
}

// EnsureLocalPortRangePersisted freezes the resolver's port choice into the
// env config so future opens stay stable against alphabetical reshuffles of
// unrelated tenants/envs.
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

func WaitForShellDeployment(req configpkg.ShellLaunchParams) error {
	if configpkg.CurrentExecutionMode(kube.KubectlDeploymentWaitExecutionOperation) == configpkg.ExecutionModeLibrary {
		if err := kube.LibraryWaitForDeploymentAvailable(req.KubernetesContext, req.Namespace, configpkg.RuntimeReleaseName(req.Tenant), cloud.DefaultShellLaunchWaitTimeout); err != nil {
			return enrichShellDeploymentError(req, err, shared.RunOpenKubectl)
		}
		return nil
	}
	if err := shared.RunOpenKubectl(cloud.KubectlDeploymentWaitArgs(req), io.Discard, os.Stderr); err != nil {
		return enrichShellDeploymentError(req, err, shared.RunOpenKubectl)
	}
	return nil
}

func ExecShell(req configpkg.ShellLaunchParams) error {
	// Stream the SSH private key to the pod on stdin before the interactive
	// shell so it never appears in any kubectl exec argv (laptop `ps`, the
	// pod's /proc/<pid>/cmdline, or cluster exec audit logs).
	if err := seedRemoteSSHKey(req); err != nil {
		return err
	}

	script, err := buildRemoteShellScript(req)
	if err != nil {
		return err
	}

	// The interactive shell needs kubectl's `exec -it` to allocate a TTY, which
	// requires inheriting this process's console. Command() applies
	// CREATE_NO_WINDOW (to stop background children of the windowless desktop app
	// flashing a console) — but that detaches the child from the console, so
	// kubectl reports "Unable to use a TTY", falls back to non-interactive, and
	// the pod shell never renders a prompt (the env looks like it won't open).
	// erun open already runs inside the ERun tab's ConPTY, so inheriting that
	// console flashes nothing. Use newExecCommand to keep the ERUN_KUBECTL_BIN
	// test seam without the console-detaching flag.
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
		if isShellReplacementExit(err) && shellReplacementPodReady(req, shared.RunOpenKubectl) {
			return ErrShellPodReplaced
		}
		return enrichShellDeploymentError(req, err, shared.RunOpenKubectl)
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

func RemoteShellWorktreePath(req configpkg.ShellLaunchParams) string {
	return remoteWorktreePath(req)
}

func RemoteWorktreePathForRepoName(repoName string) string {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		repoName = "worktree"
	}
	return path.Join("/home", "erun", "git", repoName)
}

// APIDeploymentName is the erun-backend-api chart's Deployment/Service resource
// name (<prefix>-api, per TenantResourcePrefix). It is templated from
// `.Values.tenant`, not from the chart's Helm release name
// (publishedComponentReleaseName's `<tenant>-backend-api`), so callers that
// need to address the running pod or service must derive it from here rather
// than from the release name.
func APIDeploymentName(tenant string) string {
	return shared.TenantResourcePrefix(tenant) + "-api"
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
	remoteWorkdir := remoteWorktreePath(req)

	tenantConfig, err := yaml.Marshal(configpkg.TenantConfig{
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

// seedRemoteConfigLines writes a config file in the pod only when it is not
// already there. The runtime pod builds its own config from the chart-injected
// ERUN_* values, which carry fields this host-side snapshot has no source for —
// the marked registries, the runtime registry, and the build-script policy the
// in-pod build reads. Overwriting it would silently narrow the pod's config to
// the host's subset, so opening a shell seeds only what is missing, which is
// what a pod whose chart injected nothing still needs.
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
		fmt.Sprintf("if [ -e \"$request_file\" ]; then rm -f \"$request_file\"; exit %d; fi", RemoteShellReattachDeployExitCode),
		"rm -f \"$request_file\"",
		"exit \"$shell_status\"",
	)
}

// remoteShellLaunchLines runs the interactive shell. The bare CLI path (no
// AppSession) must stay the original single bash invocation byte for byte;
// with AppSession it wraps the shell in a reattachable dtach session so a
// disconnect detaches instead of spawning a parallel chain.
func remoteShellLaunchLines(req configpkg.ShellLaunchParams, bashrcPath, markerDir string) []string {
	if strings.TrimSpace(req.AppSession) == "" {
		return []string{fmt.Sprintf("/bin/bash --rcfile \"%s\" -i || shell_status=$?", bashrcPath)}
	}
	id := core.SanitizeForFilename(req.AppSession)
	socket := RemoteAppSessionSocketPath(req.Tenant, req.Environment, id)
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
	return append(lines, RemoteAppSessionAttachLines(socket, redraw, fmt.Sprintf("/bin/bash \"%s\"", launchScript))...)
}

// RemoteAppSessionAttachLines returns the sh lines that attach-or-create
// (dtach -A) a persistent session at socket and take ownership of it from any
// other viewer, evicting them rather than sharing the pty. This is the
// takeover half of the reattach contract erun-cli's own shell tabs already
// run under (the session keeps running, an evicted viewer only loses its own
// view, and dtach keeps no scrollback for one to recover it from) — exported so a caller outside
// erun-cli/erun-common (the WSS session-attach gateway) can reuse it instead
// of reimplementing the owner-id handoff. launchCommand runs
// only the first time the session is created; a reattach connects to
// whatever it is already running.
//
// redraw selects dtach's -r repaint trigger: "winch" forces a full repaint on
// attach (needed for a main-screen TUI like Claude that ignores a bare ^L),
// "ctrl_l" is quieter and right for an ordinary shell — see
// remoteShellLaunchLines's own comment for why the two tab kinds need
// different triggers.
func RemoteAppSessionAttachLines(socket, redraw, launchCommand string) []string {
	owner := strings.TrimSuffix(socket, ".dtach") + ".owner"
	lines := []string{
		// Take over the session from any other ERun window (detach-elsewhere-
		// and-reattach-here): claim ownership, then detach
		// other viewers by killing their dtach clients. The master — which
		// owns the running shell/claude — is never touched, and when it
		// cannot be identified no one is kicked. Kicked wrappers find a
		// foreign owner id below and exit 76 so their window reports the
		// handover instead of reconnecting into a tug-of-war.
		"attach_id=\"$$-$(date +%s)\"",
		fmt.Sprintf("printf '%%s' \"$attach_id\" > \"%s\"", owner),
	}
	lines = append(lines, activity.RemoteAppSessionMasterScanLines(socket)...)
	return append(lines,
		fmt.Sprintf("if [ -S \"%s\" ] && [ -n \"$master_pid\" ]; then for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do if [ \"$dtach_pid\" != \"$master_pid\" ] && grep -qF \"%s\" \"/proc/$dtach_pid/cmdline\" 2>/dev/null; then kill \"$dtach_pid\" 2>/dev/null || true; fi; done; fi", socket, socket),
		fmt.Sprintf("dtach -A \"%s\" -r %s %s || shell_status=$?", socket, redraw, launchCommand),
		fmt.Sprintf("if [ \"$(cat \"%s\" 2>/dev/null)\" != \"$attach_id\" ]; then exit %d; fi", owner, RemoteShellTakenOverExitCode),
	)
}

// RemoteAppSessionSocketPath returns the dtach socket path for a given
// tenant/environment/session id -- exported so a caller running inside the
// pod (the erun-mcp WSS attach edge) can address the same socket
// RemoteAppSessionAttachLines' own script builds, without reimplementing the
// naming.
func RemoteAppSessionSocketPath(tenant, environment, id string) string {
	return fmt.Sprintf("%s/%s-%s-%s.dtach", activity.RemoteAppSessionSocketDir, core.SanitizeForFilename(tenant), core.SanitizeForFilename(environment), core.SanitizeForFilename(id))
}

// RemoteAppSessionEndScript returns the sh script that permanently ends a
// persistent desktop session. The desktop runs it only when the user
// explicitly closes a terminal tab; closing the env or quitting the app merely
// detach and leave the session running for the next attach.
func RemoteAppSessionEndScript(tenant, environment, id string) string {
	socket := RemoteAppSessionSocketPath(tenant, environment, id)
	lines := activity.RemoteAppSessionMasterScanLines(socket)
	lines = append(lines,
		"if [ -n \"$master_pid\" ]; then kill \"$master_pid\" 2>/dev/null || true; fi",
		fmt.Sprintf("rm -f \"%s\" \"%s\"", socket, strings.TrimSuffix(socket, ".dtach")+".owner"),
	)
	return strings.Join(lines, "\n")
}

// RemoteSessionLauncherBody is the dtach session's create-time program: the
// per-tab prelude (run once on create) followed by the interactive shell. A
// reattach connects to whatever this program is running, so the prelude — and
// in particular the AI tool launch — never repeats.
func RemoteSessionLauncherBody(req configpkg.ShellLaunchParams, bashrcPath string) []string {
	var body []string
	if req.Contribute {
		// Match the desktop's former contribute prelude.
		body = append(body,
			"export PATH=\"$HOME/.erun/contribute/bin:$PATH\"",
			"cd \"$HOME/git/erun\"",
		)
	}
	if req.AI {
		body = append(body, AISessionLaunchLines(req.AITool, req.Claude, req.Gateway, req.Tenant, req.Environment)...)
	}
	return append(body, fmt.Sprintf("exec /bin/bash --rcfile \"%s\" -i", bashrcPath))
}

func remoteWorktreePath(req configpkg.ShellLaunchParams) string {
	return RemoteWorktreePathForRepoName(remoteWorktreeRepoName(req))
}

func remoteWorktreeRepoName(req configpkg.ShellLaunchParams) string {
	repoName := strings.TrimSpace(filepath.Base(strings.TrimSpace(req.Dir)))
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		return "worktree"
	}
	return repoName
}

func isShellReattachDeployExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == RemoteShellReattachDeployExitCode
}

func isShellTakenOverExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == RemoteShellTakenOverExitCode
}

func isShellReplacementExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == 137
}
