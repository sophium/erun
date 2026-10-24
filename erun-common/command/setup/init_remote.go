package setup

import (
	"fmt"
	"path"
	"strings"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
)

type remoteRepositoryState struct {
	Exists              bool
	PublicKey           string
	CodeCommitPublicKey string
	HasSSHConfig        bool
}

// ensureRemoteRepository deploys the env's runtime and wires its remote checkout.
// The env it takes is the one this run just reconciled, so init's own deploy sees
// the settings this invocation supplied rather than the params they arrived in —
// the two diverge whenever a flag was omitted and the stored value stands.
func (s BootstrapRunner) ensureRemoteRepository(params open.BootstrapInitParams, tenant, envName, projectRoot string, env config.EnvConfig) (config.ShellLaunchParams, eruncommon.RemoteRepositorySpec, remoteProvisionedSecrets, error) {
	target := s.remoteRepositoryOpenResult(tenant, envName, env.KubernetesContext, projectRoot, params.ResolvedType())
	target.EnvConfig.RuntimePod = config.NormalizeRuntimePodResources(env.RuntimePod)
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
		return config.ShellLaunchParams{}, eruncommon.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	target.EnvConfig.RegistryCredentialSecretName = registryCredentialSecretName

	// Provision the platform alias the same way and at the same point: the pod
	// this deploy creates must mount it from first boot, so a fresh environment
	// can call the platform API the moment an operator promotes it to drive the
	// merge queue, without a human signing in inside it. Resolves to "" when the
	// invoking host is not signed in to a platform alias to give.
	platformAliasSecret, err := s.resolvePlatformAliasSecret(target)
	if err != nil {
		return config.ShellLaunchParams{}, eruncommon.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	target.EnvConfig.PlatformAliasSecretName = platformAliasSecret
	provisioned := remoteProvisionedSecrets{registryCredential: registryCredentialSecretName, platformAlias: platformAliasSecret}

	req := cloud.ShellLaunchParamsFromResult(target)

	if err := s.ensureRemoteRuntime(target, req, env.RuntimeVersion, env.RuntimeImage, params.MCPAuthPublicKeyPath); err != nil {
		return config.ShellLaunchParams{}, eruncommon.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	if err := s.ensureRemoteRegistryCredentials(target, req); err != nil {
		return config.ShellLaunchParams{}, eruncommon.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	if params.NoGit {
		return req, eruncommon.RemoteRepositorySpec{}, provisioned, s.ensureRemoteWorktree(req, projectRoot)
	}

	state, err := s.remoteRepositoryState(req, projectRoot)
	if err != nil {
		return config.ShellLaunchParams{}, eruncommon.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	if state.Exists {
		return req, eruncommon.RemoteRepositorySpec{}, provisioned, s.pullRemoteRepository(req, projectRoot)
	}

	repository, err := s.remoteRepositorySpecForClone(params, tenant, envName, req, state)
	if err != nil {
		return config.ShellLaunchParams{}, eruncommon.RemoteRepositorySpec{}, remoteProvisionedSecrets{}, err
	}
	return req, repository, provisioned, s.cloneRemoteRepository(req, projectRoot, repository)
}

// remoteProvisionedSecrets names the credentials init resolved on the invoking
// host and minted into the environment's namespace. Both are returned together
// rather than as adjacent strings: the caller persists each onto its own
// EnvConfig field, and two bare strings at one call site is a swap waiting to
// happen.
type remoteProvisionedSecrets struct {
	registryCredential string
	platformAlias      string
}

// resolveRegistryCredentialSecret resolves the ghcr.io registries this env's
// build/deploy roles require a credential for and, when the host has one to
// give, mints the dockerconfigjson Secret the runtime chart mounts. It is the
// host side of ensureRemoteRegistryCredentials' in-pod check: that check can
// only fail loudly; this is what gives it something to find.
func (s BootstrapRunner) resolveRegistryCredentialSecret(target config.OpenResult) (string, error) {
	registries := ghcrRegistriesRequiringCredential(config.EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	namespace := config.KubernetesNamespaceName(target.Tenant, target.Environment)
	return registrypkg.ProvisionRegistryCredentialSecret(s.Context, target.Tenant, namespace, target.EnvConfig.KubernetesContext, registries)
}

// resolvePlatformAliasSecret mints the Secret carrying the identity the runtime
// chart mounts for the pod's `erun` to read: the environment's own machine
// identity when the platform will mint one, and this host's own signed-in alias
// otherwise. Unlike the registry credential it is not gated on anything the
// environment declared -- any agent environment may be promoted to drive the
// merge queue, so the question is only whether the host has an identity to give.
//
// The machine identity is asked for first because it is the whole point of
// preferring one: an environment's platform calls are attributed to whoever ran
// init when they are made through a delegated session, and two environments
// provisioned from one host are indistinguishable in the audit trail. Every
// reason the platform will not mint one -- this host holds no alias to ask
// with, the environment is not registered yet, the tenant resolves by no issuer
// the platform administers, or the credential it returned does not exchange for
// a token -- falls back to that delegated session, which is exactly what the
// environment would have had before this existed. The fallbacks are traced, not
// silent: a deployment that stayed on the operator's identity has to be
// distinguishable from one that never asked.
func (s BootstrapRunner) resolvePlatformAliasSecret(target config.OpenResult) (string, error) {
	namespace := config.KubernetesNamespaceName(target.Tenant, target.Environment)
	// The runner's own store, so the alias is resolved against the same config
	// the rest of init reads rather than whatever the default store finds.
	var store config.CloudReadStore = config.ConfigStore{}
	if s.Store != nil {
		store = s.Store
	}
	deps := cloud.DefaultCloudDependencies()
	name, provisioned, err := provisionMachineIdentitySecret(s.Context, store, target.Tenant, target.Environment, namespace, target.EnvConfig.KubernetesContext, deps)
	if err != nil {
		return "", err
	}
	if provisioned {
		return name, nil
	}
	return deploy.ProvisionPlatformAliasSecret(s.Context, store, target.Tenant, namespace, target.EnvConfig.KubernetesContext, deps)
}

func (s BootstrapRunner) writeRemoteInitMarker(req config.ShellLaunchParams, marker eruncommon.RemoteInitMarker) error {
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

func (s BootstrapRunner) remoteRepositorySpecForClone(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, state remoteRepositoryState) (eruncommon.RemoteRepositorySpec, error) {
	repositoryURL, err := s.resolveRemoteRepositoryURL(params, tenant, envName)
	if err != nil {
		return eruncommon.RemoteRepositorySpec{}, err
	}
	repository, err := eruncommon.ParseRemoteRepositorySpec(repositoryURL)
	if err != nil {
		return eruncommon.RemoteRepositorySpec{}, err
	}
	repository, usingHostConfig, err := s.resolveExistingRemoteHostConfig(params, tenant, envName, req, state, repository)
	if err != nil {
		return eruncommon.RemoteRepositorySpec{}, err
	}
	if !usingHostConfig {
		return s.remoteRepositorySpecWithCredentials(params, tenant, envName, req, state, repository)
	}
	return repository, nil
}

func (s BootstrapRunner) remoteRepositorySpecWithCredentials(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, state remoteRepositoryState, repository eruncommon.RemoteRepositorySpec) (eruncommon.RemoteRepositorySpec, error) {
	repository, err := s.resolveRemoteRepositoryCredentials(params, tenant, envName, repository, state.CodeCommitPublicKey)
	if err != nil {
		return eruncommon.RemoteRepositorySpec{}, err
	}
	publicKey := state.PublicKey
	if repository.CodeCommitHost != "" {
		publicKey = state.CodeCommitPublicKey
	}
	if err := s.waitForRemoteKeyImport(params, tenant, envName, req, repository, publicKey); err != nil {
		return eruncommon.RemoteRepositorySpec{}, err
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

func remoteRepositoryTenantConfig(tenant, envName string) config.TenantConfig {
	return config.TenantConfig{Name: tenant, DefaultEnvironment: envName}
}

func remoteRepositoryEnvConfig(envName, kubernetesContext, projectRoot string, envType config.EnvironmentType) config.EnvConfig {
	if !envType.IsValid() {
		envType = config.EnvironmentTypeRemoteAgent
	}
	cfg := config.EnvConfig{Name: envName, LocalRepoPath: projectRoot, KubernetesContext: kubernetesContext, Type: envType}
	return cfg
}

func (s BootstrapRunner) remoteRepositoryLocalPorts(tenant, envName string) config.EnvironmentLocalPorts {
	portStore, ok := s.Store.(config.EnvironmentPortStore)
	if !ok {
		return config.EnvironmentLocalPorts{}
	}
	resolved, err := config.ResolveEnvironmentLocalPorts(portStore, tenant, envName)
	if err != nil {
		return config.EnvironmentLocalPorts{}
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
	// Pass the env's registries to the chart as-is: a cluster: entry is expanded
	// in-pod by the runtime chart, exactly as the standalone `erun deploy` renders
	// it. Do NOT host-concretize here — that pins the in-pod BUILD registry to a
	// localhost port-forward the pod cannot reach, so a later deploy that renders
	// the correct cluster form rolls the pod. The runtime IMAGE still pulls from
	// its own registry (publishedDevopsChartRegistry, e.g. ghcr), so create works.
	spec, err := deploy.ResolvePublishedDevopsDeploySpecWithOverrides(s.Context, target, runtimeVersion, "", runtimeImageStated)
	if err != nil {
		return err
	}
	// Init owns the env's single deploy, so it must also carry the desktop's
	// MCP-auth key; otherwise the desktop would redeploy right after init just to
	// inject it, rolling the pod init just created. A blank path is a no-op.
	if err := deploy.ApplyMCPAuthToRuntimeSpec(s.Context, deploy.DeployTarget{Tenant: target.Tenant, MCPAuthPublicKeyPath: mcpAuthPublicKeyPath}, &spec); err != nil {
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
func (s BootstrapRunner) ensureRemoteRegistryCredentials(target config.OpenResult, req config.ShellLaunchParams) error {
	registries := ghcrRegistriesRequiringCredential(config.EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	for _, registry := range registries {
		configured, err := s.remoteGHCRCredentialConfigured(req, registry)
		if err != nil {
			return fmt.Errorf("check registry credentials for %s: %w", registry, err)
		}
		if !configured {
			return &registrypkg.MissingGHCRCredentialError{Registry: registry}
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
func ghcrRegistriesRequiringCredential(list config.ContainerRegistries) []string {
	registries := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, entry := range list {
		if entry.Cluster != nil {
			continue
		}
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" || !registrypkg.IsGHCRRegistry(registry) {
			continue
		}
		if !entry.HasRole(config.RegistryRoleBuild) && !entry.HasRole(config.RegistryRoleDeploy) {
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
func (s BootstrapRunner) remoteGHCRCredentialConfigured(req config.ShellLaunchParams, registry string) (bool, error) {
	output, err := s.runRemoteScript(req, "registry-credential-check", remoteGHCRCredentialCheckScript(registry))
	if err != nil {
		return false, fmt.Errorf("%w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
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
		fmt.Sprintf("if [ -f \"$HOME/.docker/config.json\" ] && grep -q %s \"$HOME/.docker/config.json\" 2>/dev/null; then found=1; fi", core.ShellQuote(host)),
		"if [ \"$found\" -eq 0 ] && command -v gh >/dev/null 2>&1 && gh auth token -h github.com >/dev/null 2>&1; then found=1; fi",
		"if [ \"$found\" -eq 0 ] && { [ -n \"${GH_TOKEN:-}\" ] || [ -n \"${GITHUB_TOKEN:-}\" ]; }; then found=1; fi",
		"printf '%s\\n' \"$found\"",
	}, "\n")
}

func (s BootstrapRunner) resolveRemoteRepositoryURL(params open.BootstrapInitParams, tenant, envName string) (string, error) {
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

func (s BootstrapRunner) resolveRemoteRepositoryCredentials(params open.BootstrapInitParams, tenant, envName string, spec eruncommon.RemoteRepositorySpec, codeCommitPublicKey string) (eruncommon.RemoteRepositorySpec, error) {
	if spec.CodeCommitHost == "" || spec.CodeCommitSSHKeyID != "" {
		return spec, nil
	}
	keyID := strings.TrimSpace(params.CodeCommitSSHKeyID)
	details := eruncommon.CodeCommitSetupDetails(spec, codeCommitPublicKey, "<SSH public key ID>")
	if keyID == "" {
		s.Context.Info(details)
		if s.PromptCodeCommitSSHKeyID == nil {
			return eruncommon.RemoteRepositorySpec{}, BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
				Type:    BootstrapInitInteractionCodeCommitSSHKeyID,
				Label:   eruncommon.CodeCommitSSHKeyIDLabel(tenant, envName),
				Details: details,
			}}
		}
		prompted, err := s.PromptCodeCommitSSHKeyID(eruncommon.CodeCommitSSHKeyIDLabel(tenant, envName))
		if err != nil {
			return eruncommon.RemoteRepositorySpec{}, err
		}
		keyID = strings.TrimSpace(prompted)
	}
	if keyID == "" {
		return eruncommon.RemoteRepositorySpec{}, BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:    BootstrapInitInteractionCodeCommitSSHKeyID,
			Label:   eruncommon.CodeCommitSSHKeyIDLabel(tenant, envName),
			Details: details,
		}}
	}
	spec.CodeCommitSSHKeyID = keyID
	return spec, nil
}

func (s BootstrapRunner) resolveExistingRemoteHostConfig(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, state remoteRepositoryState, repository eruncommon.RemoteRepositorySpec) (eruncommon.RemoteRepositorySpec, bool, error) {
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
		return eruncommon.RemoteRepositorySpec{}, false, err
	}
	if !confirmed {
		return repository, false, nil
	}
	repository.UseHostConfig = true
	return repository, true, nil
}

func (s BootstrapRunner) waitForRemoteKeyImport(params open.BootstrapInitParams, tenant, envName string, req config.ShellLaunchParams, repository eruncommon.RemoteRepositorySpec, publicKey string) error {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey != "" {
		if repository.CodeCommitHost != "" {
			s.Context.Info(eruncommon.CodeCommitSetupDetails(repository, publicKey, repository.CodeCommitSSHKeyID))
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

func (s BootstrapRunner) verifyRemoteRepositoryAccess(req config.ShellLaunchParams, repository eruncommon.RemoteRepositorySpec) error {
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

func (s BootstrapRunner) verifyRemoteRepositoryAccessWithHostConfig(req config.ShellLaunchParams, repository eruncommon.RemoteRepositorySpec) error {
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

func (s BootstrapRunner) cloneRemoteRepository(req config.ShellLaunchParams, projectRoot string, repository eruncommon.RemoteRepositorySpec) error {
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

func remoteRepositorySSHConfigScript(repository eruncommon.RemoteRepositorySpec) string {
	if repository.UseHostConfig {
		return ":"
	}
	if repository.CodeCommitHost == "" {
		return ":"
	}
	return strings.Join([]string{
		"cat > \"$HOME/.ssh/config\" <<'EOF'",
		eruncommon.CodeCommitSSHConfig(repository, repository.CodeCommitSSHKeyID),
		"EOF",
		"chmod 600 \"$HOME/.ssh/config\"",
	}, "\n")
}

func remoteRepositorySSHCommand(repository eruncommon.RemoteRepositorySpec) string {
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
