package eruncommon

import (
	"bytes"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

type remoteRepositoryState struct {
	Exists              bool
	PublicKey           string
	CodeCommitPublicKey string
	HasSSHConfig        bool
}

type remoteRepositorySpec struct {
	URL                string
	CodeCommitHost     string
	CodeCommitSSHKeyID string
	UseHostConfig      bool
}

const remoteRepositoryAccessRetryInterval = 2 * time.Second

var codeCommitHostPattern = regexp.MustCompile(`^git-codecommit\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?$`)

type remoteDefaultDevopsFile struct {
	Path    string
	Mode    string
	Content []byte
	Legacy  []string
}

func (s bootstrapRunner) ensureRemoteRepository(params BootstrapInitParams, tenant, envName, kubernetesContext, projectRoot string) (ShellLaunchParams, error) {
	ports := DefaultEnvironmentLocalPorts()
	if portStore, ok := s.Store.(environmentPortStore); ok {
		resolved, err := ResolveEnvironmentLocalPorts(portStore, tenant, envName)
		if err == nil {
			ports = resolved
		}
	}
	target := OpenResult{
		Tenant:      tenant,
		Environment: envName,
		TenantConfig: TenantConfig{
			Name:               tenant,
			ProjectRoot:        projectRoot,
			DefaultEnvironment: envName,
		},
		EnvConfig: EnvConfig{
			Name:              envName,
			RepoPath:          projectRoot,
			KubernetesContext: kubernetesContext,
			Remote:            true,
		},
		LocalPorts: ports,
		RepoPath:   projectRoot,
		Title:      tenant + "-" + envName,
	}
	req := ShellLaunchParamsFromResult(target)

	if err := s.ensureRemoteRuntime(target, req, params.RuntimeVersion, params.RuntimeImage); err != nil {
		return ShellLaunchParams{}, err
	}
	if params.NoGit {
		return req, s.ensureRemoteWorktree(req, projectRoot)
	}

	state, err := s.remoteRepositoryState(req, projectRoot)
	if err != nil {
		return ShellLaunchParams{}, err
	}
	if state.Exists {
		return req, s.pullRemoteRepository(req, projectRoot)
	}

	repositoryURL, err := s.resolveRemoteRepositoryURL(params, tenant, envName)
	if err != nil {
		return ShellLaunchParams{}, err
	}
	repository, err := parseRemoteRepositorySpec(repositoryURL)
	if err != nil {
		return ShellLaunchParams{}, err
	}
	repository, usingHostConfig, err := s.resolveExistingRemoteHostConfig(params, tenant, envName, req, state, repository)
	if err != nil {
		return ShellLaunchParams{}, err
	}
	if !usingHostConfig {
		repository, err = s.resolveRemoteRepositoryCredentials(params, tenant, envName, repository, state.CodeCommitPublicKey)
		if err != nil {
			return ShellLaunchParams{}, err
		}
		publicKey := state.PublicKey
		if repository.CodeCommitHost != "" {
			publicKey = state.CodeCommitPublicKey
		}
		if err := s.waitForRemoteKeyImport(params, tenant, envName, req, repository, publicKey); err != nil {
			return ShellLaunchParams{}, err
		}
	}
	return req, s.cloneRemoteRepository(req, projectRoot, repository)
}

func (s bootstrapRunner) ensureRemoteWorktree(req ShellLaunchParams, projectRoot string) error {
	script := strings.Join([]string{
		"set -eu",
		fmt.Sprintf("mkdir -p %s", shellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-worktree", script)
	if err != nil {
		return fmt.Errorf("create remote worktree: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) ensureRemoteRuntime(target OpenResult, req ShellLaunchParams, runtimeVersion, runtimeImage string) error {
	runtimeImage = strings.TrimSpace(runtimeImage)
	if runtimeImage == "" {
		runtimeImage = DevopsComponentName
	}
	spec, err := resolveDefaultDevopsDeploySpecWithImage(target, runtimeImage)
	if err != nil {
		return err
	}
	spec.Deploy.ReleaseName = RuntimeReleaseName(target.Tenant)
	spec.Deploy.Version = strings.TrimSpace(runtimeVersion)
	if err := RunDeploySpec(s.Context, spec, nil, nil, s.DeployHelmChart); err != nil {
		return err
	}

	s.Context.TraceCommand("", "kubectl", kubectlDeploymentWaitArgs(req)...)
	if s.Context.DryRun || s.WaitForRemoteRuntime == nil {
		return nil
	}
	return s.WaitForRemoteRuntime(req)
}

func (s bootstrapRunner) ensureRemoteDefaultDevopsBootstrap(req ShellLaunchParams, projectRoot, tenant, envName, runtimeVersion string) error {
	files, err := remoteDefaultDevopsBootstrapFiles(projectRoot, tenant, envName, runtimeVersion)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	lines := []string{"set -eu"}
	for index, file := range files {
		lines = append(lines, remoteDefaultDevopsFileScript(index, file)...)
	}

	output, err := s.runRemoteScript(req, "remote-default-devops-bootstrap", strings.Join(lines, "\n"))
	if err != nil {
		return fmt.Errorf("bootstrap remote devops module: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func remoteDefaultDevopsBootstrapFiles(projectRoot, tenant, envName, runtimeVersion string) ([]remoteDefaultDevopsFile, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	tenant = strings.TrimSpace(tenant)
	if projectRoot == "" || tenant == "" {
		return nil, nil
	}
	projectRoot = path.Clean(projectRoot)
	moduleName := RuntimeReleaseName(tenant)
	files := make([]remoteDefaultDevopsFile, 0, len(defaultDevopsModuleTemplates)+len(defaultDevopsChartTemplates)+1)
	for _, templateFile := range defaultDevopsModuleTemplates {
		data, err := defaultDevopsModuleFiles.ReadFile(templateFile.AssetPath)
		if err != nil {
			return nil, err
		}
		targetPath := strings.ReplaceAll(templateFile.TargetPath, "__MODULE_NAME__", moduleName)
		resolvedPath := path.Join(projectRoot, targetPath)
		content := renderDefaultDevopsModuleTemplate(templateFile.AssetPath, moduleName, runtimeVersion, data)
		files = append(files, remoteDefaultDevopsFile{
			Path:    resolvedPath,
			Mode:    fmt.Sprintf("%o", templateFile.Mode.Perm()),
			Content: content,
			Legacy:  defaultDevopsLegacyContents(resolvedPath, content),
		})
	}

	replacer := strings.NewReplacer("__MODULE_NAME__", moduleName)
	for _, templateFile := range defaultDevopsChartTemplates {
		data, err := defaultDevopsChartFiles.ReadFile(templateFile.AssetPath)
		if err != nil {
			return nil, err
		}
		targetPath := replacer.Replace(templateFile.TargetPath)
		resolvedPath := path.Join(projectRoot, targetPath)
		content := renderDefaultDevopsChartTemplate(templateFile.AssetPath, moduleName, moduleName, data)
		files = append(files, remoteDefaultDevopsFile{
			Path:    resolvedPath,
			Mode:    fmt.Sprintf("%o", templateFile.Mode.Perm()),
			Content: content,
			Legacy:  defaultDevopsLegacyContents(resolvedPath, content),
		})
	}

	if strings.TrimSpace(envName) != "" && !isLocalEnvironment(envName) {
		resolvedPath := path.Join(projectRoot, moduleName, "k8s", moduleName, "values."+strings.ToLower(strings.TrimSpace(envName))+".yaml")
		files = append(files, remoteDefaultDevopsFile{
			Path: resolvedPath,
			Mode: "644",
		})
	}
	return files, nil
}

func remoteDefaultDevopsFileScript(index int, file remoteDefaultDevopsFile) []string {
	tmp := fmt.Sprintf("erun_bootstrap_tmp_%d", index)
	lines := []string{
		fmt.Sprintf("mkdir -p %s", shellQuote(path.Dir(file.Path))),
		fmt.Sprintf("if [ -d %s ]; then echo %s >&2; exit 1; fi", shellQuote(file.Path), shellQuote(fmt.Sprintf("%q is a directory", file.Path))),
		fmt.Sprintf("%s=$(mktemp)", tmp),
		fmt.Sprintf("printf %%s %s > \"$%s\"", shellQuote(string(file.Content)), tmp),
		"replace=false",
		fmt.Sprintf("if [ ! -e %s ]; then", shellQuote(file.Path)),
		"  replace=true",
		fmt.Sprintf("elif cmp -s \"$%s\" %s; then", tmp, shellQuote(file.Path)),
		"  replace=false",
	}
	if len(file.Legacy) > 0 {
		lines = append(lines, "else")
		for legacyIndex, legacy := range file.Legacy {
			legacyTmp := fmt.Sprintf("erun_bootstrap_legacy_%d_%d", index, legacyIndex)
			lines = append(lines,
				fmt.Sprintf("  %s=$(mktemp)", legacyTmp),
				fmt.Sprintf("  printf %%s %s > \"$%s\"", shellQuote(legacy), legacyTmp),
				fmt.Sprintf("  if cmp -s \"$%s\" %s; then replace=true; fi", legacyTmp, shellQuote(file.Path)),
				fmt.Sprintf("  rm -f \"$%s\"", legacyTmp),
			)
		}
	} else {
		lines = append(lines,
			"else",
			"  replace=false",
		)
	}
	lines = append(lines,
		"fi",
		fmt.Sprintf("if [ \"$replace\" = true ]; then cp \"$%s\" %s; chmod %s %s; fi", tmp, shellQuote(file.Path), shellQuote(file.Mode), shellQuote(file.Path)),
		fmt.Sprintf("rm -f \"$%s\"", tmp),
	)
	return lines
}

func (s bootstrapRunner) resolveRemoteRepositoryURL(params BootstrapInitParams, tenant, envName string) (string, error) {
	if params.RemoteRepositoryURL != "" {
		return params.RemoteRepositoryURL, nil
	}
	interaction := BootstrapInitInteraction{
		Type:  BootstrapInitInteractionRemoteRepository,
		Label: remoteRepositoryLabel(tenant, envName),
	}
	if s.PromptRemoteRepositoryURL == nil {
		return "", BootstrapInitInteractionError{Interaction: interaction}
	}
	repositoryURL, err := s.PromptRemoteRepositoryURL(interaction.Label)
	if err != nil {
		return "", err
	}
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return "", BootstrapInitInteractionError{Interaction: interaction}
	}
	return repositoryURL, nil
}

func (s bootstrapRunner) resolveRemoteRepositoryCredentials(params BootstrapInitParams, tenant, envName string, spec remoteRepositorySpec, codeCommitPublicKey string) (remoteRepositorySpec, error) {
	if spec.CodeCommitHost == "" || spec.CodeCommitSSHKeyID != "" {
		return spec, nil
	}
	keyID := strings.TrimSpace(params.CodeCommitSSHKeyID)
	details := codeCommitSetupDetails(spec, codeCommitPublicKey, "<SSH public key ID>")
	if keyID == "" {
		s.Context.Info(details)
		if s.PromptCodeCommitSSHKeyID == nil {
			return remoteRepositorySpec{}, BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
				Type:    BootstrapInitInteractionCodeCommitSSHKeyID,
				Label:   codeCommitSSHKeyIDLabel(tenant, envName),
				Details: details,
			}}
		}
		prompted, err := s.PromptCodeCommitSSHKeyID(codeCommitSSHKeyIDLabel(tenant, envName))
		if err != nil {
			return remoteRepositorySpec{}, err
		}
		keyID = strings.TrimSpace(prompted)
	}
	if keyID == "" {
		return remoteRepositorySpec{}, BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:    BootstrapInitInteractionCodeCommitSSHKeyID,
			Label:   codeCommitSSHKeyIDLabel(tenant, envName),
			Details: details,
		}}
	}
	spec.CodeCommitSSHKeyID = keyID
	return spec, nil
}

func (s bootstrapRunner) resolveExistingRemoteHostConfig(params BootstrapInitParams, tenant, envName string, req ShellLaunchParams, state remoteRepositoryState, repository remoteRepositorySpec) (remoteRepositorySpec, bool, error) {
	if !state.HasSSHConfig {
		return repository, false, nil
	}
	if err := s.verifyRemoteRepositoryAccessWithHostConfig(req, repository); err != nil {
		return repository, false, nil
	}
	if params.AutoApprove {
		repository.UseHostConfig = true
		return repository, true, nil
	}
	if params.ConfirmRemoteHostConfig != nil {
		if !*params.ConfirmRemoteHostConfig {
			return repository, false, nil
		}
		repository.UseHostConfig = true
		return repository, true, nil
	}
	confirmed, err := s.confirm(BootstrapInitInteraction{
		Type:    BootstrapInitInteractionConfirmRemoteHost,
		Label:   remoteHostConfigLabel(tenant, envName),
		Details: repository.URL,
	})
	if err != nil {
		return remoteRepositorySpec{}, false, err
	}
	if !confirmed {
		return repository, false, nil
	}
	repository.UseHostConfig = true
	return repository, true, nil
}

func (s bootstrapRunner) waitForRemoteKeyImport(params BootstrapInitParams, tenant, envName string, req ShellLaunchParams, repository remoteRepositorySpec, publicKey string) error {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey != "" {
		if repository.CodeCommitHost != "" {
			s.Context.Info(codeCommitSetupDetails(repository, publicKey, repository.CodeCommitSSHKeyID))
		} else {
			s.Context.Info("Import this SSH public key into your git host before continuing:")
			s.Context.Info(publicKey)
		}
	}
	if params.ConfirmRemoteKeyImport != nil {
		if !*params.ConfirmRemoteKeyImport {
			return fmt.Errorf("remote SSH key import cancelled")
		}
	} else if s.Confirm == nil {
		return BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:    BootstrapInitInteractionConfirmRemoteKey,
			Label:   remoteKeyImportLabel(tenant, envName),
			Details: publicKey,
		}}
	}

	s.Context.Info("Waiting for the SSH key to be deployed to the git host. Rechecking every 2 seconds. Press Ctrl+C to cancel.")
	for attempts := 0; ; attempts++ {
		if err := s.verifyRemoteRepositoryAccess(req, repository); err == nil {
			if attempts > 0 {
				s.Context.Info("Remote repository access confirmed.")
			}
			return nil
		}

		s.Context.Info("SSH key not active yet; retrying in 2 seconds...")
		if s.Sleep != nil {
			s.Sleep(remoteRepositoryAccessRetryInterval)
		}
	}
}

func (s bootstrapRunner) remoteRepositoryState(req ShellLaunchParams, projectRoot string) (remoteRepositoryState, error) {
	script := strings.Join([]string{
		"set -eu",
		"mkdir -p \"$HOME/.ssh\"",
		"chmod 700 \"$HOME/.ssh\"",
		"key=\"$HOME/.ssh/id_ed25519\"",
		"if [ ! -f \"$key\" ]; then ssh-keygen -t ed25519 -N '' -f \"$key\" >/dev/null 2>&1; fi",
		"chmod 600 \"$key\"",
		"chmod 644 \"$key.pub\"",
		"codecommit_key=\"$HOME/.ssh/id_rsa_codecommit\"",
		"if [ ! -f \"$codecommit_key\" ]; then ssh-keygen -t rsa -b 4096 -N '' -f \"$codecommit_key\" >/dev/null 2>&1; fi",
		"chmod 600 \"$codecommit_key\"",
		"chmod 644 \"$codecommit_key.pub\"",
		fmt.Sprintf("mkdir -p %s", shellQuote(projectRoot)),
		fmt.Sprintf("if [ -d %s/.git ]; then printf 'repo_exists\\n'; else printf 'repo_missing\\n'; fi", shellQuote(projectRoot)),
		"printf '__ERUN_REMOTE_PUBLIC_KEY__\\n'",
		"cat \"$key.pub\"",
		"printf '\\n__ERUN_REMOTE_CODECOMMIT_PUBLIC_KEY__\\n'",
		"cat \"$codecommit_key.pub\"",
		"printf '\\n__ERUN_REMOTE_SSH_CONFIG__\\n'",
		"if [ -s \"$HOME/.ssh/config\" ]; then printf 'exists\\n'; else printf 'missing\\n'; fi",
	}, "\n")

	output, err := s.runRemoteScript(req, "remote-repository-state", script)
	if err != nil {
		return remoteRepositoryState{}, err
	}
	if s.Context.DryRun {
		return remoteRepositoryState{
			PublicKey:           "<remote-public-key>",
			CodeCommitPublicKey: "<remote-codecommit-rsa-public-key>",
			Exists:              false,
			HasSSHConfig:        false,
		}, nil
	}

	lines := strings.Split(strings.TrimSpace(output.Stdout), "\n")
	if len(lines) == 0 {
		return remoteRepositoryState{}, fmt.Errorf("remote repository state command returned no output")
	}
	state := remoteRepositoryState{Exists: strings.TrimSpace(lines[0]) == "repo_exists"}
	state.PublicKey = remoteRepositoryStateSection(lines, "__ERUN_REMOTE_PUBLIC_KEY__")
	state.CodeCommitPublicKey = remoteRepositoryStateSection(lines, "__ERUN_REMOTE_CODECOMMIT_PUBLIC_KEY__")
	state.HasSSHConfig = strings.EqualFold(remoteRepositoryStateSection(lines, "__ERUN_REMOTE_SSH_CONFIG__"), "exists")
	return state, nil
}

func remoteRepositoryStateSection(lines []string, marker string) string {
	for index, line := range lines {
		if strings.TrimSpace(line) != marker {
			continue
		}
		section := make([]string, 0, len(lines)-index-1)
		for _, value := range lines[index+1:] {
			if strings.HasPrefix(strings.TrimSpace(value), "__ERUN_REMOTE_") {
				break
			}
			section = append(section, value)
		}
		return strings.TrimSpace(strings.Join(section, "\n"))
	}
	return ""
}

func (s bootstrapRunner) verifyRemoteRepositoryAccess(req ShellLaunchParams, repository remoteRepositorySpec) error {
	script := strings.Join([]string{
		"set -eu",
		remoteRepositorySSHConfigScript(repository),
		fmt.Sprintf("ssh_command=%s", shellQuote(remoteRepositorySSHCommand(repository))),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" ls-remote %s HEAD >/dev/null", shellQuote(repository.URL)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-access", script)
	if err != nil {
		return fmt.Errorf("verify remote repository access: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) verifyRemoteRepositoryAccessWithHostConfig(req ShellLaunchParams, repository remoteRepositorySpec) error {
	script := strings.Join([]string{
		"set -eu",
		"test -s \"$HOME/.ssh/config\"",
		fmt.Sprintf("ssh_command=%s", shellQuote(`ssh -F "$HOME/.ssh/config" -o StrictHostKeyChecking=accept-new`)),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" ls-remote %s HEAD >/dev/null", shellQuote(repository.URL)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-existing-host-config", script)
	if err != nil {
		return fmt.Errorf("verify remote repository access with existing SSH host config: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) cloneRemoteRepository(req ShellLaunchParams, projectRoot string, repository remoteRepositorySpec) error {
	script := strings.Join([]string{
		"set -eu",
		remoteRepositorySSHConfigScript(repository),
		fmt.Sprintf("ssh_command=%s", shellQuote(remoteRepositorySSHCommand(repository))),
		fmt.Sprintf("mkdir -p %s", shellQuote(path.Dir(projectRoot))),
		fmt.Sprintf("mkdir -p %s", shellQuote(projectRoot)),
		fmt.Sprintf("if [ -n \"$(ls -A %s 2>/dev/null)\" ] && [ ! -d %s/.git ]; then echo 'remote worktree directory exists and is not empty' >&2; exit 1; fi", shellQuote(projectRoot), shellQuote(projectRoot)),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" clone %s %s", shellQuote(repository.URL), shellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-clone", script)
	if err != nil {
		return fmt.Errorf("clone remote repository: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) pullRemoteRepository(req ShellLaunchParams, projectRoot string) error {
	script := strings.Join([]string{
		"set -eu",
		"ssh_command='ssh -i \"$HOME/.ssh/id_ed25519\" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'",
		fmt.Sprintf("git -C %s -c core.sshCommand=\"$ssh_command\" pull --ff-only", shellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-pull", script)
	if err != nil {
		return fmt.Errorf("pull remote repository: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func parseRemoteRepositorySpec(repositoryURL string) (remoteRepositorySpec, error) {
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return remoteRepositorySpec{}, fmt.Errorf("git remote URL is required")
	}
	parseURL := repositoryURL
	codeCommitBareURL := strings.HasPrefix(parseURL, "git-codecommit.")
	if codeCommitBareURL {
		parseURL = "ssh://" + parseURL
	}
	if !codeCommitBareURL && !strings.Contains(parseURL, "://") {
		return remoteRepositorySpec{URL: repositoryURL}, nil
	}
	parsed, err := url.Parse(parseURL)
	if err != nil {
		return remoteRepositorySpec{}, err
	}
	host := strings.TrimSpace(parsed.Hostname())
	if !codeCommitHostPattern.MatchString(host) {
		return remoteRepositorySpec{URL: repositoryURL}, nil
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "ssh"
	}
	keyID := ""
	if parsed.User != nil {
		keyID = parsed.User.Username()
		parsed.User = nil
	}
	return remoteRepositorySpec{
		URL:                parsed.String(),
		CodeCommitHost:     host,
		CodeCommitSSHKeyID: keyID,
	}, nil
}

func remoteRepositorySSHConfigScript(repository remoteRepositorySpec) string {
	if repository.UseHostConfig {
		return ":"
	}
	if repository.CodeCommitHost == "" {
		return ":"
	}
	return strings.Join([]string{
		"cat > \"$HOME/.ssh/config\" <<'EOF'",
		codeCommitSSHConfig(repository, repository.CodeCommitSSHKeyID),
		"EOF",
		"chmod 600 \"$HOME/.ssh/config\"",
	}, "\n")
}

func remoteRepositorySSHCommand(repository remoteRepositorySpec) string {
	if repository.UseHostConfig {
		return `ssh -F "$HOME/.ssh/config" -o StrictHostKeyChecking=accept-new`
	}
	if repository.CodeCommitHost != "" {
		return `ssh -F "$HOME/.ssh/config"`
	}
	return `ssh -i "$HOME/.ssh/id_ed25519" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new`
}

func codeCommitSSHConfig(repository remoteRepositorySpec, keyID string) string {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		keyID = "<SSH public key ID>"
	}
	return strings.Join([]string{
		"Host " + repository.CodeCommitHost,
		"  User " + keyID,
		"  IdentityFile ~/.ssh/id_rsa_codecommit",
		"  IdentitiesOnly yes",
		"  StrictHostKeyChecking accept-new",
	}, "\n")
}

func codeCommitSetupDetails(repository remoteRepositorySpec, publicKey, keyID string) string {
	return strings.Join([]string{
		"Upload this SSH public key to the IAM user that should access CodeCommit:",
		strings.TrimSpace(publicKey),
		"",
		"Use the SSH public key ID returned by IAM in this SSH host config:",
		codeCommitSSHConfig(repository, keyID),
	}, "\n")
}

func (s bootstrapRunner) runRemoteScript(req ShellLaunchParams, label, script string) (RemoteCommandResult, error) {
	traceArgs := append([]string{}, kubectlRemoteExecArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	s.Context.TraceCommand("", "kubectl", traceArgs...)
	s.Context.TraceBlock(label, script)
	if s.Context.DryRun {
		return RemoteCommandResult{}, nil
	}
	return s.RunRemoteCommand(req, script)
}

func kubectlRemoteExecArgs(req ShellLaunchParams, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec")
	args = append(args, "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

func RunRemoteCommand(req ShellLaunchParams, script string) (RemoteCommandResult, error) {
	cmd := exec.Command("kubectl", kubectlRemoteExecArgs(req, script)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func formatRemoteCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}
