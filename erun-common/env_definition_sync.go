package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Pushing an environment definition to the platform and pulling one back are
// the same three steps in opposite directions: resolve the platform row this
// local environment corresponds to, refuse if what came back is not that row,
// and move the portable subset either way while stamping the revision that was
// moved. Keeping both here — rather than in the CLI — is what lets the desktop
// drive the identical transaction without a second implementation of the
// refusals.

// PlatformEnvDefinitionClient is the platform access a push or a pull needs.
// *PlatformClient satisfies it.
type PlatformEnvDefinitionClient interface {
	APIHost() string
	GetEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error)
	GetEnvironmentDefinition(ctx context.Context, environmentID string) (PlatformEnvDefinitionRecord, error)
	PutEnvironmentDefinition(ctx context.Context, environmentID string, definition PlatformEnvDefinition) (PlatformEnvDefinitionRecord, error)
}

// envDefinitionConfigStore is the on-disk access a push or pull needs: the
// environment's own config, and the port bookkeeping a pull-as-new has to
// consult before it writes. ConfigStore satisfies it.
type envDefinitionConfigStore interface {
	LoadEnvConfig(tenant, envName string) (EnvConfig, string, error)
	SaveEnvConfig(tenant string, config EnvConfig) error
	environmentPortStore
}

// EnvDefinitionPushParams names the local environment whose portable settings
// are uploaded. The platform row is not named here: the local marker is the
// only thing that knows which row this environment corresponds to, and letting
// a caller supply one would be a second way to say it.
type EnvDefinitionPushParams struct {
	Tenant      string
	Environment string
}

// EnvDefinitionPushResult is what an upload did, in the terms an operator reads
// back: which row it landed on, what revision it wrote, and the settings that
// travelled.
type EnvDefinitionPushResult struct {
	Marker      HostedEnvironment     `json:"marker"`
	Environment PlatformEnvironment   `json:"environment"`
	Definition  PlatformEnvDefinition `json:"definition"`
	Revision    int                   `json:"revision"`
}

// EnvDefinitionPullParams names the local environment a definition is pulled
// into.
//
// EnvironmentID is only consulted when the local environment does not exist
// yet. There is nothing on this machine to say which row to read in that case —
// the marker is what answers that question for an existing environment, and it
// does not exist yet — so a pull-as-new must name the row explicitly rather
// than guess one by name.
type EnvDefinitionPullParams struct {
	Tenant        string
	Environment   string
	EnvironmentID string
	// LocalRepoPath is only consulted for a pull-as-new: the types that
	// require a host repo path cannot build without one, and refusing here —
	// before the write — is what keeps a pull from creating an environment
	// that cannot build.
	LocalRepoPath string
	// LocalPortRangeStart is an explicit range start for a pull-as-new; zero
	// allocates the lowest free one.
	LocalPortRangeStart int
	// Confirm is asked when the local copy and the platform have both moved
	// since the last sync, with the diff between them. Nil means "no prompt is
	// available", which makes a two-sided edit a refusal rather than a silent
	// overwrite — the non-interactive transports' behaviour.
	Confirm func(EnvDefinitionPlan) (bool, error)
}

// EnvDefinitionPullResult is what a pull read and wrote.
type EnvDefinitionPullResult struct {
	Marker      HostedEnvironment   `json:"marker"`
	Environment PlatformEnvironment `json:"environment"`
	Plan        EnvDefinitionPlan   `json:"plan"`
	Config      EnvConfig           `json:"config"`
	// Revision is the definition revision the local copy now matches.
	Revision int `json:"revision"`
	// Created is true when the local environment did not exist before this
	// pull and was written from the platform row.
	Created bool `json:"created,omitempty"`
}

// ErrHostedDefinitionConflicts is returned by a pull that found a two-sided
// portable edit and had no way to ask about it. The plan rides on the error so
// a caller can render the diff without re-fetching.
type ErrHostedDefinitionConflicts struct {
	Plan EnvDefinitionPlan
}

func (e *ErrHostedDefinitionConflicts) Error() string {
	return fmt.Sprintf(
		"this environment and the platform have both changed since the last sync (%s): "+
			"re-run and confirm the diff, or revert the local changes you want the platform's copy to replace",
		renderDefinitionConflicts(e.Plan.Conflicts))
}

// ErrHostedDefinitionRefused is returned when a transfer cannot proceed at all:
// a platform-owned field disagrees, which no confirmation can resolve because
// the platform's copy is what the row is addressed by.
type ErrHostedDefinitionRefused struct {
	Refusals []EnvDefinitionRefusal
}

func (e *ErrHostedDefinitionRefused) Error() string {
	rendered := make([]string, 0, len(e.Refusals))
	for _, refusal := range e.Refusals {
		rendered = append(rendered, refusal.Error())
	}
	return "refusing to transfer this definition: " + strings.Join(rendered, "; ")
}

// ErrDefinitionPullDeclined is what a pull returns when the operator was asked
// about a two-sided edit and said no. It is a distinct value rather than a
// generic error so a caller can exit without printing a failure.
var ErrDefinitionPullDeclined = errors.New("pull declined")

func confirmDefinitionConflicts(confirm func(EnvDefinitionPlan) (bool, error), plan EnvDefinitionPlan) (bool, error) {
	if confirm == nil {
		return false, &ErrHostedDefinitionConflicts{Plan: plan}
	}
	return confirm(plan)
}

func renderDefinitionConflicts(conflicts []EnvDefinitionConflict) string {
	rendered := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		rendered = append(rendered, fmt.Sprintf("%s: %s -> %s", conflict.Label, conflict.OrUnsetLocal(), conflict.OrUnsetPlatform()))
	}
	return strings.Join(rendered, ", ")
}

// OrUnsetLocal renders the local side of a conflict, naming an empty value
// rather than leaving a blank the operator has to interpret.
func (c EnvDefinitionConflict) OrUnsetLocal() string { return valueOrUnset(c.Local) }

// OrUnsetPlatform renders the platform side of a conflict.
func (c EnvDefinitionConflict) OrUnsetPlatform() string { return valueOrUnset(c.Platform) }

// PushEnvironmentDefinition uploads the local environment's portable settings
// to the platform row its marker names.
//
// The order is deliberate: the row is fetched and checked against the marker
// *before* anything is written, so an environment whose marker points at
// another tenant's row — or at a same-named row on another plane — fails
// without having pushed its settings anywhere.
func PushEnvironmentDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, params EnvDefinitionPushParams) (EnvDefinitionPushResult, error) {
	config, _, err := store.LoadEnvConfig(params.Tenant, params.Environment)
	if err != nil {
		return EnvDefinitionPushResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
	}
	marker, ok := HostedEnvironmentFromConfig(config)
	if !ok {
		return EnvDefinitionPushResult{}, notHostedError(params.Tenant, params.Environment)
	}
	row, err := resolveHostedRow(ctx, client, config, marker)
	if err != nil {
		return EnvDefinitionPushResult{}, err
	}

	return uploadDefinition(ctx, store, client, config, row, params.Tenant)
}

// uploadDefinition is the half an upload and an adopt share: project the
// portable subset, write it, and stamp the revision that landed. Keeping it in
// one place is what makes "the marker means the platform holds this revision"
// true on both paths — a marker written before the write would claim a sync
// that never happened.
func uploadDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, config EnvConfig, row PlatformEnvironment, tenant string) (EnvDefinitionPushResult, error) {
	definition := BuildPlatformEnvDefinition(config)
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

// EnvDefinitionAdoptParams names the local environment whose portable settings
// are uploaded alongside its registration.
type EnvDefinitionAdoptParams struct {
	Tenant      string
	Environment string
}

// AdoptEnvironmentDefinition records the hosted marker for a just-registered
// platform row and uploads revision 1 of the local environment's portable
// settings, in that order.
//
// It is the same transaction as a push with one difference: the marker does not
// exist yet, so the row the caller registered is what the marker is written
// from. That is why the row is a parameter rather than resolved from the
// marker — and why a local environment that already carries a marker naming a
// *different* row is refused: adopting a second row over one this machine
// already follows would leave the first one unreachable from here.
func AdoptEnvironmentDefinition(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, row PlatformEnvironment, params EnvDefinitionAdoptParams) (EnvDefinitionPushResult, error) {
	config, _, err := store.LoadEnvConfig(params.Tenant, params.Environment)
	if err != nil {
		return EnvDefinitionPushResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
	}
	if existing, ok := HostedEnvironmentFromConfig(config); ok {
		registered := markerFor(row, client.APIHost(), existing.DefinitionRevision)
		if !existing.SameRow(registered) {
			return EnvDefinitionPushResult{}, fmt.Errorf(
				"%s/%s is already hosted at %s; refusing to re-point it at %s. Clear the hosted block in its config.yaml first if that marker is stale",
				params.Tenant, params.Environment, existing.Describe(), registered.Describe())
		}
	}
	refusals := platformOwnedMismatches(EnvDefinitionPlanParams{
		Local:                     config,
		PlatformName:              row.Name,
		PlatformType:              row.Type,
		PlatformKubernetesContext: row.KubernetesContext,
	})
	if len(refusals) > 0 {
		return EnvDefinitionPushResult{}, &ErrHostedDefinitionRefused{Refusals: refusals}
	}

	return uploadDefinition(ctx, store, client, config, row, params.Tenant)
}

// PullEnvironmentDefinition reads the platform's stored definition and writes
// its portable subset into the local environment's config, preserving every
// field the platform has no opinion about.
//
// A local environment that does not exist yet is created from the platform row
// — pull-as-new — which is the one case that needs a host repo path and a local
// port range, both settled before anything is written.
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
	plan := BuildEnvDefinitionPlan(EnvDefinitionPlanParams{
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

// loadPullBase reads the local environment a pull is about to write, reporting
// whether it is a pull-as-new rather than treating the absent config as an
// error.
func loadPullBase(store envDefinitionConfigStore, params EnvDefinitionPullParams) (EnvConfig, bool, error) {
	config, _, err := store.LoadEnvConfig(params.Tenant, params.Environment)
	if err == nil {
		return config, false, nil
	}
	if errors.Is(err, ErrNotInitialized) {
		return EnvConfig{}, true, nil
	}
	return EnvConfig{}, false, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
}

// confirmPullPlan refuses what no confirmation can resolve, and asks about what
// only the operator can decide.
func confirmPullPlan(plan EnvDefinitionPlan, confirm func(EnvDefinitionPlan) (bool, error)) error {
	if len(plan.Refusals) > 0 {
		return &ErrHostedDefinitionRefused{Refusals: plan.Refusals}
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

// applyPulledDefinition writes the portable subset and stamps the revision the
// local copy now matches.
func applyPulledDefinition(config EnvConfig, definition PlatformEnvDefinition, marker HostedEnvironment) (EnvConfig, error) {
	updated, err := ApplyPlatformEnvDefinition(config, definition)
	if err != nil {
		return EnvConfig{}, err
	}
	return stampHostedMarker(updated, marker)
}

// resolvePullTarget answers which platform row this pull reads, and refuses any
// disagreement between the local environment and that row. For an existing
// local environment the marker answers it; for a pull-as-new nothing local can,
// so the caller must name the row.
func resolvePullTarget(ctx context.Context, store envDefinitionConfigStore, client PlatformEnvDefinitionClient, params EnvDefinitionPullParams, config EnvConfig, created bool) (HostedEnvironment, PlatformEnvironment, error) {
	if !created {
		marker, ok := HostedEnvironmentFromConfig(config)
		if !ok {
			return HostedEnvironment{}, PlatformEnvironment{}, notHostedError(params.Tenant, params.Environment)
		}
		row, err := resolveHostedRow(ctx, client, config, marker)
		return marker, row, err
	}

	environmentID := strings.TrimSpace(params.EnvironmentID)
	if environmentID == "" {
		return HostedEnvironment{}, PlatformEnvironment{}, fmt.Errorf(
			"%s/%s does not exist on this machine: a pull into a new environment must name the platform row with --environment-id "+
				"(find it with `erun platform env list`)",
			params.Tenant, params.Environment)
	}
	row, err := client.GetEnvironment(ctx, environmentID)
	if err != nil {
		return HostedEnvironment{}, PlatformEnvironment{}, err
	}
	// The local config path is derived from the name, so a row whose name is
	// not the directory being pulled into would write a different environment
	// than the one named — the silent rename the marker exists to prevent,
	// caught here before anything is written.
	if row.Name != params.Environment {
		return HostedEnvironment{}, PlatformEnvironment{}, &ErrHostedDefinitionRefused{Refusals: []EnvDefinitionRefusal{{
			Field:  "Name",
			Class:  EnvFieldPlatformOwned,
			Reason: fmt.Sprintf("the platform knows it as %q, so pulling it down would write %s/%s, not %s/%s", row.Name, params.Tenant, row.Name, params.Tenant, params.Environment),
		}}}
	}
	return HostedEnvironment{APIHost: client.APIHost(), TenantID: row.TenantID, EnvironmentID: row.EnvironmentID}, row, nil
}

// buildPullAsNewConfig settles the two host-owned values a new environment
// cannot inherit, before any of the pull's writes happen.
func buildPullAsNewConfig(store envDefinitionConfigStore, params EnvDefinitionPullParams, marker HostedEnvironment, row PlatformEnvironment) (EnvConfig, error) {
	portRangeStart, err := PlanPullLocalPortRangeStart(store, params.Tenant, params.Environment, params.LocalPortRangeStart)
	if err != nil {
		return EnvConfig{}, err
	}
	return NewPullAsNewEnvironment(row, params.LocalRepoPath, portRangeStart)
}

// resolveHostedRow fetches the row the marker names and refuses any
// disagreement between it and the local environment.
func resolveHostedRow(ctx context.Context, client PlatformEnvDefinitionClient, config EnvConfig, marker HostedEnvironment) (PlatformEnvironment, error) {
	row, err := client.GetEnvironment(ctx, marker.EnvironmentID)
	if err != nil {
		return PlatformEnvironment{}, err
	}
	if err := CheckHostedMarkerTarget(marker, client.APIHost(), row.TenantID, row.EnvironmentID); err != nil {
		return PlatformEnvironment{}, err
	}
	// A platform-owned field that disagrees is a refusal in both directions,
	// not only on a pull: uploading the settings of a local directory that is
	// not the row the marker names would attribute them to the wrong
	// environment.
	refusals := platformOwnedMismatches(EnvDefinitionPlanParams{
		Local:                     config,
		PlatformName:              row.Name,
		PlatformType:              row.Type,
		PlatformKubernetesContext: row.KubernetesContext,
	})
	if len(refusals) > 0 {
		return PlatformEnvironment{}, &ErrHostedDefinitionRefused{Refusals: refusals}
	}
	return row, nil
}

func notHostedError(tenant, environment string) error {
	return fmt.Errorf(
		"%s/%s is not marked as hosted (%w). Register it first with `erun platform env register --adopt --definition %s/%s`, which records the platform row this machine's copy corresponds to",
		tenant, environment, ErrEnvironmentNotHosted, tenant, environment)
}

func markerFor(row PlatformEnvironment, apiHost string, revision int) HostedEnvironment {
	return HostedEnvironment{
		APIHost:            apiHost,
		TenantID:           row.TenantID,
		EnvironmentID:      row.EnvironmentID,
		DefinitionRevision: revision,
	}
}

func stampHostedMarker(config EnvConfig, marker HostedEnvironment) (EnvConfig, error) {
	if err := validateStatePathSegment("environment", config.Name); err != nil {
		return EnvConfig{}, err
	}
	config.Hosted = marker
	return config, nil
}

// PlanPullLocalPortRangeStart returns the local port range start a pull-as-new
// should write, refusing a start another environment on this machine already
// claims.
//
// It runs before the write on purpose. A collision discovered afterwards has
// already written a config the port resolver will refuse on the next command
// that needs a local port, which surfaces far from the pull that caused it.
func PlanPullLocalPortRangeStart(store environmentPortStore, tenant, environment string, requested int) (int, error) {
	claimed, err := claimedPortRangeIndexes(store, environmentPortKey(tenant, environment))
	if err != nil {
		return 0, err
	}
	if requested > 0 {
		return checkRequestedPortRangeStart(claimed, requested, environmentPortKey(tenant, environment))
	}
	return lowestFreePortRangeStart(claimed, environmentPortKey(tenant, environment))
}

// claimedPortRangeIndexes maps each port-range index another environment on
// this machine holds to that environment. A pull into an environment that
// already holds a range must not collide with itself, so its own claim is
// skipped rather than counted.
func claimedPortRangeIndexes(store environmentPortStore, key string) (map[int]string, error) {
	persisted, _, err := collectEnvironmentPortRefs(store)
	if err != nil {
		return nil, err
	}
	claimed := make(map[int]string, len(persisted))
	for _, ref := range persisted {
		if ref.key == key {
			continue
		}
		index, err := environmentPortIndexForRangeStart(ref.env.LocalPortRangeStart, ref.key)
		if err != nil {
			return nil, err
		}
		claimed[index] = ref.key
	}
	return claimed, nil
}

func checkRequestedPortRangeStart(claimed map[int]string, requested int, key string) (int, error) {
	index, err := environmentPortIndexForRangeStart(requested, key)
	if err != nil {
		return 0, err
	}
	if other, taken := claimed[index]; taken {
		return 0, ErrLocalPortRangeOverlap{A: other, B: key, RangeStart: requested}
	}
	return requested, nil
}

func lowestFreePortRangeStart(claimed map[int]string, key string) (int, error) {
	for index := 0; ; index++ {
		if _, taken := claimed[index]; taken {
			continue
		}
		start := LowerServicePort + index*EnvironmentPortRangeSize
		if start+EnvironmentPortRangeSize-1 > 65535 {
			return 0, fmt.Errorf("no free local port range for %s: every %d-port block from %d is claimed", key, EnvironmentPortRangeSize, LowerServicePort)
		}
		return start, nil
	}
}

// NewPullAsNewEnvironment builds the local config a pull writes when the
// environment does not exist on this machine yet. It is not the pull itself:
// the caller runs this first so the repo path and the port range are settled
// before anything reaches the disk.
func NewPullAsNewEnvironment(row PlatformEnvironment, repoPath string, portRangeStart int) (EnvConfig, error) {
	envType := EnvironmentType(strings.TrimSpace(row.Type))
	if !envType.IsValid() {
		return EnvConfig{}, fmt.Errorf("the platform row's type %q is not an environment type this erun knows", row.Type)
	}
	if !IsHostableEnvironmentType(envType) {
		return EnvConfig{}, fmt.Errorf("a %s environment cannot be hosted: it has no pod and no cluster, and a platform environment record exists to manage a pod lifecycle", envType)
	}
	if err := ValidateEnvRepoPath(envType, repoPath); err != nil {
		return EnvConfig{}, err
	}
	return EnvConfig{
		Name:                row.Name,
		Type:                envType,
		KubernetesContext:   strings.TrimSpace(row.KubernetesContext),
		LocalRepoPath:       strings.TrimSpace(repoPath),
		LocalPortRangeStart: portRangeStart,
	}, nil
}

// IsHostableEnvironmentType reports whether a local environment type can have a
// platform row at all.
//
// A host env cannot: it is a plain directory with no pod and no cluster, and
// the platform's environment record exists to manage a pod's lifecycle — its
// adopt path structurally requires a Kubernetes context a host env has none of.
// The local model carries four types and the platform admits three, and this is
// where that difference is enforced on the client side rather than left to a
// server-side 400 to explain.
func IsHostableEnvironmentType(envType EnvironmentType) bool {
	switch envType {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeRuntime:
		return true
	default:
		return false
	}
}
