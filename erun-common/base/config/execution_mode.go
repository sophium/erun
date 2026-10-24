package config

import (
	"strings"
)

// Operations that can call a Go library instead of a CLI, keyed per operation
// since a tool is rarely ported whole. The trace shows the CLI either way.
const (
	ExecutionModeSubprocess = "subprocess"
	ExecutionModeLibrary    = "library"
)

// ExecutionConfig holds per-operation overrides; anything but "library" means subprocess.
type ExecutionConfig struct {
	Modes map[string]string `yaml:"modes,omitempty" json:"modes,omitempty"`
}

var knownExecutionOperations = []string{
	"aws-sts",
	"aws-sts-web-identity-token",
	"aws-export-credentials",
	"kubectl-namespace-get",
	"kubectl-pvc-get",
	"kubectl-secret-get",
	"kubectl-pod-get",
	"kubectl-deployment-get",
	"kubectl-deployment-wait",
	"kubectl-secret-apply",
	"kubectl-pod-watch",
	"kubectl-context-configure",
}

// ExecutionModeFor resolves an operation's mode; unknown values mean subprocess.
func ExecutionModeFor(config ERunConfig, operation string) string {
	if config.Execution.Modes == nil {
		return ExecutionModeSubprocess
	}
	if strings.ToLower(strings.TrimSpace(config.Execution.Modes[operation])) == ExecutionModeLibrary {
		return ExecutionModeLibrary
	}
	return ExecutionModeSubprocess
}

// CurrentExecutionMode resolves operation's mode from the root config, defaulting
// to subprocess when it cannot be read.
func CurrentExecutionMode(operation string) string {
	config, _, err := LoadERunConfig()
	if err != nil {
		return ExecutionModeSubprocess
	}
	return ExecutionModeFor(config, operation)
}

// ExecutionModeStatus is one operation's resolved mode, for `erun doctor`.
type ExecutionModeStatus struct {
	Operation string
	Mode      string
}

// ExecutionModeReport lists every operation's resolved mode for `erun doctor`.
func ExecutionModeReport(config ERunConfig) []ExecutionModeStatus {
	statuses := make([]ExecutionModeStatus, 0, len(knownExecutionOperations))
	for _, operation := range knownExecutionOperations {
		statuses = append(statuses, ExecutionModeStatus{
			Operation: operation,
			Mode:      ExecutionModeFor(config, operation),
		})
	}
	return statuses
}
