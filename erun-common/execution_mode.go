package eruncommon

import "strings"

// Execution mode lets a handful of operations that shell out to a CLI (aws,
// helm, kubectl, ...) instead call the equivalent Go library, while keeping
// the exact same CLI invocation as the dry-run/audit trace either way. Each
// ported operation is keyed by its own name rather than by tool, since a tool
// is rarely ported all at once — e.g. "aws-sts" covers only the `aws sts
// get-caller-identity` call sites, not `aws sso login` or `aws configure
// set`, which stay subprocess-only.
const (
	ExecutionModeSubprocess = "subprocess"
	ExecutionModeLibrary    = "library"
)

// ExecutionConfig holds the per-operation execution mode overrides. Absent
// from config.yaml (the default) or set to anything other than "library"
// keeps an operation on the subprocess path.
type ExecutionConfig struct {
	Modes map[string]string `yaml:"modes,omitempty" json:"modes,omitempty"`
}

// knownExecutionOperations lists every operation with a library-backed
// alternative today, so ExecutionModeReport can enumerate them without
// depending on config.yaml already naming them explicitly.
var knownExecutionOperations = []string{
	"aws-sts",
	"aws-sts-web-identity-token",
	"aws-export-credentials",
	"kubectl-namespace-get",
	"kubectl-pvc-get",
	"kubectl-secret-get",
	"kubectl-pod-get",
	"kubectl-deployment-get",
}

// ExecutionModeFor resolves which path a promoted operation should take.
// Unset or unrecognized values normalize to subprocess so a typo in
// config.yaml cannot silently switch an operation into library mode.
func ExecutionModeFor(config ERunConfig, operation string) string {
	if config.Execution.Modes == nil {
		return ExecutionModeSubprocess
	}
	if strings.ToLower(strings.TrimSpace(config.Execution.Modes[operation])) == ExecutionModeLibrary {
		return ExecutionModeLibrary
	}
	return ExecutionModeSubprocess
}

// currentExecutionMode reads the root config to resolve operation's mode,
// tolerating an uninitialized or unreadable config by defaulting to
// subprocess — the same tolerant-load convention DefaultCloudDependencies
// uses for CloudSecretStore.
func currentExecutionMode(operation string) string {
	config, _, err := LoadERunConfig()
	if err != nil {
		return ExecutionModeSubprocess
	}
	return ExecutionModeFor(config, operation)
}

// ExecutionModeStatus is one operation's resolved mode, for `erun doctor` to
// report so "it behaves differently on my machine" has an answer.
type ExecutionModeStatus struct {
	Operation string
	Mode      string
}

// ExecutionModeReport lists config's resolved mode for every promoted
// operation, for `erun doctor` to print. Takes config explicitly (rather than
// loading it internally) so it stays a pure function callers can test without
// touching the filesystem.
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
