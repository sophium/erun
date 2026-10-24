package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// EnvDefinitionConflict names one portable field where the local value and the
// platform's stored value both moved away from the revision they last agreed
// on. It is what a pull renders as the diff it asks the operator about.
type EnvDefinitionConflict struct {
	// Field is the EnvConfig field name, and Label is the operator-facing name
	// of the same thing.
	Field string `json:"field"`
	Label string `json:"label"`
	Local string `json:"local"`
	// Platform is the value the stored definition carries.
	Platform string `json:"platform"`
}

// EnvDefinitionRefusalError is a field whose two records disagree and cannot be
// reconciled by asking: a platform-owned field differing locally, or a
// host-owned field the platform has no business carrying at all.
type EnvDefinitionRefusalError struct {
	Field  string              `json:"field"`
	Class  EnvConfigFieldClass `json:"class"`
	Reason string              `json:"reason"`
}

func (r EnvDefinitionRefusalError) Error() string {
	return fmt.Sprintf("%s is %s and cannot be transferred: %s", r.Field, r.Class, r.Reason)
}

// EnvDefinitionPlan is what a pull would do, computed without writing anything:
// the conflicts an operator must decide, the refusals that stop it, and the
// portable fields that differ and would be written.
type EnvDefinitionPlan struct {
	// Changes are the portable fields the pull would overwrite, rendered as
	// "local -> platform".
	Changes []EnvDefinitionConflict `json:"changes,omitempty"`
	// Conflicts are the subset of Changes that were edited on both sides since
	// the last sync. They are the ones that must be asked about rather than
	// answered.
	Conflicts []EnvDefinitionConflict `json:"conflicts,omitempty"`
	// Refusals stop the pull outright.
	Refusals []EnvDefinitionRefusalError `json:"refusals,omitempty"`
}

// HasChanges reports whether the pull would write anything.
func (p EnvDefinitionPlan) HasChanges() bool { return len(p.Changes) > 0 }

// EnvDefinitionPlanParams is what BuildEnvDefinitionPlan compares: the local
// environment, the definition the platform stored, the platform row's own
// platform-owned columns, and the two revisions.
type EnvDefinitionPlanParams struct {
	Local EnvConfig
	// Definition is the stored portable subset.
	Definition PlatformEnvDefinition
	// PlatformName, PlatformType and PlatformKubernetesContext are the
	// environment row's own columns — the platform-owned facts a local config
	// must agree with.
	PlatformName              string
	PlatformType              string
	PlatformKubernetesContext string
	// LocalRevision is the definition revision this local copy was last synced
	// from; PlatformRevision is the revision the platform now holds.
	LocalRevision    int
	PlatformRevision int
}

// BuildEnvDefinitionPlan compares a local EnvConfig against a platform
// definition and reports what a pull would do.
//
// The class decides what happens to a difference, never which side is newer:
//
//   - a platform-owned field differing locally is a refusal. Both sides hold a
//     record of the same fact, and the platform's copy is the one the row is
//     addressed by — a rename in particular would write a different local
//     directory and create a second environment rather than updating this one.
//   - a host-owned field is not compared at all. The platform never carried it,
//     so the host wins and there is nothing to report.
//   - a portable field differing is what a pull writes. When the local copy has
//     since moved past the revision it was synced from, the same portable field
//     may have been edited on both sides, and the operator is asked with the
//     diff rather than answered silently.
func BuildEnvDefinitionPlan(params EnvDefinitionPlanParams) EnvDefinitionPlan {
	var plan EnvDefinitionPlan
	plan.Refusals = append(plan.Refusals, PlatformOwnedMismatches(params)...)

	plan.Changes = portableDefinitionChanges(params.Local, params.Definition)
	if params.LocalRevision != params.PlatformRevision && params.LocalRevision > 0 {
		plan.Conflicts = append(plan.Conflicts, plan.Changes...)
	}
	return plan
}

// PlatformOwnedMismatches reports the platform-owned fields whose local value
// disagrees with the environment row.
func PlatformOwnedMismatches(params EnvDefinitionPlanParams) []EnvDefinitionRefusalError {
	var refusals []EnvDefinitionRefusalError
	name := strings.TrimSpace(params.PlatformName)
	if local := strings.TrimSpace(params.Local.Name); name != "" && local != "" && local != name {
		refusals = append(refusals, EnvDefinitionRefusalError{
			Field:  "Name",
			Class:  EnvFieldPlatformOwned,
			Reason: fmt.Sprintf("the platform knows this environment as %q, and the local config path is derived from the name", name),
		})
	}
	if platformType := strings.TrimSpace(params.PlatformType); platformType != "" {
		if local := strings.TrimSpace(string(params.Local.Type)); local != "" && local != platformType {
			refusals = append(refusals, EnvDefinitionRefusalError{
				Field:  "Type",
				Class:  EnvFieldPlatformOwned,
				Reason: fmt.Sprintf("the platform registered it as %q", platformType),
			})
		}
	}
	if platformContext := strings.TrimSpace(params.PlatformKubernetesContext); platformContext != "" {
		if local := strings.TrimSpace(params.Local.KubernetesContext); local != "" && local != platformContext {
			refusals = append(refusals, EnvDefinitionRefusalError{
				Field:  "KubernetesContext",
				Class:  EnvFieldPlatformOwned,
				Reason: fmt.Sprintf("the platform registered it against context %q", platformContext),
			})
		}
	}
	return refusals
}

// portableDefinitionChanges renders the portable fields whose local value
// differs from the definition's, as operator-facing before/after pairs.
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

func renderRuntimePodResources(resources RuntimePodResources) string {
	return fmt.Sprintf("cpu=%s memory=%s", valueOrUnset(resources.CPU), valueOrUnset(resources.Memory))
}

func renderNamespaceQuota(quota NamespaceResourceQuota) string {
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
	// Every other field counts too, each only when set so a definition that
	// never chose one renders as before: a field missing here is one the drift
	// check cannot see, so pulling a definition would overwrite it silently.
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
