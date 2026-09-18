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

// gatewayModelListTimeout bounds the catalog read. Claude Code bounds its own
// discovery at three seconds because it runs on every startup; this read is
// operator-initiated and one-shot, so it can afford to wait longer for a slow
// gateway before reporting that the list could not be read.
const gatewayModelListTimeout = 10 * time.Second

// gatewayModelListBodyLimit caps how much of a model list is read, so a gateway
// that streams an unbounded body cannot exhaust the desktop's memory for it.
const gatewayModelListBodyLimit = 8 << 20

// uiGatewayModel is one model the configured gateway advertises, as the catalog
// editor offers it.
type uiGatewayModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	// Context is the window the gateway reports for this id, when it reports
	// one at all. Zero means the gateway did not say — the standard discovery
	// response carries no window — so the caller leaves the operator to supply
	// it rather than inventing one.
	Context int `json:"context,omitempty"`
}

// gatewayModelsResponse is the discovery payload Claude Code itself reads: a
// data array whose entries carry id, and optionally display_name and
// description. Provider-level gateways add their own fields beside those, which
// is why the window is read opportunistically rather than required.
type gatewayModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		Description   string `json:"description"`
		ContextLength int    `json:"context_length"`
		// TopProvider is OpenRouter's provider-level block. Its own
		// context_length is the conservative figure: a model can advertise a
		// larger window than the provider serving it will accept.
		TopProvider struct {
			ContextLength int `json:"context_length"`
		} `json:"top_provider"`
	} `json:"data"`
}

// LoadGatewayModels asks the configured gateway which models it serves, so the
// catalog editor can offer them as choices instead of asking the operator to
// type an id. It issues the same request Claude Code's own model discovery
// makes — GET <base>/v1/models?limit=1000 — because a gateway that supports one
// supports the other.
//
// Unlike Claude Code's discovery, no id filter is applied: discovery keeps only
// ids containing "claude" or "anthropic", and the catalog exists precisely to
// reach the models that filter hides.
//
// The request carries no credential. A model list is not privileged, the
// catalog's credential is a Secret reference the desktop does not read, and
// sending none means a redirect cannot leak one.
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

	// A gateway without the endpoint answers 404 here. That is a supported
	// configuration rather than a fault, so the cause is named with its remedy
	// instead of surfacing as a bare status.
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

// gatewayContextWindow prefers the provider-level figure: it is the window the
// provider actually serving the model will accept, and it can be smaller than
// the model's advertised maximum. Declaring the larger one lets a conversation
// grow past what the provider takes, so the request fails with a too-long error
// instead of compacting cleanly.
func gatewayContextWindow(providerLevel, advertised int) int {
	if providerLevel > 0 {
		return providerLevel
	}
	if advertised > 0 {
		return advertised
	}
	return 0
}

// gatewayModelsURL builds the discovery URL from the configured base URL. A
// base URL that already carries a path keeps it, so a gateway mounted under a
// prefix is addressed where it actually serves.
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
	// The same bound Claude Code requests: a gateway that pages must return this
	// many in one response for the list to be complete.
	query := url.Values{}
	query.Set("limit", "1000")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/v1/models"
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
