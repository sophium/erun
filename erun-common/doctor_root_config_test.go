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
}

func (s *inspectStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, s.configPath, s.loadErr
}

func (s *inspectStore) ListTenantConfigs() ([]TenantConfig, error) {
	return s.tenants, s.tenantsErr
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
