package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/envdefinition"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/config/repopath"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// Push and pull live here, not in the CLI, so the desktop shares the same refusals.

// PlatformEnvDefinitionClient is the platform access a push or pull needs.
type PlatformEnvDefinitionClient interface {
	APIHost() string
	GetEnvironment(ctx context.Context, environmentID string) (model.PlatformEnvironment, error)
	GetEnvironmentDefinition(ctx context.Context, environmentID string) (platformclient.PlatformEnvDefinitionRecord, error)
	PutEnvironmentDefinition(ctx context.Context, environmentID string, definition configpkg.PlatformEnvDefinition) (platformclient.PlatformEnvDefinitionRecord, error)
}

// envDefinitionConfigStore is the on-disk access a push or pull needs.
type envDefinitionConfigStore interface {
	LoadEnvConfig(tenant, envName string) (configpkg.EnvConfig, string, error)
	SaveEnvConfig(tenant string, config configpkg.EnvConfig) error
	localports.EnvironmentPortStore
}

type EnvDefinitionPushResult struct {
	Marker      configpkg.HostedEnvironment     `json:"marker"`
	Environment model.PlatformEnvironment       `json:"environment"`
	Definition  configpkg.PlatformEnvDefinition `json:"definition"`
	Revision    int                             `json:"revision"`
}

// EnvDefinitionPullParams names the local environment to pull into. EnvironmentID is used
// only for a pull-as-new, where no marker exists to name the row.
type EnvDefinitionPullParams struct {
	Tenant        string
	Environment   string
	EnvironmentID string
	// LocalRepoPath is required for a pull-as-new of a type that cannot build without one.
	LocalRepoPath string
	// LocalPortRangeStart zero allocates the lowest free range.
	LocalPortRangeStart int
	// Confirm is asked about a two-sided edit; nil makes that a refusal, never an overwrite.
	Confirm func(envdefinition.EnvDefinitionPlan) (bool, error)
}

type EnvDefinitionPullResult struct {
	Marker      configpkg.HostedEnvironment     `json:"marker"`
	Environment model.PlatformEnvironment       `json:"environment"`
	Plan        envdefinition.EnvDefinitionPlan `json:"plan"`
	Config      configpkg.EnvConfig             `json:"config"`
	Revision    int                             `json:"revision"`
	// Created is true when this pull created the local environment.
	Created bool `json:"created,omitempty"`
}

// HostedDefinitionConflictsError carries the plan of an unconfirmable two-sided edit.
type HostedDefinitionConflictsError struct {
	Plan envdefinition.EnvDefinitionPlan
}

func (e *HostedDefinitionConflictsError) Error() string {
	return fmt.Sprintf(
		"this environment and the platform have both changed since the last sync (%s): "+
			"re-run and confirm the diff, or revert the local changes you want the platform's copy to replace",
		renderDefinitionConflicts(e.Plan.Conflicts))
}

// HostedDefinitionRefusedError means a platform-owned field disagrees; no confirmation resolves it.
type HostedDefinitionRefusedError struct {
	Refusals []model.EnvDefinitionRefusalError
}

func (e *HostedDefinitionRefusedError) Error() string {
	rendered := make([]string, 0, len(e.Refusals))
	for _, refusal := range e.Refusals {
		rendered = append(rendered, refusal.Error())
	}
	return "refusing to transfer this definition: " + strings.Join(rendered, "; ")
}

// ErrDefinitionPullDeclined lets a caller exit without printing a failure.
var ErrDefinitionPullDeclined = errors.New("pull declined")

func confirmDefinitionConflicts(confirm func(envdefinition.EnvDefinitionPlan) (bool, error), plan envdefinition.EnvDefinitionPlan) (bool, error) {
	if confirm == nil {
		return false, &HostedDefinitionConflictsError{Plan: plan}
	}
	return confirm(plan)
}

func renderDefinitionConflicts(conflicts []envdefinition.EnvDefinitionConflict) string {
	rendered := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		rendered = append(rendered, fmt.Sprintf("%s: %s -> %s", conflict.Label, conflict.OrUnsetLocal(), conflict.OrUnsetPlatform()))
	}
	return strings.Join(rendered, ", ")
}

// PushEnvironmentDefinition uploads portable settings to the marker's row, checking the row
// before writing anything.
func PushEnvironmentDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, params model.EnvDefinitionPushParams) (EnvDefinitionPushResult, error) {
	config, _, err := store.LoadEnvConfig(params.Tenant, params.Environment)
	if err != nil {
		return EnvDefinitionPushResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
	}
	marker, ok := envdefinition.HostedEnvironmentFromConfig(config)
	if !ok {
		return EnvDefinitionPushResult{}, notHostedError(params.Tenant, params.Environment)
	}
	row, err := resolveHostedRow(ctx, client, config, marker)
	if err != nil {
		return EnvDefinitionPushResult{}, err
	}

	return uploadDefinition(ctx, store, client, config, row, params.Tenant)
}

// uploadDefinition stamps the marker only after the write lands.
func uploadDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, config configpkg.EnvConfig, row model.PlatformEnvironment, tenant string) (EnvDefinitionPushResult, error) {
	definition := envdefinition.BuildPlatformEnvDefinition(config)
	record, err := client.PutEnvironmentDefinition(ctx, row.EnvironmentID, definition)
	if err != nil {
		return EnvDefinitionPushResult{}, err
	}
	stamped, err := stampHostedMarker(config, markerFor(row, client.APIHost(), record.Revision))
	if err != nil {
		return EnvDefinitionPushResult{}, err
	}
	if err := store.SaveEnvConfig(tenant, stamped); err != nil {
		return EnvDefinitionPushResult{}, err
	}
	return EnvDefinitionPushResult{
		Marker:      stamped.Hosted,
		Environment: row,
		Definition:  definition,
		Revision:    record.Revision,
	}, nil
}

// AdoptEnvironmentDefinition writes the marker for a just-registered row and uploads
// revision 1. A marker naming a different row is refused.
func AdoptEnvironmentDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, row model.PlatformEnvironment, params model.EnvDefinitionAdoptParams) (EnvDefinitionPushResult, error) {
	config, _, err := store.LoadEnvConfig(params.Tenant, params.Environment)
	if err != nil {
		return EnvDefinitionPushResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
	}
	if existing, ok := envdefinition.HostedEnvironmentFromConfig(config); ok {
		registered := markerFor(row, client.APIHost(), existing.DefinitionRevision)
		if !existing.SameRow(registered) {
			return EnvDefinitionPushResult{}, fmt.Errorf(
				"%s/%s is already hosted at %s; refusing to re-point it at %s. Clear the hosted block in its config.yaml first if that marker is stale",
				params.Tenant, params.Environment, existing.Describe(), registered.Describe())
		}
	}
	refusals := envdefinition.PlatformOwnedMismatches(envdefinition.EnvDefinitionPlanParams{
		Local:                     config,
		PlatformName:              row.Name,
		PlatformType:              row.Type,
		PlatformKubernetesContext: row.KubernetesContext,
	})
	if len(refusals) > 0 {
		return EnvDefinitionPushResult{}, &HostedDefinitionRefusedError{Refusals: refusals}
	}

	return uploadDefinition(ctx, store, client, config, row, params.Tenant)
}

// PullEnvironmentDefinition writes the platform definition's portable subset into the local
// config, creating the environment if absent (pull-as-new).
func PullEnvironmentDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, params EnvDefinitionPullParams) (EnvDefinitionPullResult, error) {
	config, created, err := loadPullBase(store, params)
	if err != nil {
		return EnvDefinitionPullResult{}, err
	}
	marker, row, err := resolvePullTarget(ctx, store, client, params, config, created)
	if err != nil {
		return EnvDefinitionPullResult{}, err
	}
	if created {
		if config, err = buildPullAsNewConfig(store, params, marker, row); err != nil {
			return EnvDefinitionPullResult{}, err
		}
	}

	record, err := client.GetEnvironmentDefinition(ctx, row.EnvironmentID)
	if err != nil {
		return EnvDefinitionPullResult{}, err
	}
	plan := envdefinition.BuildEnvDefinitionPlan(envdefinition.EnvDefinitionPlanParams{
		Local:                     config,
		Definition:                record.Definition,
		PlatformName:              row.Name,
		PlatformType:              row.Type,
		PlatformKubernetesContext: row.KubernetesContext,
		LocalRevision:             marker.DefinitionRevision,
		PlatformRevision:          record.Revision,
	})
	if err := confirmPullPlan(plan, params.Confirm); err != nil {
		return EnvDefinitionPullResult{}, err
	}

	updated, err := applyPulledDefinition(config, record.Definition, markerFor(row, client.APIHost(), record.Revision))
	if err != nil {
		return EnvDefinitionPullResult{}, err
	}
	if err := store.SaveEnvConfig(params.Tenant, updated); err != nil {
		return EnvDefinitionPullResult{}, err
	}

	return EnvDefinitionPullResult{
		Marker:      updated.Hosted,
		Environment: row,
		Plan:        plan,
		Config:      updated,
		Revision:    record.Revision,
		Created:     created,
	}, nil
}

// loadPullBase reports a missing config as pull-as-new rather than an error.
func loadPullBase(store envDefinitionConfigStore, params EnvDefinitionPullParams) (configpkg.EnvConfig, bool, error) {
	config, _, err := store.LoadEnvConfig(params.Tenant, params.Environment)
	if err == nil {
		return config, false, nil
	}
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return configpkg.EnvConfig{}, true, nil
	}
	return configpkg.EnvConfig{}, false, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
}

func confirmPullPlan(plan envdefinition.EnvDefinitionPlan, confirm func(envdefinition.EnvDefinitionPlan) (bool, error)) error {
	if len(plan.Refusals) > 0 {
		return &HostedDefinitionRefusedError{Refusals: plan.Refusals}
	}
	if len(plan.Conflicts) == 0 {
		return nil
	}
	confirmed, err := confirmDefinitionConflicts(confirm, plan)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrDefinitionPullDeclined
	}
	return nil
}

func applyPulledDefinition(config configpkg.EnvConfig, definition configpkg.PlatformEnvDefinition, marker configpkg.HostedEnvironment) (configpkg.EnvConfig, error) {
	updated, err := envdefinition.ApplyPlatformEnvDefinition(config, definition)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	return stampHostedMarker(updated, marker)
}

// resolvePullTarget resolves the row from the marker, or from params for a pull-as-new.
func resolvePullTarget(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, params EnvDefinitionPullParams, config configpkg.EnvConfig, created bool) (configpkg.HostedEnvironment, model.PlatformEnvironment, error) {
	if !created {
		marker, ok := envdefinition.HostedEnvironmentFromConfig(config)
		if !ok {
			return configpkg.HostedEnvironment{}, model.PlatformEnvironment{}, notHostedError(params.Tenant, params.Environment)
		}
		row, err := resolveHostedRow(ctx, client, config, marker)
		return marker, row, err
	}

	environmentID := strings.TrimSpace(params.EnvironmentID)
	if environmentID == "" {
		return configpkg.HostedEnvironment{}, model.PlatformEnvironment{}, fmt.Errorf(
			"%s/%s does not exist on this machine: a pull into a new environment must name the platform row with --environment-id "+
				"(find it with `erun platform env list`)",
			params.Tenant, params.Environment)
	}
	row, err := client.GetEnvironment(ctx, environmentID)
	if err != nil {
		return configpkg.HostedEnvironment{}, model.PlatformEnvironment{}, err
	}
	// The config path derives from the name, so a mismatched row name would write another environment.
	if row.Name != params.Environment {
		return configpkg.HostedEnvironment{}, model.PlatformEnvironment{}, &HostedDefinitionRefusedError{Refusals: []model.EnvDefinitionRefusalError{{
			Field:  "Name",
			Class:  model.EnvFieldPlatformOwned,
			Reason: fmt.Sprintf("the platform knows it as %q, so pulling it down would write %s/%s, not %s/%s", row.Name, params.Tenant, row.Name, params.Tenant, params.Environment),
		}}}
	}
	return configpkg.HostedEnvironment{APIHost: client.APIHost(), TenantID: row.TenantID, EnvironmentID: row.EnvironmentID}, row, nil
}

func buildPullAsNewConfig(store envDefinitionConfigStore, params EnvDefinitionPullParams, marker configpkg.HostedEnvironment, row model.PlatformEnvironment) (configpkg.EnvConfig, error) {
	portRangeStart, err := PlanPullLocalPortRangeStart(store, params.Tenant, params.Environment, params.LocalPortRangeStart)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	return NewPullAsNewEnvironment(row, params.LocalRepoPath, portRangeStart)
}

func resolveHostedRow(ctx context.Context, client PlatformEnvDefinitionClient, config configpkg.EnvConfig, marker configpkg.HostedEnvironment) (model.PlatformEnvironment, error) {
	row, err := client.GetEnvironment(ctx, marker.EnvironmentID)
	if err != nil {
		return model.PlatformEnvironment{}, err
	}
	if err := envdefinition.CheckHostedMarkerTarget(marker, client.APIHost(), row.TenantID, row.EnvironmentID); err != nil {
		return model.PlatformEnvironment{}, err
	}
	// A platform-owned mismatch refuses a push too, or settings land on the wrong environment.
	refusals := envdefinition.PlatformOwnedMismatches(envdefinition.EnvDefinitionPlanParams{
		Local:                     config,
		PlatformName:              row.Name,
		PlatformType:              row.Type,
		PlatformKubernetesContext: row.KubernetesContext,
	})
	if len(refusals) > 0 {
		return model.PlatformEnvironment{}, &HostedDefinitionRefusedError{Refusals: refusals}
	}
	return row, nil
}

func notHostedError(tenant, environment string) error {
	return fmt.Errorf(
		"%s/%s is not marked as hosted (%w). Register it first with `erun platform env register --adopt --definition %s/%s`, which records the platform row this machine's copy corresponds to",
		tenant, environment, envdefinition.ErrEnvironmentNotHosted, tenant, environment)
}

func markerFor(row model.PlatformEnvironment, apiHost string, revision int) configpkg.HostedEnvironment {
	return configpkg.HostedEnvironment{
		APIHost:            apiHost,
		TenantID:           row.TenantID,
		EnvironmentID:      row.EnvironmentID,
		DefinitionRevision: revision,
	}
}

// stampHostedMarker records the row and digests the post-transfer portable settings.
func stampHostedMarker(config configpkg.EnvConfig, marker configpkg.HostedEnvironment) (configpkg.EnvConfig, error) {
	if err := configpkg.ValidateStatePathSegment("environment", config.Name); err != nil {
		return configpkg.EnvConfig{}, err
	}
	marker.DefinitionDigest = envdefinition.DefinitionDigest(envdefinition.BuildPlatformEnvDefinition(config))
	config.Hosted = marker
	return config, nil
}

// PlanPullLocalPortRangeStart refuses a range start another environment claims, before the
// write rather than at the next command.
func PlanPullLocalPortRangeStart(store localports.EnvironmentPortStore, tenant, environment string, requested int) (int, error) {
	claimed, err := claimedPortRangeIndexes(store, localports.EnvironmentPortKey(tenant, environment))
	if err != nil {
		return 0, err
	}
	if requested > 0 {
		return checkRequestedPortRangeStart(claimed, requested, localports.EnvironmentPortKey(tenant, environment))
	}
	return lowestFreePortRangeStart(claimed, localports.EnvironmentPortKey(tenant, environment))
}

// claimedPortRangeIndexes skips the environment's own claim.
func claimedPortRangeIndexes(store localports.EnvironmentPortStore, key string) (map[int]string, error) {
	persisted, _, err := localports.CollectEnvironmentPortRefs(store)
	if err != nil {
		return nil, err
	}
	claimed := make(map[int]string, len(persisted))
	for _, ref := range persisted {
		if ref.Key == key {
			continue
		}
		index, err := localports.EnvironmentPortIndexForRangeStart(ref.Env.LocalPortRangeStart, ref.Key)
		if err != nil {
			return nil, err
		}
		claimed[index] = ref.Key
	}
	return claimed, nil
}

func checkRequestedPortRangeStart(claimed map[int]string, requested int, key string) (int, error) {
	index, err := localports.EnvironmentPortIndexForRangeStart(requested, key)
	if err != nil {
		return 0, err
	}
	if other, taken := claimed[index]; taken {
		return 0, localports.LocalPortRangeOverlapError{A: other, B: key, RangeStart: requested}
	}
	return requested, nil
}

func lowestFreePortRangeStart(claimed map[int]string, key string) (int, error) {
	for index := 0; ; index++ {
		if _, taken := claimed[index]; taken {
			continue
		}
		start := configpkg.LowerServicePort + index*configpkg.EnvironmentPortRangeSize
		if start+configpkg.EnvironmentPortRangeSize-1 > 65535 {
			return 0, fmt.Errorf("no free local port range for %s: every %d-port block from %d is claimed", key, configpkg.EnvironmentPortRangeSize, configpkg.LowerServicePort)
		}
		return start, nil
	}
}

// NewPullAsNewEnvironment builds a pull-as-new config; callers run it before writing.
func NewPullAsNewEnvironment(row model.PlatformEnvironment, repoPath string, portRangeStart int) (configpkg.EnvConfig, error) {
	envType := configpkg.EnvironmentType(strings.TrimSpace(row.Type))
	if !envType.IsValid() {
		return configpkg.EnvConfig{}, fmt.Errorf("the platform row's type %q is not an environment type this erun knows", row.Type)
	}
	if !IsHostableEnvironmentType(envType) {
		return configpkg.EnvConfig{}, fmt.Errorf("a %s environment cannot be hosted: it has no pod and no cluster, and a platform environment record exists to manage a pod lifecycle", envType)
	}
	if err := repopath.ValidateEnvRepoPath(envType, repoPath); err != nil {
		return configpkg.EnvConfig{}, err
	}
	return configpkg.EnvConfig{
		Name:                row.Name,
		Type:                envType,
		KubernetesContext:   strings.TrimSpace(row.KubernetesContext),
		LocalRepoPath:       strings.TrimSpace(repoPath),
		LocalPortRangeStart: portRangeStart,
	}, nil
}

// IsHostableEnvironmentType reports whether a type can have a platform row; a host env has no
// pod for the platform to manage.
func IsHostableEnvironmentType(envType configpkg.EnvironmentType) bool {
	switch envType {
	case configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeRemoteAgent, configpkg.EnvironmentTypeRuntime:
		return true
	case configpkg.EnvironmentTypeHost:
		return false
	default:
		return false
	}
}
