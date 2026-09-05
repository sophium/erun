package eruncommon

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
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

var codeCommitHostPattern = regexp.MustCompile(`^git-codecommit\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?$`)

// ensureRemoteRepository deploys the env's runtime and wires its remote checkout.
// The env it takes is the one this run just reconciled, so init's own deploy sees
// the settings this invocation supplied rather than the params they arrived in —
// the two diverge whenever a flag was omitted and the stored value stands.
func (s bootstrapRunner) ensureRemoteRepository(params BootstrapInitParams, tenant, envName, projectRoot string, env EnvConfig) (ShellLaunchParams, remoteRepositorySpec, string, error) {
	target := s.remoteRepositoryOpenResult(tenant, envName, env.KubernetesContext, projectRoot, params.ResolvedType())
	target.EnvConfig.RuntimePod = NormalizeRuntimePodResources(env.RuntimePod)
	// The runtime registry is the one field that redirects chart resolution, so
	// init's own deploy must see it. Without it `erun init --runtime-registry`
	// would only take effect on the next deploy — no use to an env that cannot
	// complete one.
	target.EnvConfig.RuntimeRegistry = strings.TrimSpace(env.RuntimeRegistry)
	// A private runtime image is exactly the case the pull secrets exist for, so
	// init's own deploy must carry them; otherwise the env init just created
	// cannot pull, and the flag only takes effect on a redeploy the operator has
	// to know to run.
	target.EnvConfig.ImagePullSecrets = env.ImagePullSecrets
	// Carry the env's configured registries onto the deploy target so the
	// init-time runtime deploy renders the same container registry (cluster or
	// --container-registry) the standalone `erun deploy` does; without this the
	// target's minimal EnvConfig had no registries and the deploy fell back to the
	// default, so an in-pod build would target the wrong registry until a redeploy.
	target.EnvConfig.ContainerRegistries = env.ContainerRegistries

	// Provision a registry credential from the host BEFORE the runtime deploy, so
	// the pod it creates can mount it from first boot rather than needing a
	// redeploy once init's own in-pod check (below) discovers it is still
	// missing. Resolves to "" when the host has nothing to give.
	registryCredentialSecretName, err := s.resolveRegistryCredentialSecret(target)
	if err != nil {
		return ShellLaunchParams{}, remoteRepositorySpec{}, "", err
	}
	target.EnvConfig.RegistryCredentialSecretName = registryCredentialSecretName

	req := ShellLaunchParamsFromResult(target)

	if err := s.ensureRemoteRuntime(target, req, env.RuntimeVersion, env.RuntimeImage, params.MCPAuthPublicKeyPath); err != nil {
		return ShellLaunchParams{}, remoteRepositorySpec{}, "", err
	}
	if err := s.ensureRemoteRegistryCredentials(target, req); err != nil {
		return ShellLaunchParams{}, remoteRepositorySpec{}, "", err
	}
	if params.NoGit {
		return req, remoteRepositorySpec{}, registryCredentialSecretName, s.ensureRemoteWorktree(req, projectRoot)
	}

	state, err := s.remoteRepositoryState(req, projectRoot)
	if err != nil {
		return ShellLaunchParams{}, remoteRepositorySpec{}, "", err
	}
	if state.Exists {
		return req, remoteRepositorySpec{}, registryCredentialSecretName, s.pullRemoteRepository(req, projectRoot)
	}

	repository, err := s.remoteRepositorySpecForClone(params, tenant, envName, req, state)
	if err != nil {
		return ShellLaunchParams{}, remoteRepositorySpec{}, "", err
	}
	return req, repository, registryCredentialSecretName, s.cloneRemoteRepository(req, projectRoot, repository)
}

// resolveRegistryCredentialSecret resolves the ghcr.io registries this env's
// build/deploy roles require a credential for and, when the host has one to
// give, mints the dockerconfigjson Secret the runtime chart mounts. It is the
// host side of ensureRemoteRegistryCredentials' in-pod check: that check can
// only fail loudly; this is what gives it something to find.
func (s bootstrapRunner) resolveRegistryCredentialSecret(target OpenResult) (string, error) {
	registries := ghcrRegistriesRequiringCredential(EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	namespace := KubernetesNamespaceName(target.Tenant, target.Environment)
	return provisionRegistryCredentialSecret(s.Context, target.Tenant, namespace, target.EnvConfig.KubernetesContext, registries)
}

func (s bootstrapRunner) writeRemoteInitMarker(req ShellLaunchParams, marker RemoteInitMarker) error {
	script := strings.Join([]string{
		"set -eu",
		remoteInitMarkerWriteScript(marker),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-init-marker", script)
	if err != nil {
		return fmt.Errorf("write remote init marker: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) remoteRepositorySpecForClone(params BootstrapInitParams, tenant, envName string, req ShellLaunchParams, state remoteRepositoryState) (remoteRepositorySpec, error) {
	repositoryURL, err := s.resolveRemoteRepositoryURL(params, tenant, envName)
	if err != nil {
		return remoteRepositorySpec{}, err
	}
	repository, err := parseRemoteRepositorySpec(repositoryURL)
	if err != nil {
		return remoteRepositorySpec{}, err
	}
	repository, usingHostConfig, err := s.resolveExistingRemoteHostConfig(params, tenant, envName, req, state, repository)
	if err != nil {
		return remoteRepositorySpec{}, err
	}
	if !usingHostConfig {
		return s.remoteRepositorySpecWithCredentials(params, tenant, envName, req, state, repository)
	}
	return repository, nil
}

func (s bootstrapRunner) remoteRepositorySpecWithCredentials(params BootstrapInitParams, tenant, envName string, req ShellLaunchParams, state remoteRepositoryState, repository remoteRepositorySpec) (remoteRepositorySpec, error) {
	repository, err := s.resolveRemoteRepositoryCredentials(params, tenant, envName, repository, state.CodeCommitPublicKey)
	if err != nil {
		return remoteRepositorySpec{}, err
	}
	publicKey := state.PublicKey
	if repository.CodeCommitHost != "" {
		publicKey = state.CodeCommitPublicKey
	}
	if err := s.waitForRemoteKeyImport(params, tenant, envName, req, repository, publicKey); err != nil {
		return remoteRepositorySpec{}, err
	}
	return repository, nil
}

func (s bootstrapRunner) remoteRepositoryOpenResult(tenant, envName, kubernetesContext, projectRoot string, envType EnvironmentType) OpenResult {
	return OpenResult{
		Tenant:       tenant,
		Environment:  envName,
		TenantConfig: remoteRepositoryTenantConfig(tenant, envName),
		EnvConfig:    remoteRepositoryEnvConfig(envName, kubernetesContext, projectRoot, envType),
		LocalPorts:   s.remoteRepositoryLocalPorts(tenant, envName),
		RepoPath:     projectRoot,
		Title:        tenant + "-" + envName,
	}
}

func remoteRepositoryTenantConfig(tenant, envName string) TenantConfig {
	return TenantConfig{Name: tenant, DefaultEnvironment: envName}
}

func remoteRepositoryEnvConfig(envName, kubernetesContext, projectRoot string, envType EnvironmentType) EnvConfig {
	if !envType.IsValid() {
		envType = EnvironmentTypeRemoteAgent
	}
	cfg := EnvConfig{Name: envName, LocalRepoPath: projectRoot, KubernetesContext: kubernetesContext, Type: envType}
	return cfg
}

func (s bootstrapRunner) remoteRepositoryLocalPorts(tenant, envName string) EnvironmentLocalPorts {
	portStore, ok := s.Store.(environmentPortStore)
	if !ok {
		return EnvironmentLocalPorts{}
	}
	resolved, err := ResolveEnvironmentLocalPorts(portStore, tenant, envName)
	if err != nil {
		return EnvironmentLocalPorts{}
	}
	return resolved
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

func (s bootstrapRunner) ensureRemoteRuntime(target OpenResult, req ShellLaunchParams, runtimeVersion, runtimeImage, mcpAuthPublicKeyPath string) error {
	runtimeImage = strings.TrimSpace(runtimeImage)
	runtimeImageStated := runtimeImage != "" && runtimeImage != DevopsComponentName
	if runtimeImageStated {
		target.EnvConfig.RuntimeImage = runtimeImage
	}
	// Pass the env's registries to the chart as-is: a cluster: entry is expanded
	// in-pod by the runtime chart, exactly as the standalone `erun deploy` renders
	// it. Do NOT host-concretize here — that pins the in-pod BUILD registry to a
	// localhost port-forward the pod cannot reach, so a later deploy that renders
	// the correct cluster form rolls the pod. The runtime IMAGE still pulls from
	// its own registry (publishedDevopsChartRegistry, e.g. ghcr), so create works.
	spec, err := resolvePublishedDevopsDeploySpec(s.Context, target, runtimeVersion, "", runtimeImageStated)
	if err != nil {
		return err
	}
	// Init owns the env's single deploy, so it must also carry the desktop's
	// MCP-auth key; otherwise the desktop would redeploy right after init just to
	// inject it, rolling the pod init just created. A blank path is a no-op.
	if err := applyMCPAuthToRuntimeSpec(s.Context, DeployTarget{Tenant: target.Tenant, MCPAuthPublicKeyPath: mcpAuthPublicKeyPath}, &spec); err != nil {
		return err
	}
	if err := RunDeploySpec(s.Context, spec, s.DeployHelmChart); err != nil {
		return err
	}

	s.Context.TraceCommand("", "kubectl", kubectlDeploymentWaitArgs(req)...)
	if s.Context.DryRun || s.WaitForRemoteRuntime == nil {
		return nil
	}
	return s.WaitForRemoteRuntime(req)
}

// ensureRemoteRegistryCredentials fails init when the pod it just deployed has
// no way to authenticate to a ghcr.io registry the env is configured to build
// to or deploy from. Left unchecked, a build-role registry with no credential
// is only discovered after a full multi-arch release build spends itself at
// the push (MissingGHCRCredentialError, checked again by release's own
// preflight), and a deploy-role registry with no credential leaves every
// registry read this pod makes unable to tell "denied" from "not published"
// (#1193). This only proves a credential source EXISTS in the pod -- a docker
// config entry, a gh session, or GH_TOKEN/GITHUB_TOKEN -- not that it is valid
// or correctly scoped; release's own preflight still confirms that before
// spending a build.
func (s bootstrapRunner) ensureRemoteRegistryCredentials(target OpenResult, req ShellLaunchParams) error {
	registries := ghcrRegistriesRequiringCredential(EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	for _, registry := range registries {
		configured, err := s.remoteGHCRCredentialConfigured(req, registry)
		if err != nil {
			return fmt.Errorf("check registry credentials for %s: %w", registry, err)
		}
		if !configured {
			return &MissingGHCRCredentialError{Registry: registry}
		}
	}
	return nil
}

// ghcrRegistriesRequiringCredential returns the distinct ghcr.io registries in
// list carrying the build or deploy role, in list order. Both roles need this
// pod to authenticate to ghcr.io itself -- build to push, deploy to read the
// images and runtime chart it resolves -- so a registry with neither role, a
// non-ghcr registry (a separate credential story this check does not police),
// or a cluster: entry (the pod never leaves the cluster to reach it) is not
// checked.
func ghcrRegistriesRequiringCredential(list ContainerRegistries) []string {
	registries := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, entry := range list {
		if entry.Cluster != nil {
			continue
		}
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" || !isGHCRRegistry(registry) {
			continue
		}
		if !entry.hasRole(RegistryRoleBuild) && !entry.hasRole(RegistryRoleDeploy) {
			continue
		}
		if _, ok := seen[registry]; ok {
			continue
		}
		seen[registry] = struct{}{}
		registries = append(registries, registry)
	}
	return registries
}

// remoteGHCRCredentialConfigured execs into the pod to check for a ghcr.io
// credential. Dry-run does not exec into a real pod, so it reports configured
// rather than failing a plan preview over pod state a preview cannot know.
func (s bootstrapRunner) remoteGHCRCredentialConfigured(req ShellLaunchParams, registry string) (bool, error) {
	output, err := s.runRemoteScript(req, "registry-credential-check", remoteGHCRCredentialCheckScript(registry))
	if err != nil {
		return false, fmt.Errorf("%w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	if s.Context.DryRun {
		return true, nil
	}
	return strings.TrimSpace(output.Stdout) == "1", nil
}

// remoteGHCRCredentialCheckScript mirrors resolveGHCRBasicAuth's three routes
// (erun-common/registry_auth.go) in shell: that Go logic runs wherever the
// pod's own erun binary executes and cannot be invoked remotely, so this
// checks the same three sources directly -- a docker config entry for the
// registry host, a gh session, or GH_TOKEN/GITHUB_TOKEN. It reports 1/0 on
// stdout rather than failing the script itself, so a missing credential is a
// normal result init can act on rather than a script error.
func remoteGHCRCredentialCheckScript(registry string) string {
	host, _, _ := strings.Cut(registry, "/")
	return strings.Join([]string{
		"set -eu",
		"found=0",
		fmt.Sprintf("if [ -f \"$HOME/.docker/config.json\" ] && grep -q %s \"$HOME/.docker/config.json\" 2>/dev/null; then found=1; fi", shellQuote(host)),
		"if [ \"$found\" -eq 0 ] && command -v gh >/dev/null 2>&1 && gh auth token -h github.com >/dev/null 2>&1; then found=1; fi",
		"if [ \"$found\" -eq 0 ] && { [ -n \"${GH_TOKEN:-}\" ] || [ -n \"${GITHUB_TOKEN:-}\" ]; }; then found=1; fi",
		"printf '%s\\n' \"$found\"",
	}, "\n")
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

	return WaitForGitAccess(s.Context, s.Sleep, func() error {
		return s.verifyRemoteRepositoryAccess(req, repository)
	})
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
	cmd := Command("kubectl", kubectlRemoteExecArgs(req, script)...)
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
