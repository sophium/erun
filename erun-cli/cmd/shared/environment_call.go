package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// Off-environment, idle/job/activity-lease verbs must go to the environment's edge;
// the local store would answer about the wrong machine.

// EnvironmentTargetsItself reports whether this process is the target environment.
func EnvironmentTargetsItself() bool {
	return config.IsInRuntimeEnvironment(nil)
}

// CallEnvironmentTool runs one tool on the environment's MCP edge; the bool reports whether a call was made.
// idleProbe exempts the request from activity accounting, so set it only for read-only tools.
func CallEnvironmentTool[T any](ctx context.Context, commandCtx core.Context, resolveOpen OpenResolver, tenant, environment, tool string, arguments map[string]any, idleProbe bool) (T, bool, error) {
	var decoded T
	if arguments == nil {
		arguments = map[string]any{}
	}
	target, err := ResolveMCPEdgeTarget(commandCtx, resolveOpen, ScopedOpenParams(commandCtx.Command, tenant, environment))
	if err != nil {
		return decoded, false, err
	}
	// Naming the target explicitly makes a stale edge refuse instead of acting on the wrong environment.
	arguments["tenant"] = target.Tenant
	arguments["environment"] = target.Environment
	commandCtx.TraceCommand("", "mcp", "tools/call", target.Endpoint, tool, CompactMCPArguments(arguments))
	if commandCtx.DryRun {
		return decoded, false, nil
	}
	result, err := CallMCPToolWithReattach(ctx, commandCtx, target, tool, arguments, idleProbe)
	if err != nil {
		return decoded, false, McpEdgeErrorWithExitCode(target, err)
	}
	// An empty payload is not an empty answer.
	if len(result.Structured) == 0 {
		return decoded, false, fmt.Errorf("%s/%s returned no result for %s", target.Tenant, target.Environment, tool)
	}
	if err := json.Unmarshal(result.Structured, &decoded); err != nil {
		return decoded, false, fmt.Errorf("decode the %s result from %s/%s: %w", tool, target.Tenant, target.Environment, err)
	}
	return decoded, true, nil
}

// PutEnvironmentToolArgument omits unset values so the environment applies its own default.
func PutEnvironmentToolArgument(arguments map[string]any, name string, value any) {
	if isZeroToolArgument(value) {
		return
	}
	arguments[name] = value
}

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
	case bool:
		return !typed
	}
	return false
}

// LeaseTTLSeconds converts a TTL to seconds; zero lets the environment apply its default.
func LeaseTTLSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return int64(ttl.Seconds())
}
