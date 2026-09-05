package eruncommon

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
)

// cloudflareAPIBaseURLEnv redirects every Cloudflare API call to a mock server
// in tests. It must be an env var rather than the in-process CloudDependencies
// override because the desktop runs the wizard as a PTY subprocess the
// in-process override cannot reach.
const cloudflareAPIBaseURLEnv = "ERUN_CLOUDFLARE_API_BASE_URL"

const defaultCloudflareAPIBaseURL = "https://api.cloudflare.com"

// Kept as paths, not full URLs, so the base-URL test seam can redirect every call.
const (
	cloudflareTokenVerifyPath = "/client/v4/user/tokens/verify"
	cloudflareAccountsPath    = "/client/v4/accounts"
	cloudflareZonesPath       = "/client/v4/zones"
)

// CloudflareCreateTokenURL is the dashboard page the guided flow points the
// operator at to mint a scoped token. A browser destination, not an API call,
// so the base-URL test seam never applies to it.
const CloudflareCreateTokenURL = "https://dash.cloudflare.com/profile/api-tokens"

func cloudflareAPIBaseURL() string {
	if override := strings.TrimSpace(os.Getenv(cloudflareAPIBaseURLEnv)); override != "" {
		return strings.TrimRight(override, "/")
	}
	return defaultCloudflareAPIBaseURL
}

// CloudSecretStore persists provider secrets (today: Cloudflare API tokens)
// outside erun-config.yaml, keyed by an opaque ref so the config file carries
// only the ref, never the secret value.
type CloudSecretStore interface {
	SaveCloudSecret(ref, value string) error
	LoadCloudSecret(ref string) (string, error)
	DeleteCloudSecret(ref string) error
}

// CloudflareTokenInfo is the subset of Cloudflare's tokens/verify response the
// alias lifecycle cares about.
type CloudflareTokenInfo struct {
	ID     string
	Status string
}

// CloudflareAccount identifies a Cloudflare account a token can act on.
type CloudflareAccount struct {
	ID   string
	Name string
}

// InitCloudflareCloudProviderParams are the explicit inputs for adding a
// Cloudflare cloud alias. APIToken is a custom token the operator minted with
// Zone + DNS edit for delegation, plus any scopes they will use (e.g. Pages).
type InitCloudflareCloudProviderParams struct {
	AccountID string
	TokenName string
	APIToken  string
}

// InitCloudflareCloudProvider verifies a scoped API token, stores it in the
// secret store, and saves the cloud alias that references it by TokenRef.
func InitCloudflareCloudProvider(ctx Context, store CloudStore, params InitCloudflareCloudProviderParams, deps CloudDependencies) (CloudProviderConfig, error) {
	if store == nil {
		return CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudDependencies(deps)
	accountID := strings.TrimSpace(params.AccountID)
	tokenName := strings.TrimSpace(params.TokenName)
	apiToken := strings.TrimSpace(params.APIToken)
	ctx.Trace(fmt.Sprintf("cloud init cloudflare: account-id=%s token-name=%s api-token=%s",
		accountID, tokenName, redactSecretPresence(apiToken)))
	if err := validateCloudflareInitParams(accountID, tokenName, apiToken); err != nil {
		return CloudProviderConfig{}, err
	}
	alias := CloudProviderAlias(tokenName, accountID, CloudProviderCloudflare)
	if alias == "" {
		return CloudProviderConfig{}, fmt.Errorf("cloudflare cloud provider alias cannot be resolved")
	}
	ctx.Trace("cloud init cloudflare: verifying scoped api token")
	if _, err := deps.VerifyCloudflareToken(ctx, apiToken); err != nil {
		ctx.Trace("cloud init cloudflare: token verification failed: " + err.Error())
		return CloudProviderConfig{}, err
	}
	tokenRef := cloudflareTokenRef(alias)
	provider := cloudflareProviderConfig(alias, accountID, tokenName, tokenRef)
	if ctx.DryRun {
		traceCloudflareInitDryRunPlan(ctx, deps, tokenRef, alias)
		return provider, nil
	}
	if deps.CloudSecretStore == nil {
		return CloudProviderConfig{}, fmt.Errorf("cloud secret store is not configured")
	}
	if err := deps.CloudSecretStore.SaveCloudSecret(tokenRef, apiToken); err != nil {
		return CloudProviderConfig{}, fmt.Errorf("store cloudflare api token: %w", err)
	}
	saved, err := SaveCloudProviderConfig(store, provider)
	if err != nil {
		return CloudProviderConfig{}, err
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

func traceCloudflareInitDryRunPlan(ctx Context, deps CloudDependencies, tokenRef, alias string) {
	if deps.CloudSecretStore == nil {
		ctx.Trace("cloud init cloudflare: secret store is not configured (real run would fail to persist the token)")
	} else {
		ctx.Trace("cloud init cloudflare: secret store is configured")
	}
	ctx.Trace("store cloudflare api token at ref " + tokenRef)
	ctx.Trace("write cloud provider " + alias)
}

// VerifyCloudflareAPIToken validates a scoped token so the guided setup can
// check a pasted token before asking for anything else.
func VerifyCloudflareAPIToken(ctx Context, token string, deps CloudDependencies) (CloudflareTokenInfo, error) {
	return normalizeCloudDependencies(deps).VerifyCloudflareToken(ctx, token)
}

// ResolveCloudflareAccounts lists the accounts a token can act on, so the
// guided CLI setup can auto-derive the account id instead of asking for it.
func ResolveCloudflareAccounts(ctx Context, token string, deps CloudDependencies) ([]CloudflareAccount, error) {
	return normalizeCloudDependencies(deps).ListCloudflareAccounts(ctx, token)
}

// cloudflareProviderConfig mirrors the Cloudflare identity into the generic
// Username/AccountID fields so generic listing and alias derivation need no
// Cloudflare special-casing.
func cloudflareProviderConfig(alias, accountID, tokenName, tokenRef string) CloudProviderConfig {
	return NormalizeCloudProviderConfig(CloudProviderConfig{
		Alias:     alias,
		Provider:  CloudProviderCloudflare,
		Username:  tokenName,
		AccountID: accountID,
		Cloudflare: &CloudflareProviderConfig{
			AccountID: accountID,
			TokenName: tokenName,
			TokenRef:  tokenRef,
		},
	})
}

func cloudflareCloudProviderTokenStatus(provider CloudProviderConfig, deps CloudDependencies) CloudProviderStatus {
	if provider.Cloudflare == nil || strings.TrimSpace(provider.Cloudflare.TokenRef) == "" {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusNotConfigured, Message: "cloudflare api token is not configured"}
	}
	if deps.CloudSecretStore == nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusUnknown, Message: "cloud secret store is not configured"}
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.Cloudflare.TokenRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusNotConfigured, Message: "cloudflare api token is not available on this machine"}
	}
	if _, err := deps.VerifyCloudflareToken(stderrOnlyContext(), token); err != nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusExpired, Message: err.Error()}
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
}

// deleteCloudflareToken is the Cloudflare equivalent of an SSO logout: the alias
// stays configured but loses its credential until re-initialized.
func deleteCloudflareToken(ctx Context, provider CloudProviderConfig, deps CloudDependencies) error {
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

func defaultVerifyCloudflareToken(ctx Context, token string) (CloudflareTokenInfo, error) {
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

// defaultListCloudflareAccounts resolves a token's accounts for the guided
// setup. It tries /accounts first, then falls back to deriving the account from
// the token's zones — a least-privilege Zone-only token cannot read /accounts
// but can list its zones.
func defaultListCloudflareAccounts(ctx Context, token string) ([]CloudflareAccount, error) {
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

// fileCloudSecretStore hashes the ref to a filename so alias punctuation never
// reaches the filesystem path.
type fileCloudSecretStore struct {
	dir string
}

// NewFileCloudSecretStore returns a CloudSecretStore backed by 0600 files under dir.
func NewFileCloudSecretStore(dir string) CloudSecretStore {
	return fileCloudSecretStore{dir: strings.TrimSpace(dir)}
}

const cloudSecretStoreDirName = "cloud-secrets"

// DefaultCloudSecretStore returns a file-backed CloudSecretStore rooted under
// the erun config dir.
func DefaultCloudSecretStore() (CloudSecretStore, error) {
	dir, err := ERunConfigDir()
	if err != nil {
		return nil, err
	}
	return NewFileCloudSecretStore(filepath.Join(dir, cloudSecretStoreDirName)), nil
}

func (s fileCloudSecretStore) path(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".token")
}

func (s fileCloudSecretStore) SaveCloudSecret(ref, value string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("secret ref is required")
	}
	if s.dir == "" {
		return fmt.Errorf("cloud secret store directory is not configured")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create cloud secret store directory: %w", err)
	}
	if err := os.WriteFile(s.path(ref), []byte(value), 0o600); err != nil {
		return fmt.Errorf("write cloud secret: %w", err)
	}
	return nil
}

func (s fileCloudSecretStore) LoadCloudSecret(ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("secret ref is required")
	}
	data, err := os.ReadFile(s.path(ref))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s fileCloudSecretStore) DeleteCloudSecret(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return nil
	}
	if err := os.Remove(s.path(ref)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete cloud secret: %w", err)
	}
	return nil
}
