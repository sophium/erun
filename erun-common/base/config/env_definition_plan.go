package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// EnvDefinitionConflict is one portable field that differs between local and platform.
type EnvDefinitionConflict struct {
	Field    string `json:"field"`
	Label    string `json:"label"`
	Local    string `json:"local"`
	Platform string `json:"platform"`
}

// EnvDefinitionPlan is what a pull would do, computed without writing.
type EnvDefinitionPlan struct {
	Changes []EnvDefinitionConflict `json:"changes,omitempty"`
	// Conflicts are the Changes edited on both sides since the last sync.
	Conflicts []EnvDefinitionConflict           `json:"conflicts,omitempty"`
	Refusals  []model.EnvDefinitionRefusalError `json:"refusals,omitempty"`
}

func (p EnvDefinitionPlan) HasChanges() bool { return len(p.Changes) > 0 }

// EnvDefinitionPlanParams is what BuildEnvDefinitionPlan compares.
type EnvDefinitionPlanParams struct {
	Local                     EnvConfig
	Definition                PlatformEnvDefinition
	PlatformName              string
	PlatformType              string
	PlatformKubernetesContext string
	LocalRevision             int
	PlatformRevision          int
}

// BuildEnvDefinitionPlan reports what a pull would do. Platform-owned differences
// refuse (a rename would create a second environment), host-owned fields are not
// compared, and portable fields edited on both sides become conflicts.
func BuildEnvDefinitionPlan(params EnvDefinitionPlanParams) EnvDefinitionPlan {
	var plan EnvDefinitionPlan
	plan.Refusals = append(plan.Refusals, PlatformOwnedMismatches(params)...)

	plan.Changes = portableDefinitionChanges(params.Local, params.Definition)
	if params.LocalRevision != params.PlatformRevision && params.LocalRevision > 0 {
		plan.Conflicts = append(plan.Conflicts, plan.Changes...)
	}
	return plan
}

// PlatformOwnedMismatches reports platform-owned fields that disagree with the row.
func PlatformOwnedMismatches(params EnvDefinitionPlanParams) []model.EnvDefinitionRefusalError {
	var refusals []model.EnvDefinitionRefusalError
	name := strings.TrimSpace(params.PlatformName)
	if local := strings.TrimSpace(params.Local.Name); name != "" && local != "" && local != name {
		refusals = append(refusals, model.EnvDefinitionRefusalError{
			Field:  "Name",
			Class:  model.EnvFieldPlatformOwned,
			Reason: fmt.Sprintf("the platform knows this environment as %q, and the local config path is derived from the name", name),
		})
	}
	if platformType := strings.TrimSpace(params.PlatformType); platformType != "" {
		if local := strings.TrimSpace(string(params.Local.Type)); local != "" && local != platformType {
			refusals = append(refusals, model.EnvDefinitionRefusalError{
				Field:  "Type",
				Class:  model.EnvFieldPlatformOwned,
				Reason: fmt.Sprintf("the platform registered it as %q", platformType),
			})
		}
	}
	if platformContext := strings.TrimSpace(params.PlatformKubernetesContext); platformContext != "" {
		if local := strings.TrimSpace(params.Local.KubernetesContext); local != "" && local != platformContext {
			refusals = append(refusals, model.EnvDefinitionRefusalError{
				Field:  "KubernetesContext",
				Class:  model.EnvFieldPlatformOwned,
				Reason: fmt.Sprintf("the platform registered it against context %q", platformContext),
			})
		}
	}
	return refusals
}

func portableDefinitionChanges(base EnvConfig, definition PlatformEnvDefinition) []EnvDefinitionConflict {
	changes := make([]EnvDefinitionConflict, 0, len(platformDefinitionFields))
	for _, field := range platformDefinitionFields {
		platform, hasOpinion := field.Stored(definition)
		if !hasOpinion {
			continue
		}
		local := field.Local(base)
		if strings.TrimSpace(local) == strings.TrimSpace(platform) {
			continue
		}
		changes = append(changes, EnvDefinitionConflict{Field: field.Field, Label: field.Label, Local: local, Platform: platform})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Field < changes[j].Field })
	return changes
}

func renderRuntimePodResources(resources model.RuntimePodResources) string {
	return fmt.Sprintf("cpu=%s memory=%s", valueOrUnset(resources.CPU), valueOrUnset(resources.Memory))
}

func renderNamespaceQuota(quota model.NamespaceResourceQuota) string {
	if quota.IsZero() {
		return "none"
	}
	return fmt.Sprintf("cpu=%s memory=%s storage=%s", valueOrUnset(quota.CPU), valueOrUnset(quota.Memory), valueOrUnset(quota.Storage))
}

func renderIdleConfig(idle EnvironmentIdleConfig) string {
	return fmt.Sprintf("timeout=%s working-hours=%s timezone=%s idle-traffic-bytes=%d",
		valueOrUnset(idle.Timeout), valueOrUnset(idle.WorkingHours), valueOrUnset(idle.Timezone), idle.IdleTrafficBytes)
}

func renderClaudeConfig(config EnvironmentClaudeConfig) string {
	parts := []string{
		"use-mantle=" + renderBoolPointer(config.UseMantle),
		"use-bedrock=" + renderBoolPointer(config.UseBedrock),
		"use-gateway=" + renderBoolPointer(config.UseGateway),
	}
	if len(config.Models) > 0 {
		parts = append(parts, "models="+strings.Join(config.Models, ","))
	}
	// A field missing here would be overwritten silently by a pull.
	if config.MaxOutputTokens != nil {
		parts = append(parts, "max-output-tokens="+strconv.Itoa(*config.MaxOutputTokens))
	}
	if config.Effort != nil {
		parts = append(parts, "effort="+valueOrUnset(*config.Effort))
	}
	if config.DefaultModel != nil {
		parts = append(parts, "default-model="+valueOrUnset(*config.DefaultModel))
	}
	if config.VerboseDebug {
		parts = append(parts, "verbose-debug=enabled")
	}
	return strings.Join(parts, " ")
}

func renderContainerRegistries(registries ContainerRegistries) string {
	if len(registries) == 0 {
		return "none"
	}
	names := make([]string, 0, len(registries))
	for _, entry := range registries {
		name := strings.TrimSpace(entry.Registry)
		if name == "" && entry.Cluster != nil {
			name = "cluster:" + entry.Cluster.Service
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func renderBool(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func renderBoolPointer(value *bool) string {
	if value == nil {
		return "unset"
	}
	return renderBool(*value)
}

func valueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unset"
	}
	return value
}
