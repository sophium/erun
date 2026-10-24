package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func (b runtimeBoot) mcpURL() string {
	return "http://127.0.0.1:" + b.getOr("ERUN_MCP_PORT", "17000") + b.getOr("ERUN_MCP_PATH", "/mcp")
}

func describeRuntimeAgentConfig(boot runtimeBoot) string {
	return "configure Codex (" + boot.path(".codex") + ") and Claude Code (" + boot.path(".claude") + "): instructions, skills from " + boot.skillsRoot + ", agents from " + boot.agentsRoot + ", the erun MCP server at " + boot.mcpURL()
}

// configureRuntimeAgents reconciles both assistants' configuration with the
// image's baked skills and agents and the container's environment. It runs once
// per container boot: nothing it reads changes while the container lives.
func configureRuntimeAgents(ctx core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	removeLegacyRuntimeConfigureScripts(boot)
	// Each part stands alone: a skill that cannot be copied must not leave the
	// assistants without their MCP server or settings.
	var errs []error
	if err := EnsureGlobalAgentInstructions(boot.home); err != nil {
		errs = append(errs, fmt.Errorf("agent instructions: %w", err))
	}
	if err := installRuntimeAgentAssets(ctx, boot, RuntimeAgentAssetTargetsFor(boot.home)); err != nil {
		errs = append(errs, err)
	}
	if err := rewriteRuntimeCodexConfig(boot.path(".codex", "config.toml"), boot.mcpURL()); err != nil {
		errs = append(errs, fmt.Errorf("codex config: %w", err))
	}
	if err := configureRuntimeClaude(boot); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// RuntimeAgentAssetTargets are the discovery directories the runtime pod installs
// into: skills for both assistants, reusable agents for Claude Code only (Codex
// has no equivalent).
type RuntimeAgentAssetTargets struct {
	SkillDirs []string
	AgentDirs []string
}

// RuntimeAgentAssetTargetsFor lists the discovery directories under homeDir.
func RuntimeAgentAssetTargetsFor(homeDir string) RuntimeAgentAssetTargets {
	return RuntimeAgentAssetTargets{
		SkillDirs: []string{filepath.Join(homeDir, ".claude", "skills"), filepath.Join(homeDir, ".codex", "skills")},
		AgentDirs: []string{filepath.Join(homeDir, ".claude", "agents")},
	}
}

func installRuntimeAgentAssets(ctx core.Context, boot runtimeBoot, targets RuntimeAgentAssetTargets) error {
	_, err := InstallRuntimeAgentAssets(ctx, boot.skillsRoot, boot.agentsRoot, targets)
	return err
}

// RuntimeAgentAssetsInstall is what one install of the baked skills and agents
// did, per discovery directory, in install order.
type RuntimeAgentAssetsInstall struct {
	Targets []RuntimeAgentAssetsTarget `json:"targets"`
}

// RuntimeAgentAssetsTarget is one discovery directory's outcome.
type RuntimeAgentAssetsTarget struct {
	Kind string `json:"kind"`
	Dir  string `json:"dir"`
	AgentAssetsInstallResult
}

// InstallRuntimeAgentAssets installs or refreshes the skills under skillsRoot and
// the agents under agentsRoot into every target directory. A source root that
// does not exist has nothing to install. Under ctx.DryRun it only traces.
func InstallRuntimeAgentAssets(ctx core.Context, skillsRoot, agentsRoot string, targets RuntimeAgentAssetTargets) (RuntimeAgentAssetsInstall, error) {
	result := RuntimeAgentAssetsInstall{Targets: []RuntimeAgentAssetsTarget{}}
	install := func(kind, source string, dests []string, apply func(string, string) (AgentAssetsInstallResult, error)) error {
		if info, err := os.Stat(source); err != nil || !info.IsDir() {
			return nil
		}
		for _, dest := range dests {
			ctx.TraceCommand("", "install-or-refresh", source, dest)
			if ctx.DryRun {
				continue
			}
			installed, err := apply(source, dest)
			if err != nil {
				return err
			}
			result.Targets = append(result.Targets, RuntimeAgentAssetsTarget{Kind: kind, Dir: dest, AgentAssetsInstallResult: installed})
		}
		return nil
	}
	if err := install("skills", skillsRoot, targets.SkillDirs, InstallOrRefreshSkills); err != nil {
		return result, err
	}
	return result, install("agents", agentsRoot, targets.AgentDirs, InstallOrRefreshAgents)
}

// removeLegacyRuntimeConfigureScripts deletes the configure scripts older images
// wrote to the home volume; they call helpers this image no longer has.
func removeLegacyRuntimeConfigureScripts(boot runtimeBoot) {
	for _, name := range []string{"configure-codex-mcp.sh", "configure-claude-code.sh"} {
		_ = os.Remove(boot.path(".erun", name))
	}
}

const runtimeCodexMCPSection = "[mcp_servers.erun]"

// rewriteRuntimeCodexConfig gives Codex full access without approval prompts
// (the pod is the sandbox) and the erun MCP server, keeping everything else in
// config.toml. The policy keys go before the first table, where TOML requires
// top-level keys; erun's own MCP table is replaced, never duplicated, so a
// reboot rewrites the file to the same bytes.
func rewriteRuntimeCodexConfig(path, mcpURL string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	kept := withoutRuntimeCodexSettings(string(existing))
	firstTable := len(kept)
	for i, line := range kept {
		if strings.HasPrefix(line, "[") {
			firstTable = i
			break
		}
	}
	var out []string
	if head := trimRuntimeBlankLines(kept[:firstTable]); len(head) > 0 {
		out = append(append(out, head...), "")
	}
	out = append(out, `sandbox_mode = "danger-full-access"`, `approval_policy = "on-request"`, "")
	if tail := trimRuntimeBlankLines(kept[firstTable:]); len(tail) > 0 {
		out = append(append(out, tail...), "")
	}
	out = append(out, runtimeCodexMCPSection, fmt.Sprintf("url = %q", mcpURL), "tool_timeout_sec = 600")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return config.WriteFileAtomic(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// withoutRuntimeCodexSettings drops the lines erun owns -- the two policy keys
// and its own MCP table -- keeping everything else in order.
func withoutRuntimeCodexSettings(content string) []string {
	var kept []string
	skipping := false
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "[") {
			skipping = strings.TrimSpace(line) == runtimeCodexMCPSection
		}
		if skipping || strings.HasPrefix(line, "sandbox_mode = ") || strings.HasPrefix(line, "approval_policy = ") {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

func trimRuntimeBlankLines(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}
