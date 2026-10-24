package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// gatewayModelListTimeout is longer than Claude Code's 3s: this read is
// operator-initiated and one-shot.
const gatewayModelListTimeout = 10 * time.Second

const gatewayModelListBodyLimit = 8 << 20

type uiGatewayModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	// Context is zero when the gateway does not report a window.
	Context int `json:"context,omitempty"`
}

// gatewayModelsResponse is Claude Code's discovery payload; the window fields
// are provider extensions, read opportunistically.
type gatewayModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		Description   string `json:"description"`
		ContextLength int    `json:"context_length"`
		// TopProvider's context_length can be smaller than the advertised window.
		TopProvider struct {
			ContextLength int `json:"context_length"`
		} `json:"top_provider"`
	} `json:"data"`
}

// LoadGatewayModels lists the gateway's models via Claude Code's discovery
// request, without its claude/anthropic id filter. It sends no credential, so
// a redirect cannot leak one.
func (a *App) LoadGatewayModels(baseURL string) ([]uiGatewayModel, error) {
	endpoint, err := gatewayModelsURL(baseURL)
	if err != nil {
		return nil, err
	}
	body, err := a.fetchGatewayModels(endpoint)
	if err != nil {
		return nil, err
	}
	return parseGatewayModels(body)
}

func (a *App) fetchGatewayModels(endpoint string) ([]byte, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayModelListTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list the gateway's models: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	// A gateway without the endpoint is supported; name the remedy.
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("the gateway does not implement /v1/models, so its models cannot be listed; add model ids to the catalog directly")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list the gateway's models: %s from %s", response.Status, endpoint)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, gatewayModelListBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read the gateway's model list: %w", err)
	}
	return body, nil
}

func parseGatewayModels(body []byte) ([]uiGatewayModel, error) {
	var payload gatewayModelsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("the gateway's model list was not readable JSON: %w", err)
	}
	out := make([]uiGatewayModel, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, entry := range payload.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, uiGatewayModel{
			ID:          id,
			DisplayName: strings.TrimSpace(entry.DisplayName),
			Description: strings.TrimSpace(entry.Description),
			Context:     gatewayContextWindow(entry.TopProvider.ContextLength, entry.ContextLength),
		})
	}
	return out, nil
}

// gatewayContextWindow prefers the provider-level figure: declaring a larger
// one lets a conversation overflow instead of compacting.
func gatewayContextWindow(providerLevel, advertised int) int {
	if providerLevel > 0 {
		return providerLevel
	}
	if advertised > 0 {
		return advertised
	}
	return 0
}

// gatewayModelsURL keeps a base path so a prefix-mounted gateway is addressed.
func gatewayModelsURL(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("a gateway base URL is required to list its models")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse the gateway base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("the gateway base URL %q needs a scheme and host, such as https://openrouter.ai/api", base)
	}
	// Claude Code's own bound; a paging gateway must return all in one response.
	query := url.Values{}
	query.Set("limit", "1000")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/v1/models"
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
