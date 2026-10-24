package setup

import (
	"fmt"
	"path"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
)

type remoteRepositoryState struct {
	Exists              bool
	PublicKey           string
	CodeCommitPublicKey string
	HasSSHConfig        bool
}

// ensureRemoteRepository takes the reconciled env, not params, so stored values
// stand for omitted flags.
func (s BootstrapRunner) ensureRemoteRepository(params open.BootstrapInitParams, tenant, envName, projectRoot string, env config.EnvConfig) (config.ShellLaunchParams, remoteinit.RemoteRepositorySpec, remoteProvisionedSecrets, error) {
	target := s.remoteRepositoryOpenResult(tenant, envName, env.KubernetesContext, projectRoot, params.ResolvedType())
	target.EnvConfig.RuntimePod = config.NormalizeRuntimePodResources(env.RuntimePod)
	// The runtime registry redirects chart resolution, so init's own deploy needs it.
	target.EnvConfig.RuntimeRegistry = strings.TrimSpace(env.RuntimeRegistry)
	// A private runtime image cannot be pulled on init's own deploy without these.
	target.EnvConfig.ImagePullSecrets = env.ImagePullSecrets
	// Without registries the deploy falls back to the default registry.
	target.EnvConfig.ContainerRegistries = env.ContainerRegistries

	// Provision before deploy so the pod mounts it from first boot.
	registryCredentialSecretName, err := s.resolveRegistryCredentialSecret(target)
	if err != nil {
		return config.ShellLaunchParams{}, remoteinit.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	target.EnvConfig.RegistryCredentialSecretName = registryCredentialSecretName

	// Likewise, so a fresh env can drive the merge queue without an in-pod sign-in.
	platformAliasSecret, err := s.resolvePlatformAliasSecret(target)
	if err != nil {
		return config.ShellLaunchParams{}, remoteinit.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	target.EnvConfig.PlatformAliasSecretName = platformAliasSecret
	provisioned := remoteProvisionedSecrets{registryCredential: registryCredentialSecretName, platformAlias: platformAliasSecret}

	req := cloud.ShellLaunchParamsFromResult(target)

	if err := s.ensureRemoteRuntime(target, req, env.RuntimeVersion, env.RuntimeImage, params.MCPAuthPublicKeyPath); err != nil {
		return config.ShellLaunchParams{}, remoteinit.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	if err := s.ensureRemoteRegistryCredentials(target, req); err != nil {
		return config.ShellLaunchParams{}, remoteinit.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	if params.NoGit {
		return req, remoteinit.RemoteRepositorySpec{}, provisioned, s.ensureRemoteWorktree(req, projectRoot)
	}

	state, err := s.remoteRepositoryState(req, projectRoot)
	if err != nil {
		return config.ShellLaunchParams{}, remoteinit.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	if state.Exists {
		return req, remoteinit.RemoteRepositorySpec{}, provisioned, s.pullRemoteRepository(req, projectRoot)
	}

	repository, err := s.remoteRepositorySpecForClone(params, tenant, envName, req, state)
	if err != nil {
		return config.ShellLaunchParams{}, remoteinit.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	return req, repository, provisioned, s.cloneRemoteRepository(req, projectRoot, repository)
}

func (s BootstrapRunner) writeRemoteInitMarker(req config.ShellLaunchParams, marker model.RemoteInitMarker) error {
	script := strings.Join([]string{
		"set -eu",
		remoteInitMarkerWriteScript(marker),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-init-marker", script)
	if err != nil {
		return fmt.Errorf("write remote init marker: %w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s BootstrapRunner) remoteRepositorySpecForClone(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, state remoteRepositoryState) (remoteinit.RemoteRepositorySpec, error) {
	repositoryURL, err := s.resolveRemoteRepositoryURL(params, tenant, envName)
	if err != nil {
		return remoteinit.RemoteRepositorySpec{}, err
	}
	repository, err := remoteinit.ParseRemoteRepositorySpec(repositoryURL)
	if err != nil {
		return remoteinit.RemoteRepositorySpec{}, err
	}
	repository, usingHostConfig, err := s.resolveExistingRemoteHostConfig(params, tenant, envName, req, state, repository)
	if err != nil {
		return remoteinit.RemoteRepositorySpec{}, err
	}
	if !usingHostConfig {
		return s.remoteRepositorySpecWithCredentials(params, tenant, envName, req, state, repository)
	}
	return repository, nil
}

func (s BootstrapRunner) remoteRepositorySpecWithCredentials(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, state remoteRepositoryState, repository remoteinit.RemoteRepositorySpec) (remoteinit.RemoteRepositorySpec, error) {
	repository, err := s.resolveRemoteRepositoryCredentials(params, tenant, envName, repository, state.CodeCommitPublicKey)
	if err != nil {
		return remoteinit.RemoteRepositorySpec{}, err
	}
	publicKey := state.PublicKey
	if repository.CodeCommitHost != "" {
		publicKey = state.CodeCommitPublicKey
	}
	if err := s.waitForRemoteKeyImport(params, tenant, envName, req, repository, publicKey); err != nil {
		return remoteinit.RemoteRepositorySpec{}, err
	}
	return repository, nil
}

func (s BootstrapRunner) remoteRepositoryOpenResult(tenant, envName, kubernetesContext, projectRoot string, envType config.EnvironmentType) config.OpenResult {
	return config.OpenResult{
		Tenant:       tenant,
		Environment:  envName,
		TenantConfig: remoteRepositoryTenantConfig(tenant, envName),
		EnvConfig:    remoteRepositoryEnvConfig(envName, kubernetesContext, projectRoot, envType),
		LocalPorts:   s.remoteRepositoryLocalPorts(tenant, envName),
		RepoPath:     projectRoot,
		Title:        tenant + "-" + envName,
	}
}

func remoteRepositoryTenantConfig(tenant, envName string) model.TenantConfig {
	return model.TenantConfig{Name: tenant, DefaultEnvironment: envName}
}

func remoteRepositoryEnvConfig(envName, kubernetesContext, projectRoot string, envType config.EnvironmentType) config.EnvConfig {
	if !envType.IsValid() {
		envType = config.EnvironmentTypeRemoteAgent
	}
	cfg := config.EnvConfig{Name: envName, LocalRepoPath: projectRoot, KubernetesContext: kubernetesContext, Type: envType}
	return cfg
}

func (s BootstrapRunner) remoteRepositoryLocalPorts(tenant, envName string) model.EnvironmentLocalPorts {
	portStore, ok := s.Store.(config.EnvironmentPortStore)
	if !ok {
		return model.EnvironmentLocalPorts{}
	}
	resolved, err := config.ResolveEnvironmentLocalPorts(portStore, tenant, envName)
	if err != nil {
		return model.EnvironmentLocalPorts{}
	}
	return resolved
}

func (s BootstrapRunner) ensureRemoteWorktree(req config.ShellLaunchParams, projectRoot string) error {
	script := strings.Join([]string{
		"set -eu",
		fmt.Sprintf("mkdir -p %s", core.ShellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-worktree", script)
	if err != nil {
		return fmt.Errorf("create remote worktree: %w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s BootstrapRunner) ensureRemoteRuntime(target config.OpenResult, req config.ShellLaunchParams, runtimeVersion, runtimeImage, mcpAuthPublicKeyPath string) error {
	runtimeImage = strings.TrimSpace(runtimeImage)
	runtimeImageStated := runtimeImage != "" && runtimeImage != config.DevopsComponentName
	if runtimeImageStated {
		target.EnvConfig.RuntimeImage = runtimeImage
	}
	// Pass cluster: entries as-is; the chart expands them in-pod. Host-concretizing
	// pins the build registry to an unreachable localhost port-forward.
	spec, err := deploy.ResolvePublishedDevopsDeploySpecWithOverrides(s.Context, target, runtimeVersion, "", runtimeImageStated)
	if err != nil {
		return err
	}
	// Carry the MCP-auth key so the desktop need not redeploy right after init.
	if err := deploy.ApplyMCPAuthToRuntimeSpec(s.Context, model.DeployTarget{Tenant: target.Tenant, MCPAuthPublicKeyPath: mcpAuthPublicKeyPath}, &spec); err != nil {
		return err
	}
	if err := deploy.RunDeploySpec(s.Context, spec, s.DeployHelmChart); err != nil {
		return err
	}

	s.Context.TraceCommand("", "kubectl", cloud.KubectlDeploymentWaitArgs(req)...)
	if s.Context.DryRun || s.WaitForRemoteRuntime == nil {
		return nil
	}
	return s.WaitForRemoteRuntime(req)
}

func (s BootstrapRunner) resolveRemoteRepositoryURL(params open.BootstrapInitParams, tenant, envName string) (string, error) {
	if params.RemoteRepositoryURL != "" {
		return params.RemoteRepositoryURL, nil
	}
	interaction := model.BootstrapInitInteraction{
		Type:  model.BootstrapInitInteractionRemoteRepository,
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

func (s BootstrapRunner) resolveRemoteRepositoryCredentials(params open.BootstrapInitParams, tenant, envName string, spec remoteinit.RemoteRepositorySpec, codeCommitPublicKey string) (remoteinit.RemoteRepositorySpec, error) {
	if spec.CodeCommitHost == "" || spec.CodeCommitSSHKeyID != "" {
		return spec, nil
	}
	keyID := strings.TrimSpace(params.CodeCommitSSHKeyID)
	details := remoteinit.CodeCommitSetupDetails(spec, codeCommitPublicKey, "<SSH public key ID>")
	if keyID == "" {
		s.Context.Info(details)
		if s.PromptCodeCommitSSHKeyID == nil {
			return remoteinit.RemoteRepositorySpec{}, BootstrapInitInteractionError{Interaction: model.BootstrapInitInteraction{
				Type:    model.BootstrapInitInteractionCodeCommitSSHKeyID,
				Label:   remoteinit.CodeCommitSSHKeyIDLabel(tenant, envName),
				Details: details,
			}}
		}
		prompted, err := s.PromptCodeCommitSSHKeyID(remoteinit.CodeCommitSSHKeyIDLabel(tenant, envName))
		if err != nil {
			return remoteinit.RemoteRepositorySpec{}, err
		}
		keyID = strings.TrimSpace(prompted)
	}
	if keyID == "" {
		return remoteinit.RemoteRepositorySpec{}, BootstrapInitInteractionError{Interaction: model.BootstrapInitInteraction{
			Type:    model.BootstrapInitInteractionCodeCommitSSHKeyID,
			Label:   remoteinit.CodeCommitSSHKeyIDLabel(tenant, envName),
			Details: details,
		}}
	}
	spec.CodeCommitSSHKeyID = keyID
	return spec, nil
}

func (s BootstrapRunner) resolveExistingRemoteHostConfig(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, state remoteRepositoryState, repository remoteinit.RemoteRepositorySpec) (remoteinit.RemoteRepositorySpec, bool, error) {
	if !state.HasSSHConfig {
		return repository, false, nil
	}
	if !s.hostConfigReachesRemoteRepository(req, repository) {
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
	confirmed, err := s.confirm(model.BootstrapInitInteraction{
		Type:    model.BootstrapInitInteractionConfirmRemoteHost,
		Label:   remoteHostConfigLabel(tenant, envName),
		Details: repository.URL,
	})
	if err != nil {
		return remoteinit.RemoteRepositorySpec{}, false, err
	}
	if !confirmed {
		return repository, false, nil
	}
	repository.UseHostConfig = true
	return repository, true, nil
}

func (s BootstrapRunner) waitForRemoteKeyImport(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, repository remoteinit.RemoteRepositorySpec, publicKey string) error {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey != "" {
		if repository.CodeCommitHost != "" {
			s.Context.Info(remoteinit.CodeCommitSetupDetails(repository, publicKey, repository.CodeCommitSSHKeyID))
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
		return BootstrapInitInteractionError{Interaction: model.BootstrapInitInteraction{
			Type:    model.BootstrapInitInteractionConfirmRemoteKey,
			Label:   remoteKeyImportLabel(tenant, envName),
			Details: publicKey,
		}}
	}

	return gitx.WaitForGitAccess(s.Context, s.Sleep, func() error {
		return s.verifyRemoteRepositoryAccess(req, repository)
	})
}

func (s BootstrapRunner) remoteRepositoryState(req config.ShellLaunchParams, projectRoot string) (remoteRepositoryState, error) {
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
		fmt.Sprintf("mkdir -p %s", core.ShellQuote(projectRoot)),
		fmt.Sprintf("if [ -d %s/.git ]; then printf 'repo_exists\\n'; else printf 'repo_missing\\n'; fi", core.ShellQuote(projectRoot)),
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

func (s BootstrapRunner) verifyRemoteRepositoryAccess(req config.ShellLaunchParams, repository remoteinit.RemoteRepositorySpec) error {
	script := strings.Join([]string{
		"set -eu",
		remoteRepositorySSHConfigScript(repository),
		fmt.Sprintf("ssh_command=%s", core.ShellQuote(remoteRepositorySSHCommand(repository))),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" ls-remote %s HEAD >/dev/null", core.ShellQuote(repository.URL)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-access", script)
	if err != nil {
		return fmt.Errorf("verify remote repository access: %w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s BootstrapRunner) hostConfigReachesRemoteRepository(req config.ShellLaunchParams, repository remoteinit.RemoteRepositorySpec) bool {
	return s.verifyRemoteRepositoryAccessWithHostConfig(req, repository) == nil
}

func (s BootstrapRunner) verifyRemoteRepositoryAccessWithHostConfig(req config.ShellLaunchParams, repository remoteinit.RemoteRepositorySpec) error {
	script := strings.Join([]string{
		"set -eu",
		"test -s \"$HOME/.ssh/config\"",
		fmt.Sprintf("ssh_command=%s", core.ShellQuote(`ssh -F "$HOME/.ssh/config" -o StrictHostKeyChecking=accept-new`)),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" ls-remote %s HEAD >/dev/null", core.ShellQuote(repository.URL)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-existing-host-config", script)
	if err != nil {
		return fmt.Errorf("verify remote repository access with existing SSH host config: %w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s BootstrapRunner) cloneRemoteRepository(req config.ShellLaunchParams, projectRoot string, repository remoteinit.RemoteRepositorySpec) error {
	script := strings.Join([]string{
		"set -eu",
		remoteRepositorySSHConfigScript(repository),
		fmt.Sprintf("ssh_command=%s", core.ShellQuote(remoteRepositorySSHCommand(repository))),
		fmt.Sprintf("mkdir -p %s", core.ShellQuote(path.Dir(projectRoot))),
		fmt.Sprintf("mkdir -p %s", core.ShellQuote(projectRoot)),
		fmt.Sprintf("if [ -n \"$(ls -A %s 2>/dev/null)\" ] && [ ! -d %s/.git ]; then echo 'remote worktree directory exists and is not empty' >&2; exit 1; fi", core.ShellQuote(projectRoot), core.ShellQuote(projectRoot)),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" clone %s %s", core.ShellQuote(repository.URL), core.ShellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-clone", script)
	if err != nil {
		return fmt.Errorf("clone remote repository: %w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s BootstrapRunner) pullRemoteRepository(req config.ShellLaunchParams, projectRoot string) error {
	script := strings.Join([]string{
		"set -eu",
		"ssh_command='ssh -i \"$HOME/.ssh/id_ed25519\" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'",
		fmt.Sprintf("git -C %s -c core.sshCommand=\"$ssh_command\" pull --ff-only", core.ShellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-pull", script)
	if err != nil {
		return fmt.Errorf("pull remote repository: %w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func remoteRepositorySSHConfigScript(repository remoteinit.RemoteRepositorySpec) string {
	if repository.UseHostConfig {
		return ":"
	}
	if repository.CodeCommitHost == "" {
		return ":"
	}
	return strings.Join([]string{
		"cat > \"$HOME/.ssh/config\" <<'EOF'",
		remoteinit.CodeCommitSSHConfig(repository, repository.CodeCommitSSHKeyID),
		"EOF",
		"chmod 600 \"$HOME/.ssh/config\"",
	}, "\n")
}

func remoteRepositorySSHCommand(repository remoteinit.RemoteRepositorySpec) string {
	if repository.UseHostConfig {
		return `ssh -F "$HOME/.ssh/config" -o StrictHostKeyChecking=accept-new`
	}
	if repository.CodeCommitHost != "" {
		return `ssh -F "$HOME/.ssh/config"`
	}
	return `ssh -i "$HOME/.ssh/id_ed25519" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new`
}

func (s BootstrapRunner) runRemoteScript(req config.ShellLaunchParams, label, script string) (kube.RemoteCommandResult, error) {
	traceArgs := append([]string{}, kube.KubectlRemoteExecArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	s.Context.TraceCommand("", "kubectl", traceArgs...)
	s.Context.TraceBlock(label, script)
	if s.Context.DryRun {
		return kube.RemoteCommandResult{}, nil
	}
	return s.RunRemoteCommand(req, script)
}
