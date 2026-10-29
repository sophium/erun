package config

import (
	"strings"
)

// ProjectPathsConfig is the project-global `paths:` block relocating devops asset
// folders; unset fields keep the convention.
type ProjectPathsConfig struct {
	// Docker holds per-component build contexts. Default <tenant>-devops/docker.
	Docker string `yaml:"docker,omitempty"`
	// DockerContext is "repo-root" or "component", overriding the build-context
	// heuristic; empty keeps the heuristic.
	DockerContext string `yaml:"dockercontext,omitempty"`
	// K8s holds per-component Helm charts. Default <tenant>-devops/k8s.
	K8s string `yaml:"k8s,omitempty"`
	// Terraform is the base for per-environment roots. Default terraform-<tenant>.
	Terraform string `yaml:"terraform,omitempty"`
	// Version is the VERSION file, or a directory containing it.
	Version string `yaml:"version,omitempty"`
	// E2E holds e2e suites, one Dockerfile with an `e2e` stage each. Default
	// <tenant>-devops/e2e; refused under components:.
	E2E string `yaml:"e2e,omitempty"`
	// Playwright is retired; erun e2e refuses it with a migration hint.
	Playwright string `yaml:"playwright,omitempty"`
}

func (c ProjectPathsConfig) IsZero() bool {
	return strings.TrimSpace(c.Docker) == "" &&
		strings.TrimSpace(c.DockerContext) == "" &&
		strings.TrimSpace(c.K8s) == "" &&
		strings.TrimSpace(c.Terraform) == "" &&
		strings.TrimSpace(c.Version) == "" &&
		strings.TrimSpace(c.E2E) == "" &&
		strings.TrimSpace(c.Playwright) == ""
}
