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

// ContainerRegistryEntry is one registry in the project's marked list: a host
// (or host/namespace) plus the roles it carries.
type ContainerRegistryEntry struct {
	Registry string         `yaml:"registry" json:"registry"`
	Roles    []RegistryRole `yaml:"roles" json:"roles"`
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
		registry := strings.TrimSpace(entry.Registry)
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
