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

// ProjectDockerConfig holds docker settings: project-wide at the top level,
// per-environment under environments.<env>.docker.
type ProjectDockerConfig struct {
	Fingerprints map[string]string `yaml:"fingerprints,omitempty"`
	// Platforms pins non-release build targets; release builds always publish every
	// platform. An explicit empty list opts an environment out of the project default.
	Platforms []string `yaml:"platforms,omitempty"`
	// Secrets declares BuildKit build secrets as references (env var or path), never
	// values. Inheritance matches Platforms.
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
	ContainerRegistries ContainerRegistries                 `yaml:"containerregistries,omitempty"`
	Docker              ProjectDockerConfig                 `yaml:"docker,omitempty"`
	Environments        map[string]ProjectEnvironmentConfig `yaml:"environments,omitempty"`
	Release             ReleaseConfig                       `yaml:"release,omitempty"`
	Platform            PlatformConfig                      `yaml:"platform,omitempty"`
	// Paths overrides where devops assets are discovered; empty keeps the convention.
	Paths ProjectPathsConfig `yaml:"paths,omitempty"`
	// Components declares several paths:-shaped roots for a monorepo; empty keeps
	// paths: as the only root.
	Components map[string]ProjectPathsConfig `yaml:"components,omitempty"`
	E2E        ProjectE2EConfig              `yaml:"e2e,omitempty"`
}

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

// ProjectK8sConfig is the `erun deploy` plan: ordered steps, each a parallel
// group given as a scalar or a sequence.
type ProjectK8sConfig struct {
	Deployments []ProjectK8sDeploymentStep `yaml:"deployments,omitempty"`
}

func (c ProjectK8sConfig) IsZero() bool {
	return len(c.Deployments) == 0
}

// ProjectK8sDeploymentStep is one deploy step; a scalar unmarshals to one element.
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

// MarshalYAML emits a scalar for a single component so round-trips stay readable.
func (s ProjectK8sDeploymentStep) MarshalYAML() (any, error) {
	if len(s.Components) == 1 {
		return s.Components[0], nil
	}
	return s.Components, nil
}

// DockerFingerprintsForEnvironment returns the environment's fingerprints, or nil.
// Hashes are validated at materialization so a bad entry fails only the build.
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

// DockerPlatformsForEnvironment returns the non-release platform targets, or nil
// for the multi-arch default. The environment's own list, even empty, wins.
func (c ProjectConfig) DockerPlatformsForEnvironment(environment string) []string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Platforms != nil {
			return normalizedDockerPlatforms(envConfig.Docker.Platforms)
		}
	}
	return normalizedDockerPlatforms(c.Docker.Platforms)
}

// DockerPlatformsOrigin names the config key DockerPlatformsForEnvironment used.
func (c ProjectConfig) DockerPlatformsOrigin(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Platforms != nil {
			return "environments." + environment + ".docker.platforms"
		}
	}
	return "docker.platforms (project default)"
}

// DockerSecretsForEnvironment returns the environment's build secrets with the
// same inheritance as DockerPlatformsForEnvironment.
func (c ProjectConfig) DockerSecretsForEnvironment(environment string) []DockerBuildSecret {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Secrets != nil {
			return envConfig.Docker.Secrets
		}
	}
	return c.Docker.Secrets
}

// DockerSecretsOrigin names the config key DockerSecretsForEnvironment used.
func (c ProjectConfig) DockerSecretsOrigin(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok && envConfig.Docker.Secrets != nil {
			return "environments." + environment + ".docker.secrets"
		}
	}
	return "docker.secrets (project default)"
}

// normalizedDockerPlatforms trims entries; an empty result is nil ("unpinned").
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
