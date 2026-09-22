package eruncommon

import "strings"

// Delivering the credential is erun's own business, not an operator entry.
//
// One erun-level catalog means one credential, so there is nothing for the
// operator to name: they supply a value, and erun writes it into each
// environment's namespace under these fixed names and points the chart at them.
// Naming them here rather than only in the chart keeps the one place that
// creates the Secret and the one place that reads it in agreement.
const (
	// GatewaySecretName is the Secret erun creates in each environment's
	// namespace holding the gateway credential.
	GatewaySecretName = "erun-claude-gateway"
	// GatewaySecretKey is the entry within GatewaySecretName the chart reads.
	GatewaySecretKey = "token"
)

// DefaultOpenRouterAuthTokenRef is the operator secret store ref the gateway
// credential is saved under. A ref, not a value: the token lives in erun's own
// operator secret store — the same store the Cloudflare token uses — so
// config.yaml stays safe to back up and share.
const DefaultOpenRouterAuthTokenRef = "claude-gateway"

// OpenRouterConfig is the operator's erun-level catalog of gateway models and
// the gateway that serves them. It is deliberately root config rather than a
// per-environment setting: the catalog is one list the operator maintains, and
// an environment selects from it. Nil keeps an install that has not configured
// a gateway on exactly its existing behaviour.
type OpenRouterConfig struct {
	// BaseURL is the gateway's Anthropic-compatible endpoint, e.g.
	// https://openrouter.ai/api.
	BaseURL string `yaml:"baseurl,omitempty" json:"baseURL,omitempty"`
	// AuthTokenRef names the gateway credential in erun's own operator secret
	// store — the same store the Cloudflare token uses. It is a reference
	// rather than the token so config.yaml stays safe to back up and share, and
	// so no credential value ever passes through helm's argv, a chart value, or
	// a launch command.
	//
	// A store ref, precisely because the catalog is erun-level: the credential
	// is one value on this machine, not a per-cluster Secret to go looking for.
	// Deploy reads it and delivers it into each environment's namespace under
	// GatewaySecretName.
	AuthTokenRef string `yaml:"authtokenref,omitempty" json:"authTokenRef,omitempty"`
	// DefaultModel is the catalog entry an environment selects when it has not
	// chosen one. Ignored when it names no catalog entry.
	DefaultModel string            `yaml:"defaultmodel,omitempty" json:"defaultModel,omitempty"`
	Models       []OpenRouterModel `yaml:"models,omitempty" json:"models,omitempty"`
}

// OpenRouterModel is one selectable model id and the context window Claude Code
// must assume for it.
//
// The window is per model because a gateway id carries none of its own and the
// real windows differ between models, so a single env-wide value would be wrong
// for at least one of them. Context must be the provider-level figure, which
// can be smaller than the model's advertised maximum: declaring the larger
// headline lets a conversation grow past what the serving provider accepts, so
// the request fails with a too-long error instead of compacting cleanly.
// RequiresReasoningEcho marks a model whose provider refuses a conversation
// unless the model's own reasoning is handed back to it on the next request
// ("The `reasoning_content` in the thinking mode must be passed back to the
// API").
//
// No erun AI lane can drive such a model, and that is a property of the client
// rather than of this catalog: every lane drives Claude Code, which builds the
// requests erun never builds, and Claude Code models no reasoning_content at
// all — the string does not occur anywhere in the 2.1.222 binary, while the
// `thinking` and `reasoning` vocabulary it does handle occurs hundreds of times.
// A client cannot echo a block it never modelled, so the refusal is structural
// and no invocation flag, environment value, or erun-side proxy can clear it.
//
// The catalog declares this so an operator curating a listing they know about
// can mark it, but the declaration alone is not enough to cover the installs
// that need it. A catalog is written before the declaration is known: the editor
// seeds an unconfigured one from the host's own Claude Code settings, so a host
// already running one of these models writes back a row carrying nothing but an
// id and a window, and the published configuration reference shows the same
// shape. Those catalogs exist now and no edit reaches them, which is why
// reasoningEchoModelIDs states the same fact against the model id itself: the
// wire contract is a property of the model, so it is knowable without an
// operator annotating a row.
//
// It is stated rather than detected because a model id carries nothing about the
// wire contract its provider enforces, and because the refusal happens inside
// Claude Code's own request building tens of turns into a conversation — erun
// neither builds those requests nor survives the process that dies on them.
// Stating it keeps the failure at the point erun chooses a model, where the lane
// can still refuse to start, instead of at the turn the provider refuses, where
// the work is already lost.
type OpenRouterModel struct {
	ID      string `yaml:"id" json:"id"`
	Context int    `yaml:"context,omitempty" json:"context,omitempty"`
	// RequiresReasoningEcho is the declaration described above. Set it on a
	// listing whose provider demands its own reasoning back.
	RequiresReasoningEcho bool `yaml:"requiresreasoningecho,omitempty" json:"requiresReasoningEcho,omitempty"`
}

// reasoningEchoModelIDs are the model ids whose provider is known to refuse a
// conversation erun's lanes drive, whether or not the catalog declares them.
// They are matched case-insensitively on the whole id.
//
// Add an id here only on an observed refusal, and alongside a declaration in the
// published catalog where one applies: listing a model that can in fact be
// driven refuses a lane that would have worked, which is its own defect. The set
// is deliberately the ids erun has evidence for rather than every model of a
// suspect family, because a family shares a vendor but not necessarily a wire
// contract.
var reasoningEchoModelIDs = map[string]struct{}{
	"deepseek/deepseek-v4.1-flash": {},
}

// modelRequiresReasoningEcho reports whether the id itself names a model erun
// knows its lanes cannot drive, independent of any catalog.
func modelRequiresReasoningEcho(id string) bool {
	_, known := reasoningEchoModelIDs[strings.ToLower(strings.TrimSpace(id))]
	return known
}

// driveable reports whether an erun AI lane can be launched on this listing.
func (m OpenRouterModel) driveable() bool {
	return !m.RequiresReasoningEcho && !modelRequiresReasoningEcho(m.ID)
}

// EffectiveGateway resolves the gateway an environment actually uses: the
// erun-level catalog, unless the environment opts out of it.
//
// The tri-state mirrors UseMantle and UseBedrock. Unset inherits, so an
// environment follows the operator's erun-level decision; an explicit false
// keeps that one environment on its own Claude sign-in while every other
// environment still uses the gateway; an explicit true is the same as
// inheriting when a catalog exists, and is reserved for a future catalog that
// arrives after the override was recorded.
//
// It never returns a gateway for an environment that opted out, so every caller
// — the chart values, the launch, and the auth-mode decision — reaches the same
// answer from one place.
func EffectiveGateway(claude EnvironmentClaudeConfig, catalog *OpenRouterConfig) *OpenRouterConfig {
	if !catalog.Configured() {
		return nil
	}
	if claude.UseGateway != nil && !*claude.UseGateway {
		return nil
	}
	return catalog
}

// AuthTokenRefName returns the operator secret store ref the credential is read
// from: the one the catalog names, or the conventional default when it names
// none. It returns "" only for a catalog that is not configured at all, so a
// caller can tell "no gateway" from "gateway with the default ref".
//
// There is deliberately no per-environment resolution here. The catalog is
// erun-level and the credential is one erun-level value, so an environment
// takes the gateway and its credential together or opts out of both.
func (c *OpenRouterConfig) AuthTokenRefName() string {
	if !c.Configured() {
		return ""
	}
	if ref := strings.TrimSpace(c.AuthTokenRef); ref != "" {
		return ref
	}
	return DefaultOpenRouterAuthTokenRef
}

// Endpoint returns the gateway's base URL, trimmed, or "" for a nil catalog.
//
// It exists so a caller holding a catalog that may be nil — the desktop's
// credential status, which reads one before deciding what to show — reaches the
// address through the same nil-safe surface as every other accessor here rather
// than dereferencing the struct.
func (c *OpenRouterConfig) Endpoint() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.BaseURL)
}

// Configured reports whether the catalog names a usable gateway. A base URL
// with no models still routes Claude at the gateway; the model then comes from
// the environment's own selection or is typed at launch.
func (c *OpenRouterConfig) Configured() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.BaseURL) != ""
}

// ModelIDs returns the catalog's model ids in configured order, dropping blanks
// and duplicates so a caller can offer them as selectable options directly.
//
// A listing declared to require reasoning echo is omitted: it is exactly what a
// selectable set must not offer, and both readers of this list — the
// environment's ERUN_CLAUDE_AVAILABLE_MODELS and the desktop's model choices —
// are offering it to an operator who would then lose a conversation to it.
func (c *OpenRouterConfig) ModelIDs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Models))
	seen := make(map[string]struct{}, len(c.Models))
	for _, m := range c.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" || !m.driveable() {
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

// RequiresReasoningEcho reports whether an erun AI lane would be refused on id:
// either the catalog lists it as a model whose provider demands its own
// reasoning back, or the id names one of reasoningEchoModelIDs.
//
// It stays false for an id that is merely absent from the catalog and unknown,
// because naming an uncurated model is a supported act. A model erun has
// evidence against is a different case, and the catalog's silence about it is
// not evidence of anything — the listing may simply have been written before the
// evidence existed.
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

// ContextFor returns the configured context window for a model id, or 0 when
// the id is not in the catalog. A zero result means the window is unknown --
// the caller decides whether to fall back or leave Claude Code's own assumption
// in place, rather than this resolving to a made-up default.
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

// HasModel reports whether the catalog lists an id.
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

// ResolveDefaultModel picks the catalog entry an unconfigured environment
// selects: the configured default when it names a catalog entry, otherwise the
// first entry. It never returns an id the catalog does not list, so a stale
// default cannot route an environment at a model the gateway no longer serves,
// and never one the catalog declares undriveable — this is the value an
// environment renders as ANTHROPIC_MODEL, which is the model an exec agent job
// outside the AI tab starts on, so resolving an undriveable listing here would
// put every agent job in the environment on it.
func (c *OpenRouterConfig) ResolveDefaultModel() string {
	if c == nil {
		return ""
	}
	if def := strings.TrimSpace(c.DefaultModel); def != "" && c.HasModel(def) && !c.RequiresReasoningEcho(def) {
		return def
	}
	if ids := c.ModelIDs(); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// RootConfigStore is the narrow read this package needs from whatever owns the
// on-disk erun config.
type RootConfigStore interface {
	LoadERunConfig() (ERunConfig, string, error)
}

// ResolveOpenRouterConfig reads the erun-level gateway catalog, treating an
// absent or unreadable root config as "no gateway configured" rather than an
// error: launching an AI session must not start failing because a root config
// is missing or momentarily unreadable, and the unconfigured behaviour is the
// pre-existing one. A store that cannot be read is reported to the caller so a
// genuinely broken config is still visible where it matters.
func ResolveOpenRouterConfig(store RootConfigStore) (*OpenRouterConfig, error) {
	if store == nil {
		return nil, nil
	}
	config, _, err := store.LoadERunConfig()
	if err != nil {
		return nil, err
	}
	return config.OpenRouter, nil
}

// GatewayEnvVars returns the non-secret environment Claude Code needs to reach
// the configured gateway, in the order they are written to a settings env
// block. The credential is deliberately absent: it is read pod-side from the
// operator's secret store, so it never appears in a settings file authored by
// erun or in a launch command's argv.
func (c *OpenRouterConfig) GatewayEnvVars() map[string]string {
	if !c.Configured() {
		return nil
	}
	return map[string]string{
		"ANTHROPIC_BASE_URL": strings.TrimSpace(c.BaseURL),
		// Explicitly empty rather than unset: an API key left unset lets Claude
		// Code fall back to a direct provider, which would bypass the gateway
		// the operator asked for.
		"ANTHROPIC_API_KEY": "",
	}
}
