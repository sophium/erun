package eruncommon

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// sentinelEnvConfig returns an EnvConfig with every field populated with a
// value distinctive enough to grep for, so a test can assert that a value did
// NOT travel somewhere by looking for its marker string.
//
// The guard below is the point of building it by hand: a field added to
// EnvConfig with no sentinel here leaves a zero value, and the sweep then has a
// hole it cannot see. Failing on the zero value makes the omission loud.
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
		RuntimePod:                   RuntimePodResources{CPU: "111m", Memory: "111Mi"},
		RuntimeDindPod:               RuntimePodResources{CPU: "222m", Memory: "222Mi"},
		NamespaceQuota:               NamespaceResourceQuota{CPU: "333", Memory: "333Gi", Storage: "333Gi"},
		SSHD: SSHDConfig{
			Enabled:       true,
			LocalPort:     1234,
			PublicKeyPath: "/sentinel/host-owned/sshd-public-key",
			WorkspaceSync: SSHDWorkspaceSyncConfig{
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

// TestSentinelEnvConfigPopulatesEveryField is the guard described on
// sentinelEnvConfig: a new EnvConfig field that carries no sentinel here leaves
// the leak sweep below blind to it, so its absence is a test failure rather
// than a silent hole.
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

// TestEveryEnvConfigFieldIsClassified is the allowlist's own guard: an
// unclassified field means nobody has decided whether it travels, and the
// classification is default-deny precisely so that decision cannot be skipped
// by accident.
func TestEveryEnvConfigFieldIsClassified(t *testing.T) {
	if missing := unclassifiedEnvConfigFields(); len(missing) > 0 {
		t.Fatalf("EnvConfig fields with no class in portableEnvConfigFields: %s", strings.Join(missing, ", "))
	}
}

// TestPortableAndHostOwnedFieldListsAreDisjointAndComplete checks that the two
// published lists partition EnvConfig, so a reader of either one is not misled
// about a field the other silently owns.
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
		if entry.Class == EnvFieldPlatformOwned {
			names = append(names, name)
		}
	}
	return names
}

// TestUploadPayloadCarriesNoHostOwnedValue is the upload half of the
// default-deny proof. Every EnvConfig field holds a distinctive sentinel; the
// payload must not carry a single host-owned or platform-owned one.
//
// It also plants a sentinel for the root config's own plaintext admin token.
// That value cannot reach a definition payload for a structural reason — it
// lives on a different config type entirely, and this payload is built from
// EnvConfig alone — but "structurally impossible" is what a future refactor
// that starts threading a root config through would break, and the sweep is
// cheap enough to say so out loud.
func TestUploadPayloadCarriesNoHostOwnedValue(t *testing.T) {
	const rootAdminTokenSentinel = "sentinel-root-config-admin-token"
	config := sentinelEnvConfig()

	payload, err := json.Marshal(BuildPlatformEnvDefinition(config))
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	serialized := string(payload)

	// A field whose sentinel is not a string (a bare bool, say) has nothing to
	// grep for; sentinelForField reports that as empty and the sweep skips it.
	// Every field that *can* carry a distinctive string is checked.
	for _, field := range HostOwnedEnvConfigFieldNames() {
		assertSentinelValueAbsent(t, serialized, sentinelForField(t, config, field), field)
	}
	for _, field := range platformOwnedFieldNames() {
		assertSentinelValueAbsent(t, serialized, sentinelForField(t, config, field), field)
	}
	assertSentinelValueAbsent(t, serialized, rootAdminTokenSentinel, "root config admin token")

	// The three Secret-name fields are checked by name, because they are the ones
	// most easily mistaken for portable: each
	// names a Kubernetes Secret, and a name is a cluster-local reference whose
	// absence on another cluster is not an error.
	for _, secretField := range []string{"ImagePullSecrets", "RegistryCredentialSecretName", "PlatformAliasSecretName"} {
		if strings.Contains(serialized, sentinelForField(t, config, secretField)) {
			t.Errorf("definition payload carries %s, which names a Kubernetes Secret", secretField)
		}
	}
}

// TestPlatformDefinitionTableCoversEveryPortableField is the table's own
// guard: every EnvConfig field classified portable must have a row, or the
// projection, the apply, and the diff would all silently skip it while the
// classification table claimed it travels.
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
		// Deploy is the one field split across classes: its timeout travels as
		// its own row, and its components list is host-owned.
		if name == "Deploy" && covered["Deploy.Timeout"] {
			continue
		}
		t.Errorf("EnvConfig.%s is classified portable but has no row in platformDefinitionFields", name)
	}
}

// TestPlatformDefinitionRoundTripsThroughItsOwnProjection proves the payload
// is stable: projecting a config and applying the result back changes nothing
// about the projection, which is what makes a push followed by a pull a no-op
// for the fields that travelled rather than a rewrite.
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

// TestUploadPayloadDoesCarryPortableValues is the other direction of the same
// sweep: a payload that excluded everything would pass the leak test and still
// be useless. Every portable field's sentinel must appear.
func TestUploadPayloadDoesCarryPortableValues(t *testing.T) {
	config := sentinelEnvConfig()
	payload, err := json.Marshal(BuildPlatformEnvDefinition(config))
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	serialized := string(payload)

	// Fields whose portable projection is the whole value, or a value rendered
	// into a nested object, are checked by their sentinel appearing somewhere.
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

// sentinelForField extracts the distinctive string a given EnvConfig field
// holds in sentinelEnvConfig, so the sweep can look for it without a
// hand-maintained table that would drift from the struct.
func sentinelForField(t *testing.T, config EnvConfig, field string) string {
	t.Helper()
	// Deploy is the one field split across classes: its components list is
	// host-owned and its timeout travels, so the class-based sweep has to look
	// for the half that must not move. The travelling half is asserted
	// separately, in TestUploadPayloadDoesCarryPortableValues.
	if field == "Deploy" {
		return config.Deploy.Components[0]
	}
	return firstStringLeaf(reflect.ValueOf(config).FieldByName(field))
}

// firstStringLeaf returns the first distinctive string inside a value of any
// shape sentinelEnvConfig uses: a string itself, a struct's first string field
// (recursively), or the first string element of a slice, array or map. A kind
// with no string inside — a bool, a bare number, a nil pointer — reports empty,
// which is how a caller knows there is nothing to grep for.
func firstStringLeaf(value reflect.Value) string {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		return value.String()
	case reflect.Struct:
		return firstStringLeafInStruct(value)
	case reflect.Slice, reflect.Array:
		return firstStringLeafInSequence(value)
	case reflect.Map:
		return firstStringLeafInMap(value)
	default:
		return ""
	}
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

// TestApplyIgnoresNonPortableKeysOnTheWire covers the case the issue names
// directly: a platform row that carries a non-portable field must not have it
// written locally. The payload type has no such field, so the key is dropped at
// decode time — this asserts that, rather than assuming it.
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

	// Each case names a non-portable value the payload tried to carry and the
	// local value it must not have displaced. runtimePod closes the list with a
	// PORTABLE field, so the assertion is not simply "nothing is ever written".
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

func TestBuildEnvDefinitionPlanRefusesAPlatformNameMismatch(t *testing.T) {
	config := EnvConfig{Name: "local-name", Type: EnvironmentTypeRuntime}
	plan := BuildEnvDefinitionPlan(EnvDefinitionPlanParams{
		Local:            config,
		PlatformName:     "platform-name",
		PlatformType:     string(EnvironmentTypeRuntime),
		LocalRevision:    1,
		PlatformRevision: 2,
	})

	if len(plan.Refusals) == 0 {
		t.Fatal("a platform name that disagrees with the local name was not refused")
	}
	if plan.Refusals[0].Field != "Name" {
		t.Errorf("refusal names %q, want Name", plan.Refusals[0].Field)
	}
}

func TestBuildEnvDefinitionPlanRefusesAPlatformTypeMismatch(t *testing.T) {
	plan := BuildEnvDefinitionPlan(EnvDefinitionPlanParams{
		Local:         EnvConfig{Name: "same", Type: EnvironmentTypeRuntime},
		PlatformName:  "same",
		PlatformType:  string(EnvironmentTypeLocalAgent),
		LocalRevision: 1,
	})
	if len(plan.Refusals) != 1 || plan.Refusals[0].Field != "Type" {
		t.Fatalf("refusals = %+v, want exactly one Type refusal", plan.Refusals)
	}
}

// TestBuildEnvDefinitionPlanPromptsOnATwoSidedPortableEdit is the case the
// issue requires: when the platform moved ahead of the revision the local copy
// was synced from AND the portable value also differs locally, the pull must
// ask rather than decide.
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

// TestBuildEnvDefinitionPlanDoesNotPromptOnACatchUp is the other half: a local
// copy that has not moved past its synced revision is a plain catch-up, not a
// conflict, so a pull applies it without asking.
func TestBuildEnvDefinitionPlanDoesNotPromptOnACatchUp(t *testing.T) {
	platformVersion := "1.2.3"
	plan := BuildEnvDefinitionPlan(EnvDefinitionPlanParams{
		Local:            EnvConfig{Name: "same", Type: EnvironmentTypeRuntime, RuntimeVersion: "1.2.2"},
		Definition:       PlatformEnvDefinition{RuntimeVersion: &platformVersion},
		PlatformName:     "same",
		PlatformType:     string(EnvironmentTypeRuntime),
		LocalRevision:    2,
		PlatformRevision: 2,
	})
	if len(plan.Changes) != 1 {
		t.Fatalf("changes = %+v, want one", plan.Changes)
	}
	if len(plan.Conflicts) != 0 {
		t.Errorf("a one-sided catch-up raised a conflict prompt: %+v", plan.Conflicts)
	}
}

func TestCheckHostedMarkerTargetRefusesATenantMismatch(t *testing.T) {
	marker := HostedEnvironment{APIHost: "api.erunpaas.com", TenantID: "tenant-a", EnvironmentID: "env-1"}

	if err := CheckHostedMarkerTarget(marker, "api.erunpaas.com", "tenant-a", "env-1"); err != nil {
		t.Fatalf("a marker matching the resolved target was refused: %v", err)
	}
	err := CheckHostedMarkerTarget(marker, "api.erunpaas.com", "tenant-b", "env-1")
	if err == nil {
		t.Fatal("a tenant mismatch was adopted rather than refused")
	}
	var mismatch *HostedMarkerMismatchError
	if !errors.As(err, &mismatch) || mismatch.Field != "tenant" {
		t.Fatalf("tenant mismatch reported as %v", err)
	}
	if err := CheckHostedMarkerTarget(marker, "other.erunpaas.com", "tenant-a", "env-1"); err == nil {
		t.Fatal("an api host mismatch was adopted rather than refused")
	}
	if err := CheckHostedMarkerTarget(marker, "api.erunpaas.com", "tenant-a", "env-2"); err == nil {
		t.Fatal("an environment id mismatch was adopted rather than refused")
	}
	// An unresolved environment id means "not resolved yet", not a
	// disagreement: an upload checks the marker before the adopt call returns.
	if err := CheckHostedMarkerTarget(marker, "api.erunpaas.com", "tenant-a", ""); err != nil {
		t.Fatalf("an unresolved environment id was treated as a mismatch: %v", err)
	}
}

func TestHostedMarkerNormalizesTheAPIHost(t *testing.T) {
	trailing := HostedEnvironment{APIHost: "https://api.erunpaas.com/", TenantID: "t", EnvironmentID: "e"}
	if err := CheckHostedMarkerTarget(trailing, "https://api.erunpaas.com", "t", "e"); err != nil {
		t.Fatalf("the same host written two ways read as two platforms: %v", err)
	}
}
