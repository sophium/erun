package outputs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/codesign"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/spf13/cobra"
)

func NewOutputsCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	return clicmd.NewCommandGroup(
		"outputs",
		"List and download files an agent produced in the runtime pod",
		newOutputsListCmd(resolveOpen),
		newOutputsDownloadCmd(resolveOpen),
	)
}

func newOutputsListCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant, environment, dirPath string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List files an agent produced in the runtime pod's outputs directory",
		Long: "List the files and folders an agent (Claude/Codex) wrote to the runtime pod's " +
			"outputs directory, newest first.\n\n" +
			"The outputs directory is the canonical place agents and skills drop deliverables " +
			"(reports, generated artifacts, logs) for you to pull out with `erun outputs download`. " +
			"It defaults to the pod's $ERUN_OUTPUTS_DIR (/home/erun/.erun/outputs); pass --path to " +
			"list another directory. This is read-only.",
		Example:       "  erun outputs list\n  erun outputs list --environment prod --limit 20",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOutputsListCommand(clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment), dirPath, limit, kube.RunRemoteCommand)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addOutputsScopeFlags(cmd, &tenant, &environment, &dirPath)
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of entries to list (newest-first); 0 lists all")
	return cmd
}

func newOutputsDownloadCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant, environment, dirPath, dest string
	var force bool
	cmd := &cobra.Command{
		Use:   "download <name>",
		Short: "Download a file or folder an agent produced in the runtime pod",
		Long: "Download one entry from the runtime pod's outputs directory onto this machine.\n\n" +
			"A file lands as-is; a folder lands as a <name>.tar.gz archive. The source defaults to " +
			"the pod's $ERUN_OUTPUTS_DIR (/home/erun/.erun/outputs); pass --path to download from " +
			"another directory. Use --dest to choose where it lands locally (default: the current " +
			"directory) and --force to overwrite an existing local file.\n\n" +
			"On macOS an arriving macOS binary that carries no code signature is ad-hoc signed and made " +
			"executable, because the system kills an unsigned one on exec without printing anything.",
		Example:       "  erun outputs download report.pdf\n  erun outputs download results --dest ./out",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOutputsDownloadCommand(clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment), dirPath, args[0], dest, force, kube.RunRemoteCommand)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addOutputsScopeFlags(cmd, &tenant, &environment, &dirPath)
	cmd.Flags().StringVar(&dest, "dest", "", "Local destination file or directory (default: the current directory)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite the local destination if it already exists")
	return cmd
}

func addOutputsScopeFlags(cmd *cobra.Command, tenant, environment, dirPath *string) {
	cmd.Flags().StringVar(tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(environment, "environment", "", "Target a specific environment; requires --tenant")
	cmd.Flags().StringVar(dirPath, "path", "", "Pod directory to operate on (default: $ERUN_OUTPUTS_DIR, /home/erun/.erun/outputs)")
}

func runOutputsListCommand(ctx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams, dirPath string, limit int, run seam.RuntimeOutputsRunner) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := cloud.ShellLaunchParamsFromResult(result)
	list, err := jobs.ResolveRuntimeOutputs(ctx, req, model.RuntimeOutputsParams{Dir: dirPath, Limit: limit}, run)
	if err != nil {
		return err
	}
	if ctx.Output == model.OutputJSON {
		return ctx.WriteResult(list)
	}
	if ctx.DryRun {
		return nil
	}
	return writeOutputsListText(ctx, list)
}

func writeOutputsListText(ctx core.Context, list model.RuntimeOutputsListResult) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Outputs in %s:\n", list.Dir); err != nil {
		return err
	}
	if len(list.Entries) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  (none)")
		return err
	}
	for _, entry := range list.Entries {
		kind := "file"
		if entry.IsDir {
			kind = "dir"
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s  %s  %s  %s\n", entry.Name, textfmt.FormatOutputSize(entry.Size), entry.ModTime.UTC().Format(time.RFC3339), kind); err != nil {
			return err
		}
	}
	if list.Truncated {
		if _, err := fmt.Fprintf(ctx.Stdout, "  ... %d more (raise --limit to see all)\n", list.Total-len(list.Entries)); err != nil {
			return err
		}
	}
	return nil
}

type outputsDownloadCLIResult struct {
	Name          string `json:"name"`
	Dest          string `json:"dest"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	IsArchive     bool   `json:"isArchive"`
	ArchiveFormat string `json:"archiveFormat,omitempty"`
	// Signing is set only when host-side ad-hoc signing had something to report.
	Signing *model.HostArtifactSigning `json:"signing,omitempty"`
}

func runOutputsDownloadCommand(ctx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams, dirPath, name, dest string, force bool, run seam.RuntimeOutputsRunner) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := cloud.ShellLaunchParamsFromResult(result)
	out, err := jobs.DownloadRuntimeOutput(ctx, req, model.RuntimeOutputDownloadParams{Dir: dirPath, Name: name}, run)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		ctx.Trace("outputs: would write " + resolveOutputsDest(dest, out.Name))
		return nil
	}
	destPath, err := writeOutputDownloadToDisk(out, dest, force)
	if err != nil {
		return err
	}
	// A darwin binary gets signed here, where it becomes runnable; signing problems are reported, never fatal.
	signing := codesign.SignHostArtifact(destPath)
	if ctx.Output == model.OutputJSON {
		result := outputsDownloadCLIResult{
			Name:          out.Name,
			Dest:          destPath,
			Size:          out.Size,
			SHA256:        out.SHA256,
			IsArchive:     out.IsArchive,
			ArchiveFormat: out.ArchiveFormat,
		}
		if signing.Describe() != "" {
			result.Signing = &signing
		}
		return ctx.WriteResult(result)
	}
	ctx.Info(fmt.Sprintf("Downloaded %s (%s, sha256 %s) to %s", out.Name, textfmt.FormatOutputSize(out.Size), out.SHA256, destPath))
	if note := signing.Describe(); note != "" {
		ctx.Info(note)
	}
	return nil
}

// resolveOutputsDest keeps only the base name so a download cannot escape dest.
func resolveOutputsDest(dest, name string) string {
	name = filepath.Base(filepath.FromSlash(name))
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return name
	}
	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		return filepath.Join(dest, name)
	}
	return dest
}

func writeOutputDownloadToDisk(out model.RuntimeOutputResult, dest string, force bool) (string, error) {
	destPath := resolveOutputsDest(dest, out.Name)
	if !force {
		if _, err := os.Stat(destPath); err == nil {
			return "", fmt.Errorf("destination already exists: %s (use --force to overwrite)", destPath)
		}
	}
	if dir := filepath.Dir(destPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(destPath, out.Bytes, 0o644); err != nil {
		return "", err
	}
	return destPath, nil
}
