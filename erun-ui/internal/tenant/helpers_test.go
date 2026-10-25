package tenant

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"sync"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// newTestService fills every seam a test leaves unset with an inert stand-in;
// the secret store is a temp dir so no test reads the host's credentials.
func newTestService(t *testing.T, d Deps) *Service {
	t.Helper()
	if d.Store == nil {
		d.Store = stubStore{}
	}
	if d.Cloud.CloudSecretStore == nil {
		d.Cloud.CloudSecretStore = cloud.NewFileCloudSecretStore(t.TempDir())
	}
	if d.Context == nil {
		d.Context = context.Background
	}
	if d.MCPBearer == nil {
		d.MCPBearer = func(string, string) string { return "" }
	}
	if d.LoadAPILog == nil {
		d.LoadAPILog = func(context.Context, DashboardInput) (string, error) {
			return "", errors.New("no API log in this test")
		}
	}
	if d.ConfiguredERunAliases == nil {
		store := d.Store
		d.ConfiguredERunAliases = func() ([]string, error) { return configuredERunAliases(store) }
	}
	return New(d)
}

// configuredERunAliases mirrors the desktop's filter over the root config.
func configuredERunAliases(store configpkg.CloudReadStore) ([]string, error) {
	providers, err := cloud.ListCloudProviders(store)
	if err != nil {
		return nil, err
	}
	aliases := []string{}
	for _, provider := range providers {
		if provider.Provider == configpkg.CloudProviderERun {
			aliases = append(aliases, provider.Alias)
		}
	}
	return aliases, nil
}

type stubStore struct {
	config  *configpkg.ERunConfig
	tenants map[string]model.TenantConfig
}

// stubStoreConfigMu guards the root config across goroutines; the stub is
// copied by value, so the lock cannot live on it.
var stubStoreConfigMu sync.Mutex

func (s stubStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	stubStoreConfigMu.Lock()
	defer stubStoreConfigMu.Unlock()
	if s.config == nil {
		return configpkg.ERunConfig{}, "", nil
	}
	return *s.config, "", nil
}

func (s stubStore) SaveERunConfig(config configpkg.ERunConfig) error {
	stubStoreConfigMu.Lock()
	defer stubStoreConfigMu.Unlock()
	if s.config != nil {
		*s.config = config
	}
	return nil
}

func (s stubStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubStore) SaveTenantConfig(config model.TenantConfig) error {
	if s.tenants != nil {
		s.tenants[config.Name] = config
	}
	return nil
}

func testUIJWT(issuer string) string {
	return testUIJWTWithSubject(issuer, "test-subject")
}

func testUIJWTWithSubject(issuer, subject string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `","sub":"` + subject + `"}`))
	return header + "." + payload + ".signature"
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
