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

// envInfos joins each linked env against the environment-activity poller's
// last observation for it, keyed the same way the poller itself keys the
// sidebar's per-env state (selectionKey) — so an orchestrator's card and the
// sidebar row for the same environment can never disagree about what "busy"
// or "outage" means, because both read the one map the poller writes.
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

// directoryPaths lists the orchestrator's own directories for the payload the
// desktop renders. Only the non-empty ones survive, so a definition with a blank
// entry (hand-edited config) reports the directories it actually has rather than
// a row the operator cannot act on.
func directoryPaths(dirs []configpkg.OrchestratorDirectoryConfig) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if path := strings.TrimSpace(dir.Directory); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// orchestratorScopeMismatch reports whether a running agent's wired
// environment scope (wiredScope, i.e. what its MCP config held when the agent
// last started) has drifted from the environments it is configured with right
// now. True means the live agent still holds tools for an environment it was
// unlinked from, is missing tools for one it was newly linked to, or both —
// and only restarting it (RestartOrchestrator) fixes it.
func orchestratorScopeMismatch(wiredScope []string, configuredEnvs []configpkg.OrchestratorEnvConfig) bool {
	return !equalOrchestratorScope(normalizedOrchestratorScope(wiredScope), orchestratorScopeOf(configuredEnvs))
}

// normalizedOrchestratorScope reads a recorded scope the way orchestratorScopeOf
// renders one, so a record that held no environments compares equal to none.
func normalizedOrchestratorScope(scope []string) []string {
	out := make([]string, 0, len(scope))
	out = append(out, scope...)
	sort.Strings(out)
	return out
}

// orchestratorRolesChanged reports whether any environment linked in both
// wiredEnvs and configuredEnvs now carries a different Role. Unlike scope,
// nothing the running session already resolved is keyed on Role -- it wires no
// MCP tool differently for a code environment than a build one -- so this is
// not the same "the process is stale" fact orchestratorScopeMismatch reports.
// It exists so the Edit orchestrator dialog can tell the operator their edit
// is not guaranteed live yet without overloading RestartRequired's specific,
// accurate "tools are missing" wording with a change that never affects tools.
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

// ListOrchestratorAliasChoices returns the erun platform aliases configured on
// this host, for the Edit orchestrator dialog's Platform alias picker.
//
// A picker rather than a text field, for the reason the environment candidate
// list is one: the writer refuses an alias this host cannot resolve
// (eruncommon.ValidateOrchestratorAlias), so offering the resolved list is what
// keeps the dialog from letting an operator choose something the save will
// reject. An empty list is a real answer — no erun alias is configured on this
// machine — and the control renders it as such rather than as an empty choice.
func (a *App) ListOrchestratorAliasChoices() ([]string, error) {
	return a.configuredERunAliases()
}

// ListOrchestratorLaunchChoices returns what the Edit orchestrator dialog's
// gateway, model and effort controls offer, read from the same catalog the
// save validates against and the launch resolves through, so the dialog cannot
// offer a model the save refuses. An uninitialized root config is no catalog.
func (a *App) ListOrchestratorLaunchChoices() (orchestratorLaunchChoices, error) {
	catalog, err := configpkg.ResolveOpenRouterConfig(a.deps.store)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return orchestratorLaunchChoices{}, fmt.Errorf("cannot read the gateway catalog from the ERun settings: %w", err)
	}
	return orchestratorLaunchChoicesFor(catalog), nil
}

// configuredERunAliases is the one filter for "which configured cloud aliases
// are erun platform aliases", shared by this picker and the tenant platform
// resolution, so the two cannot come to disagree about what the host has.
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

// ListOrchestratorEnvCandidates returns every environment the operator could
// consider linking, eligible or not — an env orchestratableEnv rejects is
// still listed, disabled, with IneligibleReason explaining why, rather than
// silently dropped: an operator who knows an env exists must be able to see
// that it was considered. An eligible env also carries the host directory the
// orchestrator reviews it in: a mirror the sync fills for a remote-agent env,
// or the env's own directory for a local-agent or host env, which is already on
// this machine — because the pod hostPath-mounts it in the local-agent case, and
// because there is no pod at all in the host one.
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
				// A candidate is eligible if it can be linked under whatever
				// role requiredRole names ("" when no single role is required,
				// which is every type but runtime; the runtime role for a
				// runtime env) — the role picker enforces the specific choice
				// once the operator selects the environment, including the
				// runtime role a host env is refused.
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

// resolveEnvInputs turns the frontend's selection into links. A mirror directory
// is the operator's to place, so their input wins; a local-agent env's review
// directory is derived from its repository path, so it is resolved here and any
// supplied value ignored — that keeps the env config the single source of truth
// and a link from outliving a repository path that moved. A runtime env has no
// review directory at all (orchestratorReviewDirectory answers explicitly), so
// its "" is expected rather than the missing-repository-path failure a
// local-agent/host env with an unset LocalRepoPath would hit.
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

// resolveOrchestratorDirectories validates the orchestrator's own directories.
// Each must be an absolute path to a directory that exists on this machine: the
// orchestrator authors and builds there directly, so a path that is not there is
// a link that cannot work. Duplicates collapse, so adding the same path twice is
// one row rather than two identical ones.
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

// resolveOrchestratorScope resolves both halves of what an orchestrator operates
// on -- the environments it links and the directories of its own -- and requires
// at least one of them. It deliberately does not require an environment: a
// directory the orchestrator works in itself is a complete definition, which is
// the whole point of having directories at all.
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

// resolveEnvInput resolves one candidate's role and review directory,
// split out of resolveEnvInputs to keep that loop within the lint
// complexity budget.
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

// wireEnvironmentReview prepares the env's host review directory. A remote-agent
// env needs the mirror created and its one-way sync turned on so the window fills
// from the pod (SSHD must be deployed for it to flow; enabling it here is the
// configuration half, activated on the env's next open/deploy). A local-agent env
// needs neither: the pod hostPath-mounts the worktree, so the directory already
// exists and no sync is involved — creating it would hand the orchestrator an
// empty review window instead of surfacing a repository path that is not there.
// A runtime env needs neither either, for a different reason: it has no
// worktree and no mirror to prepare at all, by design (see
// orchestratorReviewDirectory), so there is nothing to wire.
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

// linkOrchestratorEnvironments prepares every ref the same way, shared by
// CreateOrchestrator and UpdateOrchestrator: wire its review directory, then
// kick off its MCP port-forward so a linked environment with a healthy pod is
// reachable the moment an orchestrator session asks for its tools, instead of
// only working because an operator happened to run `erun open` for it earlier
// and nobody tells them that step is required. The forward ensure
// reuses ensureEnvRuntimeOnce, the same mechanism a terminal tab spawn already
// gets: fire-and-forget, deduped per (re)start window, and carrying its own
// not-force-start and surfaced-failure contract, so a stopped or undeployed
// environment is left alone and a genuine failure to open still reaches the
// operator rather than leaving a configured MCP entry pointed at nothing.
func (a *App) linkOrchestratorEnvironments(refs []configpkg.OrchestratorEnvConfig) error {
	for _, ref := range refs {
		if err := a.wireEnvironmentReview(ref); err != nil {
			return err
		}
		// A host env has no pod and no runtime to ensure — its worktree is
		// already this machine's own directory, so there is nothing to forward
		// to and nothing to start. Ensuring anyway would fail on an env that was
		// never going to have a runtime, and surface that failure as a
		// runtime-unreachable warning about a working environment.
		if a.orchestratorRefNamesHostEnv(ref) {
			continue
		}
		a.ensureEnvRuntimeOnce(uiSelection{Tenant: ref.Tenant, Environment: ref.Environment})
	}
	return nil
}

// orchestratorRefNamesHostEnv reports whether a linked ref names a host
// environment. A ref whose env config no longer loads answers false, so the
// caller's ensure path still runs and surfaces that failure rather than this
// silently absorbing it.
func (a *App) orchestratorRefNamesHostEnv(ref configpkg.OrchestratorEnvConfig) bool {
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return false
	}
	return env.ResolvedType() == configpkg.EnvironmentTypeHost
}
