package eruncommon

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RegistryRole marks the part a registry plays in the build → copy → deploy
// flow. A single registry entry may carry more than one role (commonly
// build+deploy on a fresh project, or build+from on the source side of a
// copy).
type RegistryRole string

const (
	// RegistryRoleBuild marks the registry `erun build`/`erun push` push to.
	RegistryRoleBuild RegistryRole = "build"
	// RegistryRoleFrom marks the copy source consulted on deploy.
	RegistryRoleFrom RegistryRole = "from"
	// RegistryRoleTo marks a copy destination written on deploy.
	RegistryRoleTo RegistryRole = "to"
	// RegistryRoleDeploy marks the registry the cluster pulls from — the value
	// rendered into the chart as containerRegistry.
	RegistryRoleDeploy RegistryRole = "deploy"
)

// ContainerRegistryEntry is one registry in the project's marked list. It names
// its target one of two ways: a static host (or host/namespace) in Registry, or
// a Cluster block resolved from the env's kube-context. Exactly one is set.
type ContainerRegistryEntry struct {
	Registry string           `yaml:"registry,omitempty" json:"registry,omitempty"`
	Cluster  *ClusterRegistry `yaml:"cluster,omitempty" json:"cluster,omitempty"`
	Roles    []RegistryRole   `yaml:"roles" json:"roles"`
}

// ClusterRegistry describes an in-cluster image registry whose addresses are
// resolved from the env's kube-context rather than hardcoded. The cluster pulls
// it at its in-cluster address (the DEPLOY role); operator-side roles
// (BUILD/FROM/TO) reach it at a push address — the in-cluster address for an
// in-pod build, or a managed port-forward (localhost:<port>) from the host.
type ClusterRegistry struct {
	Service   string `yaml:"service,omitempty" json:"service,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Port      int    `yaml:"port,omitempty" json:"port,omitempty"`
	// Insecure marks the registry as plain HTTP (a local dev registry), so the
	// in-pod dind daemon is told to trust it and the resolver never assumes TLS.
	Insecure bool `yaml:"insecure,omitempty" json:"insecure,omitempty"`
}

// Cluster registry defaults match the erun-registry convention the local k3s
// setup provisions, so a bare `cluster: {}` block resolves without extra config.
const (
	DefaultClusterRegistryService   = "erun-registry"
	DefaultClusterRegistryNamespace = "kube-system"
	DefaultClusterRegistryPort      = 5000
)

// WithDefaults fills unset fields with the erun-registry convention.
func (c ClusterRegistry) WithDefaults() ClusterRegistry {
	if strings.TrimSpace(c.Service) == "" {
		c.Service = DefaultClusterRegistryService
	}
	if strings.TrimSpace(c.Namespace) == "" {
		c.Namespace = DefaultClusterRegistryNamespace
	}
	if c.Port == 0 {
		c.Port = DefaultClusterRegistryPort
	}
	return c
}

// identity names the entry for role tallies and dedup: the static host, or a
// stable pseudo-host for a cluster entry so two cluster entries for the same
// service collapse.
func (e ContainerRegistryEntry) identity() string {
	if e.Cluster != nil {
		c := e.Cluster.WithDefaults()
		return fmt.Sprintf("cluster:%s.%s:%d", c.Service, c.Namespace, c.Port)
	}
	return strings.TrimSpace(e.Registry)
}

// ContainerRegistries is the marked registry list. Build pushes to the BUILD
// registry; deploy copies images FROM → every TO when both are set, then the
// cluster pulls from the DEPLOY registry.
type ContainerRegistries []ContainerRegistryEntry

func normalizeRole(role RegistryRole) RegistryRole {
	return RegistryRole(strings.ToLower(strings.TrimSpace(string(role))))
}

func (e ContainerRegistryEntry) hasRole(role RegistryRole) bool {
	for _, candidate := range e.Roles {
		if normalizeRole(candidate) == role {
			return true
		}
	}
	return false
}

// IsZero reports whether the list carries no entries.
func (r ContainerRegistries) IsZero() bool {
	return len(r) == 0
}

func (r ContainerRegistries) registryWithRole(role RegistryRole) (string, bool) {
	for _, entry := range r {
		if entry.hasRole(role) {
			if registry := strings.TrimSpace(entry.Registry); registry != "" {
				return registry, true
			}
		}
	}
	return "", false
}

// BuildRegistry returns the BUILD-marked registry. ok is false when no entry
// carries the build role — callers treat that as "this environment cannot
// build".
func (r ContainerRegistries) BuildRegistry() (string, bool) {
	return r.registryWithRole(RegistryRoleBuild)
}

// FromRegistry returns the FROM-marked copy source.
func (r ContainerRegistries) FromRegistry() (string, bool) {
	return r.registryWithRole(RegistryRoleFrom)
}

// DeployRegistry returns the registry the cluster pulls from. When more than
// one entry carries the deploy role the first wins.
func (r ContainerRegistries) DeployRegistry() (string, bool) {
	return r.registryWithRole(RegistryRoleDeploy)
}

// ToRegistries returns every TO-marked copy destination in list order.
func (r ContainerRegistries) ToRegistries() []string {
	out := make([]string, 0, len(r))
	seen := make(map[string]struct{}, len(r))
	for _, entry := range r {
		if !entry.hasRole(RegistryRoleTo) {
			continue
		}
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" {
			continue
		}
		if _, ok := seen[registry]; ok {
			continue
		}
		seen[registry] = struct{}{}
		out = append(out, registry)
	}
	return out
}

// ClusterRegistryAddresses is a cluster registry resolved to concrete hosts:
// Push is where operator-side roles (build/from/to) read and write; Pull is the
// in-cluster host the DEPLOY role renders into the chart for the cluster to pull.
type ClusterRegistryAddresses struct {
	Push string
	Pull string
}

// ClusterRegistryResolver turns a cluster block into concrete push/pull hosts.
// It is injected so resolution (kube-context queries, port-forward setup) stays
// out of the pure config layer and is faked in tests.
type ClusterRegistryResolver func(ClusterRegistry) (ClusterRegistryAddresses, error)

// isClusterPullRole reports whether a role is served by the cluster itself
// (DEPLOY) rather than the operator side; the cluster pulls at the pull host.
func isClusterPullRole(role RegistryRole) bool {
	return normalizeRole(role) == RegistryRoleDeploy
}

// HasClusterEntry reports whether any entry is a context-resolved cluster
// registry, so callers know a resolver is required before use.
func (r ContainerRegistries) HasClusterEntry() bool {
	for _, entry := range r {
		if entry.Cluster != nil {
			return true
		}
	}
	return false
}

// ClusterEntry returns the first cluster block (defaults filled), if any —
// callers use it to set up the port-forward and dind insecure trust.
func (r ContainerRegistries) ClusterEntry() (ClusterRegistry, bool) {
	for _, entry := range r {
		if entry.Cluster != nil {
			return entry.Cluster.WithDefaults(), true
		}
	}
	return ClusterRegistry{}, false
}

// Concrete expands cluster entries into plain per-role entries using resolved
// push/pull hosts, so BuildRegistry/DeployRegistry/copy resolution operate on
// static hosts unchanged. Plain entries pass through untouched. A cluster entry
// splits by role: DEPLOY → pull host; BUILD/FROM/TO → push host.
func (r ContainerRegistries) Concrete(resolve ClusterRegistryResolver) (ContainerRegistries, error) {
	if !r.HasClusterEntry() {
		return r, nil
	}
	if resolve == nil {
		return nil, errors.New("cluster registry entry requires a resolver")
	}
	out := make(ContainerRegistries, 0, len(r)+1)
	for _, entry := range r {
		if entry.Cluster == nil {
			out = append(out, entry)
			continue
		}
		expanded, err := expandClusterEntry(entry, resolve)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// expandClusterEntry resolves one cluster entry's push/pull hosts and splits its
// roles into plain per-host entries: DEPLOY pulls; BUILD/FROM/TO push.
func expandClusterEntry(entry ContainerRegistryEntry, resolve ClusterRegistryResolver) (ContainerRegistries, error) {
	addrs, err := resolve(entry.Cluster.WithDefaults())
	if err != nil {
		return nil, err
	}
	var pull, push []RegistryRole
	for _, role := range entry.Roles {
		if isClusterPullRole(role) {
			pull = append(pull, role)
		} else {
			push = append(push, role)
		}
	}
	out := make(ContainerRegistries, 0, 2)
	if len(push) > 0 {
		if strings.TrimSpace(addrs.Push) == "" {
			return nil, errors.New("cluster registry resolved an empty push address")
		}
		out = append(out, ContainerRegistryEntry{Registry: addrs.Push, Roles: push})
	}
	if len(pull) > 0 {
		if strings.TrimSpace(addrs.Pull) == "" {
			return nil, errors.New("cluster registry resolved an empty pull address")
		}
		out = append(out, ContainerRegistryEntry{Registry: addrs.Pull, Roles: pull})
	}
	return out, nil
}

// Validate enforces the marker invariants. A DEPLOY registry need not carry
// BUILD or TO — the image it serves may be published there externally (e.g. a
// runtime env pulling a released image), which erun cannot police at config
// time.
func (r ContainerRegistries) Validate() error {
	if r.IsZero() {
		return errors.New("registry list is empty")
	}
	tally, err := r.tallyRoles()
	if err != nil {
		return err
	}
	if tally.build > 1 {
		return errors.New("at most one registry may be marked build")
	}
	if tally.from > 1 {
		return errors.New("at most one registry may be marked from")
	}
	if tally.deploy < 1 {
		return errors.New("at least one registry must be marked deploy")
	}
	if (tally.from > 0) != (len(tally.toRegistries) > 0) {
		return errors.New("from and to must be set together (a copy needs both a source and a destination)")
	}
	if tally.fromRegistry != "" {
		if _, ok := tally.toRegistries[tally.fromRegistry]; ok {
			return fmt.Errorf("registry %q cannot be both from and to", tally.fromRegistry)
		}
	}
	return nil
}

type registryRoleTally struct {
	build        int
	from         int
	deploy       int
	fromRegistry string
	toRegistries map[string]struct{}
}

func (r ContainerRegistries) tallyRoles() (registryRoleTally, error) {
	tally := registryRoleTally{toRegistries: make(map[string]struct{}, len(r))}
	for _, entry := range r {
		if entry.Cluster != nil && strings.TrimSpace(entry.Registry) != "" {
			return registryRoleTally{}, errors.New("registry list entry sets both registry and cluster; use exactly one")
		}
		registry := entry.identity()
		if registry == "" {
			return registryRoleTally{}, errors.New("registry list entry is missing a registry")
		}
		if entry.hasRole(RegistryRoleBuild) {
			tally.build++
		}
		if entry.hasRole(RegistryRoleFrom) {
			tally.from++
			tally.fromRegistry = registry
		}
		if entry.hasRole(RegistryRoleTo) {
			tally.toRegistries[registry] = struct{}{}
		}
		if entry.hasRole(RegistryRoleDeploy) {
			tally.deploy++
		}
	}
	return tally, nil
}

// Equal reports whether two lists carry the same registries with the same
// roles in the same order. Used to collapse a per-env override that matches
// the project default.
func (r ContainerRegistries) Equal(other ContainerRegistries) bool {
	if len(r) != len(other) {
		return false
	}
	for i := range r {
		if strings.TrimSpace(r[i].Registry) != strings.TrimSpace(other[i].Registry) {
			return false
		}
		if (r[i].Cluster == nil) != (other[i].Cluster == nil) {
			return false
		}
		if r[i].Cluster != nil && r[i].Cluster.WithDefaults() != other[i].Cluster.WithDefaults() {
			return false
		}
		if len(r[i].Roles) != len(other[i].Roles) {
			return false
		}
		for j := range r[i].Roles {
			if normalizeRole(r[i].Roles[j]) != normalizeRole(other[i].Roles[j]) {
				return false
			}
		}
	}
	return true
}

// Clone returns a deep copy so callers can mutate the result without aliasing
// the stored config.
func (r ContainerRegistries) Clone() ContainerRegistries {
	if r == nil {
		return nil
	}
	out := make(ContainerRegistries, len(r))
	for i, entry := range r {
		out[i] = ContainerRegistryEntry{Registry: entry.Registry, Roles: append([]RegistryRole(nil), entry.Roles...)}
		if entry.Cluster != nil {
			cluster := *entry.Cluster
			out[i].Cluster = &cluster
		}
	}
	return out
}

// DefaultContainerRegistries is the seed a fresh project starts with: build and
// pull from the same host, no copy.
func DefaultContainerRegistries() ContainerRegistries {
	return ContainerRegistries{{
		Registry: DefaultContainerRegistry,
		Roles:    []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy},
	}}
}

// SingleContainerRegistries builds the one-entry list that seeds a project from
// a single `--container-registry` value.
func SingleContainerRegistries(registry string) ContainerRegistries {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return nil
	}
	return ContainerRegistries{{
		Registry: registry,
		Roles:    []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy},
	}}
}

// ClusterContainerRegistries builds the one-entry list that seeds a new env from
// an in-cluster registry (e.g. the erun-registry the local k3s setup deploys),
// marked build+deploy. Its addresses resolve from the env's kube-context, so the
// same entry works for an in-pod build (ClusterIP) and a host build (managed
// port-forward) without hardcoding a host that only one of them can reach.
func ClusterContainerRegistries(cluster ClusterRegistry) ContainerRegistries {
	c := cluster.WithDefaults()
	return ContainerRegistries{{
		Cluster: &c,
		Roles:   []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy},
	}}
}

// migrateLegacyContainerRegistry folds a legacy `containerregistry` scalar into
// the marked list. The key is dropped on the next save, so the migration is
// one-way.
func migrateLegacyContainerRegistry(existing ContainerRegistries, legacy string) ContainerRegistries {
	if !existing.IsZero() {
		return existing
	}
	return SingleContainerRegistries(legacy)
}

// UnmarshalYAML migrates the legacy `containerregistry` scalar into the marked
// list so already-initialized projects keep working after the field was
// removed from the struct.
func (c *ProjectConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain ProjectConfig
	aux := struct {
		plain                   `yaml:",inline"`
		LegacyContainerRegistry string `yaml:"containerregistry,omitempty"`
	}{}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	*c = ProjectConfig(aux.plain)
	c.ContainerRegistries = migrateLegacyContainerRegistry(c.ContainerRegistries, aux.LegacyContainerRegistry)
	return nil
}

// UnmarshalYAML migrates an env's legacy fields on read so configs written by
// older binaries keep working after those fields were removed from the struct.
func (c *EnvConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain EnvConfig
	aux := struct {
		plain                   `yaml:",inline"`
		LegacyContainerRegistry string `yaml:"containerregistry,omitempty"`
		LegacyRemote            bool   `yaml:"remote,omitempty"`
		LegacySnapshot          *bool  `yaml:"snapshot,omitempty"`
		LegacyRepoPath          string `yaml:"repopath,omitempty"`
	}{}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	*c = EnvConfig(aux.plain)
	c.ContainerRegistries = migrateLegacyContainerRegistry(c.ContainerRegistries, aux.LegacyContainerRegistry)
	if !c.Type.IsValid() {
		c.Type = legacyEnvTypeFromRemoteSnapshot(aux.LegacyRemote, aux.LegacySnapshot)
	}
	// The legacy `repopath` fold is unconditional across all env types:
	// EffectiveLocalRepoPath already resolved `repopath` for every type via its
	// fallback, so only the stored field name moves. This intentionally broadens
	// the old local-agent-only NormalizeEnvConfig backfill, which was
	// inconsistent with that fallback.
	if strings.TrimSpace(c.LocalRepoPath) == "" {
		c.LocalRepoPath = strings.TrimSpace(aux.LegacyRepoPath)
	}
	return nil
}

// UnmarshalYAML migrates the per-env legacy `containerregistry` scalar the same
// way as the project default.
func (c *ProjectEnvironmentConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain ProjectEnvironmentConfig
	aux := struct {
		plain                   `yaml:",inline"`
		LegacyContainerRegistry string `yaml:"containerregistry,omitempty"`
	}{}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	*c = ProjectEnvironmentConfig(aux.plain)
	c.ContainerRegistries = migrateLegacyContainerRegistry(c.ContainerRegistries, aux.LegacyContainerRegistry)
	return nil
}

// ContainerRegistriesForEnvironment resolves the marked list for an
// environment: the per-env override wins when set, otherwise the project
// default. Returns nil when neither is configured (callers fall back to the
// default seed).
func (c ProjectConfig) ContainerRegistriesForEnvironment(environment string) ContainerRegistries {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && !envConfig.ContainerRegistries.IsZero() {
			return envConfig.ContainerRegistries
		}
	}
	return c.ContainerRegistries
}

// SetContainerRegistriesForEnvironment stores the marked list for an
// environment. An empty environment sets the project default; an empty list
// clears the per-env override; a per-env list equal to the project default is
// collapsed rather than stored.
func (c *ProjectConfig) SetContainerRegistriesForEnvironment(environment string, list ContainerRegistries) {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		c.ContainerRegistries = list.Clone()
		return
	}
	if list.IsZero() || list.Equal(c.ContainerRegistries) {
		if c.Environments != nil {
			if envConfig, ok := c.Environments[environment]; ok {
				envConfig.ContainerRegistries = nil
				if envConfig.isEmpty() {
					delete(c.Environments, environment)
				} else {
					c.Environments[environment] = envConfig
				}
				if len(c.Environments) == 0 {
					c.Environments = nil
				}
			}
		}
		return
	}
	if c.Environments == nil {
		c.Environments = make(map[string]ProjectEnvironmentConfig)
	}
	envConfig := c.Environments[environment]
	envConfig.ContainerRegistries = list.Clone()
	c.Environments[environment] = envConfig
}

// isEmpty reports whether a per-env entry carries nothing worth persisting, so
// a registry-only entry collapses away rather than being stored.
func (c ProjectEnvironmentConfig) isEmpty() bool {
	return c.ContainerRegistries.IsZero() && c.Docker.IsZero() && c.K8s.IsZero()
}

// DistinctRegistries returns the unique registry hosts in the list, in order.
// Version discovery queries each one so an offered version can be attributed to
// the registry it came from.
func (r ContainerRegistries) DistinctRegistries() []string {
	out := make([]string, 0, len(r))
	seen := make(map[string]struct{}, len(r))
	for _, entry := range r {
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" {
			continue
		}
		if _, ok := seen[registry]; ok {
			continue
		}
		seen[registry] = struct{}{}
		out = append(out, registry)
	}
	return out
}

// ResolveEnvironmentContainerRegistries returns the marked list for an
// environment: the per-env list carried on the env config (remote and runtime
// envs) when set, otherwise the project's configured list resolved through the
// env's local repo path. Best-effort and never errors; returns nil when nothing
// is configured so callers can apply the default seed or omit the field.
func ResolveEnvironmentContainerRegistries(env EnvConfig) ContainerRegistries {
	if !env.ContainerRegistries.IsZero() {
		return env.ContainerRegistries
	}
	repoPath := strings.TrimSpace(env.EffectiveLocalRepoPath())
	if repoPath == "" {
		return nil
	}
	projectConfig, _, err := LoadProjectConfig(repoPath)
	if err != nil {
		return nil
	}
	return projectConfig.ContainerRegistriesForEnvironment(env.Name)
}

// deployTargetContainerRegistries resolves the marked list for a deploy target,
// mirroring publishedDevopsChartRegistry's source order, and validates it so
// marker errors surface at deploy time.
func deployTargetContainerRegistries(target OpenResult) (ContainerRegistries, error) {
	if !target.EnvConfig.ContainerRegistries.IsZero() {
		list := target.EnvConfig.ContainerRegistries
		if err := list.Validate(); err != nil {
			return nil, fmt.Errorf("container registries for environment %q: %w", strings.TrimSpace(target.Environment), err)
		}
		return list, nil
	}
	return effectiveContainerRegistries(target.RepoPath, target.Environment)
}

// effectiveContainerRegistries applies the default seed when nothing is
// configured. Build and deploy both resolve through here so marker invariants
// are enforced at the point of use.
func effectiveContainerRegistries(projectRoot, environment string) (ContainerRegistries, error) {
	list := DefaultContainerRegistries()
	if strings.TrimSpace(projectRoot) != "" {
		projectConfig, _, err := LoadProjectConfig(projectRoot)
		if err != nil {
			if !errors.Is(err, ErrNotInitialized) {
				return nil, err
			}
		} else if configured := projectConfig.ContainerRegistriesForEnvironment(environment); !configured.IsZero() {
			list = configured
		}
	}
	if err := list.Validate(); err != nil {
		return nil, fmt.Errorf("container registries for environment %q: %w", strings.TrimSpace(environment), err)
	}
	return list, nil
}
