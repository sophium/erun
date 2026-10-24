package podruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

const runtimeMCPRestartDelay = 2 * time.Second

// runtimeMCPEnabled reports whether this container serves the environment's MCP
// edge. The chart sets ERUN_MCP_ENABLED so an operator can turn the edge off; a
// chart that predates it leaves it unset, and a configured port then keeps an
// existing environment serving.
func (b runtimeBoot) runtimeMCPEnabled() bool {
	value := b.get("ERUN_MCP_ENABLED")
	if runtimeEnvFlagEnabled(value) {
		return true
	}
	if runtimeEnvFlagDisabled(value) {
		return false
	}
	return b.get("ERUN_MCP_PORT") != ""
}

func (b runtimeBoot) namespace() string {
	if namespace := b.get("ERUN_NAMESPACE"); namespace != "" {
		return namespace
	}
	data, err := os.ReadFile(filepath.Join(b.serviceAccountDir, "namespace"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// runtimeMCPArgs is the emcp invocation. The bool flag is written
// --metrics-enabled=<value>: a space-separated value would end Go's flag parsing
// at the bare "true" and silently drop every flag after it.
func (b runtimeBoot) runtimeMCPArgs() []string {
	args := []string{
		"--host", b.getOr("ERUN_MCP_HOST", "0.0.0.0"),
		"--port", b.getOr("ERUN_MCP_PORT", "17000"),
		"--path", b.getOr("ERUN_MCP_PATH", "/mcp"),
		"--metrics-host", b.getOr("ERUN_METRICS_HOST", "0.0.0.0"),
		"--metrics-port", b.getOr("ERUN_METRICS_PORT", "9100"),
		"--metrics-enabled=" + b.getOr("ERUN_METRICS_ENABLED", "true"),
		"--tenant", b.tenant(),
		"--environment", b.environment(),
		"--repo-path", b.repoDir(),
		"--kubernetes-context", b.kubernetesContext(),
	}
	if namespace := b.namespace(); namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	return args
}

func (b runtimeBoot) runtimeMCPStartLine() string {
	return fmt.Sprintf("starting erun MCP on %s:%s%s, metrics on %s:%s (enabled=%s)",
		b.getOr("ERUN_MCP_HOST", "0.0.0.0"), b.getOr("ERUN_MCP_PORT", "17000"), b.getOr("ERUN_MCP_PATH", "/mcp"),
		b.getOr("ERUN_METRICS_HOST", "0.0.0.0"), b.getOr("ERUN_METRICS_PORT", "9100"), b.getOr("ERUN_METRICS_ENABLED", "true"))
}

func describeRuntimeMCP(boot runtimeBoot) string {
	if !boot.runtimeMCPEnabled() {
		return ""
	}
	return "serve the MCP edge, restarting it whenever it exits: " + core.FormatShellCommand("", "emcp", boot.runtimeMCPArgs()...)
}

// startRuntimeMCP serves the environment's MCP edge from the runtime container
// itself, so every MCP-driven command runs with the environment's own toolchain.
// A crashed server is restarted and each restart is logged, so a crash loop stays
// visible in the container log.
func startRuntimeMCP(ctx core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	attempt := 1
	supervisor.goLoop(func() bool {
		ctx.Info(boot.runtimeMCPStartLine())
		cmd := core.Command("emcp", boot.runtimeMCPArgs()...)
		cmd.Stdout, cmd.Stderr = ctx.Stdout, ctx.Stderr
		_ = supervisor.run(cmd)
		if supervisor.stopping() {
			return false
		}
		attempt++
		ctx.Info(fmt.Sprintf("erun MCP exited; restarting (attempt %d)", attempt))
		return supervisor.sleep(runtimeMCPRestartDelay)
	})
	return nil
}
