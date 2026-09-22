package eruncommon

import (
	"sort"
	"strings"
)

const (
	DefaultClaudeUseMantle  = false
	DefaultClaudeUseBedrock = false
	// DefaultClaudeMaxOutputTokens is the shipped default for an agent's output
	// cap. 4096 (roughly a 200-line source file) made agent mode unable to write
	// the files it exists to write — a job died mid-turn the moment it authored
	// anything non-trivial. 32000 clears a large source file with headroom while
	// staying well under the ceiling below. The runtime chart's own default
	// (erun-devops/k8s/erun-devops/templates/service.yaml, `$claudeMaxOutputTokens`)
	// mirrors this value; a change here must move there too.
	DefaultClaudeMaxOutputTokens = 32000
	defaultClaudeAvailableModels = "opus,sonnet,haiku"
	claudeMaxOutputTokensCeiling = 200000
	claudeMaxOutputTokensFloor   = 1
)

func DefaultClaudeAvailableModels() []string {
	return splitClaudeModels(defaultClaudeAvailableModels)
}

func KnownClaudeModels() []string {
	return []string{"opus", "sonnet", "haiku", "fable"}
}

type EnvironmentClaudeConfig struct {
	UseMantle  *bool `yaml:"usemantle,omitempty" json:"useMantle,omitempty"`
	UseBedrock *bool `yaml:"usebedrock,omitempty" json:"useBedrock,omitempty"`
	// UseGateway tri-states this environment's use of the erun-level gateway
	// catalog, the same way UseMantle and UseBedrock do: unset inherits the
	// catalog, so an environment follows the operator's erun-level decision;
	// true and false override it for this environment alone. A catalog is one
	// erun-level list, so without this an environment could only be moved onto
	// the gateway by moving every environment onto it.
	UseGateway *bool `yaml:"usegateway,omitempty" json:"useGateway,omitempty"`
	// There is deliberately no credential field here. The gateway credential is
	// one erun-level value in the operator's secret store, delivered by deploy
	// into this environment's namespace — so a per-environment reference would
	// be a second way to say the same thing, and the wrong one when the catalog
	// is global.
	Models          []string `yaml:"models,omitempty" json:"models,omitempty"`
	MaxOutputTokens *int     `yaml:"maxoutputtokens,omitempty" json:"maxOutputTokens,omitempty"`
	// Effort is the per-env Claude Code session effort level, one of
	// low|medium|high|xhigh|max|ultracode. ultracode is not an --effort value:
	// it enables xhigh effort plus standing workflow orchestration. Unset means
	// the default, ultracode.
	Effort *string `yaml:"effort,omitempty" json:"effort,omitempty"`
	// DefaultModel is the model the env's AI session starts on when it is one of
	// the env's available models; unset or no-longer-available falls back to the
	// first available model, not the agent's own default. It does not touch the
	// chart's claude.model pod slot.
	DefaultModel *string `yaml:"defaultmodel,omitempty" json:"defaultModel,omitempty"`
	// VerboseDebug streams Claude's own verbose diagnostics into the AI tab.
	VerboseDebug bool `yaml:"verbosedebug,omitempty" json:"verboseDebug,omitempty"`
}

func (c EnvironmentClaudeConfig) IsZero() bool {
	// UseGateway counts: an environment whose only claude setting is opting out
	// of the erun-level gateway has a configuration, and reporting it as zero
	// would drop that override on the next normalize.
	return c.UseMantle == nil && c.UseBedrock == nil && c.UseGateway == nil && len(c.Models) == 0 &&
		c.MaxOutputTokens == nil && c.Effort == nil && c.DefaultModel == nil && !c.VerboseDebug
}

func (c EnvironmentClaudeConfig) NormalizedModels() []string {
	return normalizeClaudeModels(c.Models)
}

func ClaudeMaxOutputTokensRange() (int, int) {
	return claudeMaxOutputTokensFloor, claudeMaxOutputTokensCeiling
}

func splitClaudeModels(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeClaudeModels(models []string) []string {
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

func formatClaudeModels(models []string) string {
	return strings.Join(normalizeClaudeModels(models), ",")
}
