package eruncommon

import (
	"bufio"
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

	openUserHomeDir = os.UserHomeDir
)

const defaultShellLaunchWaitTimeout = "2m0s"
const remoteShellReattachDeployExitCode = 75

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
	RepoPath     string
	Title        string
}

func (r OpenResult) RemoteRepo() bool {
	return r.EnvConfig.Remote || r.TenantConfig.Remote
}

type ShellLaunchParams struct {
	Dir               string
	Tenant            string
	Environment       string
	Title             string
	Namespace         string
	KubernetesContext string
	RemoteRepo        bool
}

type ShellLaunchPreview struct {
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
		return BootstrapInitParams{
			Tenant:      tenant,
			Environment: environment,
		}, nil
	case tenant != "":
		return BootstrapInitParams{Tenant: tenant}, nil
	case environment != "":
		resolvedTenant, err := loadOpenDefaultTenant(store)
		if err != nil {
			if errors.Is(err, ErrDefaultTenantNotConfigured) || errors.Is(err, ErrNotInitialized) {
				return BootstrapInitParams{
					Environment:   environment,
					ResolveTenant: true,
				}, nil
			}
			return BootstrapInitParams{}, err
		}
		return BootstrapInitParams{
			Tenant:      resolvedTenant,
			Environment: environment,
		}, nil
	}

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

	return BootstrapInitParams{
		Tenant:      resolvedTenant,
		Environment: defaultEnvironment,
	}, nil
}

func ResolveOpen(store OpenStore, params OpenParams) (OpenResult, error) {
	return resolveOpenWithFinder(store, FindProjectRoot, params)
}

func resolveOpenWithFinder(store OpenStore, findProjectRoot ProjectFinderFunc, params OpenParams) (OpenResult, error) {
	if store == nil {
		return OpenResult{}, fmt.Errorf("store is required")
	}

	tenant := params.Tenant
	if tenant == "" && params.UseDefaultTenant {
		if currentTenant, ok, err := loadCurrentDirectoryTenant(store, findProjectRoot); err != nil {
			return OpenResult{}, err
		} else if ok {
			tenant = currentTenant
		}
	}
	if tenant == "" && params.UseDefaultTenant {
		toolConfig, _, err := store.LoadERunConfig()
		if errors.Is(err, ErrNotInitialized) {
			return OpenResult{}, ErrDefaultTenantNotConfigured
		}
		if err != nil {
			return OpenResult{}, err
		}
		tenant = toolConfig.DefaultTenant
		if tenant == "" {
			return OpenResult{}, ErrDefaultTenantNotConfigured
		}
	}
	if tenant == "" {
		return OpenResult{}, fmt.Errorf("tenant is required")
	}

	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, ErrNotInitialized) {
		return OpenResult{}, fmt.Errorf("%w: %s", ErrTenantNotFound, tenant)
	}
	if err != nil {
		return OpenResult{}, err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}

	environment := params.Environment
	if environment == "" && params.UseDefaultEnvironment {
		environment = tenantConfig.DefaultEnvironment
		if environment == "" {
			return OpenResult{}, ErrDefaultEnvironmentNotConfigured
		}
	}
	if environment == "" {
		return OpenResult{}, fmt.Errorf("environment is required")
	}

	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, ErrNotInitialized) {
		return OpenResult{}, fmt.Errorf("%w: %s", ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return OpenResult{}, err
	}
	if envConfig.Name == "" {
		envConfig.Name = environment
	}
	if resolver, ok := store.(effectiveKubernetesContextResolver); ok {
		envConfig.KubernetesContext = resolver.ResolveEffectiveKubernetesContext(environment, envConfig.KubernetesContext)
	}

	repoPath := envConfig.RepoPath
	if repoPath == "" {
		repoPath = tenantConfig.ProjectRoot
	}
	if repoPath == "" {
		return OpenResult{}, ErrRepoPathNotConfigured
	}

	repoPath = filepath.Clean(repoPath)
	if !(envConfig.Remote || tenantConfig.Remote) {
		info, err := os.Stat(repoPath)
		if err != nil {
			return OpenResult{}, err
		}
		if !info.IsDir() {
			return OpenResult{}, fmt.Errorf("%q is not a directory", repoPath)
		}
	}
	if strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return OpenResult{}, fmt.Errorf("%w: %s/%s", ErrKubernetesContextNotConfigured, tenant, environment)
	}

	return OpenResult{
		Tenant:       tenant,
		Environment:  environment,
		TenantConfig: tenantConfig,
		EnvConfig:    envConfig,
		RepoPath:     repoPath,
		Title:        tenant + "-" + environment,
	}, nil
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
	output, err := exec.Command("kubectl", "config", "get-contexts", "-o=name").Output()
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
	output, err := exec.Command("kubectl", "config", "current-context").Output()
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
		Dir:               result.RepoPath,
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		Title:             result.Title,
		Namespace:         KubernetesNamespaceName(result.Tenant, result.Environment),
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
		RemoteRepo:        result.RemoteRepo(),
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

func LaunchShell(req ShellLaunchParams) error {
	if err := WaitForShellDeployment(req); err != nil {
		return err
	}
	return ExecShell(req)
}

func WaitForShellDeployment(req ShellLaunchParams) error {
	waitCmd := exec.Command("kubectl", kubectlDeploymentWaitArgs(req)...)
	waitCmd.Stdout = io.Discard
	waitCmd.Stderr = os.Stderr
	return waitCmd.Run()
}

func ExecShell(req ShellLaunchParams) error {
	script, err := buildRemoteShellScript(req, false)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", kubectlExecArgs(req, script)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if isShellReattachDeployExit(err) {
			return ErrShellReattachDeploy
		}
		return err
	}
	return nil
}

func PreviewShellLaunch(req ShellLaunchParams) (ShellLaunchPreview, error) {
	script, err := buildRemoteShellScript(req, true)
	if err != nil {
		return ShellLaunchPreview{}, err
	}

	return ShellLaunchPreview{
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
	args = append(args, "exec", "-it", "-c", RuntimeReleaseName(req.Tenant))
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

func buildRemoteShellScript(req ShellLaunchParams, redactHostSecrets bool) (string, error) {
	remoteWorkdir := remoteWorktreePath(req)

	tenantConfig, err := yaml.Marshal(TenantConfig{
		Name:               req.Tenant,
		ProjectRoot:        remoteWorkdir,
		DefaultEnvironment: req.Environment,
		Remote:             req.RemoteRepo,
	})
	if err != nil {
		return "", err
	}
	toolConfig, err := yaml.Marshal(ERunConfig{
		DefaultTenant: req.Tenant,
	})
	if err != nil {
		return "", err
	}
	envConfig, err := yaml.Marshal(EnvConfig{
		Name:              req.Environment,
		RepoPath:          remoteWorkdir,
		KubernetesContext: req.KubernetesContext,
		Remote:            req.RemoteRepo,
	})
	if err != nil {
		return "", err
	}

	workdir := shellQuote(remoteWorkdir)
	toolYAML := string(toolConfig)
	tenantYAML := string(tenantConfig)
	envYAML := string(envConfig)
	title := shellQuote(req.Title)

	scriptLines := []string{
		"set -eu",
		fmt.Sprintf("mkdir -p %s", workdir),
		fmt.Sprintf("cd %s", workdir),
		"config_home=\"${XDG_CONFIG_HOME:-$HOME/.config}\"",
		"mkdir -p \"$config_home/erun\"",
		fmt.Sprintf("cat > \"$config_home/erun/config.yaml\" <<'EOF'\n%s\nEOF", toolYAML),
		fmt.Sprintf("mkdir -p \"$config_home/erun/%s\"", req.Tenant),
		fmt.Sprintf("cat > \"$config_home/erun/%s/config.yaml\" <<'EOF'\n%s\nEOF", req.Tenant, tenantYAML),
		fmt.Sprintf("mkdir -p \"$config_home/erun/%s/%s\"", req.Tenant, req.Environment),
		fmt.Sprintf("cat > \"$config_home/erun/%s/%s/config.yaml\" <<'EOF'\n%s\nEOF", req.Tenant, req.Environment, envYAML),
		fmt.Sprintf("cat > \"$HOME/.erun_bashrc\" <<'EOF'\nexport ERUN_SHELL_HOST=%s\nerun() {\n  if [ \"${1:-}\" = \"deploy\" ] && [ \"$#\" -eq 1 ] && [ -n \"${ERUN_SHELL_REQUEST_FILE:-}\" ]; then\n    : > \"$ERUN_SHELL_REQUEST_FILE\"\n    exit 0\n  fi\n  command erun \"$@\"\n}\n[ -r /etc/bash.bashrc ] && . /etc/bash.bashrc\nEOF", title),
		fmt.Sprintf("printf '\\033]0;%s\\007'", title),
		"request_file=\"$HOME/.erun-shell-request\"",
		"rm -f \"$request_file\"",
		"export ERUN_SHELL_REQUEST_FILE=\"$request_file\"",
		"shell_status=0",
		"/bin/bash --rcfile \"$HOME/.erun_bashrc\" -i || shell_status=$?",
		fmt.Sprintf("if [ -e \"$request_file\" ]; then rm -f \"$request_file\"; exit %d; fi", remoteShellReattachDeployExitCode),
		"rm -f \"$request_file\"",
		"exit \"$shell_status\"",
	}

	if !req.RemoteRepo {
		if gitHost, gitUser, gitRepo, err := resolveGitRemote(req.Dir); err == nil {
			hostConfigEntries, err := resolveSSHConfigEntries(gitHost)
			if err != nil {
				return "", err
			}

			knownHostsLines, err := loadKnownHostsLines(gitHost)
			if err != nil {
				return "", err
			}

			keyLines, err := loadPrivateKeyMaterial(hostConfigEntries, redactHostSecrets)
			if err != nil {
				return "", err
			}

			knownHosts := strings.Join(knownHostsLines, "\n")
			keys := strings.Join(keyLines, "\n")
			gitUser = shellQuote(gitUser)
			gitRepo = shellQuote(gitRepo)
			sshConfig := strings.Join([]string{
				fmt.Sprintf("Host %s", gitHost),
				fmt.Sprintf("  HostName %s", gitHost),
				"  IdentityFile ~/.ssh/keys",
				"  IdentitiesOnly yes",
				"  UserKnownHostsFile ~/.ssh/known_hosts",
			}, "\n")

			scriptLines = append([]string{
				"set -eu",
				"mkdir -p \"$HOME/.ssh\"",
				"chmod 700 \"$HOME/.ssh\"",
				"rm -f \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/keys\" \"$HOME/.ssh/config\"",
				"old_umask=\"$(umask)\"",
				"umask 077",
				fmt.Sprintf("cat > \"$HOME/.ssh/known_hosts\" <<'EOF'\n%s\nEOF", knownHosts),
				fmt.Sprintf("cat > \"$HOME/.ssh/keys\" <<'EOF'\n%s\nEOF", keys),
				fmt.Sprintf("cat > \"$HOME/.ssh/config\" <<'EOF'\n%s\nEOF", sshConfig),
				"umask \"$old_umask\"",
				"chmod 600 \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/keys\" \"$HOME/.ssh/config\"",
				fmt.Sprintf("mkdir -p %s", workdir),
				fmt.Sprintf("cd %s", workdir),
				fmt.Sprintf("if command -v git >/dev/null 2>&1; then if [ ! -d .git ]; then git clone git@%s:%s/%s.git .; fi; git config --global --add safe.directory '*'; fi", gitHost, gitUser, gitRepo),
			}, scriptLines[1:]...)
		}
	}

	script := strings.Join(scriptLines, "\n")

	return script, nil
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

func resolveGitRemote(repoPath string) (string, string, string, error) {
	output, err := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
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

func loadPrivateKeyMaterial(entries []sshConfigEntry, redact bool) ([]string, error) {
	keyPaths := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	addKeyPath := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		keyPaths = append(keyPaths, path)
	}

	for _, entry := range entries {
		for _, identityFile := range entry.identityFiles {
			addKeyPath(identityFile)
		}
	}

	if len(keyPaths) == 0 {
		for _, fallback := range []string{"id_rsa", "id_ed25519", "id_ecdsa"} {
			addKeyPath(filepath.Join(resolveOpenUserHomeDir(), ".ssh", fallback))
		}
	}

	lines := make([]string, 0, len(keyPaths))
	for _, keyPath := range keyPaths {
		data, err := os.ReadFile(keyPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if redact {
			lines = append(lines, fmt.Sprintf("# %s", filepath.Base(keyPath)))
			lines = append(lines, "<redacted>")
			continue
		}
		lines = append(lines, string(data))
	}
	return lines, nil
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
