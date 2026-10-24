package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// EnvConfigFieldClass classifies EnvConfig fields for definition transfer. The table
// is default-deny so a new field never travels until opted in; the root config.yaml
// (plaintext AdminToken) is permanently out of scope.
type EnvConfigFieldClass string

const (
	EnvFieldPortable EnvConfigFieldClass = "portable"
	// EnvFieldHostOwned fields describe this machine or a cluster-local object; never transferred.
	EnvFieldHostOwned EnvConfigFieldClass = "host-owned"
	// EnvFieldPlatformOwned fields mirror the environment row; a local difference refuses.
	EnvFieldPlatformOwned EnvConfigFieldClass = "platform-owned"
)

type envConfigField struct {
	Class EnvConfigFieldClass
	// Note is operator-facing: it is rendered in refusals and the docs.
	Note string
}

// portableEnvConfigFields classifies every top-level EnvConfig field by Go name.
var portableEnvConfigFields = map[string]envConfigField{
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
	// init nulls ContainerRegistries for non-remote worktrees, so a sync must not
	// read that absence as a removal.
	"ContainerRegistries": {EnvFieldPortable, "the marked registry list"},

	"Name":              {EnvFieldPlatformOwned, "the environment row's name, and the local config path derived from it"},
	"Type":              {EnvFieldPlatformOwned, "the environment row's type"},
	"KubernetesContext": {EnvFieldPlatformOwned, "the Kubernetes context the environment row was registered with"},

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
	"MountSource":                  {EnvFieldHostOwned, "how the local worktree is populated"},
	"RepoURL":                      {EnvFieldHostOwned, "the remote this machine's checkout came from"},
	"Deploy":                       {EnvFieldHostOwned, "deploy.components is this machine's saved selection; deploy.timeout travels separately"},
}

// PortableEnvConfigFieldNames returns the transferred field names, sorted.
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

// HostOwnedEnvConfigFieldNames returns the host-owned field names, sorted.
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

// PopulatedPortableEnvConfigFields returns the portable fields config sets, sorted.
func PopulatedPortableEnvConfigFields(config EnvConfig) []string {
	value := reflect.ValueOf(config)
	names := make([]string, 0, len(portableEnvConfigFields))
	for _, name := range PortableEnvConfigFieldNames() {
		if field := value.FieldByName(name); field.IsValid() && !field.IsZero() {
			names = append(names, name)
		}
	}
	// Only Deploy.Timeout travels.
	if strings.TrimSpace(config.Deploy.Timeout) != "" {
		names = append(names, "Deploy.Timeout")
	}
	sort.Strings(names)
	return names
}

// unclassifiedEnvConfigFields returns EnvConfig fields missing from the table.
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

// PlatformEnvDefinition is the portable EnvConfig subset on the wire. Pointers let
// a pull leave unset fields alone; JSON keys are explicit because several
// EnvConfig fields have no json tag.
type PlatformEnvDefinition struct {
	RuntimeVersion      *string                  `json:"runtimeVersion,omitempty"`
	RuntimeImage        *string                  `json:"runtimeImage,omitempty"`
	RuntimeChart        *string                  `json:"runtimeChart,omitempty"`
	RuntimePod          *RuntimePodResources     `json:"runtimePod,omitempty"`
	RuntimeDindPod      *RuntimePodResources     `json:"runtimeDindPod,omitempty"`
	NamespaceQuota      *NamespaceResourceQuota  `json:"namespaceQuota,omitempty"`
	Idle                *EnvironmentIdleConfig   `json:"idle,omitempty"`
	Claude              *EnvironmentClaudeConfig `json:"claude,omitempty"`
	AITool              *string                  `json:"aiTool,omitempty"`
	AutoUpgrade         *bool                    `json:"autoUpgrade,omitempty"`
	UpgradeChannel      *string                  `json:"upgradeChannel,omitempty"`
	ContainerRegistries *ContainerRegistries     `json:"containerRegistries,omitempty"`
	// DeployTimeout is the portable half of EnvConfig.Deploy.
	DeployTimeout *string `json:"deployTimeout,omitempty"`
}

// BuildPlatformEnvDefinition projects the portable subset out of an EnvConfig.
func BuildPlatformEnvDefinition(config EnvConfig) PlatformEnvDefinition {
	definition := PlatformEnvDefinition{}
	for _, field := range platformDefinitionFields {
		field.Project(&definition, config)
	}
	return definition
}

// DefinitionDigest fingerprints the portable subset so a machine can tell whether
// its settings changed without a platform round-trip.
func DefinitionDigest(definition PlatformEnvDefinition) string {
	encoded, err := json.Marshal(definition)
	if err != nil {
		// Unreachable; an empty digest never matches a recorded one.
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

// platformDefinitionField is one portable field, shared by projection, apply and
// diff so they cannot disagree.
type platformDefinitionField struct {
	// Field is the EnvConfig field name, "Deploy.Timeout" for the split half.
	Field string
	Label string
	// Stored reports the definition's value and whether it carries one.
	Stored  func(PlatformEnvDefinition) (string, bool)
	Local   func(EnvConfig) string
	Apply   func(*EnvConfig, PlatformEnvDefinition)
	Project func(*PlatformEnvDefinition, EnvConfig)
}

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

// setContainerRegistries copies so later mutation of the definition cannot reach the config.
func setContainerRegistries(target *ContainerRegistries, value *ContainerRegistries) {
	if value == nil {
		return
	}
	registries := make(ContainerRegistries, len(*value))
	copy(registries, *value)
	*target = registries
}

// ApplyPlatformEnvDefinition overwrites only the portable fields the definition carries.
func ApplyPlatformEnvDefinition(base EnvConfig, definition PlatformEnvDefinition) (EnvConfig, error) {
	applied := base
	for _, field := range platformDefinitionFields {
		field.Apply(&applied, definition)
	}
	return applied, nil
}
