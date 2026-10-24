package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// sentinelEnvConfig sets every field to a greppable marker; a zero field fails so
// a new field cannot leave the sweep blind.
func sentinelEnvConfig() EnvConfig {
	enabled := true
	return EnvConfig{
		Name:               "sentinel-environment-name",
		Type:               EnvironmentType("sentinel-environment-type"),
		LocalRepoPath:      "/sentinel/host-owned/localrepopath",
		MountSource:        true,
		RepoURL:            "https://sentinel.example/host-owned/repourl",
		KubernetesContext:  "sentinel-kubernetes-context",
		CloudProviderAlias: "sentinel-cloud-provider-alias",
		CloudProviderAliases: map[string]string{
			"aws": "sentinel-cloud-provider-aliases",
		},
		ManagedCloud: true,
		Hosted: HostedEnvironment{
			APIHost:            "sentinel-apihost.example",
			TenantID:           "sentinel-tenant-id",
			EnvironmentID:      "sentinel-environment-id",
			DefinitionRevision: 7,
		},
		RuntimeVersion:               "sentinel-runtime-version",
		RuntimeRegistry:              "sentinel-host-owned/runtimeregistry",
		ContainerRegistries:          ContainerRegistries{{Registry: "sentinel-container-registry"}},
		RuntimeImage:                 "sentinel-runtime-image",
		RuntimeRunningImage:          "sentinel-host-owned/runtimerunningimage",
		RuntimeChart:                 "sentinel-runtime-chart",
		MCPAuthPublicKeyPath:         "/sentinel/host-owned/mcpauthpublickeypath",
		ImagePullSecrets:             []string{"sentinel-image-pull-secret"},
		RegistryCredentialSecretName: "sentinel-registry-credential-secret-name",
		PlatformAliasSecretName:      "sentinel-platform-alias-secret-name",
		RuntimePod:                   model.RuntimePodResources{CPU: "111m", Memory: "111Mi"},
		RuntimeDindPod:               model.RuntimePodResources{CPU: "222m", Memory: "222Mi"},
		NamespaceQuota:               model.NamespaceResourceQuota{CPU: "333", Memory: "333Gi", Storage: "333Gi"},
		SSHD: SSHDConfig{
			Enabled:       true,
			LocalPort:     1234,
			PublicKeyPath: "/sentinel/host-owned/sshd-public-key",
			WorkspaceSync: model.SSHDWorkspaceSyncConfig{
				Enabled:   true,
				LocalPath: "/sentinel/host-owned/sshd-workspace-sync",
			},
		},
		Idle: EnvironmentIdleConfig{
			Timeout:          "sentinel-idle-timeout",
			WorkingHours:     "sentinel-idle-working-hours",
			Timezone:         "sentinel-idle-timezone",
			IdleTrafficBytes: 4444,
		},
		Deploy: EnvironmentDeployConfig{
			Timeout:    "sentinel-deploy-timeout",
			Components: []string{"sentinel-host-owned/deploy-components"},
		},
		Claude: EnvironmentClaudeConfig{
			UseMantle:  &enabled,
			UseBedrock: &enabled,
			UseGateway: &enabled,
			Models:     []string{"sentinel-claude-model"},
		},
		AITool:                "sentinel-ai-tool",
		LocalPortRangeStart:   5555,
		AutoStart:             &enabled,
		RemoteHostCredentials: true,
		AutoUpgrade:           true,
		UpgradeChannel:        "sentinel-upgrade-channel",
		DisableBuildScript:    true,
		PlatformAccount:       true,
		Stopped:               true,
	}
}

func TestSentinelEnvConfigPopulatesEveryField(t *testing.T) {
	config := sentinelEnvConfig()
	value := reflect.ValueOf(config)
	configType := value.Type()
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)
		if value.Field(i).IsZero() {
			t.Errorf("EnvConfig.%s has no sentinel value in sentinelEnvConfig; add one so the payload leak sweep covers it", field.Name)
		}
	}
}

func TestEveryEnvConfigFieldIsClassified(t *testing.T) {
	if missing := unclassifiedEnvConfigFields(); len(missing) > 0 {
		t.Fatalf("EnvConfig fields with no class in portableEnvConfigFields: %s", strings.Join(missing, ", "))
	}
}

func TestPortableAndHostOwnedFieldListsAreDisjointAndComplete(t *testing.T) {
	portable := map[string]bool{}
	for _, name := range PortableEnvConfigFieldNames() {
		portable[name] = true
	}
	hostOwned := 0
	for _, name := range HostOwnedEnvConfigFieldNames() {
		if portable[name] {
			t.Errorf("%s is listed as both portable and host-owned", name)
		}
		hostOwned++
	}
	configType := reflect.TypeOf(EnvConfig{})
	if total := len(portable) + hostOwned + len(platformOwnedFieldNames()); total != configType.NumField() {
		t.Fatalf("classification covers %d fields, EnvConfig has %d", total, configType.NumField())
	}
}

func platformOwnedFieldNames() []string {
	var names []string
	for name, entry := range portableEnvConfigFields {
		if entry.Class == model.EnvFieldPlatformOwned {
			names = append(names, name)
		}
	}
	return names
}

// TestUploadPayloadCarriesNoHostOwnedValue also plants the root admin token in case a
// refactor threads the root config through.
func TestUploadPayloadCarriesNoHostOwnedValue(t *testing.T) {
	const rootAdminTokenSentinel = "sentinel-root-config-admin-token"
	config := sentinelEnvConfig()

	payload, err := json.Marshal(BuildPlatformEnvDefinition(config))
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	serialized := string(payload)

	// Non-string sentinels have nothing to grep and are skipped.
	for _, field := range HostOwnedEnvConfigFieldNames() {
		assertSentinelValueAbsent(t, serialized, sentinelForField(t, config, field), field)
	}
	for _, field := range platformOwnedFieldNames() {
		assertSentinelValueAbsent(t, serialized, sentinelForField(t, config, field), field)
	}
	assertSentinelValueAbsent(t, serialized, rootAdminTokenSentinel, "root config admin token")

	// Secret-name fields are cluster-local references most easily mistaken for portable.
	for _, secretField := range []string{"ImagePullSecrets", "RegistryCredentialSecretName", "PlatformAliasSecretName"} {
		if strings.Contains(serialized, sentinelForField(t, config, secretField)) {
			t.Errorf("definition payload carries %s, which names a Kubernetes Secret", secretField)
		}
	}
}

func TestPlatformDefinitionTableCoversEveryPortableField(t *testing.T) {
	covered := map[string]bool{}
	for _, field := range platformDefinitionFields {
		if covered[field.Field] {
			t.Errorf("platformDefinitionFields lists %s twice", field.Field)
		}
		covered[field.Field] = true
	}
	for _, name := range PortableEnvConfigFieldNames() {
		if covered[name] {
			continue
		}
		// Deploy is split: Deploy.Timeout travels, components do not.
		if name == "Deploy" && covered["Deploy.Timeout"] {
			continue
		}
		t.Errorf("EnvConfig.%s is classified portable but has no row in platformDefinitionFields", name)
	}
}

func TestPlatformDefinitionRoundTripsThroughItsOwnProjection(t *testing.T) {
	config := sentinelEnvConfig()
	applied, err := ApplyPlatformEnvDefinition(config, BuildPlatformEnvDefinition(config))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	first, err := json.Marshal(BuildPlatformEnvDefinition(config))
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	second, err := json.Marshal(BuildPlatformEnvDefinition(applied))
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("projection is not stable across a round trip:\n%s\n%s", first, second)
	}
}

func TestUploadPayloadDoesCarryPortableValues(t *testing.T) {
	config := sentinelEnvConfig()
	payload, err := json.Marshal(BuildPlatformEnvDefinition(config))
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	serialized := string(payload)

	for _, field := range []string{
		"RuntimeVersion", "RuntimeImage", "RuntimeChart", "AITool", "UpgradeChannel",
		"Claude", "ContainerRegistries", "RuntimePod", "RuntimeDindPod", "NamespaceQuota", "Idle",
	} {
		if sentinel := sentinelForField(t, config, field); !strings.Contains(serialized, sentinel) {
			t.Errorf("definition payload is missing portable field %s (sentinel %q)", field, sentinel)
		}
	}
	if !strings.Contains(serialized, "sentinel-deploy-timeout") {
		t.Error("definition payload is missing deploy.timeout, the portable half of Deploy")
	}
	if strings.Contains(serialized, "sentinel-host-owned/deploy-components") {
		t.Error("definition payload carries deploy.components, which is this machine's saved selection")
	}
}

func assertSentinelValueAbsent(t *testing.T, serialized, sentinel, field string) {
	t.Helper()
	if sentinel == "" {
		return
	}
	if strings.Contains(serialized, sentinel) {
		t.Errorf("definition payload carries %s (sentinel %q)", field, sentinel)
	}
}

// sentinelForField returns the field's marker string from sentinelEnvConfig.
func sentinelForField(t *testing.T, config EnvConfig, field string) string {
	t.Helper()
	// Deploy's components are host-owned; the timeout half is checked elsewhere.
	if field == "Deploy" {
		return config.Deploy.Components[0]
	}
	return firstStringLeaf(reflect.ValueOf(config).FieldByName(field))
}

// firstStringLeaf returns the first string inside value, or "" if none.
func firstStringLeaf(value reflect.Value) string {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	kind := value.Kind()
	if kind == reflect.String {
		return value.String()
	}
	if kind == reflect.Struct {
		return firstStringLeafInStruct(value)
	}
	if kind == reflect.Slice || kind == reflect.Array {
		return firstStringLeafInSequence(value)
	}
	if kind == reflect.Map {
		return firstStringLeafInMap(value)
	}
	return ""
}

func firstStringLeafInStruct(value reflect.Value) string {
	for i := 0; i < value.NumField(); i++ {
		if found := firstStringLeaf(value.Field(i)); found != "" {
			return found
		}
	}
	return ""
}

func firstStringLeafInSequence(value reflect.Value) string {
	for i := 0; i < value.Len(); i++ {
		if found := firstStringLeaf(value.Index(i)); found != "" {
			return found
		}
	}
	return ""
}

func firstStringLeafInMap(value reflect.Value) string {
	for _, key := range value.MapKeys() {
		if found := firstStringLeaf(value.MapIndex(key)); found != "" {
			return found
		}
	}
	return ""
}

func TestApplyWritesOnlyPortableFields(t *testing.T) {
	source := sentinelEnvConfig()
	base := sentinelEnvConfig()

	applied, err := ApplyPlatformEnvDefinition(base, BuildPlatformEnvDefinition(source))
	if err != nil {
		t.Fatalf("apply definition: %v", err)
	}

	appliedValue := reflect.ValueOf(applied)
	for _, field := range HostOwnedEnvConfigFieldNames() {
		baseField := reflect.ValueOf(base).FieldByName(field)
		if !reflect.DeepEqual(appliedValue.FieldByName(field).Interface(), baseField.Interface()) {
			t.Errorf("applying a definition changed host-owned field %s", field)
		}
	}
	for _, field := range platformOwnedFieldNames() {
		baseField := reflect.ValueOf(base).FieldByName(field)
		if !reflect.DeepEqual(appliedValue.FieldByName(field).Interface(), baseField.Interface()) {
			t.Errorf("applying a definition changed platform-owned field %s", field)
		}
	}
	if applied.Deploy.Components[0] != base.Deploy.Components[0] {
		t.Error("applying a definition changed deploy.components, which is host-owned")
	}
	if applied.Deploy.Timeout != source.Deploy.Timeout {
		t.Error("applying a definition did not write deploy.timeout, which is portable")
	}
}

func TestApplyIgnoresNonPortableKeysOnTheWire(t *testing.T) {
	base := sentinelEnvConfig()
	hostile := []byte(`{
		"localRepoPath": "/attacker/localrepopath",
		"sshd": {"enabled": true, "localPort": 9999},
		"registryCredentialSecretName": "attacker-secret",
		"platformAliasSecretName": "attacker-secret",
		"imagePullSecrets": ["attacker-secret"],
		"cloudProviderAliases": {"aws": "attacker"},
		"managedCloud": false,
		"stopped": false,
		"platformAccount": false,
		"hosted": {"apiHost": "attacker.example"},
		"localPortRangeStart": 9999,
		"runtimePod": {"cpu": "999m", "memory": "999Mi"}
	}`)

	var definition PlatformEnvDefinition
	if err := json.Unmarshal(hostile, &definition); err != nil {
		t.Fatalf("decode hostile definition: %v", err)
	}
	applied, err := ApplyPlatformEnvDefinition(base, definition)
	if err != nil {
		t.Fatalf("apply hostile definition: %v", err)
	}

	// runtimePod is portable, so the assertion is not "nothing is ever written".
	cases := []struct {
		field string
		got   any
		want  any
	}{
		{"LocalRepoPath", applied.LocalRepoPath, base.LocalRepoPath},
		{"SSHD.LocalPort", applied.SSHD.LocalPort, base.SSHD.LocalPort},
		{"RegistryCredentialSecretName", applied.RegistryCredentialSecretName, base.RegistryCredentialSecretName},
		{"PlatformAliasSecretName", applied.PlatformAliasSecretName, base.PlatformAliasSecretName},
		{"ImagePullSecrets", applied.ImagePullSecrets, base.ImagePullSecrets},
		{"CloudProviderAliases", applied.CloudProviderAliases, base.CloudProviderAliases},
		{"ManagedCloud", applied.ManagedCloud, base.ManagedCloud},
		{"Stopped", applied.Stopped, base.Stopped},
		{"PlatformAccount", applied.PlatformAccount, base.PlatformAccount},
		{"Hosted", applied.Hosted, base.Hosted},
		{"LocalPortRangeStart", applied.LocalPortRangeStart, base.LocalPortRangeStart},
		{"RuntimePod.CPU (portable, must be written)", applied.RuntimePod.CPU, "999m"},
	}
	for _, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("applying a hostile definition changed %s: got %v, want %v", tc.field, tc.got, tc.want)
		}
	}
}

func TestBuildEnvDefinitionPlanPromptsOnATwoSidedPortableEdit(t *testing.T) {
	platformVersion := "1.2.3"
	plan := BuildEnvDefinitionPlan(EnvDefinitionPlanParams{
		Local:            EnvConfig{Name: "same", Type: EnvironmentTypeRuntime, RuntimeVersion: "1.2.2"},
		Definition:       PlatformEnvDefinition{RuntimeVersion: &platformVersion},
		PlatformName:     "same",
		PlatformType:     string(EnvironmentTypeRuntime),
		LocalRevision:    1,
		PlatformRevision: 2,
	})

	if len(plan.Changes) != 1 {
		t.Fatalf("changes = %+v, want one", plan.Changes)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("a two-sided portable edit did not raise a prompt: %+v", plan)
	}
	if plan.Conflicts[0].Local != "1.2.2" || plan.Conflicts[0].Platform != "1.2.3" {
		t.Errorf("conflict diff = %+v, want both sides", plan.Conflicts[0])
	}
}

func TestBuildEnvDefinitionPlanSeesEveryClaudeField(t *testing.T) {
	low, high := "low", "high"
	opus, sonnet := "opus", "sonnet"
	small, large := 16000, 64000
	cases := []struct {
		name            string
		local, platform EnvironmentClaudeConfig
	}{
		{"effort", EnvironmentClaudeConfig{Effort: &low}, EnvironmentClaudeConfig{Effort: &high}},
		{"default model", EnvironmentClaudeConfig{DefaultModel: &opus}, EnvironmentClaudeConfig{DefaultModel: &sonnet}},
		{"max output tokens", EnvironmentClaudeConfig{MaxOutputTokens: &small}, EnvironmentClaudeConfig{MaxOutputTokens: &large}},
		{"verbose debug", EnvironmentClaudeConfig{}, EnvironmentClaudeConfig{VerboseDebug: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			platform := tc.platform
			plan := BuildEnvDefinitionPlan(EnvDefinitionPlanParams{
				Local:            EnvConfig{Name: "same", Type: EnvironmentTypeRuntime, Claude: tc.local},
				Definition:       PlatformEnvDefinition{Claude: &platform},
				PlatformName:     "same",
				PlatformType:     string(EnvironmentTypeRuntime),
				LocalRevision:    1,
				PlatformRevision: 1,
			})
			if len(plan.Changes) != 1 {
				t.Fatalf("a pull that changes the Claude %s reported changes %+v, want one", tc.name, plan.Changes)
			}
		})
	}
}
