package eruncommon

import (
	"sort"
	"testing"
)

// TestParseCloudProviderAliasRoundTrip locks the contract with
// CloudProviderAlias: the parser must be its exact inverse for any
// well-formed triple.
func TestParseCloudProviderAliasRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		username  string
		accountID string
		provider  string
	}{
		{"alice", "1234567890", "aws"},
		{"Rihards.Freimanis", "020362606330", "aws"},
		{"user+with.dots", "9", "aws"},
	} {
		alias := CloudProviderAlias(tc.username, tc.accountID, tc.provider)
		gotUser, gotAccount, gotProvider, ok := ParseCloudProviderAlias(alias)
		if !ok {
			t.Fatalf("alias %q failed to parse", alias)
		}
		if gotUser != tc.username || gotAccount != tc.accountID || gotProvider != tc.provider {
			t.Fatalf("alias=%q got=(%q,%q,%q) want=(%q,%q,%q)", alias, gotUser, gotAccount, gotProvider, tc.username, tc.accountID, tc.provider)
		}
	}
}

// TestParseCloudProviderAliasMalformed documents the negative
// surface: anything that does not contain both separators with
// non-empty segments must surface as ok=false.
func TestParseCloudProviderAliasMalformed(t *testing.T) {
	for _, alias := range []string{
		"",
		"no-separators",
		"missing@account",
		"user+account",
		"+1@aws",
		"user+1@",
		"user+@aws",
		"@aws",
		"+aws",
	} {
		if _, _, _, ok := ParseCloudProviderAlias(alias); ok {
			t.Fatalf("expected ok=false for %q", alias)
		}
	}
}

// inspectStore is a tiny in-memory RootConfigInspectionStore so the
// inspection tests do not depend on the real on-disk config layout.
type inspectStore struct {
	config     ERunConfig
	configPath string
	loadErr    error
	tenants    []TenantConfig
	tenantsErr error
	envs       map[string][]EnvConfig
	envsErr    error
}

func (s *inspectStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, s.configPath, s.loadErr
}

func (s *inspectStore) ListTenantConfigs() ([]TenantConfig, error) {
	return s.tenants, s.tenantsErr
}

func (s *inspectStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	if s.envsErr != nil {
		return nil, s.envsErr
	}
	return s.envs[tenant], nil
}

// TestInspectRootConfigClean covers the happy path: every alias the
// tenants and cloud contexts reference is configured. Inspection
// must report ok status, zero orphans, and the configured count.
func TestInspectRootConfigClean(t *testing.T) {
	alias := CloudProviderAlias("alice", "1", "aws")
	store := &inspectStore{
		config: ERunConfig{
			CloudProviders: []CloudProviderConfig{{Alias: alias, Provider: CloudProviderAWS}},
			CloudContexts:  []CloudContextConfig{{Name: "ctx-1", CloudProviderAlias: alias, Region: "eu-west-2"}},
		},
		configPath: "/tmp/erun-config.yaml",
		tenants:    []TenantConfig{{Name: "team", CloudProviderAliases: []string{alias}, PrimaryCloudProviderAlias: alias}},
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if result.ConfigStatus != RootConfigStatusOK {
		t.Fatalf("status %q", result.ConfigStatus)
	}
	if len(result.OrphanedAliases) != 0 {
		t.Fatalf("unexpected orphans: %v", result.OrphanedAliases)
	}
	if result.ConfiguredCount != 1 {
		t.Fatalf("configuredCount=%d", result.ConfiguredCount)
	}
	if !result.Complete() {
		t.Fatalf("Complete() must be true for clean state")
	}
}

// TestInspectRootConfigSurfacesOrphanedAlias reproduces the failure
// mode that motivated this work: tenant + cloud-context both name an
// alias that is missing from CloudProviders. Inspection must collect
// both back-references on a single OrphanedAlias entry, parse the
// alias into its three components, and mark Complete()=false.
func TestInspectRootConfigSurfacesOrphanedAlias(t *testing.T) {
	alias := CloudProviderAlias("alice", "1234567890", "aws")
	store := &inspectStore{
		config: ERunConfig{
			CloudContexts: []CloudContextConfig{
				{Name: "ctx-1", CloudProviderAlias: alias, Region: "eu-west-2"},
				{Name: "ctx-2", CloudProviderAlias: alias, Region: "eu-west-2"},
			},
		},
		configPath: "/tmp/erun-config.yaml",
		tenants: []TenantConfig{
			{Name: "team", CloudProviderAliases: []string{alias}, PrimaryCloudProviderAlias: alias},
			{Name: "other", CloudProviderAliases: []string{alias}},
		},
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if result.Complete() {
		t.Fatalf("Complete() must be false when orphans exist")
	}
	if len(result.OrphanedAliases) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(result.OrphanedAliases))
	}
	orphan := result.OrphanedAliases[0]
	if orphan.Alias != alias {
		t.Fatalf("alias mismatch: %q", orphan.Alias)
	}
	if !orphan.Parsed || orphan.AccountID != "1234567890" || orphan.Username != "alice" || orphan.Provider != CloudProviderAWS {
		t.Fatalf("decoded fields wrong: %+v", orphan)
	}
	sort.Strings(orphan.ReferencedByTenants)
	if len(orphan.ReferencedByTenants) != 2 || orphan.ReferencedByTenants[0] != "other" || orphan.ReferencedByTenants[1] != "team" {
		t.Fatalf("tenant refs wrong: %v", orphan.ReferencedByTenants)
	}
	if len(orphan.ReferencedByCloudContexts) != 2 {
		t.Fatalf("context refs wrong: %v", orphan.ReferencedByCloudContexts)
	}
	if orphan.ReferencedByCloudContexts[0].Region != "eu-west-2" {
		t.Fatalf("region not captured: %v", orphan.ReferencedByCloudContexts[0])
	}
}

// TestInspectRootConfigCorrupted: a corrupted root config must
// short-circuit the alias walk (we have no source of truth for
// configured providers to compare against). The inspection still
// surfaces ConfigStatus=corrupted so the doctor knows to offer the
// restore-from-backup path.
func TestInspectRootConfigCorrupted(t *testing.T) {
	store := &inspectStore{
		configPath: "/tmp/erun-config.yaml",
		loadErr:    ErrConfigCorrupted,
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if result.ConfigStatus != RootConfigStatusCorrupted {
		t.Fatalf("expected corrupted, got %q", result.ConfigStatus)
	}
	if len(result.OrphanedAliases) != 0 {
		t.Fatalf("must not walk orphans when root config is unloadable")
	}
	if result.Complete() {
		t.Fatal("Complete() must be false for corrupted root config")
	}
}

// TestInspectRootConfigMissing: same as corrupted, but distinguished
// in ConfigStatus so the doctor UI can offer different language.
func TestInspectRootConfigMissing(t *testing.T) {
	store := &inspectStore{
		configPath: "/tmp/erun-config.yaml",
		loadErr:    ErrNotInitialized,
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if result.ConfigStatus != RootConfigStatusMissing {
		t.Fatalf("expected missing, got %q", result.ConfigStatus)
	}
}

// TestInspectRootConfigMalformedAliasParsedFalse: when a tenant or
// context names an alias that does not match the documented shape,
// it is still surfaced as an orphan, but Parsed=false so the repair
// path skips it instead of attempting a doomed init.
func TestInspectRootConfigMalformedAliasParsedFalse(t *testing.T) {
	store := &inspectStore{
		config:     ERunConfig{},
		configPath: "/tmp/erun-config.yaml",
		tenants:    []TenantConfig{{Name: "team", CloudProviderAliases: []string{"not-a-valid-alias"}}},
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(result.OrphanedAliases) != 1 {
		t.Fatalf("orphan count: %d", len(result.OrphanedAliases))
	}
	if result.OrphanedAliases[0].Parsed {
		t.Fatalf("Parsed=true for malformed alias")
	}
}

// TestInspectRootConfigSurfacesOrphanedCloudContext covers the case
// the screenshot reproduced in real life: an env config names a
// cloud-managed kubernetes context that the root config no longer
// lists. The walk must aggregate references across multiple
// tenants/envs into one OrphanedCloudContext entry and decode the
// account/region from the erun naming convention.
func TestInspectRootConfigSurfacesOrphanedCloudContext(t *testing.T) {
	alias := CloudProviderAlias("alice", "020362606330", "aws")
	store := &inspectStore{
		config: ERunConfig{
			CloudProviders: []CloudProviderConfig{{Alias: alias, Provider: CloudProviderAWS}},
			// Note: NO matching CloudContextConfig — the env's
			// KubernetesContext is an orphaned reference.
		},
		configPath: "/tmp/erun-config.yaml",
		tenants: []TenantConfig{
			{Name: "petios", CloudProviderAliases: []string{alias}, PrimaryCloudProviderAlias: alias},
		},
		envs: map[string][]EnvConfig{
			"petios": {
				{
					Name:               "rihards-review",
					KubernetesContext:  "erun-001-020362606330-eu-west-2",
					CloudProviderAlias: alias,
				},
				{
					Name:               "rihards-hotfix",
					KubernetesContext:  "erun-001-020362606330-eu-west-2",
					CloudProviderAlias: alias,
				},
			},
		},
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(result.OrphanedAliases) != 0 {
		t.Fatalf("unexpected orphaned aliases: %v", result.OrphanedAliases)
	}
	if len(result.OrphanedContexts) != 1 {
		t.Fatalf("orphan contexts: %d", len(result.OrphanedContexts))
	}
	orphan := result.OrphanedContexts[0]
	if orphan.KubernetesContext != "erun-001-020362606330-eu-west-2" {
		t.Fatalf("name: %q", orphan.KubernetesContext)
	}
	if orphan.AccountID != "020362606330" {
		t.Fatalf("account: %q", orphan.AccountID)
	}
	if orphan.Region != "eu-west-2" {
		t.Fatalf("region: %q", orphan.Region)
	}
	if orphan.CloudProviderAlias != alias {
		t.Fatalf("alias: %q", orphan.CloudProviderAlias)
	}
	if len(orphan.ReferencedByEnvs) != 2 {
		t.Fatalf("env refs: %v", orphan.ReferencedByEnvs)
	}
	if result.Complete() {
		t.Fatalf("Complete() must be false when cloud-context orphans exist")
	}
}

// TestInspectRootConfigSkipsLocalKubeContexts: a non-cloud env (no
// CloudProviderAlias, ManagedCloud=false) must NOT register its
// KubernetesContext as an orphan even when that name isn't in the
// root config's CloudContexts. Otherwise every local orbstack/kind
// env would show up as a doctor finding.
func TestInspectRootConfigSkipsLocalKubeContexts(t *testing.T) {
	store := &inspectStore{
		config:     ERunConfig{},
		configPath: "/tmp/erun-config.yaml",
		tenants:    []TenantConfig{{Name: "team"}},
		envs: map[string][]EnvConfig{
			"team": {
				{Name: "local", KubernetesContext: "orbstack"},
			},
		},
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(result.OrphanedContexts) != 0 {
		t.Fatalf("local env should not produce an orphan, got %v", result.OrphanedContexts)
	}
}

// TestInspectRootConfigMatchesExistingCloudContext locks the
// negative path: when the root config DOES carry a matching
// CloudContextConfig (matched on either Name or KubernetesContext),
// the env reference is healthy and does not register as an orphan.
func TestInspectRootConfigMatchesExistingCloudContext(t *testing.T) {
	alias := CloudProviderAlias("alice", "020362606330", "aws")
	store := &inspectStore{
		config: ERunConfig{
			CloudProviders: []CloudProviderConfig{{Alias: alias, Provider: CloudProviderAWS}},
			CloudContexts: []CloudContextConfig{{
				Name:               "erun-001-020362606330-eu-west-2",
				KubernetesContext:  "erun-001-020362606330-eu-west-2",
				CloudProviderAlias: alias,
				Region:             "eu-west-2",
			}},
		},
		configPath: "/tmp/erun-config.yaml",
		tenants:    []TenantConfig{{Name: "petios", CloudProviderAliases: []string{alias}, PrimaryCloudProviderAlias: alias}},
		envs: map[string][]EnvConfig{
			"petios": {
				{Name: "review", KubernetesContext: "erun-001-020362606330-eu-west-2", CloudProviderAlias: alias},
			},
		},
	}
	result, err := InspectRootConfig(store)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(result.OrphanedContexts) != 0 {
		t.Fatalf("expected no orphans, got %v", result.OrphanedContexts)
	}
	if !result.Complete() {
		t.Fatalf("Complete() must be true when all references resolve")
	}
}

// TestParseErunCloudContextNameDecodesAccountAndRegion is a focused
// test on the naming convention parser — region names contain
// hyphens which the SplitN call has to handle correctly.
func TestParseErunCloudContextNameDecodesAccountAndRegion(t *testing.T) {
	cases := []struct {
		name    string
		account string
		region  string
	}{
		{"erun-001-020362606330-eu-west-2", "020362606330", "eu-west-2"},
		{"erun-99-123456789012-us-east-1", "123456789012", "us-east-1"},
		{"orbstack", "", ""},
		{"erun-1-tooshort-eu-west-1", "", ""},
		{"erun-1-12345678901a-eu-west-1", "", ""},
		{"erun-001", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		account, region := parseErunCloudContextName(tc.name)
		if account != tc.account || region != tc.region {
			t.Fatalf("%q -> (%q, %q), want (%q, %q)", tc.name, account, region, tc.account, tc.region)
		}
	}
}
