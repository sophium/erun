package opener

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

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"gopkg.in/yaml.v3"
)

var (
	ErrDefaultTenantNotConfigured      = errors.New("default tenant is not configured")
	ErrDefaultEnvironmentNotConfigured = errors.New("default environment is not configured")
	ErrTenantNotFound                  = errors.New("no such tenant exists")
	ErrEnvironmentNotFound             = errors.New("no such environment exists")
	ErrKubernetesContextNotConfigured  = errors.New("kubernetes context is not configured")
	ErrRepoPathNotConfigured           = errors.New("repo path is not configured")
)

const defaultShellLaunchWaitTimeout = "2m0s"

type Store interface {
	LoadERunConfig() (internal.ERunConfig, string, error)
	LoadTenantConfig(string) (internal.TenantConfig, string, error)
	LoadEnvConfig(string, string) (internal.EnvConfig, string, error)
}

type Request struct {
	Tenant                string
	Environment           string
	UseDefaultTenant      bool
	UseDefaultEnvironment bool
}

type Result struct {
	Tenant       string
	Environment  string
	TenantConfig internal.TenantConfig
	EnvConfig    internal.EnvConfig
	RepoPath     string
	Title        string
}

type ShellLaunchRequest struct {
	Dir               string
	Tenant            string
	Environment       string
	Title             string
	Namespace         string
	KubernetesContext string
}

type ShellLaunchPreview struct {
	WaitArgs []string
	ExecArgs []string
	Script   string
}

type ShellLauncher func(ShellLaunchRequest) error

type Service struct {
	Store       Store
	LaunchShell ShellLauncher
}

func (s Service) Resolve(req Request) (Result, error) {
	if s.Store == nil {
		return Result{}, fmt.Errorf("store is required")
	}

	tenant := req.Tenant
	if tenant == "" && req.UseDefaultTenant {
		toolConfig, _, err := s.Store.LoadERunConfig()
		if errors.Is(err, internal.ErrNotInitialized) {
			return Result{}, ErrDefaultTenantNotConfigured
		}
		if err != nil {
			return Result{}, err
		}
		tenant = toolConfig.DefaultTenant
		if tenant == "" {
			return Result{}, ErrDefaultTenantNotConfigured
		}
	}
	if tenant == "" {
		return Result{}, fmt.Errorf("tenant is required")
	}

	tenantConfig, _, err := s.Store.LoadTenantConfig(tenant)
	if errors.Is(err, internal.ErrNotInitialized) {
		return Result{}, fmt.Errorf("%w: %s", ErrTenantNotFound, tenant)
	}
	if err != nil {
		return Result{}, err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}

	environment := req.Environment
	if environment == "" && req.UseDefaultEnvironment {
		environment = tenantConfig.DefaultEnvironment
		if environment == "" {
			return Result{}, ErrDefaultEnvironmentNotConfigured
		}
	}
	if environment == "" {
		return Result{}, fmt.Errorf("environment is required")
	}

	envConfig, _, err := s.Store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, internal.ErrNotInitialized) {
		return Result{}, fmt.Errorf("%w: %s", ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return Result{}, err
	}
	if envConfig.Name == "" {
		envConfig.Name = environment
	}

	repoPath := envConfig.RepoPath
	if repoPath == "" {
		repoPath = tenantConfig.ProjectRoot
	}
	if repoPath == "" {
		return Result{}, ErrRepoPathNotConfigured
	}

	repoPath = filepath.Clean(repoPath)
	info, err := os.Stat(repoPath)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%q is not a directory", repoPath)
	}
	if strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return Result{}, fmt.Errorf("%w: %s/%s", ErrKubernetesContextNotConfigured, tenant, environment)
	}

	return Result{
		Tenant:       tenant,
		Environment:  environment,
		TenantConfig: tenantConfig,
		EnvConfig:    envConfig,
		RepoPath:     repoPath,
		Title:        tenant + "-" + environment,
	}, nil
}

func (s Service) Run(req Request) (Result, error) {
	result, err := s.Resolve(req)
	if err != nil {
		return Result{}, err
	}

	launcher := s.LaunchShell
	if launcher == nil {
		launcher = DefaultShellLauncher
	}

	if err := launcher(ShellLaunchRequestFromResult(result)); err != nil {
		return Result{}, err
	}

	return result, nil
}

func ShellLaunchRequestFromResult(result Result) ShellLaunchRequest {
	return ShellLaunchRequest{
		Dir:               result.RepoPath,
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		Title:             result.Title,
		Namespace:         bootstrap.KubernetesNamespaceName(result.Tenant, result.Environment),
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
	}
}

func DefaultShellLauncher(req ShellLaunchRequest) error {
	waitCmd := exec.Command("kubectl", kubectlDeploymentWaitArgs(req)...)
	waitCmd.Stdout = io.Discard
	waitCmd.Stderr = os.Stderr
	if err := waitCmd.Run(); err != nil {
		return err
	}

	script, err := remoteShellScript(req)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", kubectlExecArgs(req, script)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func PreviewShellLaunch(req ShellLaunchRequest) (ShellLaunchPreview, error) {
	script, err := remoteShellScriptPreview(req)
	if err != nil {
		return ShellLaunchPreview{}, err
	}

	return ShellLaunchPreview{
		WaitArgs: kubectlDeploymentWaitArgs(req),
		ExecArgs: kubectlExecArgs(req, script),
		Script:   script,
	}, nil
}

func kubectlDeploymentWaitArgs(req ShellLaunchRequest) []string {
	args := kubectlTargetArgs(req)
	args = append(args,
		"wait",
		"--for=condition=Available",
		"--timeout", defaultShellLaunchWaitTimeout,
		"deployment/erun-devops",
	)
	return args
}

func kubectlExecArgs(req ShellLaunchRequest, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec", "-it")
	args = append(args,
		"deployment/erun-devops",
		"--",
		"/bin/sh",
		"-lc",
		script,
	)
	return args
}

func kubectlTargetArgs(req ShellLaunchRequest) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--context", req.KubernetesContext)
	}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	return args
}

func remoteShellScript(req ShellLaunchRequest) (string, error) {
	return buildRemoteShellScript(req, false)
}

func remoteShellScriptPreview(req ShellLaunchRequest) (string, error) {
	return buildRemoteShellScript(req, true)
}

func buildRemoteShellScript(req ShellLaunchRequest, redactSecrets bool) (string, error) {
	worktreeDir := path.Join("/home/erun/git", filepath.Base(filepath.Clean(req.Dir)))
	scriptLines := []string{"set -eu"}
	if title := strings.TrimSpace(req.Title); title != "" {
		scriptLines = append(scriptLines, fmt.Sprintf("export ERUN_SHELL_HOST=%s", shellQuote(title)))
	}

	configFiles, err := remoteERunConfigFiles(req, worktreeDir)
	if err != nil {
		return "", err
	}
	gitFiles, gitCommands, err := remoteGitConfigFiles(req.Dir, redactSecrets)
	if err != nil {
		return "", err
	}
	configFiles = append(configFiles, gitFiles...)
	for _, configFile := range configFiles {
		scriptLines = append(scriptLines, fmt.Sprintf("mkdir -p %s", shellQuote(path.Dir(configFile.path))))
		scriptLines = append(scriptLines, shellWriteFile(configFile.path, configFile.contents))
		if configFile.mode != 0 {
			scriptLines = append(scriptLines, fmt.Sprintf("chmod %04o %s", configFile.mode.Perm(), shellQuote(configFile.path)))
		}
	}
	scriptLines = append(scriptLines, gitCommands...)

	scriptLines = append(scriptLines,
		fmt.Sprintf("cd %s", shellQuote(worktreeDir)),
		"if command -v bash >/dev/null 2>&1; then exec bash -i; fi",
		"exec sh -i",
	)

	return strings.Join(scriptLines, "\n"), nil
}

type remoteConfigFile struct {
	path     string
	contents string
	mode     os.FileMode
}

func remoteERunConfigFiles(req ShellLaunchRequest, worktreeDir string) ([]remoteConfigFile, error) {
	tenant := strings.TrimSpace(req.Tenant)
	environment := strings.TrimSpace(req.Environment)
	if tenant == "" || environment == "" {
		return nil, nil
	}

	toolConfig, err := yaml.Marshal(internal.ERunConfig{
		DefaultTenant: tenant,
	})
	if err != nil {
		return nil, err
	}
	tenantConfig, err := yaml.Marshal(internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        worktreeDir,
		DefaultEnvironment: environment,
	})
	if err != nil {
		return nil, err
	}
	envConfig, err := yaml.Marshal(internal.EnvConfig{
		Name:              environment,
		RepoPath:          worktreeDir,
		KubernetesContext: "in-cluster",
	})
	if err != nil {
		return nil, err
	}

	configRoot := path.Join("/home/erun", ".config", "erun")
	return []remoteConfigFile{
		{
			path:     path.Join(configRoot, "config.yaml"),
			contents: string(toolConfig),
			mode:     0o644,
		},
		{
			path:     path.Join(configRoot, tenant, "config.yaml"),
			contents: string(tenantConfig),
			mode:     0o644,
		},
		{
			path:     path.Join(configRoot, tenant, environment, "config.yaml"),
			contents: string(envConfig),
			mode:     0o644,
		},
	}, nil
}

func remoteGitConfigFiles(repoDir string, redactSecrets bool) ([]remoteConfigFile, []string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}

	targets, err := resolveGitRemoteCredentialTargets(repoDir)
	if err != nil {
		return nil, nil, err
	}
	if targets.isEmpty() {
		return nil, nil, nil
	}

	files := make([]remoteConfigFile, 0, 8)
	if file, ok, err := filteredGitCredentialsFile(filepath.Join(homeDir, ".git-credentials"), path.Join("/home/erun", ".git-credentials"), targets.httpsHosts, redactSecrets); err != nil {
		return nil, nil, err
	} else if ok {
		files = append(files, file)
	}
	sshFiles, err := relevantRemoteSSHFiles(filepath.Join(homeDir, ".ssh"), targets.sshHosts, redactSecrets)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, sshFiles...)

	commands := make([]string, 0, 2)
	if containsRemotePath(files, "/home/erun/.ssh/") {
		commands = append(commands, "chmod 700 '/home/erun/.ssh'")
	}
	if containsExactRemotePath(files, "/home/erun/.git-credentials") {
		commands = append(commands, "if command -v git >/dev/null 2>&1; then git config --global credential.helper 'store --file /home/erun/.git-credentials'; fi")
	}

	return files, commands, nil
}

type gitRemoteCredentialTargets struct {
	httpsHosts map[string]struct{}
	sshHosts   []string
}

func (t gitRemoteCredentialTargets) isEmpty() bool {
	return len(t.httpsHosts) == 0 && len(t.sshHosts) == 0
}

func resolveGitRemoteCredentialTargets(repoDir string) (gitRemoteCredentialTargets, error) {
	urls, err := readGitRemoteURLs(repoDir)
	if err != nil {
		if errors.Is(err, internal.ErrNotInGitRepository) || errors.Is(err, os.ErrNotExist) {
			return gitRemoteCredentialTargets{}, nil
		}
		return gitRemoteCredentialTargets{}, err
	}

	targets := gitRemoteCredentialTargets{
		httpsHosts: make(map[string]struct{}),
		sshHosts:   make([]string, 0, len(urls)),
	}
	seenSSH := make(map[string]struct{}, len(urls))
	for _, remoteURL := range urls {
		kind, host := classifyGitRemoteURL(remoteURL)
		switch kind {
		case "https":
			if host != "" {
				targets.httpsHosts[host] = struct{}{}
			}
		case "ssh":
			if host == "" {
				continue
			}
			if _, ok := seenSSH[host]; ok {
				continue
			}
			seenSSH[host] = struct{}{}
			targets.sshHosts = append(targets.sshHosts, host)
		}
	}
	return targets, nil
}

func readGitRemoteURLs(repoDir string) ([]string, error) {
	configPath, err := gitConfigPath(repoDir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	return parseGitRemoteURLs(string(data)), nil
}

func gitConfigPath(repoDir string) (string, error) {
	repoDir = strings.TrimSpace(repoDir)
	if repoDir == "" {
		return "", internal.ErrNotInGitRepository
	}

	gitPath := filepath.Join(repoDir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", internal.ErrNotInGitRepository
		}
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(gitPath, "config"), nil
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir:"
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(strings.ToLower(content), prefix) {
		return "", internal.ErrNotInGitRepository
	}
	gitDir := strings.TrimSpace(content[len(prefix):])
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoDir, gitDir)
	}
	return filepath.Join(filepath.Clean(gitDir), "config"), nil
}

func parseGitRemoteURLs(config string) []string {
	urls := make([]string, 0, 4)
	scanner := bufio.NewScanner(strings.NewReader(config))
	inRemoteSection := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			inRemoteSection = strings.HasPrefix(strings.ToLower(section), `remote "`)
			continue
		}
		if !inRemoteSection {
			continue
		}
		key, value, ok := splitConfigAssignment(line)
		if !ok || !strings.EqualFold(key, "url") {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			urls = append(urls, value)
		}
	}
	return urls
}

func splitConfigAssignment(line string) (string, string, bool) {
	if key, value, ok := strings.Cut(line, "="); ok {
		return strings.TrimSpace(key), strings.TrimSpace(value), true
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], strings.Join(fields[1:], " "), true
}

func classifyGitRemoteURL(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}

	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			return "https", strings.ToLower(parsed.Hostname())
		case "ssh", "git+ssh":
			return "ssh", strings.ToLower(parsed.Hostname())
		}
	}

	if strings.Contains(raw, ":") && !strings.Contains(strings.SplitN(raw, ":", 2)[0], "/") {
		hostPart := strings.SplitN(raw, ":", 2)[0]
		if user, host, ok := strings.Cut(hostPart, "@"); ok {
			_ = user
			return "ssh", strings.ToLower(host)
		}
		return "ssh", strings.ToLower(hostPart)
	}

	return "", ""
}

func filteredGitCredentialsFile(localPath, remotePath string, allowedHosts map[string]struct{}, redact bool) (remoteConfigFile, bool, error) {
	if len(allowedHosts) == 0 {
		return remoteConfigFile{}, false, nil
	}

	contents, ok, err := optionalLocalFileContents(localPath)
	if err != nil || !ok {
		return remoteConfigFile{}, ok, err
	}

	lines := make([]string, 0, 4)
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil {
			continue
		}
		if _, ok := allowedHosts[strings.ToLower(parsed.Hostname())]; !ok {
			continue
		}
		if redact {
			lines = append(lines, redactedPreviewContents(localPath))
		} else {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return remoteConfigFile{}, false, nil
	}

	return remoteConfigFile{
		path:     remotePath,
		contents: strings.Join(lines, "\n"),
		mode:     0o600,
	}, true, nil
}

func optionalLocalFileContents(localPath string) (string, bool, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

func relevantRemoteSSHFiles(localSSHDir string, allowedHosts []string, redact bool) ([]remoteConfigFile, error) {
	if len(allowedHosts) == 0 {
		return nil, nil
	}

	info, err := os.Stat(localSSHDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	files := make([]remoteConfigFile, 0, 8)
	seenPaths := make(map[string]struct{}, 8)

	configPath := filepath.Join(localSSHDir, "config")
	if configFile, ok, identityPaths, err := filteredSSHConfigFile(configPath, localSSHDir, allowedHosts, redact); err != nil {
		return nil, err
	} else {
		if ok {
			files = append(files, configFile)
			seenPaths[configFile.path] = struct{}{}
		}
		for _, identityPath := range identityPaths {
			remoteFile, ok, err := localSSHIdentityFile(identityPath, localSSHDir, redact)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, exists := seenPaths[remoteFile.path]; exists {
				continue
			}
			files = append(files, remoteFile)
			seenPaths[remoteFile.path] = struct{}{}
		}
	}

	if len(files) == 0 {
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa", "id_dsa"} {
			identityPath := filepath.Join(localSSHDir, name)
			remoteFile, ok, err := localSSHIdentityFile(identityPath, localSSHDir, redact)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, exists := seenPaths[remoteFile.path]; exists {
				continue
			}
			files = append(files, remoteFile)
			seenPaths[remoteFile.path] = struct{}{}
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})
	return files, nil
}

func filteredSSHConfigFile(configPath, localSSHDir string, allowedHosts []string, redact bool) (remoteConfigFile, bool, []string, error) {
	contents, ok, err := optionalLocalFileContents(configPath)
	if err != nil || !ok {
		return remoteConfigFile{}, false, nil, err
	}

	filtered, identityPaths := filterSSHConfig(contents, filepath.Dir(localSSHDir), allowedHosts)
	if strings.TrimSpace(filtered) == "" {
		return remoteConfigFile{}, false, identityPaths, nil
	}
	if redact {
		filtered = redactedPreviewContents(configPath)
	}

	return remoteConfigFile{
		path:     path.Join("/home/erun", ".ssh", "config"),
		contents: filtered,
		mode:     0o600,
	}, true, identityPaths, nil
}

func filterSSHConfig(configText, homeDir string, allowedHosts []string) (string, []string) {
	globalLines := make([]string, 0, 8)
	selectedSections := make([][]string, 0, 4)
	identityPaths := make([]string, 0, 4)
	seenIdentityPaths := make(map[string]struct{}, 4)

	var currentSection []string
	var currentPatterns []string
	inSection := false
	sectionSelected := false

	flushSection := func() {
		if inSection && sectionSelected && len(currentSection) > 0 {
			selectedSections = append(selectedSections, append([]string{}, currentSection...))
		}
		currentSection = nil
		currentPatterns = nil
		inSection = false
		sectionSelected = false
	}

	for _, line := range strings.Split(configText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flushSection()
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "host ") {
			flushSection()
			inSection = true
			currentSection = []string{line}
			currentPatterns = strings.Fields(strings.TrimSpace(trimmed[5:]))
			sectionSelected = sshHostPatternsMatch(currentPatterns, allowedHosts)
			continue
		}

		key, value, ok := splitConfigAssignment(trimmed)
		if !inSection {
			globalLines = append(globalLines, line)
			if ok && strings.EqualFold(key, "IdentityFile") {
				expanded := expandSSHIdentityFile(value, homeDir, "")
				if expanded != "" {
					if _, exists := seenIdentityPaths[expanded]; !exists {
						seenIdentityPaths[expanded] = struct{}{}
						identityPaths = append(identityPaths, expanded)
					}
				}
			}
			continue
		}

		currentSection = append(currentSection, line)
		if !sectionSelected || !ok || !strings.EqualFold(key, "IdentityFile") {
			continue
		}
		for _, host := range allowedHosts {
			if !sshHostPatternSetMatches(currentPatterns, host) {
				continue
			}
			expanded := expandSSHIdentityFile(value, homeDir, host)
			if expanded == "" {
				continue
			}
			if _, exists := seenIdentityPaths[expanded]; exists {
				continue
			}
			seenIdentityPaths[expanded] = struct{}{}
			identityPaths = append(identityPaths, expanded)
		}
	}
	flushSection()

	lines := make([]string, 0, len(globalLines)+len(selectedSections)*4)
	lines = append(lines, globalLines...)
	for _, section := range selectedSections {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, section...)
	}

	return strings.TrimSpace(strings.Join(lines, "\n")), identityPaths
}

func sshHostPatternsMatch(patterns []string, allowedHosts []string) bool {
	for _, host := range allowedHosts {
		if sshHostPatternSetMatches(patterns, host) {
			return true
		}
	}
	return false
}

func sshHostPatternSetMatches(patterns []string, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(patterns) == 0 {
		return false
	}
	matched := false
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		ok, err := filepath.Match(pattern, host)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

func expandSSHIdentityFile(value, homeDir, host string) string {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"~", homeDir,
		"%d", homeDir,
		"%h", host,
	)
	expanded := replacer.Replace(value)
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(homeDir, expanded)
	}
	return filepath.Clean(expanded)
}

func localSSHIdentityFile(localPath, localSSHDir string, redact bool) (remoteConfigFile, bool, error) {
	readPath := localPath
	info, err := os.Lstat(localPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return remoteConfigFile{}, false, nil
		}
		return remoteConfigFile{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(localPath)
		if err != nil {
			return remoteConfigFile{}, false, err
		}
		resolvedInfo, err := os.Stat(resolved)
		if err != nil {
			return remoteConfigFile{}, false, err
		}
		if !resolvedInfo.Mode().IsRegular() {
			return remoteConfigFile{}, false, nil
		}
		readPath = resolved
	} else if !info.Mode().IsRegular() {
		return remoteConfigFile{}, false, nil
	}

	contents, ok, err := optionalLocalFileContents(readPath)
	if err != nil || !ok {
		return remoteConfigFile{}, ok, err
	}
	if redact {
		contents = redactedPreviewContents(localPath)
	}
	relPath, err := filepath.Rel(localSSHDir, localPath)
	if err != nil {
		return remoteConfigFile{}, false, err
	}
	return remoteConfigFile{
		path:     path.Join("/home/erun", ".ssh", filepath.ToSlash(relPath)),
		contents: contents,
		mode:     0o600,
	}, true, nil
}

func redactedPreviewContents(localPath string) string {
	return fmt.Sprintf("# redacted preview of %s\n", localPath)
}

func containsRemotePath(files []remoteConfigFile, prefix string) bool {
	for _, file := range files {
		if strings.HasPrefix(file.path, prefix) {
			return true
		}
	}
	return false
}

func containsExactRemotePath(files []remoteConfigFile, target string) bool {
	for _, file := range files {
		if file.path == target {
			return true
		}
	}
	return false
}

func shellWriteFile(pathValue, contents string) string {
	if !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	return fmt.Sprintf("cat > %s <<'EOF'\n%sEOF", shellQuote(pathValue), contents)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
