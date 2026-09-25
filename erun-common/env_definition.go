package eruncommon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// The hosted environment definition is the portable subset of an
// EnvConfig: the settings that describe an environment rather than the machine
// it happens to be authored on. It is transferable between hosts, so it can be
// uploaded to the platform and pulled back down onto a second machine.
//
// The rule is a positive allowlist and it is default-deny. A field travels only
// if portableEnvConfigFields lists it as portable; anything absent from that
// table is never uploaded AND never written by a pull. That direction matters:
// the failure mode a deny-list invites is a *new* EnvConfig field silently
// joining the payload because nobody remembered to exclude it, and the field
// most likely to be added next is another host-local secret reference. With an
// allowlist a new field is invisible until somebody deliberately opts it in.
// TestEveryEnvConfigFieldIsClassified is what keeps the table honest: adding a
// field to EnvConfig fails that test until it is classified here.
//
// Two classes of field are therefore excluded, and they are excluded for
// different reasons:
//
//   - host-owned fields describe this machine or name a cluster-local object.
//     They are never merged: the host always wins, because the platform never
//     carried them in the first place.
//   - platform-owned fields mirror real columns on the environment row and are
//     fixed at registration. A local value that differs is not merged and not
//     overwritten — it is a refusal, because the two are records of the same
//     fact and only one of them can be right.
//
// The root config.yaml and the secret store are permanently out of scope. The
// root file holds CloudContextConfig.AdminToken in plaintext — `json:"-"` hides
// it from JSON only, never from its own yaml tag — and a definition transfer
// that could reach it would be a credential-exfiltration path, not a
// convenience.
type EnvConfigFieldClass string

const (
	// EnvFieldPortable is a field that travels in the definition payload.
	EnvFieldPortable EnvConfigFieldClass = "portable"
	// EnvFieldHostOwned is a field that describes the machine the environment
	// is authored on, or names a cluster-local Kubernetes object. It is never
	// uploaded and never written by a pull.
	EnvFieldHostOwned EnvConfigFieldClass = "host-owned"
	// EnvFieldPlatformOwned is a field the platform owns outright: it mirrors
	// a column on the environment row and is fixed at registration. A locally
	// differing value refuses rather than merging.
	EnvFieldPlatformOwned EnvConfigFieldClass = "platform-owned"
)

// envConfigField is one row of the classification table.
type envConfigField struct {
	Class EnvConfigFieldClass
	// Note is the operator-facing reason this field carries its class. It is
	// rendered by the refusal messages and by the docs' "which settings never
	// leave the machine" list, so it is written for an operator, not a reader
	// of this file.
	Note string
}

// portableEnvConfigFields classifies every top-level EnvConfig field.
//
// A field name here is a Go field name, matched exactly. Nesting is expressed
// where the nested value is genuinely one decision (`SSHD` is host-owned in all
// three sub-fields) and split where it is not (`Deploy`, whose timeout is
// portable and whose components list is the operator's per-machine selection).
var portableEnvConfigFields = map[string]envConfigField{
	// Portable: authored intent about the environment itself.
	"RuntimeVersion": {EnvFieldPortable, "the release this environment runs"},
	"RuntimeImage":   {EnvFieldPortable, "which runtime image, when it is not the stock default"},
	"RuntimeChart":   {EnvFieldPortable, "which runtime chart the environment rides"},
	"RuntimePod":     {EnvFieldPortable, "authored container sizing"},
	"RuntimeDindPod": {EnvFieldPortable, "authored sidecar sizing"},
	"NamespaceQuota": {EnvFieldPortable, "authored namespace ceiling"},
	"Idle":           {EnvFieldPortable, "idle policy — the authored intent, not the observed state"},
	"Claude":         {EnvFieldPortable, "how the environment's in-pod agent is configured"},
	"AITool":         {EnvFieldPortable, "which AI tool the environment uses"},
	"AutoUpgrade":    {EnvFieldPortable, "whether the environment rides upgrades"},
	"UpgradeChannel": {EnvFieldPortable, "which channel it rides"},
	// ContainerRegistries is portable, but note init's own saveEnvConfig
	// wrapper nulls it for anything that is not a remote worktree — a sync
	// reading a just-inited config therefore sees cleared registries and must
	// not read that absence as "the operator removed them".
	"ContainerRegistries": {EnvFieldPortable, "the marked registry list"},

	// Platform-owned: mirrors a column on the environment row, fixed at
	// registration.
	"Name":              {EnvFieldPlatformOwned, "the environment row's name, and the local config path derived from it"},
	"Type":              {EnvFieldPlatformOwned, "the environment row's type"},
	"KubernetesContext": {EnvFieldPlatformOwned, "the Kubernetes context the environment row was registered with"},

	// Host-owned: this machine, or a cluster-local reference.
	"LocalRepoPath":                {EnvFieldHostOwned, "where this machine keeps the checkout"},
	"LocalPortRangeStart":          {EnvFieldHostOwned, "this machine's local port allocation"},
	"SSHD":                         {EnvFieldHostOwned, "host keys, ports and paths for tunnelling into this machine"},
	"MCPAuthPublicKeyPath":         {EnvFieldHostOwned, "a path to a key on this machine"},
	"RuntimeRunningImage":          {EnvFieldHostOwned, "a deploy-derived display memo, not authored intent"},
	"ImagePullSecrets":             {EnvFieldHostOwned, "names a cluster-local Kubernetes Secret; its absence elsewhere is not an error"},
	"RegistryCredentialSecretName": {EnvFieldHostOwned, "names a cluster-local Kubernetes Secret"},
	"PlatformAliasSecretName":      {EnvFieldHostOwned, "names a cluster-local Kubernetes Secret"},
	"CloudProviderAlias":           {EnvFieldHostOwned, "binds to this machine's cloud credentials"},
	"CloudProviderAliases":         {EnvFieldHostOwned, "binds to this machine's cloud credentials"},
	"ManagedCloud":                 {EnvFieldHostOwned, "records that the platform manages this environment's lifecycle, which the hosted marker does not replace"},
	"RuntimeRegistry":              {EnvFieldHostOwned, "this machine's registry endpoint"},
	"RemoteHostCredentials":        {EnvFieldHostOwned, "host credential delivery"},
	"PlatformAccount":              {EnvFieldHostOwned, "grants the environment's ServiceAccount cluster-admin, a decision about this cluster"},
	"AutoStart":                    {EnvFieldHostOwned, "whether this machine starts the environment"},
	"Stopped":                      {EnvFieldHostOwned, "the durable half of a stop this machine performed"},
	"DisableBuildScript":           {EnvFieldHostOwned, "how this machine builds"},
	"Hosted":                       {EnvFieldHostOwned, "the local marker of which platform row this environment corresponds to"},
	// Default-deny: absent from the portable table, so never transferred.
	"MountSource": {EnvFieldHostOwned, "how the local worktree is populated"},
	"RepoURL":     {EnvFieldHostOwned, "the remote this machine's checkout came from"},
	"Deploy":      {EnvFieldHostOwned, "deploy.components is this machine's saved selection; deploy.timeout travels separately"},
}

// PortableEnvConfigFieldNames returns the names of every EnvConfig field a
// definition transfers, sorted so callers can render a stable list.
func PortableEnvConfigFieldNames() []string {
	names := make([]string, 0, len(portableEnvConfigFields))
	for name, entry := range portableEnvConfigFields {
		if entry.Class == EnvFieldPortable {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// HostOwnedEnvConfigFieldNames returns the names of every host-owned field,
// sorted. It backs the "which settings never leave the machine" list the docs
// publish and the error text a refusal renders.
func HostOwnedEnvConfigFieldNames() []string {
	names := make([]string, 0, len(portableEnvConfigFields))
	for name, entry := range portableEnvConfigFields {
		if entry.Class == EnvFieldHostOwned {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// PopulatedPortableEnvConfigFields returns the portable fields this config
// carries a value for, sorted. It is what a push reports as travelling, so an
// operator auditing an upload reads the same list the payload was built from
// rather than a second enumeration that could drift from it.
func PopulatedPortableEnvConfigFields(config EnvConfig) []string {
	value := reflect.ValueOf(config)
	names := make([]string, 0, len(portableEnvConfigFields))
	for _, name := range PortableEnvConfigFieldNames() {
		if field := value.FieldByName(name); field.IsValid() && !field.IsZero() {
			names = append(names, name)
		}
	}
	// Deploy travels as its timeout alone; its components list stays here.
	if strings.TrimSpace(config.Deploy.Timeout) != "" {
		names = append(names, "Deploy.Timeout")
	}
	sort.Strings(names)
	return names
}

// unclassifiedEnvConfigFields reports the EnvConfig top-level fields the
// classification table does not mention. It is the allowlist's own guard: a
// field added to EnvConfig lands here, and the test that calls it fails, until
// somebody writes down which class it belongs to.
func unclassifiedEnvConfigFields() []string {
	configType := reflect.TypeOf(EnvConfig{})
	var missing []string
	for i := 0; i < configType.NumField(); i++ {
		name := configType.Field(i).Name
		if _, ok := portableEnvConfigFields[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// PlatformEnvDefinition is the portable subset of an EnvConfig as it travels to
// and from the platform.
//
// Every field is a pointer so that "the platform has no opinion about this" is
// distinguishable from "the platform recorded an empty value". That distinction
// is what lets a pull leave a local setting alone instead of clearing it, and
// it is why this type does not embed or alias EnvConfig.
//
// The JSON keys are written out here rather than inherited from EnvConfig's own
// tags, deliberately. Eight EnvConfig fields carry no json tag at all (Name,
// KubernetesContext, CloudProviderAlias, RuntimeVersion, RuntimePod,
// RuntimeDindPod, SSHD, Idle), so a payload that inherited Go's marshalling
// defaults would emit `RuntimeVersion` beside every camelCase sibling. A wire
// contract whose key names depend on which struct field happens to have a tag
// today is not a contract.
type PlatformEnvDefinition struct {
	RuntimeVersion *string `json:"runtimeVersion,omitempty"`
	RuntimeImage   *string `json:"runtimeImage,omitempty"`
	RuntimeChart   *string `json:"runtimeChart,omitempty"`
	// RuntimePod and RuntimeDindPod are authored sizing. `deploy` may still
	// resolve them live; the definition carries the authored intent.
	RuntimePod     *RuntimePodResources     `json:"runtimePod,omitempty"`
	RuntimeDindPod *RuntimePodResources     `json:"runtimeDindPod,omitempty"`
	NamespaceQuota *NamespaceResourceQuota  `json:"namespaceQuota,omitempty"`
	Idle           *EnvironmentIdleConfig   `json:"idle,omitempty"`
	Claude         *EnvironmentClaudeConfig `json:"claude,omitempty"`
	AITool         *string                  `json:"aiTool,omitempty"`
	AutoUpgrade    *bool                    `json:"autoUpgrade,omitempty"`
	UpgradeChannel *string                  `json:"upgradeChannel,omitempty"`
	// ContainerRegistries is the marked registry list. See the portable table's
	// note on init's registry-nulling wrapper.
	ContainerRegistries *ContainerRegistries `json:"containerRegistries,omitempty"`
	// DeployTimeout is the portable half of EnvConfig.Deploy. Deploy.Components
	// stays on the machine that saved it.
	DeployTimeout *string `json:"deployTimeout,omitempty"`
}

// BuildPlatformEnvDefinition projects the portable subset out of an EnvConfig.
// It walks the same table the apply and the diff walk, so a field can reach the
// payload only by appearing there — there is no second list to keep in step.
func BuildPlatformEnvDefinition(config EnvConfig) PlatformEnvDefinition {
	definition := PlatformEnvDefinition{}
	for _, field := range platformDefinitionFields {
		field.Project(&definition, config)
	}
	return definition
}

// DefinitionDigest fingerprints a definition payload: the same portable
// settings always produce the same value, and any changed setting produces a
// different one.
//
// It is what lets a machine answer "have my settings moved since the platform
// last heard them?" without a platform read, which matters because that
// question is asked on paths that must not spend a network round-trip — the
// config watcher deciding whether a file change is worth an upload, and the
// marker panel on a machine that is offline. It is a fingerprint of the
// *portable subset*, so it covers exactly what a transfer would carry and
// nothing a host-owned field could leak into it.
//
// The encoding is canonical for this type: PlatformEnvDefinition is a closed
// struct of strings, bools and slices with explicit JSON tags, so field order
// and key spelling come from the declaration rather than from Go's
// marshalling defaults.
func DefinitionDigest(definition PlatformEnvDefinition) string {
	encoded, err := json.Marshal(definition)
	if err != nil {
		// Unreachable for this type. A caller about to write a marker must
		// still get a value rather than a panic, and an empty digest is never
		// equal to a recorded one — so the copy reads as diverged rather than
		// as in step with a transfer that was never fingerprinted.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func pointerIfSet[T any](value T, set bool) *T {
	if !set {
		return nil
	}
	return &value
}

func buildPlatformClaudeConfig(config EnvironmentClaudeConfig) *EnvironmentClaudeConfig {
	clone := config
	if len(config.Models) > 0 {
		clone.Models = append([]string(nil), config.Models...)
	}
	return &clone
}

func stringPointerOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func boolPointerOrNil(value bool) *bool {
	if !value {
		return nil
	}
	return &value
}

func containerRegistriesPointerOrNil(registries ContainerRegistries) *ContainerRegistries {
	if len(registries) == 0 {
		return nil
	}
	clone := make(ContainerRegistries, len(registries))
	copy(clone, registries)
	return &clone
}

// platformDefinitionField is one portable field, expressed once so the
// projection, the apply, and the conflict diff cannot disagree about which
// fields travel or how each one is spelled.
//
// Deploy is the reason this exists as a table rather than three parallel
// switch statements: its timeout travels and its components list does not, so
// the field that is split across classes needs one row that names the half
// which moves.
type platformDefinitionField struct {
	// Field is the EnvConfig field name — "Deploy.Timeout" for the split half.
	Field string
	// Label is the operator-facing name the conflict diff renders.
	Label string
	// Stored reports the value the definition carries and whether the
	// definition has an opinion about this field at all.
	Stored func(PlatformEnvDefinition) (string, bool)
	// Local renders the config's own value for the same field.
	Local func(EnvConfig) string
	// Apply writes the definition's value onto the config, and only when the
	// definition carries one.
	Apply func(*EnvConfig, PlatformEnvDefinition)
	// Project writes the config's value into a definition, so the payload is
	// built from the same row the apply and the diff read.
	Project func(*PlatformEnvDefinition, EnvConfig)
}

// platformDefinitionFields is the ordered list every projection, apply, and
// diff walks. A portable field reaches a payload only by appearing here.
var platformDefinitionFields = []platformDefinitionField{
	{
		Field: "RuntimeVersion", Label: "runtime version",
		Stored: stringField(func(d PlatformEnvDefinition) *string { return d.RuntimeVersion }),
		Local:  func(c EnvConfig) string { return c.RuntimeVersion },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setString(&c.RuntimeVersion, d.RuntimeVersion) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.RuntimeVersion = stringPointerOrNil(c.RuntimeVersion)
		},
	},
	{
		Field: "RuntimeImage", Label: "runtime image",
		Stored: stringField(func(d PlatformEnvDefinition) *string { return d.RuntimeImage }),
		Local:  func(c EnvConfig) string { return c.RuntimeImage },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setString(&c.RuntimeImage, d.RuntimeImage) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.RuntimeImage = stringPointerOrNil(c.RuntimeImage)
		},
	},
	{
		Field: "RuntimeChart", Label: "runtime chart",
		Stored: stringField(func(d PlatformEnvDefinition) *string { return d.RuntimeChart }),
		Local:  func(c EnvConfig) string { return c.RuntimeChart },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setString(&c.RuntimeChart, d.RuntimeChart) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.RuntimeChart = stringPointerOrNil(c.RuntimeChart)
		},
	},
	{
		Field: "RuntimePod", Label: "runtime pod sizing",
		Stored: renderField(func(d PlatformEnvDefinition) *RuntimePodResources { return d.RuntimePod }, renderRuntimePodResources),
		Local:  func(c EnvConfig) string { return renderRuntimePodResources(c.RuntimePod) },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setStruct(&c.RuntimePod, d.RuntimePod) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.RuntimePod = pointerIfSet(c.RuntimePod, c.RuntimePod != RuntimePodResources{})
		},
	},
	{
		Field: "RuntimeDindPod", Label: "runtime dind sizing",
		Stored: renderField(func(d PlatformEnvDefinition) *RuntimePodResources { return d.RuntimeDindPod }, renderRuntimePodResources),
		Local:  func(c EnvConfig) string { return renderRuntimePodResources(c.RuntimeDindPod) },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setStruct(&c.RuntimeDindPod, d.RuntimeDindPod) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.RuntimeDindPod = pointerIfSet(c.RuntimeDindPod, c.RuntimeDindPod != RuntimePodResources{})
		},
	},
	{
		Field: "NamespaceQuota", Label: "namespace quota",
		Stored: renderField(func(d PlatformEnvDefinition) *NamespaceResourceQuota { return d.NamespaceQuota }, renderNamespaceQuota),
		Local:  func(c EnvConfig) string { return renderNamespaceQuota(c.NamespaceQuota) },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setStruct(&c.NamespaceQuota, d.NamespaceQuota) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.NamespaceQuota = pointerIfSet(c.NamespaceQuota, !c.NamespaceQuota.IsZero())
		},
	},
	{
		Field: "Idle", Label: "idle policy",
		Stored: renderField(func(d PlatformEnvDefinition) *EnvironmentIdleConfig { return d.Idle }, renderIdleConfig),
		Local:  func(c EnvConfig) string { return renderIdleConfig(c.Idle) },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setStruct(&c.Idle, d.Idle) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.Idle = pointerIfSet(c.Idle, c.Idle != EnvironmentIdleConfig{})
		},
	},
	{
		Field: "Claude", Label: "agent configuration",
		Stored: renderField(func(d PlatformEnvDefinition) *EnvironmentClaudeConfig { return d.Claude }, renderClaudeConfig),
		Local:  func(c EnvConfig) string { return renderClaudeConfig(c.Claude) },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setStruct(&c.Claude, d.Claude) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.Claude = buildPlatformClaudeConfig(c.Claude)
		},
	},
	{
		Field: "AITool", Label: "AI tool",
		Stored: stringField(func(d PlatformEnvDefinition) *string { return d.AITool }),
		Local:  func(c EnvConfig) string { return c.AITool },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setString(&c.AITool, d.AITool) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.AITool = stringPointerOrNil(c.AITool)
		},
	},
	{
		Field: "AutoUpgrade", Label: "auto upgrade",
		Stored: boolField(func(d PlatformEnvDefinition) *bool { return d.AutoUpgrade }),
		Local:  func(c EnvConfig) string { return renderBool(c.AutoUpgrade) },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setBool(&c.AutoUpgrade, d.AutoUpgrade) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.AutoUpgrade = boolPointerOrNil(c.AutoUpgrade)
		},
	},
	{
		Field: "UpgradeChannel", Label: "upgrade channel",
		Stored: stringField(func(d PlatformEnvDefinition) *string { return d.UpgradeChannel }),
		Local:  func(c EnvConfig) string { return c.UpgradeChannel },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setString(&c.UpgradeChannel, d.UpgradeChannel) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.UpgradeChannel = stringPointerOrNil(c.UpgradeChannel)
		},
	},
	{
		Field: "ContainerRegistries", Label: "container registries",
		Stored: renderField(func(d PlatformEnvDefinition) *ContainerRegistries { return d.ContainerRegistries }, renderContainerRegistries),
		Local:  func(c EnvConfig) string { return renderContainerRegistries(c.ContainerRegistries) },
		Apply: func(c *EnvConfig, d PlatformEnvDefinition) {
			setContainerRegistries(&c.ContainerRegistries, d.ContainerRegistries)
		},
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.ContainerRegistries = containerRegistriesPointerOrNil(c.ContainerRegistries)
		},
	},
	{
		Field: "Deploy.Timeout", Label: "deploy timeout",
		Stored: stringField(func(d PlatformEnvDefinition) *string { return d.DeployTimeout }),
		Local:  func(c EnvConfig) string { return c.Deploy.Timeout },
		Apply:  func(c *EnvConfig, d PlatformEnvDefinition) { setString(&c.Deploy.Timeout, d.DeployTimeout) },
		Project: func(d *PlatformEnvDefinition, c EnvConfig) {
			d.DeployTimeout = stringPointerOrNil(c.Deploy.Timeout)
		},
	},
}

func stringField(pick func(PlatformEnvDefinition) *string) func(PlatformEnvDefinition) (string, bool) {
	return func(definition PlatformEnvDefinition) (string, bool) {
		value := pick(definition)
		if value == nil {
			return "", false
		}
		return *value, true
	}
}

func boolField(pick func(PlatformEnvDefinition) *bool) func(PlatformEnvDefinition) (string, bool) {
	return func(definition PlatformEnvDefinition) (string, bool) {
		value := pick(definition)
		if value == nil {
			return "", false
		}
		return renderBool(*value), true
	}
}

func renderField[T any](pick func(PlatformEnvDefinition) *T, render func(T) string) func(PlatformEnvDefinition) (string, bool) {
	return func(definition PlatformEnvDefinition) (string, bool) {
		value := pick(definition)
		if value == nil {
			return "", false
		}
		return render(*value), true
	}
}

func setString(target *string, value *string) {
	if value != nil {
		*target = *value
	}
}

func setBool(target *bool, value *bool) {
	if value != nil {
		*target = *value
	}
}

func setStruct[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

// setContainerRegistries copies the value rather than aliasing the caller's
// slice: a caller that later mutates the definition it passed must not be able
// to reach through this config and change it.
func setContainerRegistries(target *ContainerRegistries, value *ContainerRegistries) {
	if value == nil {
		return
	}
	registries := make(ContainerRegistries, len(*value))
	copy(registries, *value)
	*target = registries
}

// ApplyPlatformEnvDefinition returns base with every portable field the
// definition carries replaced by the definition's value, and every field the
// definition is silent about — plus every host-owned and platform-owned field —
// left exactly as base had it.
func ApplyPlatformEnvDefinition(base EnvConfig, definition PlatformEnvDefinition) (EnvConfig, error) {
	applied := base
	for _, field := range platformDefinitionFields {
		field.Apply(&applied, definition)
	}
	return applied, nil
}

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

// EnvDefinitionRefusal is a field whose two records disagree and cannot be
// reconciled by asking: a platform-owned field differing locally, or a
// host-owned field the platform has no business carrying at all.
type EnvDefinitionRefusal struct {
	Field  string              `json:"field"`
	Class  EnvConfigFieldClass `json:"class"`
	Reason string              `json:"reason"`
}

func (r EnvDefinitionRefusal) Error() string {
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
	Refusals []EnvDefinitionRefusal `json:"refusals,omitempty"`
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
	plan.Refusals = append(plan.Refusals, platformOwnedMismatches(params)...)

	plan.Changes = portableDefinitionChanges(params.Local, params.Definition)
	if params.LocalRevision != params.PlatformRevision && params.LocalRevision > 0 {
		plan.Conflicts = append(plan.Conflicts, plan.Changes...)
	}
	return plan
}

// platformOwnedMismatches reports the platform-owned fields whose local value
// disagrees with the environment row.
func platformOwnedMismatches(params EnvDefinitionPlanParams) []EnvDefinitionRefusal {
	var refusals []EnvDefinitionRefusal
	name := strings.TrimSpace(params.PlatformName)
	if local := strings.TrimSpace(params.Local.Name); name != "" && local != "" && local != name {
		refusals = append(refusals, EnvDefinitionRefusal{
			Field:  "Name",
			Class:  EnvFieldPlatformOwned,
			Reason: fmt.Sprintf("the platform knows this environment as %q, and the local config path is derived from the name", name),
		})
	}
	if platformType := strings.TrimSpace(params.PlatformType); platformType != "" {
		if local := strings.TrimSpace(string(params.Local.Type)); local != "" && local != platformType {
			refusals = append(refusals, EnvDefinitionRefusal{
				Field:  "Type",
				Class:  EnvFieldPlatformOwned,
				Reason: fmt.Sprintf("the platform registered it as %q", platformType),
			})
		}
	}
	if platformContext := strings.TrimSpace(params.PlatformKubernetesContext); platformContext != "" {
		if local := strings.TrimSpace(params.Local.KubernetesContext); local != "" && local != platformContext {
			refusals = append(refusals, EnvDefinitionRefusal{
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
