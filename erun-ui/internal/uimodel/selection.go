// Package uimodel is the shared kernel of the desktop's backend services: the
// selection most bound methods take, the session result and event payloads more
// than one service produces, and the event names the frontend listens for. It
// is a leaf, so every service can depend on it without depending on another.
package uimodel

import (
	"errors"
	"fmt"
	"strings"
)

type Selection struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Version           string `json:"version,omitempty"`
	RuntimeImage      string `json:"runtimeImage,omitempty"`
	RuntimeCPU        string `json:"runtimeCpu,omitempty"`
	RuntimeMemory     string `json:"runtimeMemory,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	ContainerRegistry string `json:"containerRegistry,omitempty"`
	// ClusterRegistry wins over ContainerRegistry when set.
	ClusterRegistry bool `json:"clusterRegistry,omitempty"`
	// ErunRegistry excludes ContainerRegistry and ClusterRegistry.
	ErunRegistry bool `json:"erunRegistry,omitempty"`
	// RuntimeRegistry and ImagePullSecrets let the env pull a private runtime image.
	RuntimeRegistry  string   `json:"runtimeRegistry,omitempty"`
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`
	Type             string   `json:"type,omitempty"`
	LocalRepoPath    string   `json:"localRepoPath,omitempty"`
	NoGit            bool     `json:"noGit,omitempty"`
	SetDefaultTenant bool     `json:"setDefaultTenant,omitempty"`
	// Components are chart directory names, never erun-* dependency names; empty
	// uses the env's saved default.
	Components []string `json:"components,omitempty"`
}

func NormalizeSelection(selection Selection) Selection {
	return Selection{
		Tenant:            strings.TrimSpace(selection.Tenant),
		Environment:       strings.TrimSpace(selection.Environment),
		Version:           strings.TrimSpace(selection.Version),
		RuntimeImage:      strings.TrimSpace(selection.RuntimeImage),
		RuntimeCPU:        strings.TrimSpace(selection.RuntimeCPU),
		RuntimeMemory:     strings.TrimSpace(selection.RuntimeMemory),
		KubernetesContext: strings.TrimSpace(selection.KubernetesContext),
		ContainerRegistry: strings.TrimSpace(selection.ContainerRegistry),
		Type:              strings.TrimSpace(selection.Type),
		LocalRepoPath:     strings.TrimSpace(selection.LocalRepoPath),
		NoGit:             selection.NoGit,
		SetDefaultTenant:  selection.SetDefaultTenant,
		Components:        TrimmedNonEmpty(selection.Components),
	}
}

// TrimmedNonEmpty trims each value and drops the blank ones; nil when none remain.
func TrimmedNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if value := strings.TrimSpace(raw); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ErrMissingTenantOrEnvironment returns nil when both are set, so it can be a caller's whole guard.
func ErrMissingTenantOrEnvironment(operation, tenant, environment string) error {
	var missing []string
	if strings.TrimSpace(tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(environment) == "" {
		missing = append(missing, "environment")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s: %s not set -- select an environment in the sidebar before retrying this action",
		operation, strings.Join(missing, " and "),
	)
}

// ErrTenantNotGiven means frontend state was not resolved yet, not that the operator typed nothing.
var ErrTenantNotGiven = errors.New("no tenant given")

// RequireTenant's error tells the operator to reopen the surface, which re-resolves the tenant.
func RequireTenant(operation, tenant string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", fmt.Errorf("%w: %s needs a tenant, and none was given — close and reopen the tab or dialog and try again", ErrTenantNotGiven, operation)
	}
	return tenant, nil
}
