package envcall

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// mcpChannelReattachTimeout covers a port-forward dial plus readiness check, not a cold start.
const mcpChannelReattachTimeout = 60 * time.Second

// ReattachEnvironmentMCPChannel runs the `erun open --reconnect` path, which refuses
// rather than restarting a deliberately stopped environment.
func ReattachEnvironmentMCPChannel(commandCtx core.Context, tenant, environment string) error {
	executable, err := ErunExecutablePath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpChannelReattachTimeout)
	defer cancel()
	commandCtx.Trace(fmt.Sprintf("mcp: %s/%s channel unreachable, reattaching with `erun open --reconnect`", tenant, environment))
	cmd := core.CommandContext(ctx, executable, "open", tenant, environment, "--reconnect", "--no-shell", "--no-alias-prompt")
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
