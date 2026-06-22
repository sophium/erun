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

// InitCloudflareCloudProviderParams are the explicit inputs for adding a
// Cloudflare cloud alias. The API token is an account-scoped, delegated token
// (account-level Zone:Edit + DNS:Edit) the operator minted in the Cloudflare
// dashboard; erun stores it via the CloudSecretStore, never inline.
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
