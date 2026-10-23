package cmd

import (
	"fmt"
	"os"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

// newRuntimeInitCmd is the erun-devops image's ENTRYPOINT: the init of the
// runtime container, never something an operator runs.
func newRuntimeInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime-init [devops | COMMAND [ARGS...]]",
		Short: "Boot the runtime container as its init (the erun-devops image's entrypoint)",
		Long: "Prepares the runtime container from the environment the runtime chart injects and\n" +
			"then runs as its init: it reaps orphaned processes, forwards termination signals to\n" +
			"what it started, and exits with the code of the work it was running.\n\n" +
			"Every mode writes the kubeconfig for the pod's service account, restores private modes\n" +
			"under ~/.ssh, creates the outputs directory, adds the provisioned registry credential\n" +
			"to ~/.docker/config.json where it has none, starts sshd and its activity proxy when\n" +
			"ERUN_SSHD_ENABLED is set, and starts the activity monitor (which stops a cloud host\n" +
			"once its idle policy fires).\n\n" +
			"devops (the default) then bounds the go build cache, clones mounted source or links\n" +
			"the baked release tree, writes the erun config and the platform alias, configures\n" +
			"Codex and Claude Code (instructions, skills, agents, settings, MCP server), installs\n" +
			"the shell hook, serves the MCP edge, and keeps running until the container is stopped.\n" +
			"Any other COMMAND runs after the common preparation, and the container exits with its\n" +
			"code. Runs only on Linux, as the container's init; --dry-run lists what this container\n" +
			"would do without changing anything.",
		Example: "  erun runtime-init --dry-run\n" +
			"  erun runtime-init devops\n" +
			"  erun runtime-init -- bash -lc 'erun doctor'",
		Args:          cobra.ArbitraryArgs,
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRuntimeInitCommand(commandContext(cmd), args)
		},
	}
	// Everything after COMMAND belongs to it, not to erun.
	cmd.Flags().SetInterspersed(false)
	addDryRunFlag(cmd)
	return cmd
}

func runRuntimeInitCommand(ctx common.Context, args []string) error {
	erunPath, err := os.Executable()
	if err != nil {
		erunPath = "erun"
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve the runtime container's home directory: %w", err)
	}
	code, err := common.RunRuntimeEntrypoint(ctx, common.RuntimeEntrypointParams{
		Command:  args,
		ERunPath: erunPath,
		HomeDir:  homeDir,
	})
	if err != nil {
		return internal.WithExitCode(err, code)
	}
	if code != 0 {
		// The container's command already reported its own failure; only its
		// exit code is the entrypoint's to pass on.
		return internal.MarkReported(internal.WithExitCode(fmt.Errorf("exit status %d", code), code))
	}
	return nil
}

// newTrimCacheCmd bounds the go build cache. The runtime entrypoint runs the same
// trim in-process every half hour; this is the manual form.
func newTrimCacheCmd() *cobra.Command {
	var maxBytes int64
	cmd := &cobra.Command{
		Use:   "trim-cache [DIR]",
		Short: "Bound the go build cache by size, evicting its least recently used entries",
		Long: "Brings the go build cache under a byte cap by deleting the cache entries that have\n" +
			"gone longest unused, so the entries the next build wants stay and the build stays\n" +
			"warm. Only go's own entries are removed; the cache's bookkeeping files are kept, and a\n" +
			"cache already under its cap is left untouched.\n\n" +
			"DIR defaults to GOCACHE, else ~/.cache/go-build. The cap defaults to\n" +
			"ERUN_GO_BUILD_CACHE_MAX_BYTES, the value the runtime chart renders (0 means no bound),\n" +
			"else 16 GiB. --dry-run reports how many entries would go without deleting any.",
		Example: "  erun trim-cache --dry-run\n" +
			"  erun trim-cache --max-bytes 8589934592 ~/.cache/go-build",
		Args:          cobra.MaximumNArgs(1),
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrimCacheCommand(commandContext(cmd), args, maxBytes, cmd.Flags().Changed("max-bytes"))
		},
	}
	cmd.Flags().Int64Var(&maxBytes, "max-bytes", 0, "Cap in bytes (default: ERUN_GO_BUILD_CACHE_MAX_BYTES, else 16 GiB)")
	addDryRunFlag(cmd)
	return cmd
}

func runTrimCacheCommand(ctx common.Context, args []string, maxBytes int64, maxBytesSet bool) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve the home directory holding the go build cache: %w", err)
	}
	dir, configured, _ := common.RuntimeGoBuildCacheTrimSettings(os.Getenv, homeDir)
	if len(args) == 1 {
		dir = args[0]
	}
	if !maxBytesSet {
		if configured <= 0 {
			ctx.Info("erun: ERUN_GO_BUILD_CACHE_MAX_BYTES sets no bound for the go build cache; pass --max-bytes to trim it anyway")
			return nil
		}
		maxBytes = configured
	}
	result, err := common.TrimGoBuildCache(ctx, dir, maxBytes)
	if err != nil {
		return err
	}
	return ctx.WriteResult(result)
}

// newInstallSkillsCmd installs erun's skills and reusable agents into the
// assistants' discovery directories; the runtime entrypoint does the same at
// every boot.
func newInstallSkillsCmd() *cobra.Command {
	var skillsDir, agentsDir string
	cmd := &cobra.Command{
		Use:   "install-skills",
		Short: "Install or refresh erun's skills and agents for Claude Code and Codex",
		Long: "Installs every skill under --skills-dir into ~/.claude/skills and ~/.codex/skills, and\n" +
			"every agent under --agents-dir into ~/.claude/agents. A missing skill or agent is\n" +
			"installed, one erun installed earlier is refreshed when its source changed, and one\n" +
			"edited in place is preserved: a marker beside each install records the source it was\n" +
			"copied from. The defaults are the copies the runtime image bakes.",
		Example: "  erun install-skills\n" +
			"  erun install-skills --skills-dir erun-skills/skills --agents-dir erun-skills/agents --dry-run",
		Args:          cobra.NoArgs,
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstallSkillsCommand(commandContext(cmd), skillsDir, agentsDir)
		},
	}
	cmd.Flags().StringVar(&skillsDir, "skills-dir", common.RuntimeBakedSkillsDir, "Directory holding one subdirectory per skill")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", common.RuntimeBakedAgentsDir, "Directory holding one <name>.md per agent")
	addDryRunFlag(cmd)
	return cmd
}

func runInstallSkillsCommand(ctx common.Context, skillsDir, agentsDir string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve the home directory to install skills into: %w", err)
	}
	result, err := common.InstallRuntimeAgentAssets(ctx, skillsDir, agentsDir, common.RuntimeAgentAssetTargetsFor(homeDir))
	if err != nil {
		return err
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(result)
	}
	for _, target := range result.Targets {
		ctx.Info(fmt.Sprintf("%s -> %s: %s", target.Kind, target.Dir, formatAgentAssetsInstall(target.AgentAssetsInstallResult)))
	}
	return nil
}

func formatAgentAssetsInstall(result common.AgentAssetsInstallResult) string {
	parts := []string{
		fmt.Sprintf("%d installed", len(result.Installed)),
		fmt.Sprintf("%d refreshed", len(result.Refreshed)),
		fmt.Sprintf("%d current", len(result.Current)),
		fmt.Sprintf("%d preserved", len(result.Preserved)),
	}
	if len(result.Preserved) > 0 {
		parts[3] += " (" + strings.Join(result.Preserved, ", ") + ")"
	}
	return strings.Join(parts, ", ")
}
