package config

import (
	"strings"
)

// Fixed names for the gateway credential Secret erun writes into each namespace;
// the creator and the chart both read them from here.
const (
	GatewaySecretName = "erun-claude-gateway"
	GatewaySecretKey  = "token"
)

// DefaultOpenRouterAuthTokenRef is the operator secret store ref for the gateway
// credential; config.yaml holds only the ref.
const DefaultOpenRouterAuthTokenRef = "claude-gateway"

// OpenRouterConfig is the erun-level gateway model catalog; environments select
// from it. Nil means no gateway.
type OpenRouterConfig struct {
	BaseURL string `yaml:"baseurl,omitempty" json:"baseURL,omitempty"`
	// AuthTokenRef names the credential in the operator secret store, so no value
	// reaches config.yaml, helm argv, or a launch command.
	AuthTokenRef string `yaml:"authtokenref,omitempty" json:"authTokenRef,omitempty"`
	// DefaultModel is used when an environment chose none; ignored if not in the catalog.
	DefaultModel string            `yaml:"defaultmodel,omitempty" json:"defaultModel,omitempty"`
	Models       []OpenRouterModel `yaml:"models,omitempty" json:"models,omitempty"`
}

// OpenRouterModel is a model id and its context window, which must be the
// provider-level figure or conversations fail instead of compacting.
// RequiresReasoningEcho marks a model whose provider demands its reasoning back;
// Claude Code cannot echo it, so no erun lane can drive such a model.
type OpenRouterModel struct {
	ID                    string `yaml:"id" json:"id"`
	Context               int    `yaml:"context,omitempty" json:"context,omitempty"`
	RequiresReasoningEcho bool   `yaml:"requiresreasoningecho,omitempty" json:"requiresReasoningEcho,omitempty"`
}

// reasoningEchoModelIDs are ids known to refuse erun's lanes regardless of the
// catalog, matched case-insensitively. Add an id only on an observed refusal.
var reasoningEchoModelIDs = map[string]struct{}{
	"deepseek/deepseek-v4.1-flash": {},
}

func modelRequiresReasoningEcho(id string) bool {
	_, known := reasoningEchoModelIDs[strings.ToLower(strings.TrimSpace(id))]
	return known
}

func (m OpenRouterModel) driveable() bool {
	return !m.RequiresReasoningEcho && !modelRequiresReasoningEcho(m.ID)
}

// EffectiveGateway returns the catalog unless the environment explicitly opted out
// (false); unset and true both inherit.
func EffectiveGateway(claude EnvironmentClaudeConfig, catalog *OpenRouterConfig) *OpenRouterConfig {
	if !catalog.Configured() {
		return nil
	}
	if claude.UseGateway != nil && !*claude.UseGateway {
		return nil
	}
	return catalog
}

// AuthTokenRefName returns the catalog's ref or the default, and "" only when no
// catalog is configured.
func (c *OpenRouterConfig) AuthTokenRefName() string {
	if !c.Configured() {
		return ""
	}
	if ref := strings.TrimSpace(c.AuthTokenRef); ref != "" {
		return ref
	}
	return DefaultOpenRouterAuthTokenRef
}

// Endpoint returns the trimmed base URL, or "" for a nil catalog.
func (c *OpenRouterConfig) Endpoint() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.BaseURL)
}

// Configured reports whether a base URL is set; models are optional.
func (c *OpenRouterConfig) Configured() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.BaseURL) != ""
}

// ModelIDs returns selectable model ids in order, without blanks, duplicates, or
// undriveable listings.
func (c *OpenRouterConfig) ModelIDs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Models))
	seen := make(map[string]struct{}, len(c.Models))
	for _, m := range c.Models {
		id := strings.TrimSpace(m.ID)
		// A non-token id would be flag or shell syntax on a launch line.
		if id == "" || !m.driveable() || !ClaudeModelTokenPattern.MatchString(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// RequiresReasoningEcho reports whether a lane would be refused on id. An unknown
// id is false: naming an uncurated model is supported.
func (c *OpenRouterConfig) RequiresReasoningEcho(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if c != nil {
		for _, m := range c.Models {
			if strings.TrimSpace(m.ID) == id {
				return !m.driveable()
			}
		}
	}
	return modelRequiresReasoningEcho(id)
}

// ContextFor returns the model's context window, or 0 when unknown.
func (c *OpenRouterConfig) ContextFor(id string) int {
	if c == nil {
		return 0
	}
	id = strings.TrimSpace(id)
	for _, m := range c.Models {
		if strings.TrimSpace(m.ID) == id {
			return m.Context
		}
	}
	return 0
}

func (c *OpenRouterConfig) HasModel(id string) bool {
	if c == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, m := range c.Models {
		if strings.TrimSpace(m.ID) == id {
			return true
		}
	}
	return false
}

// ResolveDefaultModel returns the default if listed and driveable, else the first
// driveable entry; it becomes ANTHROPIC_MODEL for every agent job.
func (c *OpenRouterConfig) ResolveDefaultModel() string {
	if c == nil {
		return ""
	}
	if def := strings.TrimSpace(c.DefaultModel); def != "" && c.HasModel(def) && !c.RequiresReasoningEcho(def) && ClaudeModelTokenPattern.MatchString(def) {
		return def
	}
	if ids := c.ModelIDs(); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

type RootConfigStore interface {
	LoadERunConfig() (ERunConfig, string, error)
}

// ResolveOpenRouterConfig reads the catalog; a missing root config means no
// gateway rather than an error, so AI launches keep working.
func ResolveOpenRouterConfig(store RootConfigStore) (*OpenRouterConfig, error) {
	var config ERunConfig
	if store != nil {
		loaded, _, err := store.LoadERunConfig()
		if err != nil {
			return nil, err
		}
		config = loaded
	}
	return config.OpenRouter, nil
}

// GatewayEnvVars returns the non-secret env Claude Code needs for the gateway;
// the credential is read pod-side.
func (c *OpenRouterConfig) GatewayEnvVars() map[string]string {
	if !c.Configured() {
		return nil
	}
	return map[string]string{
		"ANTHROPIC_BASE_URL": strings.TrimSpace(c.BaseURL),
		// Empty, not unset: unset lets Claude Code bypass the gateway.
		"ANTHROPIC_API_KEY": "",
	}
}
