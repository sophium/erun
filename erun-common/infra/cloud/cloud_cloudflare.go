package cloud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// cloudflareAPIBaseURLEnv redirects Cloudflare API calls in tests; an env var because the
// desktop runs the wizard as a subprocess.
const cloudflareAPIBaseURLEnv = "ERUN_CLOUDFLARE_API_BASE_URL"

const defaultCloudflareAPIBaseURL = "https://api.cloudflare.com"

// Paths, not URLs, so the base-URL seam applies.
const (
	cloudflareTokenVerifyPath = "/client/v4/user/tokens/verify"
	cloudflareAccountsPath    = "/client/v4/accounts"
	cloudflareZonesPath       = "/client/v4/zones"
)

// CloudflareCreateTokenURL is the dashboard page for minting a scoped token.
const CloudflareCreateTokenURL = "https://dash.cloudflare.com/profile/api-tokens"

func cloudflareAPIBaseURL() string {
	if override := strings.TrimSpace(os.Getenv(cloudflareAPIBaseURLEnv)); override != "" {
		return strings.TrimRight(override, "/")
	}
	return defaultCloudflareAPIBaseURL
}

// CloudSecretStore keeps provider secrets outside the config, which carries only a ref.
type CloudSecretStore interface {
	SaveCloudSecret(ref, value string) error
	LoadCloudSecret(ref string) (string, error)
	DeleteCloudSecret(ref string) error
}

// CloudflareTokenInfo is the used subset of Cloudflare's tokens/verify response.
type CloudflareTokenInfo struct {
	ID     string
	Status string
}

// CloudflareAccount is an account a token can act on.
type CloudflareAccount struct {
	ID   string
	Name string
}

// InitCloudflareCloudProvider verifies and stores the token, then saves the alias by TokenRef.
func InitCloudflareCloudProvider(ctx core.Context, store CloudStore, params model.InitCloudflareCloudProviderParams, deps CloudDependencies) (model.CloudProviderConfig, error) {
	if store == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = NormalizeCloudDependencies(deps)
	accountID := strings.TrimSpace(params.AccountID)
	tokenName := strings.TrimSpace(params.TokenName)
	apiToken := strings.TrimSpace(params.APIToken)
	ctx.Trace(fmt.Sprintf("cloud init cloudflare: account-id=%s token-name=%s api-token=%s",
		accountID, tokenName, redactSecretPresence(apiToken)))
	if err := validateCloudflareInitParams(accountID, tokenName, apiToken); err != nil {
		return model.CloudProviderConfig{}, err
	}
	alias := CloudProviderAlias(tokenName, accountID, config.CloudProviderCloudflare)
	if alias == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("cloudflare cloud provider alias cannot be resolved")
	}
	ctx.Trace("cloud init cloudflare: verifying scoped api token")
	if _, err := deps.VerifyCloudflareToken(ctx, apiToken); err != nil {
		ctx.Trace("cloud init cloudflare: token verification failed: " + err.Error())
		return model.CloudProviderConfig{}, err
	}
	tokenRef := cloudflareTokenRef(alias)
	provider := cloudflareProviderConfig(alias, accountID, tokenName, tokenRef)
	if ctx.DryRun {
		traceCloudflareInitDryRunPlan(ctx, deps, tokenRef, alias)
		return provider, nil
	}
	if deps.CloudSecretStore == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("cloud secret store is not configured")
	}
	if err := deps.CloudSecretStore.SaveCloudSecret(tokenRef, apiToken); err != nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store cloudflare api token: %w", err)
	}
	saved, err := SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	return saved, nil
}

func validateCloudflareInitParams(accountID, tokenName, apiToken string) error {
	switch {
	case accountID == "":
		return fmt.Errorf("cloudflare account id is required")
	case tokenName == "":
		return fmt.Errorf("cloudflare token name is required")
	case apiToken == "":
		return fmt.Errorf("cloudflare api token is required")
	}
	return nil
}

func traceCloudflareInitDryRunPlan(ctx core.Context, deps CloudDependencies, tokenRef, alias string) {
	if deps.CloudSecretStore == nil {
		ctx.Trace("cloud init cloudflare: secret store is not configured (real run would fail to persist the token)")
	} else {
		ctx.Trace("cloud init cloudflare: secret store is configured")
	}
	ctx.Trace("store cloudflare api token at ref " + tokenRef)
	ctx.Trace("write cloud provider " + alias)
}

// VerifyCloudflareAPIToken validates a scoped token.
func VerifyCloudflareAPIToken(ctx core.Context, token string, deps CloudDependencies) (CloudflareTokenInfo, error) {
	return NormalizeCloudDependencies(deps).VerifyCloudflareToken(ctx, token)
}

// ResolveCloudflareAccounts lists the accounts a token can act on.
func ResolveCloudflareAccounts(ctx core.Context, token string, deps CloudDependencies) ([]CloudflareAccount, error) {
	return NormalizeCloudDependencies(deps).ListCloudflareAccounts(ctx, token)
}

// Mirrors the identity into generic fields so listing needs no Cloudflare special case.
func cloudflareProviderConfig(alias, accountID, tokenName, tokenRef string) model.CloudProviderConfig {
	return NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:     alias,
		Provider:  config.CloudProviderCloudflare,
		Username:  tokenName,
		AccountID: accountID,
		Cloudflare: &model.CloudflareProviderConfig{
			AccountID: accountID,
			TokenName: tokenName,
			TokenRef:  tokenRef,
		},
	})
}

func cloudflareCloudProviderTokenStatus(provider model.CloudProviderConfig, deps CloudDependencies) model.CloudProviderStatus {
	if provider.Cloudflare == nil || strings.TrimSpace(provider.Cloudflare.TokenRef) == "" {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusNotConfigured, Message: "cloudflare api token is not configured"}
	}
	if deps.CloudSecretStore == nil {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusUnknown, Message: "cloud secret store is not configured"}
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.Cloudflare.TokenRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusNotConfigured, Message: "cloudflare api token is not available on this machine"}
	}
	if _, err := deps.VerifyCloudflareToken(core.StderrOnlyContext(), token); err != nil {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusExpired, Message: err.Error()}
	}
	return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
}

// deleteCloudflareToken keeps the alias but drops its credential, like an SSO logout.
func deleteCloudflareToken(ctx core.Context, provider model.CloudProviderConfig, deps CloudDependencies) error {
	if provider.Cloudflare == nil || strings.TrimSpace(provider.Cloudflare.TokenRef) == "" {
		return nil
	}
	ctx.Trace("delete cloudflare api token at ref " + provider.Cloudflare.TokenRef)
	if ctx.DryRun {
		return nil
	}
	if deps.CloudSecretStore == nil {
		return fmt.Errorf("cloud secret store is not configured")
	}
	return deps.CloudSecretStore.DeleteCloudSecret(provider.Cloudflare.TokenRef)
}

func cloudflareTokenRef(alias string) string {
	return "cloudflare/" + strings.TrimSpace(alias)
}

func redactSecretPresence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return "<redacted>"
}

func defaultVerifyCloudflareToken(ctx core.Context, token string) (CloudflareTokenInfo, error) {
	verifyURL := cloudflareAPIBaseURL() + cloudflareTokenVerifyPath
	ctx.Trace("GET " + verifyURL)
	if ctx.DryRun {
		return CloudflareTokenInfo{Status: "active"}, nil
	}
	return verifyCloudflareTokenAt(verifyURL, token)
}

func verifyCloudflareTokenAt(url, token string) (CloudflareTokenInfo, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return CloudflareTokenInfo{}, fmt.Errorf("cloudflare api token is required")
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return CloudflareTokenInfo{}, fmt.Errorf("build cloudflare token verify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return CloudflareTokenInfo{}, fmt.Errorf("verify cloudflare token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var payload struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return CloudflareTokenInfo{}, fmt.Errorf("parse cloudflare token verify response (status %d): %w", resp.StatusCode, err)
	}
	if !payload.Success {
		message := "cloudflare rejected the api token"
		if len(payload.Errors) > 0 && strings.TrimSpace(payload.Errors[0].Message) != "" {
			message = payload.Errors[0].Message
		}
		return CloudflareTokenInfo{}, fmt.Errorf("verify cloudflare token: %s", message)
	}
	if status := strings.ToLower(strings.TrimSpace(payload.Result.Status)); status != "" && status != "active" {
		return CloudflareTokenInfo{}, fmt.Errorf("cloudflare api token is %s", payload.Result.Status)
	}
	return CloudflareTokenInfo{ID: payload.Result.ID, Status: payload.Result.Status}, nil
}

// Falls back to the token's zones: a Zone-only token cannot read /accounts.
func defaultListCloudflareAccounts(ctx core.Context, token string) ([]CloudflareAccount, error) {
	base := cloudflareAPIBaseURL()
	accountsURL := base + cloudflareAccountsPath
	ctx.Trace("GET " + accountsURL)
	if ctx.DryRun {
		return []CloudflareAccount{{ID: "cf-account-id", Name: "Cloudflare account"}}, nil
	}
	accounts, err := listCloudflareAccountsAt(accountsURL, token)
	if err == nil && len(accounts) > 0 {
		return accounts, nil
	}
	zonesURL := base + cloudflareZonesPath
	ctx.Trace("GET " + zonesURL)
	zoneAccounts, zonesErr := resolveCloudflareAccountsViaZones(zonesURL, token)
	if zonesErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, zonesErr
	}
	return zoneAccounts, nil
}

func resolveCloudflareAccountsViaZones(url, token string) ([]CloudflareAccount, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("cloudflare api token is required")
	}
	req, err := http.NewRequest(http.MethodGet, url+"?per_page=50", nil)
	if err != nil {
		return nil, fmt.Errorf("build cloudflare zones request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list cloudflare zones: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []cloudflareZone `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse cloudflare zones response (status %d): %w", resp.StatusCode, err)
	}
	if !payload.Success {
		message := "cloudflare rejected the zones request"
		if len(payload.Errors) > 0 && strings.TrimSpace(payload.Errors[0].Message) != "" {
			message = payload.Errors[0].Message
		}
		return nil, fmt.Errorf("list cloudflare zones: %s", message)
	}
	return distinctCloudflareZoneAccounts(payload.Result), nil
}

type cloudflareZone struct {
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
}

func distinctCloudflareZoneAccounts(zones []cloudflareZone) []CloudflareAccount {
	seen := make(map[string]struct{})
	accounts := make([]CloudflareAccount, 0, 1)
	for _, zone := range zones {
		id := strings.TrimSpace(zone.Account.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		accounts = append(accounts, CloudflareAccount{ID: id, Name: zone.Account.Name})
	}
	return accounts
}

func listCloudflareAccountsAt(url, token string) ([]CloudflareAccount, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("cloudflare api token is required")
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build cloudflare accounts request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list cloudflare accounts: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))
	var payload struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse cloudflare accounts response (status %d): %w", resp.StatusCode, err)
	}
	if !payload.Success {
		message := "cloudflare rejected the accounts request"
		if len(payload.Errors) > 0 && strings.TrimSpace(payload.Errors[0].Message) != "" {
			message = payload.Errors[0].Message
		}
		return nil, fmt.Errorf("list cloudflare accounts: %s", message)
	}
	accounts := make([]CloudflareAccount, 0, len(payload.Result))
	for _, account := range payload.Result {
		if strings.TrimSpace(account.ID) == "" {
			continue
		}
		accounts = append(accounts, CloudflareAccount{ID: account.ID, Name: account.Name})
	}
	return accounts, nil
}

// FileCloudSecretStore hashes the ref so alias punctuation never reaches the path.
type FileCloudSecretStore struct {
	dir string
}

// NewFileCloudSecretStore stores secrets in 0600 files under dir.
func NewFileCloudSecretStore(dir string) CloudSecretStore {
	return FileCloudSecretStore{dir: strings.TrimSpace(dir)}
}

const CloudSecretStoreDirName = "cloud-secrets"

// DefaultCloudSecretStore is rooted under the erun config dir.
func DefaultCloudSecretStore() (CloudSecretStore, error) {
	dir, err := config.ERunConfigDir()
	if err != nil {
		return nil, err
	}
	return NewFileCloudSecretStore(filepath.Join(dir, CloudSecretStoreDirName)), nil
}

// CloudSecretFileName lets pod provisioning name the file the store will later read.
func CloudSecretFileName(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(sum[:]) + ".token"
}

func (s FileCloudSecretStore) Path(ref string) string {
	return filepath.Join(s.dir, CloudSecretFileName(ref))
}

func (s FileCloudSecretStore) SaveCloudSecret(ref, value string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("secret ref is required")
	}
	if s.dir == "" {
		return fmt.Errorf("cloud secret store directory is not configured")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create cloud secret store directory: %w", err)
	}
	if err := os.WriteFile(s.Path(ref), []byte(value), 0o600); err != nil {
		return fmt.Errorf("write cloud secret: %w", err)
	}
	return nil
}

func (s FileCloudSecretStore) LoadCloudSecret(ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("secret ref is required")
	}
	data, err := os.ReadFile(s.Path(ref))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s FileCloudSecretStore) DeleteCloudSecret(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return nil
	}
	if err := os.Remove(s.Path(ref)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete cloud secret: %w", err)
	}
	return nil
}
