package config

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

type ProjectEnvironmentConfig struct {
	ContainerRegistries ContainerRegistries `yaml:"containerregistries,omitempty"`
	Docker              ProjectDockerConfig `yaml:"docker,omitempty"`
	K8s                 ProjectK8sConfig    `yaml:"k8s,omitempty"`
}

// ProjectDockerConfig holds docker settings. In `docker:` at the top level of
// .erun/config.yaml it is the project-wide default every environment inherits;
// in environments.<env>.docker it is that environment's own settings.
// Fingerprints maps an image name to its canonical content fingerprint as
// published by release CI, so fresh dev clones can promote pinned base images
// without rebuilding them while local Dockerfile edits still force a rebuild
// (the locally computed fingerprint diverges from the tagged hash).
type ProjectDockerConfig struct {
	Fingerprints map[string]string `yaml:"fingerprints,omitempty"`
	// Platforms pins the docker --platform targets a non-release build/push mints
	// (e.g. ["linux/amd64"]), for a machine or cluster that can only ever run one
	// architecture. It never applies to a release build (`erun build --release`,
	// `erun release`): those always publish every platform erun supports, since a
	// release artifact must be deployable anywhere.
	//
	// At the top level it is the project default, inherited by every environment
	// that declares no platforms of its own — so a project whose machines are all
	// single-architecture states that once instead of listing each environment,
	// and an environment nobody remembered to list cannot silently fall back to
	// the slow multi-arch path. An environment's own list wins outright, and an
	// explicit empty list (`platforms: []`) opts that environment out of the
	// project default and restores the built-in multi-arch build — the escape
	// hatch for a generic environment name such as `local`, which can belong to a
	// contributor's machine of any architecture. Absent or empty everywhere keeps
	// the default multi-arch build.
	Platforms []string `yaml:"platforms,omitempty"`
	// Secrets declares the BuildKit build secrets every build of this project
	// receives, as `--secret id=<id>,env=<VAR>` / `,src=<path>` references. It
	// exists so a Dockerfile step that must fetch something private — a chart
	// from a registry that is not anonymously pullable, for instance — can run
	// to full coverage under a real build instead of degrading to skipping that
	// work, which is a gate reporting success having verified less than it was
	// asked to.
	//
	// Each entry carries a reference (an environment variable name or a host
	// path), never a credential value, so no secret's contents are ever held in
	// a Go struct and cannot reach a command line, a trace, or a log.
	//
	// Inheritance follows docker.platforms exactly: at the top level this is the
	// project default every environment inherits, an environment's own list wins
	// outright, and a declared-but-empty list (`secrets: []`) opts that
	// environment out of the default.
	Secrets []DockerBuildSecret `yaml:"secrets,omitempty"`
}

func (c ProjectDockerConfig) IsZero() bool {
	return len(c.Fingerprints) == 0 && len(c.Platforms) == 0 && len(c.Secrets) == 0
}

type ReleaseConfig struct {
	MainBranch    string `yaml:"mainbranch,omitempty"`
	DevelopBranch string `yaml:"developbranch,omitempty"`
}

type ProjectConfig struct {
	ContainerRegistries ContainerRegistries `yaml:"containerregistries,omitempty"`
	// Docker holds project-wide docker defaults. An environment inherits any
	// setting it does not declare itself; see DockerPlatformsForEnvironment.
	Docker       ProjectDockerConfig                 `yaml:"docker,omitempty"`
	Environments map[string]ProjectEnvironmentConfig `yaml:"environments,omitempty"`
	Release      ReleaseConfig                       `yaml:"release,omitempty"`
	// Platform holds the per-instance erunpaas platform configuration; empty for
	// projects that do not run a platform deployment.
	Platform PlatformConfig `yaml:"platform,omitempty"`
	// Paths overrides where erun discovers the project's devops assets (docker/,
	// k8s/, terraform-<tenant>/, VERSION); empty keeps the conventional layout.
	Paths ProjectPathsConfig `yaml:"paths,omitempty"`
	// Components declares more than one paths:-shaped root, keyed by component
	// name, for a monorepo of independent deployables that do not share one
	// docker/k8s root (e.g. harnesses/<name>/{docker,k8s}). Empty keeps paths: as
	// the project's only root. See project_components_config.go for selection.
	Components map[string]ProjectPathsConfig `yaml:"components,omitempty"`
	// E2E holds erun e2e's own settings; see ProjectE2EConfig.
	E2E ProjectE2EConfig `yaml:"e2e,omitempty"`
}

// K8sForEnvironment returns the k8s deploy plan declared for the given
// environment, or an empty plan when none exists.
func (c ProjectConfig) K8sForEnvironment(environment string) ProjectK8sConfig {
	environment = strings.TrimSpace(environment)
	if environment == "" || c.Environments == nil {
		return ProjectK8sConfig{}
	}
	envConfig, ok := c.Environments[environment]
	if !ok {
		return ProjectK8sConfig{}
	}
	return envConfig.K8s
}

// ProjectK8sConfig declares the deploy plan for `erun deploy` in this project.
// Deployments lists the steps in the order they must run; each step is a
// group of components to deploy in parallel. A step may be a single
// component name (scalar) or a sequence of names (parallel group).
type ProjectK8sConfig struct {
	Deployments []ProjectK8sDeploymentStep `yaml:"deployments,omitempty"`
}

func (c ProjectK8sConfig) IsZero() bool {
	return len(c.Deployments) == 0
}

// ProjectK8sDeploymentStep is one ordered step in the deploy plan. The
// Components slice always holds the parallel group; YAML unmarshaling lifts a
// scalar single-component form into a one-element slice so users can write
// either form interchangeably.
type ProjectK8sDeploymentStep struct {
	Components []string
}

func (s *ProjectK8sDeploymentStep) UnmarshalYAML(node *yaml.Node) error {
	if s == nil {
		return errors.New("nil ProjectK8sDeploymentStep")
	}
	switch node.Kind {
	case yaml.ScalarNode:
		value := strings.TrimSpace(node.Value)
		if value == "" {
			s.Components = nil
			return nil
		}
		s.Components = []string{value}
		return nil
	case yaml.SequenceNode:
		components := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				return errors.New("k8s.deployments parallel group must contain only component names")
			}
			value := strings.TrimSpace(child.Value)
			if value == "" {
				continue
			}
			components = append(components, value)
		}
		s.Components = components
		return nil
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
	}
	return errors.New("k8s.deployments item must be a component name or a list of component names")
}

// MarshalYAML emits the natural form: a scalar when the step has exactly one
// component, a flow-style sequence when it has multiple. This keeps
// round-tripped configs readable instead of forcing every step into a list.
func (s ProjectK8sDeploymentStep) MarshalYAML() (any, error) {
	if len(s.Components) == 1 {
		return s.Components[0], nil
	}
	return s.Components, nil
}

// DockerFingerprintsForEnvironment returns the image-name → fingerprint map for
// the given environment, or nil when none is set. Hash values are validated lazily
// at materialization, not load time, so a malformed entry surfaces in the build
// trace instead of breaking unrelated commands that read other config sections.
func (c ProjectConfig) DockerFingerprintsForEnvironment(environment string) map[string]string {
	environment = strings.TrimSpace(environment)
	if environment == "" || c.Environments == nil {
		return nil
	}
	envConfig, ok := c.Environments[environment]
	if !ok || len(envConfig.Docker.Fingerprints) == 0 {
		return nil
	}
	out := make(map[string]string, len(envConfig.Docker.Fingerprints))
	for name, hash := range envConfig.Docker.Fingerprints {
		name = strings.TrimSpace(name)
		hash = strings.TrimSpace(hash)
		if name == "" || hash == "" {
			continue
		}
		out[name] = hash
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DockerPlatformsForEnvironment returns the docker --platform targets a
// non-release build/push mints for the given environment, or nil when the
// environment is unpinned (keeping the default multi-arch build).
//
// An environment's own environments.<env>.docker.platforms wins outright. An
// environment that declares none inherits the project-wide docker.platforms
// default, so a project whose machines are all single-architecture does not
// have to list each environment by name and a new environment cannot silently
// fall back to the slow multi-arch path. A declared-but-empty list
// (`platforms: []`) is the explicit opt-out from that default, which is how a
// generic environment name such as `local` — one that can belong to a
// contributor's machine of any architecture — stays unpinned.
func (c ProjectConfig) DockerPlatformsForEnvironment(environment string) []string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Platforms != nil {
			return normalizedDockerPlatforms(envConfig.Docker.Platforms)
		}
	}
	return normalizedDockerPlatforms(c.Docker.Platforms)
}

// DockerPlatformsOrigin names where DockerPlatformsForEnvironment's value came
// from, so a build trace says which config key decided the platform list.
func (c ProjectConfig) DockerPlatformsOrigin(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Platforms != nil {
			return "environments." + environment + ".docker.platforms"
		}
	}
	return "docker.platforms (project default)"
}

// DockerSecretsForEnvironment returns the BuildKit build secrets a build
// declares for the given environment, or nil when it declares none.
//
// It mirrors DockerPlatformsForEnvironment deliberately, so the two sibling
// keys under `docker:` obey one inheritance rule: an environment's own
// environments.<env>.docker.secrets wins outright, an environment that declares
// none inherits the project-wide docker.secrets default, and a
// declared-but-empty list (`secrets: []`) is the explicit opt-out.
func (c ProjectConfig) DockerSecretsForEnvironment(environment string) []DockerBuildSecret {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Secrets != nil {
			return envConfig.Docker.Secrets
		}
	}
	return c.Docker.Secrets
}

// DockerSecretsOrigin names where DockerSecretsForEnvironment's value came
// from, so a build trace says which config key decided the secret list.
func (c ProjectConfig) DockerSecretsOrigin(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Secrets != nil {
			return "environments." + environment + ".docker.secrets"
		}
	}
	return "docker.secrets (project default)"
}

// normalizedDockerPlatforms trims a configured platform list and reports an
// empty result as nil, which reads as "unpinned" to every caller.
func normalizedDockerPlatforms(platforms []string) []string {
	out := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		if platform = strings.TrimSpace(platform); platform != "" {
			out = append(out, platform)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (c ProjectConfig) NormalizedReleaseConfig() ReleaseConfig {
	config := c.Release
	if strings.TrimSpace(config.MainBranch) == "" {
		config.MainBranch = DefaultReleaseMainBranch
	}
	if strings.TrimSpace(config.DevelopBranch) == "" {
		config.DevelopBranch = DefaultReleaseDevelopBranch
	}
	return config
}
