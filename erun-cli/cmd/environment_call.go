package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// The idle, job, and activity-lease verbs each have two homes. Inside the
// environment they read and write its own activity store directly, which is
// what the in-pod monitor and the MCP server invoke. Run from the operator's
// machine the same code would answer from the laptop's store — an answer about
// the wrong machine, and work started on the wrong machine — so off-environment
// they go to the environment's own edge instead.

// environmentTargetsItself reports whether this process is the environment the
// verb is about, and may therefore serve it from the local store.
func environmentTargetsItself() bool {
	return common.IsInRuntimeEnvironment(nil)
}

// callEnvironmentTool runs one tool on the environment's MCP edge and decodes
// its structured result. The second return reports whether a call was actually
// made: a dry run resolves and traces the plan without reaching the edge, so the
// plan stays readable for an environment that is not currently open.
//
// idleProbe must be true only for a tool the caller knows is read-only (idle,
// job_status, job_output, job_await, activity_lease_list): the probe header it
// sets exempts the whole request from the environment's activity accounting, so
// setting it for a tool that can mutate the environment (job_start, job_cancel,
// activity_lease_take, ...) would let driving the environment read as idle.
func callEnvironmentTool[T any](ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, tool string, arguments map[string]any, idleProbe bool) (T, bool, error) {
	var decoded T
	if arguments == nil {
		arguments = map[string]any{}
	}
	target, err := resolveMCPEdgeTarget(commandCtx, resolveOpen, scopedOpenParams(tenant, environment))
	if err != nil {
		return decoded, false, err
	}
	commandCtx.TraceCommand("", "mcp", "tools/call", target.endpoint, tool, compactMCPArguments(arguments))
	if commandCtx.DryRun {
		return decoded, false, nil
	}
	result, err := callMCPToolWithReattach(ctx, commandCtx, target, tool, arguments, idleProbe)
	if err != nil {
		return decoded, false, mcpEdgeErrorWithExitCode(target, err)
	}
	// An edge that answered without a payload is not an empty answer about the
	// environment; treating it as one is the failure these verbs exist to close.
	if len(result.Structured) == 0 {
		return decoded, false, fmt.Errorf("%s/%s returned no result for %s", target.tenant, target.environment, tool)
	}
	if err := json.Unmarshal(result.Structured, &decoded); err != nil {
		return decoded, false, fmt.Errorf("decode the %s result from %s/%s: %w", tool, target.tenant, target.environment, err)
	}
	return decoded, true, nil
}

// putEnvironmentToolArgument keeps an unset flag out of the call so the
// environment applies its own default rather than receiving a zero that means
// something else.
func putEnvironmentToolArgument(arguments map[string]any, name string, value any) {
	if isZeroToolArgument(value) {
		return
	}
	arguments[name] = value
}

// isZeroToolArgument reports whether value is the zero form of one of the
// tool-argument types put on the wire — the form that means "unset" rather
// than a value the environment should receive.
func isZeroToolArgument(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed == ""
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case []string:
		return len(typed) == 0
	case map[string]string:
		return len(typed) == 0
	}
	return false
}

// leaseTTLSeconds converts a flag's duration to the seconds the environment's
// tools take. Zero stays zero so the environment applies its own default.
func leaseTTLSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return int64(ttl.Seconds())
}
