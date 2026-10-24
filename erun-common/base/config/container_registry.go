package config

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RegistryRole marks a registry's part in build → copy → deploy; an entry may carry several.
type RegistryRole string

const (
	RegistryRoleBuild RegistryRole = "build"
	RegistryRoleFrom  RegistryRole = "from"
	RegistryRoleTo    RegistryRole = "to"
	// RegistryRoleDeploy marks the registry the cluster pulls from.
	RegistryRoleDeploy RegistryRole = "deploy"
)

// ContainerRegistryEntry names a static Registry or a kube-context-resolved
// Cluster; exactly one is set.
type ContainerRegistryEntry struct {
	Registry string           `yaml:"registry,omitempty" json:"registry,omitempty"`
	Cluster  *ClusterRegistry `yaml:"cluster,omitempty" json:"cluster,omitempty"`
	Roles    []RegistryRole   `yaml:"roles" json:"roles"`
	// Insecure is carried from Cluster.Insecure onto concretized entries; never persisted.
	Insecure bool `yaml:"-" json:"-"`
}

// ClusterRegistry is an in-cluster registry: the cluster pulls at the in-cluster
// address, operator roles push via the in-cluster address or a port-forward.
type ClusterRegistry struct {
	Service   string `yaml:"service,omitempty" json:"service,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Port      int    `yaml:"port,omitempty" json:"port,omitempty"`
	Insecure  bool   `yaml:"insecure,omitempty" json:"insecure,omitempty"`
}

// Defaults match the erun-registry the local k3s setup provisions.
const (
	DefaultClusterRegistryService   = "erun-registry"
	DefaultClusterRegistryNamespace = "kube-system"
	DefaultClusterRegistryPort      = 5000
)

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

// identity keys an entry for dedup; same-service cluster entries collapse.
func (e ContainerRegistryEntry) identity() string {
	if e.Cluster != nil {
		c := e.Cluster.WithDefaults()
		return fmt.Sprintf("cluster:%s.%s:%d", c.Service, c.Namespace, c.Port)
	}
	return strings.TrimSpace(e.Registry)
}

// ContainerRegistries is the marked registry list.
type ContainerRegistries []ContainerRegistryEntry

func normalizeRole(role RegistryRole) RegistryRole {
	return RegistryRole(strings.ToLower(strings.TrimSpace(string(role))))
}

func (e ContainerRegistryEntry) HasRole(role RegistryRole) bool {
	for _, candidate := range e.Roles {
		if normalizeRole(candidate) == role {
			return true
		}
	}
	return false
}

func (r ContainerRegistries) IsZero() bool {
	return len(r) == 0
}

func (r ContainerRegistries) registryEntryWithRole(role RegistryRole) (ContainerRegistryEntry, bool) {
	for _, entry := range r {
		if entry.HasRole(role) {
			if registry := strings.TrimSpace(entry.Registry); registry != "" {
				return entry, true
			}
		}
	}
	return ContainerRegistryEntry{}, false
}

func (r ContainerRegistries) registryWithRole(role RegistryRole) (string, bool) {
	entry, ok := r.registryEntryWithRole(role)
	return strings.TrimSpace(entry.Registry), ok
}

// BuildRegistry returns the BUILD-marked registry; !ok means the environment cannot build.
func (r ContainerRegistries) BuildRegistry() (string, bool) {
	return r.registryWithRole(RegistryRoleBuild)
}

// BuildRegistryInsecure reports whether the BUILD registry is plain HTTP, which
// `docker manifest` needs since it ignores the daemon's insecure-registry list.
func (r ContainerRegistries) BuildRegistryInsecure() bool {
	entry, ok := r.registryEntryWithRole(RegistryRoleBuild)
	return ok && entry.Insecure
}

func (r ContainerRegistries) FromRegistry() (string, bool) {
	return r.registryWithRole(RegistryRoleFrom)
}

// DeployRegistry returns the first DEPLOY-marked registry.
func (r ContainerRegistries) DeployRegistry() (string, bool) {
	return r.registryWithRole(RegistryRoleDeploy)
}

func (r ContainerRegistries) ToRegistries() []string {
	out := make([]string, 0, len(r))
	seen := make(map[string]struct{}, len(r))
	for _, entry := range r {
		if !entry.HasRole(RegistryRoleTo) {
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

// ClusterRegistryAddresses is a resolved cluster registry: Push for operator roles,
// Pull for the cluster.
type ClusterRegistryAddresses struct {
	Push string
	Pull string
}

// ClusterRegistryResolver is injected to keep kube-context queries out of config.
type ClusterRegistryResolver func(ClusterRegistry) (ClusterRegistryAddresses, error)

func isClusterPullRole(role RegistryRole) bool {
	return normalizeRole(role) == RegistryRoleDeploy
}

// HasClusterEntry reports whether a resolver is needed before use.
func (r ContainerRegistries) HasClusterEntry() bool {
	for _, entry := range r {
		if entry.Cluster != nil {
			return true
		}
	}
	return false
}

// ClusterEntry returns the first cluster block with defaults filled.
func (r ContainerRegistries) ClusterEntry() (ClusterRegistry, bool) {
	for _, entry := range r {
		if entry.Cluster != nil {
			return entry.Cluster.WithDefaults(), true
		}
	}
	return ClusterRegistry{}, false
}

// Concrete expands cluster entries into static per-role entries.
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
	insecure := entry.Cluster.Insecure
	out := make(ContainerRegistries, 0, 2)
	if len(push) > 0 {
		if strings.TrimSpace(addrs.Push) == "" {
			return nil, errors.New("cluster registry resolved an empty push address")
		}
		out = append(out, ContainerRegistryEntry{Registry: addrs.Push, Roles: push, Insecure: insecure})
	}
	if len(pull) > 0 {
		if strings.TrimSpace(addrs.Pull) == "" {
			return nil, errors.New("cluster registry resolved an empty pull address")
		}
		out = append(out, ContainerRegistryEntry{Registry: addrs.Pull, Roles: pull, Insecure: insecure})
	}
	return out, nil
}

// Validate enforces the marker invariants. DEPLOY need not pair with BUILD or TO,
// since the image may be published externally.
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
		if entry.HasRole(RegistryRoleBuild) {
			tally.build++
		}
		if entry.HasRole(RegistryRoleFrom) {
			tally.from++
			tally.fromRegistry = registry
		}
		if entry.HasRole(RegistryRoleTo) {
			tally.toRegistries[registry] = struct{}{}
		}
		if entry.HasRole(RegistryRoleDeploy) {
			tally.deploy++
		}
	}
	return tally, nil
}

// Equal reports whether two lists match in registries, roles and order.
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

// Clone returns a deep copy.
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

// DefaultContainerRegistries is the seed for a fresh project.
func DefaultContainerRegistries() ContainerRegistries {
	return ContainerRegistries{{
		Registry: DefaultContainerRegistry,
		Roles:    []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy},
	}}
}

// SingleContainerRegistries seeds a project from one `--container-registry` value.
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

// ClusterContainerRegistries seeds a build+deploy in-cluster registry entry whose
// addresses resolve per caller (ClusterIP in pod, port-forward on host).
func ClusterContainerRegistries(cluster ClusterRegistry) ContainerRegistries {
	c := cluster.WithDefaults()
	return ContainerRegistries{{
		Cluster: &c,
		Roles:   []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy},
	}}
}

// HostedRegistryHost is erun's hosted registry, authenticated by the tenant's API token.
const HostedRegistryHost = "registry.erunpaas.com"

// HostedRegistryLoginUsername is ignored by the token endpoint; the tenant comes
// from the password's issuer.
const HostedRegistryLoginUsername = "erun"

// HostedRegistryReference returns the tenant's namespace under the hosted registry;
// the token service clamps scope to it.
func HostedRegistryReference(tenant string) string {
	return HostedRegistryHost + "/" + strings.TrimSpace(tenant)
}

// migrateLegacyContainerRegistry folds a legacy `containerregistry` scalar into
// the list; the next save drops the key.
func migrateLegacyContainerRegistry(existing ContainerRegistries, legacy string) ContainerRegistries {
	if !existing.IsZero() {
		return existing
	}
	return SingleContainerRegistries(legacy)
}

// UnmarshalYAML migrates the legacy `containerregistry` scalar.
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

func (ProjectConfig) legacyYAMLKeys() []string {
	return []string{"containerregistry"}
}

// UnmarshalYAML migrates an env's legacy fields.
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
		c.Type = LegacyEnvTypeFromRemoteSnapshot(aux.LegacyRemote, aux.LegacySnapshot)
	}
	if strings.TrimSpace(c.LocalRepoPath) == "" {
		c.LocalRepoPath = strings.TrimSpace(aux.LegacyRepoPath)
	}
	return nil
}

func (EnvConfig) legacyYAMLKeys() []string {
	return []string{"containerregistry", "remote", "snapshot", "repopath"}
}

// UnmarshalYAML migrates the per-env legacy `containerregistry` scalar.
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

func (ProjectEnvironmentConfig) legacyYAMLKeys() []string {
	return []string{"containerregistry"}
}

// ContainerRegistriesForEnvironment returns the per-env override, else the project
// default, else nil.
func (c ProjectConfig) ContainerRegistriesForEnvironment(environment string) ContainerRegistries {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && !envConfig.ContainerRegistries.IsZero() {
			return envConfig.ContainerRegistries
		}
	}
	return c.ContainerRegistries
}

// SetContainerRegistriesForEnvironment stores the list; an empty environment sets
// the project default, and a per-env list equal to it is collapsed.
func (c *ProjectConfig) SetContainerRegistriesForEnvironment(environment string, list ContainerRegistries) {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		c.ContainerRegistries = list.Clone()
		return
	}
	if list.IsZero() || list.Equal(c.ContainerRegistries) {
		c.clearEnvironmentContainerRegistries(environment)
		return
	}
	if c.Environments == nil {
		c.Environments = make(map[string]ProjectEnvironmentConfig)
	}
	envConfig := c.Environments[environment]
	envConfig.ContainerRegistries = list.Clone()
	c.Environments[environment] = envConfig
}

func (c *ProjectConfig) clearEnvironmentContainerRegistries(environment string) {
	envConfig, ok := c.Environments[environment]
	if !ok {
		return
	}
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

func (c ProjectEnvironmentConfig) isEmpty() bool {
	return c.ContainerRegistries.IsZero() && c.Docker.IsZero() && c.K8s.IsZero()
}

// DistinctRegistries returns the unique registry hosts in order.
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

// ResolveEnvironmentContainerRegistries returns the configured list, or nil.
// Reporting must use EffectiveEnvironmentContainerRegistries instead.
func ResolveEnvironmentContainerRegistries(env EnvConfig) ContainerRegistries {
	if !env.ContainerRegistries.IsZero() {
		return env.ContainerRegistries
	}
	configured, err := configuredContainerRegistries(env.EffectiveLocalRepoPath(), env.Name)
	if err != nil {
		return nil
	}
	return configured
}

// EffectiveEnvironmentContainerRegistries is ResolveEnvironmentContainerRegistries
// with the default seed, so reporting matches what build and deploy use.
func EffectiveEnvironmentContainerRegistries(env EnvConfig) ContainerRegistries {
	if configured := ResolveEnvironmentContainerRegistries(env); !configured.IsZero() {
		return configured
	}
	return DefaultContainerRegistries()
}

// DeployTargetContainerRegistries resolves and validates the list for a deploy target.
func DeployTargetContainerRegistries(target OpenResult) (ContainerRegistries, error) {
	if !target.EnvConfig.ContainerRegistries.IsZero() {
		list := target.EnvConfig.ContainerRegistries
		if err := list.Validate(); err != nil {
			return nil, fmt.Errorf("container registries for environment %q: %w", strings.TrimSpace(target.Environment), err)
		}
		return list, nil
	}
	return EffectiveContainerRegistries(target.RepoPath, target.Environment)
}

// configuredContainerRegistries returns the project's explicit list for an
// environment, or nil; display and execution share it.
func configuredContainerRegistries(projectRoot, environment string) (ContainerRegistries, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}
	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return nil, nil
		}
		return nil, err
	}
	return projectConfig.ContainerRegistriesForEnvironment(environment), nil
}

// EffectiveContainerRegistries applies the default seed when nothing is configured.
func EffectiveContainerRegistries(projectRoot, environment string) (ContainerRegistries, error) {
	list := DefaultContainerRegistries()
	configured, err := configuredContainerRegistries(projectRoot, environment)
	if err != nil {
		return nil, err
	}
	if !configured.IsZero() {
		list = configured
	}
	if err := list.Validate(); err != nil {
		return nil, fmt.Errorf("container registries for environment %q: %w", strings.TrimSpace(environment), err)
	}
	return list, nil
}
