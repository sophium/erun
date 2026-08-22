package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// mcpChannelReattachTimeout bounds an automatic reattach. `erun open
// --reconnect` only re-establishes port-forwards against an already-running
// deployment (see common.DecideRuntimeWake) -- no cluster bootstrap, no image
// pull -- so this only needs to cover a kubectl port-forward dial plus a
// deployment-ready check, not a cold start.
const mcpChannelReattachTimeout = 60 * time.Second

// reattachEnvironmentMCPChannel re-establishes a dropped or stale MCP
// port-forward the same way `erun open <tenant> <environment> --reconnect`
// does, so an automatic caller (job status, job await, mcp call) can recover
// from a channel drop without the trap a naive retry loop falls into: a bare
// `erun open` would silently start an environment the operator deliberately
// stopped and clear the recorded stop, where --reconnect refuses instead.
func reattachEnvironmentMCPChannel(commandCtx common.Context, tenant, environment string) error {
	executable, err := erunExecutablePath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpChannelReattachTimeout)
	defer cancel()
	commandCtx.Trace(fmt.Sprintf("mcp: %s/%s channel unreachable, reattaching with `erun open --reconnect`", tenant, environment))
	cmd := common.CommandContext(ctx, executable, "open", tenant, environment, "--reconnect", "--no-shell", "--no-alias-prompt")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(output.String()); message != "" {
			return fmt.Errorf("reattach %s/%s: %w: %s", tenant, environment, err, message)
		}
		return fmt.Errorf("reattach %s/%s: %w", tenant, environment, err)
	}
	return nil
}
