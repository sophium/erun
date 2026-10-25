package clouddefaults

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
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

func cloudflareAPIBaseURL() string {
	if override := strings.TrimSpace(os.Getenv(cloudflareAPIBaseURLEnv)); override != "" {
		return strings.TrimRight(override, "/")
	}
	return defaultCloudflareAPIBaseURL
}

func defaultVerifyCloudflareToken(ctx core.Context, token string) (cloud.CloudflareTokenInfo, error) {
	verifyURL := cloudflareAPIBaseURL() + cloudflareTokenVerifyPath
	ctx.Trace("GET " + verifyURL)
	if ctx.DryRun {
		return cloud.CloudflareTokenInfo{Status: "active"}, nil
	}
	return verifyCloudflareTokenAt(verifyURL, token)
}

func verifyCloudflareTokenAt(url, token string) (cloud.CloudflareTokenInfo, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return cloud.CloudflareTokenInfo{}, fmt.Errorf("cloudflare api token is required")
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return cloud.CloudflareTokenInfo{}, fmt.Errorf("build cloudflare token verify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return cloud.CloudflareTokenInfo{}, fmt.Errorf("verify cloudflare token: %w", err)
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
		return cloud.CloudflareTokenInfo{}, fmt.Errorf("parse cloudflare token verify response (status %d): %w", resp.StatusCode, err)
	}
	if !payload.Success {
		message := "cloudflare rejected the api token"
		if len(payload.Errors) > 0 && strings.TrimSpace(payload.Errors[0].Message) != "" {
			message = payload.Errors[0].Message
		}
		return cloud.CloudflareTokenInfo{}, fmt.Errorf("verify cloudflare token: %s", message)
	}
	if status := strings.ToLower(strings.TrimSpace(payload.Result.Status)); status != "" && status != "active" {
		return cloud.CloudflareTokenInfo{}, fmt.Errorf("cloudflare api token is %s", payload.Result.Status)
	}
	return cloud.CloudflareTokenInfo{ID: payload.Result.ID, Status: payload.Result.Status}, nil
}

// Falls back to the token's zones: a Zone-only token cannot read /accounts.
func defaultListCloudflareAccounts(ctx core.Context, token string) ([]cloud.CloudflareAccount, error) {
	base := cloudflareAPIBaseURL()
	accountsURL := base + cloudflareAccountsPath
	ctx.Trace("GET " + accountsURL)
	if ctx.DryRun {
		return []cloud.CloudflareAccount{{ID: "cf-account-id", Name: "Cloudflare account"}}, nil
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

func resolveCloudflareAccountsViaZones(url, token string) ([]cloud.CloudflareAccount, error) {
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

func distinctCloudflareZoneAccounts(zones []cloudflareZone) []cloud.CloudflareAccount {
	seen := make(map[string]struct{})
	accounts := make([]cloud.CloudflareAccount, 0, 1)
	for _, zone := range zones {
		id := strings.TrimSpace(zone.Account.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		accounts = append(accounts, cloud.CloudflareAccount{ID: id, Name: zone.Account.Name})
	}
	return accounts
}

func listCloudflareAccountsAt(url, token string) ([]cloud.CloudflareAccount, error) {
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
	accounts := make([]cloud.CloudflareAccount, 0, len(payload.Result))
	for _, account := range payload.Result {
		if strings.TrimSpace(account.ID) == "" {
			continue
		}
		accounts = append(accounts, cloud.CloudflareAccount{ID: account.ID, Name: account.Name})
	}
	return accounts, nil
}
