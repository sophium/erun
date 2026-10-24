package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// envInfos keys env state by selectionKey, as the poller does, so the card and
// sidebar never disagree.
func envInfos(envs []configpkg.OrchestratorEnvConfig, envActivity map[string]environmentActivityState, envUsage map[string]environmentUsageReading) []orchestratorEnvInfo {
	out := make([]orchestratorEnvInfo, 0, len(envs))
	for _, env := range envs {
		info := orchestratorEnvInfo{Tenant: env.Tenant, Environment: env.Environment, Directory: env.Directory, Role: env.Role}
		key := selectionKey(uiSelection{Tenant: env.Tenant, Environment: env.Environment})
		if state, ok := envActivity[key]; ok {
			info.Activity = &uiEnvironmentActivitySnapshot{
				Reachable:   state.reachable,
				Observed:    state.observed,
				Outage:      state.outage,
				CheckFailed: state.checkFailed,
				Busy:        state.busy,
				Detail:      state.detail,
			}
		}
		if reading, ok := envUsage[key]; ok {
			info.Usage = uiEnvironmentUsageSnapshotFrom(reading)
		}
		out = append(out, info)
	}
	return out
}

func tenantsFromEnvs(envs []configpkg.OrchestratorEnvConfig) []string {
	seen := make(map[string]struct{}, len(envs))
	out := make([]string, 0, len(envs))
	for _, env := range envs {
		if _, dup := seen[env.Tenant]; dup || strings.TrimSpace(env.Tenant) == "" {
			continue
		}
		seen[env.Tenant] = struct{}{}
		out = append(out, env.Tenant)
	}
	return out
}

// directoryPaths drops blank entries from a hand-edited config.
func directoryPaths(dirs []configpkg.OrchestratorDirectoryConfig) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if path := strings.TrimSpace(dir.Directory); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// orchestratorScopeMismatch reports whether the agent's wired env scope has
// drifted from config; only a restart fixes it.
func orchestratorScopeMismatch(wiredScope []string, configuredEnvs []configpkg.OrchestratorEnvConfig) bool {
	return !equalOrchestratorScope(normalizedOrchestratorScope(wiredScope), orchestratorScopeOf(configuredEnvs))
}

// normalizedOrchestratorScope makes an empty recorded scope compare equal to none.
func normalizedOrchestratorScope(scope []string) []string {
	out := make([]string, 0, len(scope))
	out = append(out, scope...)
	sort.Strings(out)
	return out
}

// orchestratorRolesChanged reports a Role change on a still-linked env. Roles
// wire no tools, so this is not a restart-required scope mismatch.
func orchestratorRolesChanged(wiredEnvs, configuredEnvs []configpkg.OrchestratorEnvConfig) bool {
	wiredRoles := make(map[string]configpkg.OrchestratorEnvRole, len(wiredEnvs))
	for _, env := range wiredEnvs {
		wiredRoles[env.Tenant+"/"+env.Environment] = env.Role
	}
	for _, env := range configuredEnvs {
		if wired, ok := wiredRoles[env.Tenant+"/"+env.Environment]; ok && wired != env.Role {
			return true
		}
	}
	return false
}

// ListOrchestratorAliasChoices returns this host's erun platform aliases; the
// writer refuses any other alias, hence a picker.
func (a *App) ListOrchestratorAliasChoices() ([]string, error) {
	return a.configuredERunAliases()
}

// ListOrchestratorLaunchChoices reads the same catalog save validates against.
func (a *App) ListOrchestratorLaunchChoices() (orchestratorLaunchChoices, error) {
	catalog, err := configpkg.ResolveOpenRouterConfig(a.deps.store)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return orchestratorLaunchChoices{}, fmt.Errorf("cannot read the gateway catalog from the ERun settings: %w", err)
	}
	return orchestratorLaunchChoicesFor(catalog), nil
}

// configuredERunAliases is the single filter for erun platform aliases.
func (a *App) configuredERunAliases() ([]string, error) {
	providers, err := cloud.ListCloudProviders(a.deps.store)
	if err != nil {
		return nil, err
	}
	aliases := []string{}
	for _, provider := range providers {
		if provider.Provider == configpkg.CloudProviderERun {
			aliases = append(aliases, provider.Alias)
		}
	}
	return aliases, nil
}

// ListOrchestratorEnvCandidates lists every environment, ineligible ones disabled
// with a reason, plus each eligible env's host review directory.
func (a *App) ListOrchestratorEnvCandidates() ([]orchestratorEnvCandidate, error) {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	out := []orchestratorEnvCandidate{}
	for _, tenant := range tenants {
		envs, envErr := a.deps.store.ListEnvConfigs(tenant.Name)
		if envErr != nil {
			continue
		}
		for _, env := range envs {
			requiredRole := configpkg.OrchestratorEnvRoleRequiredFor(env.ResolvedType())
			candidate := orchestratorEnvCandidate{
				Tenant:          tenant.Name,
				Environment:     env.Name,
				EnvironmentType: env.ResolvedType(),
				// The role picker enforces the specific role once one is chosen.
				Eligible: orchestratableEnv(env, requiredRole),
			}
			if candidate.Eligible {
				candidate.DefaultDirectory, candidate.Mirrored = orchestratorReviewDirectory(tenant.Name, env)
				candidate.RequiredRole = requiredRole
			} else {
				candidate.IneligibleReason = orchestratorIneligibilityReason(env, requiredRole)
			}
			out = append(out, candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		return out[i].Environment < out[j].Environment
	})
	return out, nil
}

// resolveEnvInputs takes the operator's mirror directory but derives a
// local-agent env's from its repository path; a runtime env has none.
func (a *App) resolveEnvInputs(inputs []orchestratorEnvInput) ([]configpkg.OrchestratorEnvConfig, error) {
	refs := make([]configpkg.OrchestratorEnvConfig, 0, len(inputs))
	for _, input := range inputs {
		tenant := strings.TrimSpace(input.Tenant)
		environment := strings.TrimSpace(input.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		ref, err := a.resolveEnvInput(tenant, environment, input)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// resolveOrchestratorDirectories requires existing absolute directories and dedupes them.
func resolveOrchestratorDirectories(dirs []string) ([]configpkg.OrchestratorDirectoryConfig, error) {
	out := make([]configpkg.OrchestratorDirectoryConfig, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		path := strings.TrimSpace(dir)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("orchestrator directory %q must be an absolute path", path)
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		info, statErr := os.Stat(clean)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("orchestrator directory %s is not a directory on this machine", clean)
		}
		seen[clean] = true
		out = append(out, configpkg.OrchestratorDirectoryConfig{Directory: clean})
	}
	return out, nil
}

// resolveOrchestratorScope requires at least one env or directory; directories alone suffice.
func (a *App) resolveOrchestratorScope(envs []orchestratorEnvInput, dirs []string) ([]configpkg.OrchestratorEnvConfig, []configpkg.OrchestratorDirectoryConfig, error) {
	refs, err := a.resolveEnvInputs(envs)
	if err != nil {
		return nil, nil, err
	}
	dirRefs, err := resolveOrchestratorDirectories(dirs)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 && len(dirRefs) == 0 {
		return nil, nil, fmt.Errorf("an orchestrator must link at least one environment or name at least one directory")
	}
	return refs, dirRefs, nil
}

func (a *App) resolveEnvInput(tenant, environment string, input orchestratorEnvInput) (configpkg.OrchestratorEnvConfig, error) {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return configpkg.OrchestratorEnvConfig{}, fmt.Errorf("load %s/%s: %w", tenant, environment, err)
	}
	if !input.Role.IsValid() {
		return configpkg.OrchestratorEnvConfig{}, fmt.Errorf("%s/%s: invalid role %q", tenant, environment, input.Role)
	}
	if !orchestratableEnv(env, input.Role) {
		return configpkg.OrchestratorEnvConfig{}, fmt.Errorf("%s/%s: %s", tenant, environment, orchestratorIneligibilityReason(env, input.Role))
	}
	derived, mirrored := orchestratorReviewDirectory(tenant, env)
	dir := derived
	if mirrored {
		if supplied := strings.TrimSpace(input.Directory); supplied != "" {
			dir = supplied
		}
	} else if dir == "" && env.ResolvedType() != configpkg.EnvironmentTypeRuntime {
		return configpkg.OrchestratorEnvConfig{}, fmt.Errorf("%s/%s has no repository path on this machine; set one before linking it to an orchestrator", tenant, environment)
	}
	return configpkg.OrchestratorEnvConfig{Tenant: tenant, Environment: environment, Directory: dir, Role: input.Role}, nil
}

// wireEnvironmentReview creates a remote-agent env's mirror and enables its
// sync. Local-agent and runtime envs need nothing: creating a missing worktree
// would hide a moved repository path.
func (a *App) wireEnvironmentReview(ref configpkg.OrchestratorEnvConfig) error {
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return fmt.Errorf("load %s/%s: %w", ref.Tenant, ref.Environment, err)
	}
	if env.ResolvedType() == configpkg.EnvironmentTypeRuntime {
		return nil
	}
	if _, mirrored := orchestratorReviewDirectory(ref.Tenant, env); !mirrored {
		info, statErr := os.Stat(ref.Directory)
		if statErr != nil || !info.IsDir() {
			return fmt.Errorf("%s/%s has no worktree at %s; check its repository path before linking it to an orchestrator", ref.Tenant, ref.Environment, ref.Directory)
		}
		return nil
	}
	if err := os.MkdirAll(ref.Directory, 0o755); err != nil {
		return fmt.Errorf("create orchestrator directory %s: %w", ref.Directory, err)
	}
	env.SSHD.Enabled = true
	env.SSHD.WorkspaceSync.Enabled = true
	env.SSHD.WorkspaceSync.LocalPath = ref.Directory
	if err := a.deps.store.SaveEnvConfig(ref.Tenant, env); err != nil {
		return fmt.Errorf("enable workspace sync for %s/%s: %w", ref.Tenant, ref.Environment, err)
	}
	return nil
}

// linkOrchestratorEnvironments wires each review directory and ensures its MCP
// port-forward via ensureEnvRuntimeOnce, so tools work without a manual `erun open`.
func (a *App) linkOrchestratorEnvironments(refs []configpkg.OrchestratorEnvConfig) error {
	for _, ref := range refs {
		if err := a.wireEnvironmentReview(ref); err != nil {
			return err
		}
		// A host env has no runtime; ensuring one would raise a false warning.
		if a.orchestratorRefNamesHostEnv(ref) {
			continue
		}
		a.ensureEnvRuntimeOnce(uiSelection{Tenant: ref.Tenant, Environment: ref.Environment})
	}
	return nil
}

// orchestratorRefNamesHostEnv answers false when the env config fails to load,
// so the ensure path surfaces that failure.
func (a *App) orchestratorRefNamesHostEnv(ref configpkg.OrchestratorEnvConfig) bool {
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return false
	}
	return env.ResolvedType() == configpkg.EnvironmentTypeHost
}
