package adopt

import (
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/spf13/cobra"
)

// NewAdoptWorktreeCmd is run by the runtime chart's adopt-worktree init
// container, never by an operator: it is the one moment the home volume and
// the worktree claim are both visible off the live /home/erun path.
func NewAdoptWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt-worktree LEGACY_DIR CLAIM_DIR",
		Short: "Move a checkout from the home volume onto an empty worktree volume (runtime init container)",
		Long: "Moves an environment's existing repository onto its dedicated worktree volume before\n" +
			"the pod mounts that volume over it. The runtime chart's init container runs it with\n" +
			"both volumes staged side by side under /mnt, where the claim cannot shadow the tree.\n\n" +
			"The tree is copied into the claim first and the original is set aside at\n" +
			"LEGACY_DIR.pre-worktree-volume only once the copy holds .git; it is never deleted.\n" +
			"A populated claim, a missing or symlinked LEGACY_DIR, or one that is not a\n" +
			"repository is left untouched, so every later boot is a no-op.",
		Example: "  erun adopt-worktree /mnt/erun-home/git/petios /mnt/erun-worktree\n" +
			"  erun adopt-worktree --dry-run /mnt/erun-home/git/petios /mnt/erun-worktree",
		Args:          cobra.ExactArgs(2),
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdoptWorktreeCommand(shared.CommandContext(cmd), args[0], args[1])
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runAdoptWorktreeCommand(ctx core.Context, legacyDir, claimDir string) error {
	result, err := podruntime.AdoptWorktree(ctx, podruntime.WorktreeAdoptParams{LegacyDir: legacyDir, ClaimDir: claimDir})
	if err != nil {
		return err
	}
	return ctx.WriteResult(result)
}
