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

// cloudflareTokenVerifyURL is Cloudflare's token self-verification endpoint. A
// successful response confirms the scoped API token is valid and active.
const cloudflareTokenVerifyURL = "https://api.cloudflare.com/client/v4/user/tokens/verify"

// cloudflareAccountsURL lists the accounts a token can act on (works for tokens
// with an account-scope permission). Used to auto-resolve the account id.
const cloudflareAccountsURL = "https://api.cloudflare.com/client/v4/accounts"

// cloudflareZonesURL lists the zones a token can act on. It is the fallback
// account source for a Zone-scope-only token (which cannot list /accounts):
// each zone object carries its account id+name.
const cloudflareZonesURL = "https://api.cloudflare.com/client/v4/zones"

// CloudflareCreateTokenURL is the Cloudflare dashboard page where an operator
// mints an API token. The guided CLI flow prints it so the operator creates the
// scoped token in their already-authenticated browser session.
const CloudflareCreateTokenURL = "https://dash.cloudflare.com/profile/api-tokens"

// CloudSecretStore persists provider secrets (today: Cloudflare scoped API
// tokens) outside erun-config.yaml. Implementations key on an opaque ref so
// the config file only ever carries the ref, never the secret value. The
// transports wire a concrete store; erun-common ships a file-backed default
// via NewFileCloudSecretStore.
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
// Cloudflare cloud alias. The API token is a delegated, custom token the
// operator minted in the Cloudflare dashboard — Zone + DNS edit for delegation,
// plus any other scopes they will use (e.g. Cloudflare Pages for static sites);
// erun stores it via the CloudSecretStore, never inline.
type InitCloudflareCloudProviderParams struct {
	AccountID string
	TokenName string
	APIToken  string
}

// InitCloudflareCloudProvider verifies a Cloudflare scoped API token, stores it
// in the secret store, and saves a Cloudflare cloud alias
// ("<token-name>+<account-id>@cloudflare"). The raw token is written only to
// the secret store; erun-config.yaml carries the TokenRef handle.
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
	switch {
	case accountID == "":
		return CloudProviderConfig{}, fmt.Errorf("cloudflare account id is required")
	case tokenName == "":
		return CloudProviderConfig{}, fmt.Errorf("cloudflare token name is required")
	case apiToken == "":
		return CloudProviderConfig{}, fmt.Errorf("cloudflare api token is required")
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
		ctx.Trace("store cloudflare api token at ref " + tokenRef)
		ctx.Trace("write cloud provider " + alias)
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

// VerifyCloudflareAPIToken validates a scoped token using the wired (or
// default) Cloudflare verifier. Exposed so the guided CLI setup can validate a
// pasted token before asking for anything else.
func VerifyCloudflareAPIToken(ctx Context, token string, deps CloudDependencies) (CloudflareTokenInfo, error) {
	return normalizeCloudDependencies(deps).VerifyCloudflareToken(ctx, token)
}

// ResolveCloudflareAccounts lists the accounts a token can act on, so the
// guided CLI setup can auto-derive the account id instead of asking for it.
func ResolveCloudflareAccounts(ctx Context, token string, deps CloudDependencies) ([]CloudflareAccount, error) {
	return normalizeCloudDependencies(deps).ListCloudflareAccounts(ctx, token)
}

// cloudflareProviderConfig assembles a normalized Cloudflare CloudProviderConfig.
// Username/AccountID mirror the Cloudflare identity so generic listing and the
// alias derivation work without special-casing; the Cloudflare sub-block holds
// the TokenRef that distinguishes the credential.
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

// cloudflareCloudProviderTokenStatus reports whether the stored scoped token is
// still valid by re-verifying it against Cloudflare. A missing store or token
// reads as not_configured; a verification failure reads as expired.
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
	if _, err := deps.VerifyCloudflareToken(Context{}, token); err != nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusExpired, Message: err.Error()}
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
}

// exportCloudflareToken loads the scoped API token for a Cloudflare alias so a
// caller (deploy, the desktop) can deliver it into a runtime environment. The
// token value is sensitive: callers must never trace, log, or echo it.
func exportCloudflareToken(provider CloudProviderConfig, deps CloudDependencies) (string, error) {
	if provider.Cloudflare == nil || strings.TrimSpace(provider.Cloudflare.TokenRef) == "" {
		return "", fmt.Errorf("cloudflare alias %q has no token reference", provider.Alias)
	}
	if deps.CloudSecretStore == nil {
		return "", fmt.Errorf("cloud secret store is not configured")
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.Cloudflare.TokenRef)
	if err != nil {
		return "", fmt.Errorf("load cloudflare api token for %q: %w", provider.Alias, err)
	}
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("cloudflare api token for %q is empty", provider.Alias)
	}
	return token, nil
}

// deleteCloudflareToken removes the stored scoped token, the Cloudflare
// equivalent of an SSO logout: the alias remains configured but loses its
// credential until re-initialized.
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

// cloudflareTokenRef derives a stable secret-store handle for an alias.
func cloudflareTokenRef(alias string) string {
	return "cloudflare/" + strings.TrimSpace(alias)
}

// redactSecretPresence reports whether a secret was supplied without echoing
// it, keeping dry-run traces deterministic and free of credentials.
func redactSecretPresence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return "<redacted>"
}

// defaultVerifyCloudflareToken calls Cloudflare's tokens/verify endpoint with
// the supplied scoped token. In dry-run it traces the call and returns a
// synthetic active result without touching the network.
func defaultVerifyCloudflareToken(ctx Context, token string) (CloudflareTokenInfo, error) {
	ctx.Trace("GET " + cloudflareTokenVerifyURL)
	if ctx.DryRun {
		return CloudflareTokenInfo{Status: "active"}, nil
	}
	return verifyCloudflareTokenAt(cloudflareTokenVerifyURL, token)
}

// verifyCloudflareTokenAt performs the live token verification against url. It
// is split from defaultVerifyCloudflareToken (which owns the trace and dry-run
// short-circuit) so the response parsing and status classification are unit
// testable against an httptest server.
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
	defer resp.Body.Close()
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

// defaultListCloudflareAccounts resolves the accounts a token can act on so the
// guided setup can auto-resolve the account id. It tries /accounts first (works
// for tokens carrying an account-scope permission, e.g. Cloudflare Pages); when
// that yields nothing — the case for a least-privilege Zone-only token, which
// has no account-read permission — it falls back to deriving the account from
// the token's zones. Dry-run returns a synthetic account without any network.
func defaultListCloudflareAccounts(ctx Context, token string) ([]CloudflareAccount, error) {
	ctx.Trace("GET " + cloudflareAccountsURL)
	if ctx.DryRun {
		return []CloudflareAccount{{ID: "cf-account-id", Name: "Cloudflare account"}}, nil
	}
	accounts, err := listCloudflareAccountsAt(cloudflareAccountsURL, token)
	if err == nil && len(accounts) > 0 {
		return accounts, nil
	}
	// Zone-scope-only tokens cannot list /accounts; derive from the zones the
	// token can see — each zone object carries its account id+name.
	ctx.Trace("GET " + cloudflareZonesURL)
	zoneAccounts, zonesErr := resolveCloudflareAccountsViaZones(cloudflareZonesURL, token)
	if zonesErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, zonesErr
	}
	return zoneAccounts, nil
}

// resolveCloudflareAccountsViaZones derives the distinct account(s) a token can
// act on from the zones it can list. A Zone-scope token can read /zones even
// when it cannot read /accounts, so this is the robust fallback for the
// least-privilege token. Split out for httptest-based unit testing.
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
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct {
			Account struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"account"`
		} `json:"result"`
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
	seen := make(map[string]struct{})
	accounts := make([]CloudflareAccount, 0, 1)
	for _, zone := range payload.Result {
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
	return accounts, nil
}

// listCloudflareAccountsAt performs the live accounts lookup against url. Split
// from defaultListCloudflareAccounts (which owns the trace and dry-run
// short-circuit) so the response parsing is unit testable against httptest.
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
	defer resp.Body.Close()
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

// fileCloudSecretStore is a 0600 file-backed CloudSecretStore. The ref is
// hashed to a filename so alias punctuation never reaches the filesystem path.
type fileCloudSecretStore struct {
	dir string
}

// NewFileCloudSecretStore returns a CloudSecretStore that persists secrets as
// 0600 files under dir (created 0700 on first write). Transports wire this with
// a directory beside the erun config so secrets stay off the YAML config.
func NewFileCloudSecretStore(dir string) CloudSecretStore {
	return fileCloudSecretStore{dir: strings.TrimSpace(dir)}
}

// cloudSecretStoreDirName is the subdirectory under the erun config dir that
// holds provider secrets.
const cloudSecretStoreDirName = "cloud-secrets"

// DefaultCloudSecretStore returns a file-backed CloudSecretStore rooted at
// <erun-config-dir>/cloud-secrets. Transports wire this onto CloudDependencies
// so Cloudflare tokens persist beside — never inside — erun-config.yaml.
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
