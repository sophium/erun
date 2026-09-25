package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// TestHostedMarkerSurvivesAReload is the case the plan names directly: the
// marker is written into the environment's own config.yaml and read back
// unchanged, so a later push or pull knows which platform row this machine
// follows without being told again.
func TestHostedMarkerSurvivesAReload(t *testing.T) {
	useConfigHome(t)
	marker := HostedEnvironment{
		APIHost:            "https://api.example.test",
		TenantID:           "tenant-1",
		EnvironmentID:      "env-1",
		DefinitionRevision: 3,
	}
	if err := SaveEnvConfig("team", EnvConfig{Name: "dev", Type: EnvironmentTypeRuntime, Hosted: marker}); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, _, err := LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	recorded, ok := HostedEnvironmentFromConfig(reloaded)
	if !ok {
		t.Fatalf("the marker did not survive the reload: %+v", reloaded.Hosted)
	}
	if recorded != marker {
		t.Fatalf("marker round-tripped as %+v, want %+v", recorded, marker)
	}
}

// TestEnvironmentWithNoMarkerOmitsIt keeps "not hosted" and "hosted somewhere
// we cannot name" apart on disk: a config that was never hosted carries no
// hosted block at all, so a reader is never asked to interpret an empty one.
func TestEnvironmentWithNoMarkerOmitsIt(t *testing.T) {
	configHome := useConfigHome(t)
	if err := SaveEnvConfig("team", EnvConfig{Name: "dev", Type: EnvironmentTypeRuntime}); err != nil {
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

// TestPlanPullLocalPortRangeStartRefusesAClaimedRange is the collision case the
// plan requires, and it is checked before any write: PlanPullLocalPortRangeStart
// answers first, so a pull that would collide never reaches SaveEnvConfig.
func TestPlanPullLocalPortRangeStartRefusesAClaimedRange(t *testing.T) {
	useConfigHome(t)
	seedTenantConfigForPortPlanning(t)
	if err := SaveEnvConfig("team", EnvConfig{Name: "taken", Type: EnvironmentTypeRuntime, LocalPortRangeStart: LowerServicePort}); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := PlanPullLocalPortRangeStart(ConfigStore{}, "team", "fresh", LowerServicePort)
	var overlap ErrLocalPortRangeOverlap
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
	if err := SaveEnvConfig("team", EnvConfig{Name: "taken", Type: EnvironmentTypeRuntime, LocalPortRangeStart: LowerServicePort}); err != nil {
		t.Fatalf("save: %v", err)
	}
	start, err := PlanPullLocalPortRangeStart(ConfigStore{}, "team", "fresh", 0)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if start != LowerServicePort+EnvironmentPortRangeSize {
		t.Fatalf("allocated %d, want the range after the claimed one", start)
	}
}

func TestPlanPullLocalPortRangeStartIgnoresTheEnvironmentItself(t *testing.T) {
	useConfigHome(t)
	seedTenantConfigForPortPlanning(t)
	// Re-pulling into an environment that already holds a range must not
	// collide with itself: the pull is updating this environment, not
	// competing with it.
	if err := SaveEnvConfig("team", EnvConfig{Name: "dev", Type: EnvironmentTypeRuntime, LocalPortRangeStart: LowerServicePort}); err != nil {
		t.Fatalf("save: %v", err)
	}
	start, err := PlanPullLocalPortRangeStart(ConfigStore{}, "team", "dev", LowerServicePort)
	if err != nil {
		t.Fatalf("an environment's own range start was treated as a collision: %v", err)
	}
	if start != LowerServicePort {
		t.Fatalf("allocated %d, want the environment's own range", start)
	}
}

func TestNewPullAsNewEnvironmentRefusesAHostType(t *testing.T) {
	_, err := NewPullAsNewEnvironment(PlatformEnvironment{Name: "dev", Type: string(EnvironmentTypeHost)}, "", 0)
	if err == nil {
		t.Fatal("a host row was accepted as hostable")
	}
	if !strings.Contains(err.Error(), "no pod and no cluster") {
		t.Fatalf("refusal does not say why a host env cannot be hosted: %v", err)
	}
}

func TestNewPullAsNewEnvironmentRequiresAUsableRepoPath(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewPullAsNewEnvironment(PlatformEnvironment{Name: "dev", Type: string(EnvironmentTypeLocalAgent)}, "", 0); err == nil {
		t.Fatal("a local-agent pull-as-new was accepted with no repo path")
	}
	if _, err := NewPullAsNewEnvironment(PlatformEnvironment{Name: "dev", Type: string(EnvironmentTypeLocalAgent)}, dir, LowerServicePort); err != nil {
		t.Fatalf("a real directory was refused: %v", err)
	}
	// A runtime env rides a PVC worktree, so it needs no host path and an
	// empty one is not a refusal.
	if _, err := NewPullAsNewEnvironment(PlatformEnvironment{Name: "dev", Type: string(EnvironmentTypeRuntime)}, "", LowerServicePort); err != nil {
		t.Fatalf("a runtime pull-as-new was refused for want of a host path: %v", err)
	}
}

func TestIsHostableEnvironmentType(t *testing.T) {
	for _, envType := range []EnvironmentType{EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeRuntime} {
		if !IsHostableEnvironmentType(envType) {
			t.Errorf("%s should be hostable", envType)
		}
	}
	if IsHostableEnvironmentType(EnvironmentTypeHost) {
		t.Error("host environments cannot be hosted and must not report as hostable")
	}
}

// seedTenantConfigForPortPlanning gives the "team" tenant its own config.yaml.
// The port planner walks tenants through ListTenantConfigs, which skips any
// directory whose tenant config is absent -- so without this the planner sees
// an empty machine and every range start looks free.
func seedTenantConfigForPortPlanning(t *testing.T) {
	t.Helper()
	if err := SaveTenantConfig(TenantConfig{Name: "team"}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
}

// useConfigHome points config resolution at a scenario-private directory and
// returns it, so a test never reads or writes the operator's real erun config.
func useConfigHome(t *testing.T) string {
	t.Helper()
	restore := setConfigHomeForModeTest(t)
	t.Cleanup(restore)
	return xdg.ConfigHome
}

// TestPullAsNewRefusesAPortCollisionBeforeWriting is the "checked before the
// write" half of the port-range case: the refusal has to land before
// SaveEnvConfig, because a config written first is one the port resolver then
// refuses on some later command, far from the pull that caused it.
func TestPullAsNewRefusesAPortCollisionBeforeWriting(t *testing.T) {
	store := &recordingDefinitionStore{
		envs: []EnvConfig{
			{Name: "taken", Type: EnvironmentTypeRuntime, LocalPortRangeStart: LowerServicePort},
		},
	}
	client := &stubDefinitionClient{row: PlatformEnvironment{
		EnvironmentID: "env-1", TenantID: "tenant-1", Name: "fresh", Type: string(EnvironmentTypeRuntime),
	}}
	_, err := PullEnvironmentDefinition(context.Background(), store, client, EnvDefinitionPullParams{
		Tenant: "team", Environment: "fresh", EnvironmentID: "env-1",
		// The operator asked for the range another environment already holds,
		// which is the shape a collision takes: an unspecified start allocates
		// the lowest free range instead and never collides.
		LocalPortRangeStart: LowerServicePort,
	})
	var overlap ErrLocalPortRangeOverlap
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

// TestPullAsNewCreatesTheEnvironmentFromThePlatformRow is the acceptance half:
// with a free range and a usable repo path, the pull writes a new environment
// carrying the platform-owned identity plus the definition's portable fields,
// and leaves the host-owned fields to this machine.
func TestPullAsNewCreatesTheEnvironmentFromThePlatformRow(t *testing.T) {
	repoPath := t.TempDir()
	store := &recordingDefinitionStore{}
	version := "1.2.3"
	hostile := "localrepopath-placeholder"
	client := &stubDefinitionClient{
		row: PlatformEnvironment{
			EnvironmentID: "env-1", TenantID: "tenant-1", Name: "fresh",
			Type: string(EnvironmentTypeLocalAgent), KubernetesContext: "test-context",
		},
		definition: PlatformEnvDefinition{RuntimeVersion: &version},
		// The payload also tries to carry a host-owned value, which the pull
		// must not write.
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
	if result.Config.LocalPortRangeStart != LowerServicePort {
		t.Errorf("port range start = %d, want the lowest free range", result.Config.LocalPortRangeStart)
	}
	if result.Marker.DefinitionRevision != 3 {
		t.Errorf("marker revision = %d, want the revision that was pulled", result.Marker.DefinitionRevision)
	}
}

// hostedDefinitionFixture is a local environment whose marker names the row
// the stub client serves, so a push resolves without a marker mismatch.
func hostedDefinitionFixture(name string) EnvConfig {
	return EnvConfig{
		Name:              name,
		Type:              EnvironmentTypeRuntime,
		KubernetesContext: "test-context",
		RuntimeVersion:    "1.2.3",
		Hosted: HostedEnvironment{
			APIHost:            "https://api.example.test",
			TenantID:           "tenant-1",
			EnvironmentID:      "env-1",
			DefinitionRevision: 3,
		},
	}
}

func hostedDefinitionClient(config EnvConfig) *stubDefinitionClient {
	return &stubDefinitionClient{row: PlatformEnvironment{
		EnvironmentID:     config.Hosted.EnvironmentID,
		TenantID:          config.Hosted.TenantID,
		Name:              config.Name,
		Type:              string(config.Type),
		KubernetesContext: config.KubernetesContext,
	}}
}

// pushOnce uploads one environment through the shared push path and returns
// the config the push stamped, which is what every case below reads back.
func pushOnce(t *testing.T, config EnvConfig) EnvConfig {
	t.Helper()
	store := &recordingDefinitionStore{envs: []EnvConfig{config}}
	if _, err := PushEnvironmentDefinition(context.Background(), store, hostedDefinitionClient(config), EnvDefinitionPushParams{
		Tenant: "team", Environment: config.Name,
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("push saved %d configs, want 1", len(store.saved))
	}
	return store.saved[0]
}

// TestPushStampsTheDigestOfWhatItSent is the dirty flag's premise: an upload
// records a fingerprint of the settings it carried, so the next reader can say
// whether this machine has moved since. Without it the marker records only
// *which* revision arrived, which cannot tell a local edit from a quiet
// environment.
func TestPushStampsTheDigestOfWhatItSent(t *testing.T) {
	stamped := pushOnce(t, hostedDefinitionFixture("dev"))

	want := DefinitionDigest(BuildPlatformEnvDefinition(hostedDefinitionFixture("dev")))
	if stamped.Hosted.DefinitionDigest != want {
		t.Fatalf("stamped digest = %q, want %q", stamped.Hosted.DefinitionDigest, want)
	}
	if change := HostedDefinitionLocalChangeFor(stamped); !change.Available || change.Changed {
		t.Fatalf("a just-pushed config reads as %+v, want available and unchanged", change)
	}
}

// TestALocalEditAfterATransferReadsAsChanged is the case the dirty flag exists
// for: a portable setting changed while nothing was watching — `erun init`,
// `erun cloud set`, a deploy — is observable on the next read, with no
// platform call and no timestamp to compare.
func TestALocalEditAfterATransferReadsAsChanged(t *testing.T) {
	stamped := pushOnce(t, hostedDefinitionFixture("dev"))
	stamped.RuntimeVersion = "2.0.0"

	change := HostedDefinitionLocalChangeFor(stamped)
	if !change.Available || !change.Changed {
		t.Fatalf("an edited portable setting reads as %+v, want available and changed", change)
	}
	if !change.HasUnsentChange() {
		t.Error("an edited portable setting did not ask for an upload")
	}
}

// TestAHostOwnedEditIsNotADivergence is the other half, and it is what keeps
// the dirty flag from crying wolf: the digest covers the portable subset only,
// so a change to a setting that never leaves the machine — the repo path here
// — must not read as "the platform is missing something".
func TestAHostOwnedEditIsNotADivergence(t *testing.T) {
	stamped := pushOnce(t, hostedDefinitionFixture("dev"))
	stamped.LocalRepoPath = "/somewhere/else"
	stamped.RuntimeRunningImage = "registry.example.test/erun-devops:9.9.9"

	change := HostedDefinitionLocalChangeFor(stamped)
	if !change.Available || change.Changed {
		t.Fatalf("a host-owned edit reads as %+v, want available and unchanged", change)
	}
	if change.HasUnsentChange() {
		t.Error("a host-owned edit asked for an upload of settings that never travel")
	}
}

// TestAMarkerWithNoDigestCannotTell keeps "unknown" apart from both verdicts.
// A marker written before the digest existed carries none, and neither
// claiming a match (which would leave the environment untracked forever) nor
// claiming a difference (which would announce a change nothing observed) is
// honest. It still asks for one upload, which is what starts tracking it.
func TestAMarkerWithNoDigestCannotTell(t *testing.T) {
	legacy := hostedDefinitionFixture("dev")

	change := HostedDefinitionLocalChangeFor(legacy)
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

// TestPullStampsTheDigestToo covers the other transfer direction: a pull
// leaves the local copy in step with the revision it just arrived at, so the
// digest must describe the merged result rather than the platform's payload —
// the platform is silent about fields this machine keeps, and a digest of its
// payload alone would read every one of them as a local edit.
func TestPullStampsTheDigestToo(t *testing.T) {
	repoPath := t.TempDir()
	version := "1.2.3"
	story := &recordingDefinitionStore{envs: []EnvConfig{{
		Name: "fresh", Type: EnvironmentTypeLocalAgent, KubernetesContext: "test-context",
		LocalRepoPath: repoPath,
		Hosted: HostedEnvironment{
			APIHost: "https://api.example.test", TenantID: "tenant-1", EnvironmentID: "env-1",
		},
	}}}
	client := &stubDefinitionClient{
		row:        PlatformEnvironment{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "fresh", Type: string(EnvironmentTypeLocalAgent), KubernetesContext: "test-context"},
		definition: PlatformEnvDefinition{RuntimeVersion: &version},
	}

	result, err := PullEnvironmentDefinition(context.Background(), story, client, EnvDefinitionPullParams{
		Tenant: "team", Environment: "fresh", LocalRepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if change := HostedDefinitionLocalChangeFor(result.Config); !change.Available || change.Changed {
		t.Fatalf("a just-pulled config reads as %+v, want available and unchanged", change)
	}
}

// TestDefinitionDigestIsStableAndSensitive pins the two properties the digest
// is used for: it must not drift between two readers of the same settings (or
// every read would look like a change), and it must not survive a change to a
// portable field (or no read ever would).
func TestDefinitionDigestIsStableAndSensitive(t *testing.T) {
	base := BuildPlatformEnvDefinition(hostedDefinitionFixture("dev"))
	if DefinitionDigest(base) != DefinitionDigest(BuildPlatformEnvDefinition(hostedDefinitionFixture("dev"))) {
		t.Fatal("two builds of the same settings produced different digests")
	}
	if DefinitionDigest(base) == "" {
		t.Fatal("the digest is empty, which no marker could ever match")
	}

	changed := hostedDefinitionFixture("dev")
	changed.RuntimeVersion = "2.0.0"
	if DefinitionDigest(base) == DefinitionDigest(BuildPlatformEnvDefinition(changed)) {
		t.Fatal("a changed portable setting produced the same digest")
	}
}

// recordingDefinitionStore is an in-memory envDefinitionConfigStore that
// records what a pull wrote, so a test can assert a refusal wrote nothing.
type recordingDefinitionStore struct {
	envs  []EnvConfig
	saved []EnvConfig
}

func (s *recordingDefinitionStore) LoadEnvConfig(tenant, environment string) (EnvConfig, string, error) {
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
	return EnvConfig{}, "", ErrNotInitialized
}

func (s *recordingDefinitionStore) SaveEnvConfig(_ string, config EnvConfig) error {
	s.saved = append(s.saved, config)
	return nil
}

func (s *recordingDefinitionStore) ListTenantConfigs() ([]TenantConfig, error) {
	return []TenantConfig{{Name: "team"}}, nil
}

func (s *recordingDefinitionStore) ListEnvConfigs(string) ([]EnvConfig, error) {
	return append(append([]EnvConfig(nil), s.envs...), s.saved...), nil
}

// stubDefinitionClient answers the three platform reads and writes a pull
// makes, from fixed values.
type stubDefinitionClient struct {
	row             PlatformEnvironment
	definition      PlatformEnvDefinition
	rawDefinition   []byte
	definitionReads int
}

func (c *stubDefinitionClient) APIHost() string { return "https://api.example.test" }

func (c *stubDefinitionClient) GetEnvironment(context.Context, string) (PlatformEnvironment, error) {
	return c.row, nil
}

func (c *stubDefinitionClient) GetEnvironmentDefinition(context.Context, string) (PlatformEnvDefinitionRecord, error) {
	c.definitionReads++
	definition := c.definition
	if len(c.rawDefinition) > 0 {
		if err := json.Unmarshal(c.rawDefinition, &definition); err != nil {
			return PlatformEnvDefinitionRecord{}, err
		}
	}
	return PlatformEnvDefinitionRecord{EnvironmentID: c.row.EnvironmentID, TenantID: c.row.TenantID, Revision: 3, Definition: definition}, nil
}

func (c *stubDefinitionClient) PutEnvironmentDefinition(context.Context, string, PlatformEnvDefinition) (PlatformEnvDefinitionRecord, error) {
	return PlatformEnvDefinitionRecord{}, nil
}
