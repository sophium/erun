package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

func TestEnvironmentWithNoMarkerOmitsIt(t *testing.T) {
	configHome := useConfigHome(t)
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev", Type: configpkg.EnvironmentTypeRuntime}); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(configHome, "erun", "team", "dev", "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), "hosted:") {
		t.Fatalf("an unhosted environment wrote a hosted block:\n%s", raw)
	}
}

func TestPlanPullLocalPortRangeStartRefusesAClaimedRange(t *testing.T) {
	useConfigHome(t)
	seedTenantConfigForPortPlanning(t)
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "taken", Type: configpkg.EnvironmentTypeRuntime, LocalPortRangeStart: configpkg.LowerServicePort}); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := PlanPullLocalPortRangeStart(configpkg.ConfigStore{}, "team", "fresh", configpkg.LowerServicePort)
	var overlap configpkg.LocalPortRangeOverlapError
	if !errors.As(err, &overlap) {
		t.Fatalf("a claimed range start was accepted: err = %v", err)
	}
	if overlap.A != "team/taken" || overlap.B != "team/fresh" {
		t.Fatalf("overlap names %q and %q, want the two environments", overlap.A, overlap.B)
	}
}

func TestPlanPullLocalPortRangeStartAllocatesTheLowestFreeRange(t *testing.T) {
	useConfigHome(t)
	seedTenantConfigForPortPlanning(t)
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "taken", Type: configpkg.EnvironmentTypeRuntime, LocalPortRangeStart: configpkg.LowerServicePort}); err != nil {
		t.Fatalf("save: %v", err)
	}
	start, err := PlanPullLocalPortRangeStart(configpkg.ConfigStore{}, "team", "fresh", 0)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if start != configpkg.LowerServicePort+configpkg.EnvironmentPortRangeSize {
		t.Fatalf("allocated %d, want the range after the claimed one", start)
	}
}

func TestPlanPullLocalPortRangeStartIgnoresTheEnvironmentItself(t *testing.T) {
	useConfigHome(t)
	seedTenantConfigForPortPlanning(t)
	// A re-pull must not collide with the environment's own range.
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev", Type: configpkg.EnvironmentTypeRuntime, LocalPortRangeStart: configpkg.LowerServicePort}); err != nil {
		t.Fatalf("save: %v", err)
	}
	start, err := PlanPullLocalPortRangeStart(configpkg.ConfigStore{}, "team", "dev", configpkg.LowerServicePort)
	if err != nil {
		t.Fatalf("an environment's own range start was treated as a collision: %v", err)
	}
	if start != configpkg.LowerServicePort {
		t.Fatalf("allocated %d, want the environment's own range", start)
	}
}

func TestNewPullAsNewEnvironmentRefusesAHostType(t *testing.T) {
	_, err := NewPullAsNewEnvironment(model.PlatformEnvironment{Name: "dev", Type: string(configpkg.EnvironmentTypeHost)}, "", 0)
	if err == nil {
		t.Fatal("a host row was accepted as hostable")
	}
	if !strings.Contains(err.Error(), "no pod and no cluster") {
		t.Fatalf("refusal does not say why a host env cannot be hosted: %v", err)
	}
}

func TestNewPullAsNewEnvironmentRequiresAUsableRepoPath(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewPullAsNewEnvironment(model.PlatformEnvironment{Name: "dev", Type: string(configpkg.EnvironmentTypeLocalAgent)}, "", 0); err == nil {
		t.Fatal("a local-agent pull-as-new was accepted with no repo path")
	}
	if _, err := NewPullAsNewEnvironment(model.PlatformEnvironment{Name: "dev", Type: string(configpkg.EnvironmentTypeLocalAgent)}, dir, configpkg.LowerServicePort); err != nil {
		t.Fatalf("a real directory was refused: %v", err)
	}
	// A runtime env needs no host path.
	if _, err := NewPullAsNewEnvironment(model.PlatformEnvironment{Name: "dev", Type: string(configpkg.EnvironmentTypeRuntime)}, "", configpkg.LowerServicePort); err != nil {
		t.Fatalf("a runtime pull-as-new was refused for want of a host path: %v", err)
	}
}

func TestIsHostableEnvironmentType(t *testing.T) {
	for _, envType := range []configpkg.EnvironmentType{configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeRemoteAgent, configpkg.EnvironmentTypeRuntime} {
		if !IsHostableEnvironmentType(envType) {
			t.Errorf("%s should be hostable", envType)
		}
	}
	if IsHostableEnvironmentType(configpkg.EnvironmentTypeHost) {
		t.Error("host environments cannot be hosted and must not report as hostable")
	}
}

// Without a tenant config the planner skips the tenant and sees every range as free.
func seedTenantConfigForPortPlanning(t *testing.T) {
	t.Helper()
	if err := configpkg.SaveTenantConfig(model.TenantConfig{Name: "team"}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
}

func useConfigHome(t *testing.T) string {
	t.Helper()
	restore := setConfigHomeForModeTest(t)
	t.Cleanup(restore)
	return xdg.ConfigHome
}

func TestPullAsNewRefusesAPortCollisionBeforeWriting(t *testing.T) {
	store := &recordingDefinitionStore{
		envs: []configpkg.EnvConfig{
			{Name: "taken", Type: configpkg.EnvironmentTypeRuntime, LocalPortRangeStart: configpkg.LowerServicePort},
		},
	}
	client := &stubDefinitionClient{row: model.PlatformEnvironment{
		EnvironmentID: "env-1", TenantID: "tenant-1", Name: "fresh", Type: string(configpkg.EnvironmentTypeRuntime),
	}}
	_, err := PullEnvironmentDefinition(context.Background(), store, client, EnvDefinitionPullParams{
		Tenant: "team", Environment: "fresh", EnvironmentID: "env-1",
		// An unspecified start allocates the lowest free range and never collides.
		LocalPortRangeStart: configpkg.LowerServicePort,
	})
	var overlap configpkg.LocalPortRangeOverlapError
	if !errors.As(err, &overlap) {
		t.Fatalf("a pull-as-new onto a claimed range start was not refused: err = %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("a refused pull wrote %d config(s) anyway", len(store.saved))
	}
	if client.definitionReads != 0 {
		t.Error("the pull read the platform's definition before settling the local port range")
	}
}

func TestPullAsNewCreatesTheEnvironmentFromThePlatformRow(t *testing.T) {
	repoPath := t.TempDir()
	store := &recordingDefinitionStore{}
	version := "1.2.3"
	hostile := "localrepopath-placeholder"
	client := &stubDefinitionClient{
		row: model.PlatformEnvironment{
			EnvironmentID: "env-1", TenantID: "tenant-1", Name: "fresh",
			Type: string(configpkg.EnvironmentTypeLocalAgent), KubernetesContext: "test-context",
		},
		definition: configpkg.PlatformEnvDefinition{RuntimeVersion: &version},
		// The payload also carries a host-owned value, which must not be written.
		rawDefinition: []byte(`{"runtimeVersion":"1.2.3","localRepoPath":"` + hostile + `"}`),
	}

	result, err := PullEnvironmentDefinition(context.Background(), store, client, EnvDefinitionPullParams{
		Tenant: "team", Environment: "fresh", EnvironmentID: "env-1", LocalRepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("pull-as-new: %v", err)
	}
	if !result.Created {
		t.Error("a pull into a non-existent environment did not report creating it")
	}
	if result.Config.RuntimeVersion != "1.2.3" {
		t.Errorf("portable runtime version = %q, want the platform's", result.Config.RuntimeVersion)
	}
	if result.Config.LocalRepoPath != repoPath {
		t.Errorf("repo path = %q, want this machine's %q", result.Config.LocalRepoPath, repoPath)
	}
	if result.Config.LocalPortRangeStart != configpkg.LowerServicePort {
		t.Errorf("port range start = %d, want the lowest free range", result.Config.LocalPortRangeStart)
	}
	if result.Marker.DefinitionRevision != 3 {
		t.Errorf("marker revision = %d, want the revision that was pulled", result.Marker.DefinitionRevision)
	}
}

func hostedDefinitionFixture(name string) configpkg.EnvConfig {
	return configpkg.EnvConfig{
		Name:              name,
		Type:              configpkg.EnvironmentTypeRuntime,
		KubernetesContext: "test-context",
		RuntimeVersion:    "1.2.3",
		Hosted: configpkg.HostedEnvironment{
			APIHost:            "https://api.example.test",
			TenantID:           "tenant-1",
			EnvironmentID:      "env-1",
			DefinitionRevision: 3,
		},
	}
}

func hostedDefinitionClient(config configpkg.EnvConfig) *stubDefinitionClient {
	return &stubDefinitionClient{row: model.PlatformEnvironment{
		EnvironmentID:     config.Hosted.EnvironmentID,
		TenantID:          config.Hosted.TenantID,
		Name:              config.Name,
		Type:              string(config.Type),
		KubernetesContext: config.KubernetesContext,
	}}
}

func pushOnce(t *testing.T, config configpkg.EnvConfig) configpkg.EnvConfig {
	t.Helper()
	store := &recordingDefinitionStore{envs: []configpkg.EnvConfig{config}}
	if _, err := PushEnvironmentDefinition(context.Background(), store, hostedDefinitionClient(config), model.EnvDefinitionPushParams{
		Tenant: "team", Environment: config.Name,
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("push saved %d configs, want 1", len(store.saved))
	}
	return store.saved[0]
}

func TestPushStampsTheDigestOfWhatItSent(t *testing.T) {
	stamped := pushOnce(t, hostedDefinitionFixture("dev"))

	want := configpkg.DefinitionDigest(configpkg.BuildPlatformEnvDefinition(hostedDefinitionFixture("dev")))
	if stamped.Hosted.DefinitionDigest != want {
		t.Fatalf("stamped digest = %q, want %q", stamped.Hosted.DefinitionDigest, want)
	}
	if change := configpkg.HostedDefinitionLocalChangeFor(stamped); !change.Available || change.Changed {
		t.Fatalf("a just-pushed config reads as %+v, want available and unchanged", change)
	}
}

func TestALocalEditAfterATransferReadsAsChanged(t *testing.T) {
	stamped := pushOnce(t, hostedDefinitionFixture("dev"))
	stamped.RuntimeVersion = "2.0.0"

	change := configpkg.HostedDefinitionLocalChangeFor(stamped)
	if !change.Available || !change.Changed {
		t.Fatalf("an edited portable setting reads as %+v, want available and changed", change)
	}
	if !change.HasUnsentChange() {
		t.Error("an edited portable setting did not ask for an upload")
	}
}

// TestAHostOwnedEditIsNotADivergence: the digest covers the portable subset only.
func TestAHostOwnedEditIsNotADivergence(t *testing.T) {
	stamped := pushOnce(t, hostedDefinitionFixture("dev"))
	stamped.LocalRepoPath = "/somewhere/else"
	stamped.RuntimeRunningImage = "registry.example.test/erun-devops:9.9.9"

	change := configpkg.HostedDefinitionLocalChangeFor(stamped)
	if !change.Available || change.Changed {
		t.Fatalf("a host-owned edit reads as %+v, want available and unchanged", change)
	}
	if change.HasUnsentChange() {
		t.Error("a host-owned edit asked for an upload of settings that never travel")
	}
}

// TestAMarkerWithNoDigestCannotTell: a marker without a digest reads as unknown, neither match nor
// change.
func TestAMarkerWithNoDigestCannotTell(t *testing.T) {
	legacy := hostedDefinitionFixture("dev")

	change := configpkg.HostedDefinitionLocalChangeFor(legacy)
	if change.Available || change.Changed {
		t.Fatalf("a marker with no digest reads as %+v, want neither available nor changed", change)
	}
	if !change.HasUnsentChange() {
		t.Error("a marker with no digest never asks for the upload that would start tracking it")
	}
	if !strings.Contains(change.Describe(), "no record") {
		t.Errorf("Describe does not say the answer is unknown: %q", change.Describe())
	}
}

// TestPullStampsTheDigestToo: the digest describes the merged result, not the platform's payload.
func TestPullStampsTheDigestToo(t *testing.T) {
	repoPath := t.TempDir()
	version := "1.2.3"
	story := &recordingDefinitionStore{envs: []configpkg.EnvConfig{{
		Name: "fresh", Type: configpkg.EnvironmentTypeLocalAgent, KubernetesContext: "test-context",
		LocalRepoPath: repoPath,
		Hosted: configpkg.HostedEnvironment{
			APIHost: "https://api.example.test", TenantID: "tenant-1", EnvironmentID: "env-1",
		},
	}}}
	client := &stubDefinitionClient{
		row:        model.PlatformEnvironment{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "fresh", Type: string(configpkg.EnvironmentTypeLocalAgent), KubernetesContext: "test-context"},
		definition: configpkg.PlatformEnvDefinition{RuntimeVersion: &version},
	}

	result, err := PullEnvironmentDefinition(context.Background(), story, client, EnvDefinitionPullParams{
		Tenant: "team", Environment: "fresh", LocalRepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if change := configpkg.HostedDefinitionLocalChangeFor(result.Config); !change.Available || change.Changed {
		t.Fatalf("a just-pulled config reads as %+v, want available and unchanged", change)
	}
}

func TestDefinitionDigestIsStableAndSensitive(t *testing.T) {
	base := configpkg.BuildPlatformEnvDefinition(hostedDefinitionFixture("dev"))
	if configpkg.DefinitionDigest(base) != configpkg.DefinitionDigest(configpkg.BuildPlatformEnvDefinition(hostedDefinitionFixture("dev"))) {
		t.Fatal("two builds of the same settings produced different digests")
	}
	if configpkg.DefinitionDigest(base) == "" {
		t.Fatal("the digest is empty, which no marker could ever match")
	}

	changed := hostedDefinitionFixture("dev")
	changed.RuntimeVersion = "2.0.0"
	if configpkg.DefinitionDigest(base) == configpkg.DefinitionDigest(configpkg.BuildPlatformEnvDefinition(changed)) {
		t.Fatal("a changed portable setting produced the same digest")
	}
}

// recordingDefinitionStore records writes, so a test can assert a refusal wrote nothing.
type recordingDefinitionStore struct {
	envs  []configpkg.EnvConfig
	saved []configpkg.EnvConfig
}

func (s *recordingDefinitionStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	for _, env := range s.envs {
		if env.Name == environment {
			return env, "/dev/null", nil
		}
	}
	for _, env := range s.saved {
		if env.Name == environment {
			return env, "/dev/null", nil
		}
	}
	return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
}

func (s *recordingDefinitionStore) SaveEnvConfig(_ string, config configpkg.EnvConfig) error {
	s.saved = append(s.saved, config)
	return nil
}

func (s *recordingDefinitionStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	return []model.TenantConfig{{Name: "team"}}, nil
}

func (s *recordingDefinitionStore) ListEnvConfigs(string) ([]configpkg.EnvConfig, error) {
	return append(append([]configpkg.EnvConfig(nil), s.envs...), s.saved...), nil
}

type stubDefinitionClient struct {
	row             model.PlatformEnvironment
	definition      configpkg.PlatformEnvDefinition
	rawDefinition   []byte
	definitionReads int
}

func (c *stubDefinitionClient) APIHost() string { return "https://api.example.test" }

func (c *stubDefinitionClient) GetEnvironment(context.Context, string) (model.PlatformEnvironment, error) {
	return c.row, nil
}

func (c *stubDefinitionClient) GetEnvironmentDefinition(context.Context, string) (platformclient.PlatformEnvDefinitionRecord, error) {
	c.definitionReads++
	definition := c.definition
	if len(c.rawDefinition) > 0 {
		if err := json.Unmarshal(c.rawDefinition, &definition); err != nil {
			return platformclient.PlatformEnvDefinitionRecord{}, err
		}
	}
	return platformclient.PlatformEnvDefinitionRecord{EnvironmentID: c.row.EnvironmentID, TenantID: c.row.TenantID, Revision: 3, Definition: definition}, nil
}

func (c *stubDefinitionClient) PutEnvironmentDefinition(context.Context, string, configpkg.PlatformEnvDefinition) (platformclient.PlatformEnvDefinitionRecord, error) {
	return platformclient.PlatformEnvDefinitionRecord{}, nil
}
