package config

import (
	"sort"
	"strings"
)

func KnownClaudeModels() []string {
	return []string{"opus", "sonnet", "haiku", "fable"}
}

type EnvironmentClaudeConfig struct {
	UseMantle  *bool `yaml:"usemantle,omitempty" json:"useMantle,omitempty"`
	UseBedrock *bool `yaml:"usebedrock,omitempty" json:"useBedrock,omitempty"`
	// UseGateway overrides the erun-level gateway catalog for this environment; nil inherits it.
	UseGateway *bool `yaml:"usegateway,omitempty" json:"useGateway,omitempty"`
	// No credential field: the gateway credential is one erun-level secret deploy delivers.
	Models          []string `yaml:"models,omitempty" json:"models,omitempty"`
	MaxOutputTokens *int     `yaml:"maxoutputtokens,omitempty" json:"maxOutputTokens,omitempty"`
	// Effort is low|medium|high|xhigh|max|ultracode; ultracode (the default) is xhigh plus workflow orchestration.
	Effort *string `yaml:"effort,omitempty" json:"effort,omitempty"`
	// DefaultModel is the starting model when still available; otherwise the first available model.
	DefaultModel *string `yaml:"defaultmodel,omitempty" json:"defaultModel,omitempty"`
	// VerboseDebug streams Claude's own verbose diagnostics into the AI tab.
	VerboseDebug bool `yaml:"verbosedebug,omitempty" json:"verboseDebug,omitempty"`
}

func (c EnvironmentClaudeConfig) IsZero() bool {
	// An explicit gateway opt-out alone still counts as configuration.
	return c.UseMantle == nil && c.UseBedrock == nil && c.UseGateway == nil && len(c.Models) == 0 &&
		c.MaxOutputTokens == nil && c.Effort == nil && c.DefaultModel == nil && !c.VerboseDebug
}

func (c EnvironmentClaudeConfig) NormalizedModels() []string {
	return NormalizeClaudeModels(c.Models)
}

func NormalizeClaudeModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return claudeModelOrder(out[i]) < claudeModelOrder(out[j]) })
	return out
}

func claudeModelOrder(name string) int {
	for i, known := range KnownClaudeModels() {
		if name == known {
			return i
		}
	}
	return len(KnownClaudeModels())
}
