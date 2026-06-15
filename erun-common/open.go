package eruncommon

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	ErrDefaultTenantNotConfigured      = errors.New("default tenant is not configured")
	ErrDefaultEnvironmentNotConfigured = errors.New("default environment is not configured")
	ErrTenantNotFound                  = errors.New("no such tenant exists")
	ErrEnvironmentNotFound             = errors.New("no such environment exists")
	ErrKubernetesContextNotConfigured  = errors.New("kubernetes context is not configured")
	ErrRepoPathNotConfigured           = errors.New("repo path is not configured")
	ErrShellReattachDeploy             = errors.New("remote shell requested deploy handoff and reattach")
	ErrShellPodReplaced                = errors.New("remote shell pod was replaced; reattach")
	ErrShellSessionTakenOver           = errors.New("remote session was re-attached in another ERun window")

	openUserHomeDir = os.UserHomeDir
)

const (
	defaultShellLaunchWaitTimeout     = "2m0s"
	remoteShellReattachDeployExitCode = 75
	remoteShellTakenOverExitCode      = 76
)

// ShellSessionTakenOverNotice is the stable line `erun open` prints when its
// persistent session is re-attached from another ERun window (screen -d -r
// semantics: the session keeps running, this viewer is detached). The desktop
// matches this exact line to stop its reconnect loop instead of stealing the
// session back, so treat the wording as a public contract.
const ShellSessionTakenOverNotice = "open: session re-attached in another ERun window"

// RemoteAppSessionSocketDir is where the bootstrap keeps the dtach sockets
// (and owner files) of persistent desktop sessions inside the runtime pod.
// Pod-ephemeral on purpose: a pod replacement clears them.
const RemoteAppSessionSocketDir = "/tmp/erun-app"

// ParseRemoteAppSessionIDs extracts this tenant+env's persistent desktop
// session ids from an `ls` of RemoteAppSessionSocketDir in the runtime pod.
// Socket files are named <tenant>-<env>-<id>.dtach with the same name
// sanitization the bootstrap applies; owner files, other envs' sockets, and
// ls noise are ignored. The desktop uses the ids to rebuild tabs for sessions
// another ERun window created.
func ParseRemoteAppSessionIDs(tenant, environment, lsOutput string) []string {
	prefix := sanitizeForFilename(tenant) + "-" + sanitizeForFilename(environment) + "-"
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

type OpenStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	LoadTenantConfig(string) (TenantConfig, string, error)
	LoadEnvConfig(string, string) (EnvConfig, string, error)
}

type effectiveKubernetesContextResolver interface {
	ResolveEffectiveKubernetesContext(environment, configured string) string
}

type OpenParams struct {
	Tenant                string
	Environment           string
	UseDefaultTenant      bool
	UseDefaultEnvironment bool
}

type OpenResult struct {
	Tenant       string
	Environment  string
	TenantConfig TenantConfig
	EnvConfig    EnvConfig
	LocalPorts   EnvironmentLocalPorts
	RepoPath     string
	Title        string
}

func (r OpenResult) RemoteRepo() bool {
	return r.EnvConfig.RemoteWorktree()
}

type ShellLaunchParams struct {
	Dir                string
	Tenant             string
	Environment        string
	Title              string
	Namespace          string
	KubernetesContext  string
	RemoteRepo         bool
	ManagedCloud       bool
	CloudProviderAlias string
	Idle               EnvironmentIdleConfig
	// AppSession, when non-empty, makes the remote shell a persistent,
	// reattachable dtach session keyed by this id (distinct per desktop tab:
	// kind + slot). Closing/reopening a tab — or a transient kubectl-exec drop —
	// then reconnects to the still-running shell (and the AI tab's claude keeps
	// working in the pod) instead of spawning a parallel one. Empty (the bare
	// `erun open` CLI path) keeps the previous ephemeral behaviour. See #478.
	AppSession string
	// AI / Contribute select the persistent session's create-time program:
	// Contribute cds into the contribute clone with its env; AI launches the AI
	// tool (claude, at the env's effort) once on create. The desktop sets these
	// instead of typing the prelude in, so a reattach never re-runs them.
	AI         bool
	Contribute bool
	AITool     string
	Claude     EnvironmentClaudeConfig
	// RuntimeImage mirrors EnvConfig.RuntimeImage. The AI session prelude
	// uses it to advise on the erun-build-env skill only when the env still
	// runs the default published runtime image.
	RuntimeImage string
}

type ShellLaunchPreview struct {
	// SeedArgs is the `kubectl exec -i` that streams the SSH private key to the
	// pod on stdin (the key is never in these args). Empty when the env has no
	// private key to seed (remote-repo env, no resolvable git remote, or no key
	// file). Traced before the shell exec so the dry-run plan shows the action.
	SeedArgs []string
	WaitArgs []string
	ExecArgs []string
	Script   string
}

type ShellLauncherFunc func(ShellLaunchParams) error

func OpenParamsForArgs(args []string) (OpenParams, error) {
	switch len(args) {
	case 0:
		return OpenParams{
			UseDefaultTenant:      true,
			UseDefaultEnvironment: true,
		}, nil
	case 1:
		return OpenParams{
			Environment:      args[0],
			UseDefaultTenant: true,
		}, nil
	case 2:
		return OpenParams{
			Tenant:      args[0],
			Environment: args[1],
		}, nil
	default:
		return OpenParams{}, fmt.Errorf("accepts 0 to 2 arg(s), received %d", len(args))
	}
}

func loadOpenDefaultTenant(store OpenStore) (string, error) {
	toolConfig, _, err := store.LoadERunConfig()
	if errors.Is(err, ErrNotInitialized) {
		return "", ErrDefaultTenantNotConfigured
	}
	if err != nil {
		return "", err
	}
	if toolConfig.DefaultTenant == "" {
		return "", ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}

func loadOpenDefaultEnvironment(store OpenStore, tenant string) (string, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, ErrNotInitialized) {
		return "", ErrDefaultEnvironmentNotConfigured
	}
	if err != nil {
		return "", err
	}
	if tenantConfig.DefaultEnvironment == "" {
		return "", ErrDefaultEnvironmentNotConfigured
	}
	return tenantConfig.DefaultEnvironment, nil
}

func InitParamsForOpenArgs(store OpenStore, args []string) (BootstrapInitParams, error) {
	params, err := OpenParamsForArgs(args)
	if err != nil {
		return BootstrapInitParams{}, err
	}
	return InitParamsForOpenTarget(store, params)
}

func InitParamsForOpenTarget(store OpenStore, params OpenParams) (BootstrapInitParams, error) {
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

func initParamsForOpenEnvironmentOnly(store OpenStore, environment string) (BootstrapInitParams, error) {
	resolvedTenant, err := loadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, ErrDefaultTenantNotConfigured) || errors.Is(err, ErrNotInitialized) {
			return BootstrapInitParams{Environment: environment, ResolveTenant: true}, nil
		}
		return BootstrapInitParams{}, err
	}
	return BootstrapInitParams{Tenant: resolvedTenant, Environment: environment}, nil
}

func initParamsForOpenDefaults(store OpenStore) (BootstrapInitParams, error) {
	resolvedTenant, err := loadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, ErrDefaultTenantNotConfigured) || errors.Is(err, ErrNotInitialized) {
			return BootstrapInitParams{ResolveTenant: true}, nil
		}
		return BootstrapInitParams{}, err
	}

	defaultEnvironment, err := loadOpenDefaultEnvironment(store, resolvedTenant)
	if err != nil {
		if errors.Is(err, ErrDefaultEnvironmentNotConfigured) || errors.Is(err, ErrNotInitialized) {
			return BootstrapInitParams{Tenant: resolvedTenant}, nil
		}
		return BootstrapInitParams{}, err
	}

	return BootstrapInitParams{Tenant: resolvedTenant, Environment: defaultEnvironment}, nil
}

func ResolveOpen(store OpenStore, params OpenParams) (OpenResult, error) {
	return resolveOpenWithFinder(store, FindProjectRoot, params)
}

// EnsureLocalPortRangePersisted freezes the resolver's choice for this env
// into its config so future opens are stable against alphabetical reshuffles
// of unrelated tenants/envs. It is a no-op when the env already declares a
// LocalPortRangeStart. In dry-run mode the assignment is traced but not
// written to disk. The persisted EnvConfig is returned via the OpenResult so
// callers can continue using it for the rest of the open flow.
func EnsureLocalPortRangePersisted(ctx Context, saveEnvConfig func(string, EnvConfig) error, result OpenResult) (OpenResult, error) {
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

func resolveOpenWithFinder(store OpenStore, findProjectRoot ProjectFinderFunc, params OpenParams) (OpenResult, error) {
	if store == nil {
		return OpenResult{}, fmt.Errorf("store is required")
	}

	tenant, err := resolveOpenTenant(store, findProjectRoot, params)
	if err != nil {
		return OpenResult{}, err
	}
	tenantConfig, err := loadOpenTenantConfig(store, tenant)
	if err != nil {
		return OpenResult{}, err
	}
	environment, err := resolveOpenEnvironment(params, tenantConfig)
	if err != nil {
		return OpenResult{}, err
	}
	envConfig, err := loadOpenEnvConfig(store, tenant, environment)
	if err != nil {
		return OpenResult{}, err
	}
	repoPath, err := resolveOpenRepoPath(tenantConfig, envConfig)
	if err != nil {
		return OpenResult{}, err
	}
	if err := validateOpenTarget(tenant, environment, repoPath, envConfig); err != nil {
		return OpenResult{}, err
	}
	localPorts, err := environmentLocalPortsForTarget(store, tenant, envConfig)
	if err != nil {
		return OpenResult{}, err
	}

	return OpenResult{
		Tenant:       tenant,
		Environment:  environment,
		TenantConfig: tenantConfig,
		EnvConfig:    envConfig,
		LocalPorts:   localPorts,
		RepoPath:     repoPath,
		Title:        tenant + "-" + environment,
	}, nil
}

func resolveOpenTenant(store OpenStore, findProjectRoot ProjectFinderFunc, params OpenParams) (string, error) {
	tenant := params.Tenant
	if tenant == "" && params.UseDefaultTenant {
		currentTenant, ok, err := loadCurrentDirectoryTenant(store, findProjectRoot)
		if err != nil {
			return "", err
		}
		if ok {
			tenant = currentTenant
		}
	}
	if tenant == "" && params.UseDefaultTenant {
		return loadOpenDefaultTenant(store)
	}
	if tenant == "" {
		return "", fmt.Errorf("tenant is required")
	}
	return tenant, nil
}

func loadOpenTenantConfig(store OpenStore, tenant string) (TenantConfig, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, ErrNotInitialized) {
		return TenantConfig{}, fmt.Errorf("%w: %s", ErrTenantNotFound, tenant)
	}
	if err != nil {
		return TenantConfig{}, err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}
	return tenantConfig, nil
}

func resolveOpenEnvironment(params OpenParams, tenantConfig TenantConfig) (string, error) {
	environment := params.Environment
	if environment == "" && params.UseDefaultEnvironment {
		environment = tenantConfig.DefaultEnvironment
		if environment == "" {
			return "", ErrDefaultEnvironmentNotConfigured
		}
	}
	if environment == "" {
		return "", fmt.Errorf("environment is required")
	}
	return environment, nil
}

func loadOpenEnvConfig(store OpenStore, tenant, environment string) (EnvConfig, error) {
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, ErrNotInitialized) {
		return EnvConfig{}, fmt.Errorf("%w: %s", ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return EnvConfig{}, err
	}
	if envConfig.Name == "" {
		envConfig.Name = environment
	}
	if resolver, ok := store.(effectiveKubernetesContextResolver); ok {
		envConfig.KubernetesContext = resolver.ResolveEffectiveKubernetesContext(environment, envConfig.KubernetesContext)
	}
	return envConfig, nil
}

func resolveOpenRepoPath(tenantConfig TenantConfig, envConfig EnvConfig) (string, error) {
	repoPath := envConfig.EffectiveLocalRepoPath()
	if repoPath == "" {
		repoPath = tenantConfig.ProjectRoot
	}
	if repoPath == "" {
		return "", ErrRepoPathNotConfigured
	}
	return filepath.Clean(repoPath), nil
}

func validateOpenTarget(tenant, environment, repoPath string, envConfig EnvConfig) error {
	if !envConfig.RemoteWorktree() {
		info, err := os.Stat(repoPath)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%q is not a directory", repoPath)
		}
	}
	if strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return fmt.Errorf("%w: %s/%s", ErrKubernetesContextNotConfigured, tenant, environment)
	}
	return nil
}

func loadCurrentDirectoryTenant(store OpenStore, findProjectRoot ProjectFinderFunc) (string, bool, error) {
	tenantLister, ok := store.(interface {
		ListTenantConfigs() ([]TenantConfig, error)
	})
	if !ok {
		return "", false, nil
	}

	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}

	tenant, _, err := findProjectRoot()
	if errors.Is(err, ErrNotInGitRepository) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", false, nil
	}

	tenants, err := tenantLister.ListTenantConfigs()
	if errors.Is(err, ErrNotInitialized) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	for _, config := range tenants {
		if strings.TrimSpace(config.Name) == tenant {
			return tenant, true, nil
		}
	}
	return "", false, nil
}

func resolveEffectiveKubernetesContext(environment, configured string, listContexts func() ([]string, error), currentContext func() (string, error)) string {
	environment = strings.TrimSpace(environment)
	configured = strings.TrimSpace(configured)
	if configured == "" || environment != DefaultEnvironment {
		return configured
	}
	if listContexts == nil || currentContext == nil {
		return configured
	}

	contexts, err := listContexts()
	if err != nil {
		return configured
	}
	if containsTrimmedString(contexts, configured) {
		return configured
	}

	current, err := currentContext()
	if err != nil {
		return configured
	}
	current = strings.TrimSpace(current)
	if current == "" || !containsTrimmedString(contexts, current) {
		return configured
	}

	return current
}

func listKubernetesContextNames() ([]string, error) {
	output, err := Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")
	contexts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		contexts = append(contexts, line)
	}
	return contexts, nil
}

func currentKubernetesContextName() (string, error) {
	output, err := Command("kubectl", "config", "current-context").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func containsTrimmedString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func ShellLaunchParamsFromResult(result OpenResult) ShellLaunchParams {
	return ShellLaunchParams{
		Dir:                result.RepoPath,
		Tenant:             result.Tenant,
		Environment:        result.Environment,
		Title:              result.Title,
		Namespace:          KubernetesNamespaceName(result.Tenant, result.Environment),
		KubernetesContext:  strings.TrimSpace(result.EnvConfig.KubernetesContext),
		RemoteRepo:         result.RemoteRepo(),
		ManagedCloud:       result.EnvConfig.ManagedCloud,
		CloudProviderAlias: strings.TrimSpace(result.EnvConfig.CloudProviderAlias),
		Idle:               result.EnvConfig.Idle,
		AITool:             strings.TrimSpace(result.EnvConfig.AITool),
		Claude:             result.EnvConfig.Claude,
		RuntimeImage:       strings.TrimSpace(result.EnvConfig.RuntimeImage),
	}
}

func LocalShellSetupScript(result OpenResult) string {
	commands := []string{
		fmt.Sprintf("kubectl config use-context %s >/dev/null", shellQuote(strings.TrimSpace(result.EnvConfig.KubernetesContext))),
		fmt.Sprintf("kubectl config set-context --current --namespace=%s >/dev/null", shellQuote(KubernetesNamespaceName(result.Tenant, result.Environment))),
		fmt.Sprintf("cd %s", shellQuote(result.RepoPath)),
	}
	return strings.Join(commands, " &&\n") + "\n"
}

func WaitForShellDeployment(req ShellLaunchParams) error {
	if err := runOpenKubectl(kubectlDeploymentWaitArgs(req), io.Discard, os.Stderr); err != nil {
		return enrichShellDeploymentError(req, err, runOpenKubectl)
	}
	return nil
}

func ExecShell(req ShellLaunchParams) error {
	// Stream the SSH private key to the pod on stdin before the interactive
	// shell, so it never appears in any kubectl exec argv (laptop `ps`, the
	// pod's /proc/<pid>/cmdline, or cluster exec audit logs). The interactive
	// script below seeds only the public known_hosts + ssh config inline.
	if err := seedRemoteSSHKey(req); err != nil {
		return err
	}

	script, err := buildRemoteShellScript(req)
	if err != nil {
		return err
	}

	cmd := Command("kubectl", kubectlExecArgs(req, script)...)
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
		if isShellReplacementExit(err) && shellReplacementPodReady(req, runOpenKubectl) {
			return ErrShellPodReplaced
		}
		return enrichShellDeploymentError(req, err, runOpenKubectl)
	}
	return nil
}

func PreviewShellLaunch(req ShellLaunchParams) (ShellLaunchPreview, error) {
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
		seedArgs = remoteSSHKeySeedArgs(req)
	}

	return ShellLaunchPreview{
		SeedArgs: seedArgs,
		WaitArgs: kubectlDeploymentWaitArgs(req),
		ExecArgs: kubectlExecArgs(req, script),
		Script:   script,
	}, nil
}

func RemoteShellWorktreePath(req ShellLaunchParams) string {
	return remoteWorktreePath(req)
}

func RemoteWorktreePathForRepoName(repoName string) string {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		repoName = "worktree"
	}
	return path.Join("/home", "erun", "git", repoName)
}

func RuntimeReleaseName(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return DevopsComponentName
	}
	return tenant + "-devops"
}

func kubectlDeploymentWaitArgs(req ShellLaunchParams) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "wait", "--for=condition=Available", "--timeout", defaultShellLaunchWaitTimeout, "deployment/"+RuntimeReleaseName(req.Tenant))
	return args
}

func kubectlExecArgs(req ShellLaunchParams, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec", "-it")
	args = append(args, "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

func kubectlTargetArgs(req ShellLaunchParams) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--context", req.KubernetesContext)
	}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	return args
}

func buildRemoteShellScript(req ShellLaunchParams) (string, error) {
	config, err := remoteShellConfigForRequest(req)
	if err != nil {
		return "", err
	}
	workdir := shellQuote(config.Workdir)
	scriptLines := remoteShellBaseScriptLines(req, config, workdir, shellQuote(req.Title))
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

func remoteShellConfigForRequest(req ShellLaunchParams) (remoteShellConfig, error) {
	remoteWorkdir := remoteWorktreePath(req)

	tenantConfig, err := yaml.Marshal(TenantConfig{
		Name:               req.Tenant,
		ProjectRoot:        remoteWorkdir,
		DefaultEnvironment: req.Environment,
	})
	if err != nil {
		return remoteShellConfig{}, err
	}
	toolConfig, err := yaml.Marshal(ERunConfig{
		DefaultTenant: req.Tenant,
	})
	if err != nil {
		return remoteShellConfig{}, err
	}
	envConfig, err := yaml.Marshal(EnvConfig{
		Name:               req.Environment,
		RepoPath:           remoteWorkdir,
		KubernetesContext:  req.KubernetesContext,
		Remote:             req.RemoteRepo,
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

func remoteShellBaseScriptLines(req ShellLaunchParams, config remoteShellConfig, workdir, title string) []string {
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
		"mkdir -p \"$config_home/erun\"",
		fmt.Sprintf("cat > \"$config_home/erun/config.yaml\" <<'EOF'\n%s\nEOF", config.ToolYAML),
		fmt.Sprintf("mkdir -p \"$config_home/erun/%s\"", req.Tenant),
		fmt.Sprintf("cat > \"$config_home/erun/%s/config.yaml\" <<'EOF'\n%s\nEOF", req.Tenant, config.TenantYAML),
		fmt.Sprintf("mkdir -p \"$config_home/erun/%s/%s\"", req.Tenant, req.Environment),
		fmt.Sprintf("cat > \"$config_home/erun/%s/%s/config.yaml\" <<'EOF'\n%s\nEOF", req.Tenant, req.Environment, config.EnvYAML),
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
		fmt.Sprintf("if [ -e \"$request_file\" ]; then rm -f \"$request_file\"; exit %d; fi", remoteShellReattachDeployExitCode),
		"rm -f \"$request_file\"",
		"exit \"$shell_status\"",
	)
}

// remoteShellLaunchLines runs the interactive shell. Without AppSession (the
// bare `erun open` CLI) it is the original single bash invocation, byte for
// byte. With AppSession (a desktop tab) it runs the shell inside a persistent,
// reattachable dtach session keyed by the id, so a disconnect detaches (the
// session — and the AI tab's claude — keeps running in the pod) and the next
// kubectl exec re-attaches rather than spawning a parallel chain. See #478.
func remoteShellLaunchLines(req ShellLaunchParams, bashrcPath, markerDir string) []string {
	if strings.TrimSpace(req.AppSession) == "" {
		return []string{fmt.Sprintf("/bin/bash --rcfile \"%s\" -i || shell_status=$?", bashrcPath)}
	}
	id := sanitizeForFilename(req.AppSession)
	socket := remoteAppSessionSocketPath(req.Tenant, req.Environment, id)
	owner := strings.TrimSuffix(socket, ".dtach") + ".owner"
	launchScript := fmt.Sprintf("%s/launch-%s.sh", markerDir, id)
	body := strings.Join(remoteSessionLauncherBody(req, bashrcPath), "\n")
	lines := []string{
		fmt.Sprintf("mkdir -p \"%s\"", RemoteAppSessionSocketDir),
		fmt.Sprintf("cat > \"%s\" <<'EOF'\n%s\nEOF", launchScript, body),
		// Take over the session from any other ERun window (screen-style
		// detach-elsewhere-and-reattach-here): claim ownership, then detach
		// other viewers by killing their dtach clients. The master — which
		// owns the running shell/claude — is never touched, and when it
		// cannot be identified no one is kicked. Kicked wrappers find a
		// foreign owner id below and exit 76 so their window reports the
		// handover instead of reconnecting into a tug-of-war.
		"attach_id=\"$$-$(date +%s)\"",
		fmt.Sprintf("printf '%%s' \"$attach_id\" > \"%s\"", owner),
	}
	lines = append(lines, remoteAppSessionMasterScanLines(socket)...)
	return append(lines,
		fmt.Sprintf("if [ -S \"%s\" ] && [ -n \"$master_pid\" ]; then for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do if [ \"$dtach_pid\" != \"$master_pid\" ] && grep -qF \"%s\" \"/proc/$dtach_pid/cmdline\" 2>/dev/null; then kill \"$dtach_pid\" 2>/dev/null || true; fi; done; fi", socket, socket),
		// ctrl_l, not winch: dtach keeps no screen buffer, so a reattach shows
		// nothing until the program repaints. A same-size attach yields no
		// effective WINCH (bash's readline and claude's TUI both stay silent);
		// the ^L dtach sends on attach makes both repaint immediately.
		fmt.Sprintf("dtach -A \"%s\" -r ctrl_l /bin/bash \"%s\" || shell_status=$?", socket, launchScript),
		fmt.Sprintf("if [ \"$(cat \"%s\" 2>/dev/null)\" != \"$attach_id\" ]; then exit %d; fi", owner, remoteShellTakenOverExitCode),
	)
}

// remoteAppSessionSocketPath is the dtach socket for one persistent desktop
// session. Pod-ephemeral on purpose: a pod replacement clears the sockets —
// no stale socket to reattach to, and claude --continue resumes the
// conversation in the fresh session.
func remoteAppSessionSocketPath(tenant, environment, id string) string {
	return fmt.Sprintf("%s/%s-%s-%s.dtach", RemoteAppSessionSocketDir, sanitizeForFilename(tenant), sanitizeForFilename(environment), sanitizeForFilename(id))
}

// remoteAppSessionMasterScanLines emits the sh lines that resolve $master_pid
// for the session socket: the dtach process with a non-dtach child (the
// session program). Clients have no children, and the -A creator's only child
// is the master itself. /proc-based on purpose: the runtime image ships no
// ss/lsof, and an unidentifiable master must leave $master_pid empty so
// callers fail open instead of killing the session.
func remoteAppSessionMasterScanLines(socket string) []string {
	return []string{
		"master_pid=\"\"",
		fmt.Sprintf("for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do if grep -qF \"%s\" \"/proc/$dtach_pid/cmdline\" 2>/dev/null; then for child_pid in $(pgrep -P \"$dtach_pid\" 2>/dev/null || true); do child_comm=\"$(cat \"/proc/$child_pid/comm\" 2>/dev/null)\"; if [ -n \"$child_comm\" ] && [ \"$child_comm\" != \"dtach\" ]; then master_pid=\"$dtach_pid\"; fi; done; fi; done", socket),
	}
}

// RemoteAppSessionEndScript returns the sh script that permanently ends a
// persistent desktop session: kill the dtach master (its program follows via
// SIGHUP when the pty disappears) and remove the socket and owner file. The
// desktop runs it when the user explicitly closes a custom terminal tab —
// unlike closing the env or quitting the app, which only detach and leave the
// session running for the next attach.
func RemoteAppSessionEndScript(tenant, environment, id string) string {
	socket := remoteAppSessionSocketPath(tenant, environment, id)
	lines := remoteAppSessionMasterScanLines(socket)
	lines = append(lines,
		"if [ -n \"$master_pid\" ]; then kill \"$master_pid\" 2>/dev/null || true; fi",
		fmt.Sprintf("rm -f \"%s\" \"%s\"", socket, strings.TrimSuffix(socket, ".dtach")+".owner"),
	)
	return strings.Join(lines, "\n")
}

// remoteSessionLauncherBody is the dtach session's create-time program: the
// per-tab prelude (run once on create) followed by the interactive shell. A
// reattach connects to whatever this program is running, so the prelude — and
// in particular the AI tool launch — never repeats.
func remoteSessionLauncherBody(req ShellLaunchParams, bashrcPath string) []string {
	var body []string
	if req.Contribute {
		// Match the desktop's former contribute prelude (issue #469): the
		// contribute clone's built binary on PATH, fast incremental rebuilds,
		// and the clone as the working directory.
		body = append(body,
			"export PATH=\"$HOME/.erun/contribute/bin:$PATH\"",
			"export ERUN_SKIP_LINT=1",
			"cd \"$HOME/git/erun\"",
		)
	}
	if req.AI {
		body = append(body, AISessionLaunchLines(req.AITool, req.Claude)...)
	}
	return append(body, fmt.Sprintf("exec /bin/bash --rcfile \"%s\" -i", bashrcPath))
}

func remoteShellGitSeedLines(req ShellLaunchParams, workdir string) ([]string, error) {
	if req.RemoteRepo {
		return nil, nil
	}
	gitHost, gitUser, gitRepo, err := resolveGitRemote(req.Dir)
	if err != nil {
		return nil, nil
	}
	knownHostsLines, err := loadKnownHostsLines(gitHost)
	if err != nil {
		return nil, err
	}
	// The private key is never written by this script — it streams to the pod
	// on stdin via seedRemoteSSHKey (see ExecShell) so it stays out of the
	// kubectl exec argv. known_hosts and the ssh config are public and inline.
	return remoteShellGitSeedScriptLines(workdir, gitHost, shellQuote(gitUser), shellQuote(gitRepo), strings.Join(knownHostsLines, "\n")), nil
}

func remoteShellGitSeedScriptLines(workdir, gitHost, gitUser, gitRepo, knownHosts string) []string {
	sshConfig := strings.Join([]string{
		fmt.Sprintf("Host %s", gitHost),
		fmt.Sprintf("  HostName %s", gitHost),
		"  IdentityFile ~/.ssh/keys",
		"  IdentitiesOnly yes",
		"  UserKnownHostsFile ~/.ssh/known_hosts",
	}, "\n")
	return []string{
		"set -eu",
		"mkdir -p \"$HOME/.ssh\"",
		"chmod 700 \"$HOME/.ssh\"",
		// Do not touch ~/.ssh/keys here — seedRemoteSSHKey owns it (stdin).
		"rm -f \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/config\"",
		"old_umask=\"$(umask)\"",
		"umask 077",
		fmt.Sprintf("cat > \"$HOME/.ssh/known_hosts\" <<'EOF'\n%s\nEOF", knownHosts),
		fmt.Sprintf("cat > \"$HOME/.ssh/config\" <<'EOF'\n%s\nEOF", sshConfig),
		"umask \"$old_umask\"",
		"chmod 600 \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/config\"",
		fmt.Sprintf("mkdir -p %s", workdir),
		fmt.Sprintf("cd %s", workdir),
		// `git config --global` writes through a `~/.gitconfig.lock`
		// created with O_CREAT|O_EXCL. When two erun open invocations
		// (typically the OPEN tab and the AI tab) hit `kubectl exec`
		// against the same pod within a few hundred milliseconds, both
		// run this script in parallel and one fails with "could not
		// lock config file: File exists", aborting the whole setup
		// under `set -eu`. Guard the write on the current value so the
		// loser of the race sees the setting already present and
		// skips entirely; tolerate a leftover lock or a tight race
		// past the guard with `|| true` because the racing process is
		// performing the same work. End state is the same.
		fmt.Sprintf("if command -v git >/dev/null 2>&1; then if [ ! -d .git ]; then git clone git@%s:%s/%s.git .; fi; if ! git config --global --get-all safe.directory 2>/dev/null | grep -qFx '*'; then git config --global --add safe.directory '*' 2>/dev/null || true; fi; fi", gitHost, gitUser, gitRepo),
	}
}

func remoteWorktreePath(req ShellLaunchParams) string {
	return RemoteWorktreePathForRepoName(remoteWorktreeRepoName(req))
}

func remoteWorktreeRepoName(req ShellLaunchParams) string {
	repoName := strings.TrimSpace(filepath.Base(strings.TrimSpace(req.Dir)))
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		return "worktree"
	}
	return repoName
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isShellReattachDeployExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == remoteShellReattachDeployExitCode
}

func isShellTakenOverExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == remoteShellTakenOverExitCode
}

func isShellReplacementExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == 137
}

func resolveGitRemote(repoPath string) (string, string, string, error) {
	output, err := Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", "", err
	}

	remote := strings.TrimSpace(string(output))
	switch {
	case strings.HasPrefix(remote, "git@"):
		hostRepo := strings.TrimPrefix(remote, "git@")
		host, repoPath, ok := strings.Cut(hostRepo, ":")
		if !ok {
			return "", "", "", fmt.Errorf("unexpected git remote %q", remote)
		}
		user, repo := splitRepoPath(repoPath)
		return host, user, repo, nil
	case strings.HasPrefix(remote, "ssh://"):
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", "", "", err
		}
		repoPath := strings.TrimPrefix(parsed.Path, "/")
		user, repo := splitRepoPath(repoPath)
		return parsed.Hostname(), user, repo, nil
	default:
		return "", "", "", fmt.Errorf("unsupported git remote %q", remote)
	}
}

func splitRepoPath(repoPath string) (string, string) {
	repoPath = strings.TrimSuffix(repoPath, ".git")
	return path.Dir(repoPath), path.Base(repoPath)
}

func resolveSSHConfigEntries(host string) ([]sshConfigEntry, error) {
	sshConfigPath := filepath.Join(resolveOpenUserHomeDir(), ".ssh", "config")
	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	entries := parseSSHConfig(string(data))
	matches := make([]sshConfigEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.matchesHost(host) {
			matches = append(matches, entry)
		}
	}
	return matches, nil
}

func loadKnownHostsLines(host string) ([]string, error) {
	knownHostsPath := filepath.Join(resolveOpenUserHomeDir(), ".ssh", "known_hosts")
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	lines := make([]string, 0, 4)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, host) {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(lines)
	return lines, nil
}

// loadPrivateKeyMaterial reads the resolved private key files. The material is
// only ever delivered to the pod on stdin (seedRemoteSSHKey) — never embedded
// in a script or a command line — so there is no redaction mode: it is not
// placed anywhere a dry-run trace or log would capture it.
func loadPrivateKeyMaterial(entries []sshConfigEntry) ([]string, error) {
	keyPaths := privateKeyPaths(entries)

	lines := make([]string, 0, len(keyPaths))
	for _, keyPath := range keyPaths {
		data, err := os.ReadFile(keyPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		lines = append(lines, string(data))
	}
	return lines, nil
}

// remoteSSHKeySeedScript writes stdin to ~/.ssh/keys at mode 600. The key bytes
// arrive on the exec's stdin, never in its argv.
const remoteSSHKeySeedScript = `umask 077; mkdir -p "$HOME/.ssh"; cat > "$HOME/.ssh/keys"; chmod 600 "$HOME/.ssh/keys"`

// remoteShellPrivateKeyMaterial resolves the SSH private key material the env's
// git remote needs, or "" when there is nothing to seed (remote-repo env, no
// resolvable git remote, or no key file present).
func remoteShellPrivateKeyMaterial(req ShellLaunchParams) (string, error) {
	if req.RemoteRepo {
		return "", nil
	}
	gitHost, _, _, err := resolveGitRemote(req.Dir)
	if err != nil {
		return "", nil
	}
	entries, err := resolveSSHConfigEntries(gitHost)
	if err != nil {
		return "", err
	}
	keyLines, err := loadPrivateKeyMaterial(entries)
	if err != nil {
		return "", err
	}
	return strings.Join(keyLines, "\n"), nil
}

// remoteSSHKeySeedArgs is the non-interactive `kubectl exec -i` that streams the
// private key to the pod. -i (not -it) keeps stdin a pipe for the key bytes;
// the key is never in these args.
func remoteSSHKeySeedArgs(req ShellLaunchParams) []string {
	args := kubectlTargetArgs(req)
	return append(args, "exec", "-i", "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-c", remoteSSHKeySeedScript)
}

// seedRemoteSSHKey writes the env's SSH private key into the pod's ~/.ssh/keys
// by piping it on stdin, so it never touches a command line. No-op when there
// is no key to seed. Runs after the deployment is Available (the open flow
// waits first) and before the interactive shell exec.
func seedRemoteSSHKey(req ShellLaunchParams) error {
	keyMaterial, err := remoteShellPrivateKeyMaterial(req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(keyMaterial) == "" {
		return nil
	}
	cmd := Command("kubectl", remoteSSHKeySeedArgs(req)...)
	cmd.Stdin = strings.NewReader(keyMaterial)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(output.String()); message != "" {
			return fmt.Errorf("seed remote ssh key: %w: %s", err, message)
		}
		return fmt.Errorf("seed remote ssh key: %w", err)
	}
	return nil
}

func privateKeyPaths(entries []sshConfigEntry) []string {
	keyPaths := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for _, entry := range entries {
		for _, identityFile := range entry.identityFiles {
			keyPaths = appendUniquePrivateKeyPath(keyPaths, seen, identityFile)
		}
	}
	if len(keyPaths) > 0 {
		return keyPaths
	}
	for _, fallback := range []string{"id_rsa", "id_ed25519", "id_ecdsa"} {
		keyPaths = appendUniquePrivateKeyPath(keyPaths, seen, filepath.Join(resolveOpenUserHomeDir(), ".ssh", fallback))
	}
	return keyPaths
}

func appendUniquePrivateKeyPath(keyPaths []string, seen map[string]struct{}, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return keyPaths
	}
	if _, ok := seen[path]; ok {
		return keyPaths
	}
	seen[path] = struct{}{}
	return append(keyPaths, path)
}

type sshConfigEntry struct {
	patterns      []string
	identityFiles []string
}

func (e sshConfigEntry) matchesHost(host string) bool {
	for _, pattern := range e.patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		ok, err := path.Match(pattern, host)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func parseSSHConfig(contents string) []sshConfigEntry {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	entries := make([]sshConfigEntry, 0, 4)
	current := sshConfigEntry{}

	flush := func() {
		if len(current.patterns) == 0 {
			return
		}
		entries = append(entries, current)
		current = sshConfigEntry{}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch strings.ToLower(fields[0]) {
		case "host":
			flush()
			current.patterns = append(current.patterns, fields[1:]...)
		case "identityfile":
			current.identityFiles = append(current.identityFiles, expandSSHPath(fields[1]))
		}
	}
	flush()
	return entries
}

func expandSSHPath(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(resolveOpenUserHomeDir(), strings.TrimPrefix(value, "~/"))
	}
	return value
}

func resolveOpenUserHomeDir() string {
	homeDir, err := openUserHomeDir()
	if err == nil && strings.TrimSpace(homeDir) != "" {
		return homeDir
	}
	return strings.TrimSpace(os.Getenv("HOME"))
}
