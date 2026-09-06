package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// buildProfileCommandName is the timing-record command prefix `erun build`
// writes under ~/.erun/timing/ (timingRecordFileName in erun-common/timing.go).
const buildProfileCommandName = "build"

func newBuildProfileCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "profile [BUILD_ID]",
		Short: "Show where a past build spent its time, CPU, and I/O",
		Long: "Show the per-step breakdown erun build already records to ~/.erun/timing/ — " +
			"duration, CPU seconds against the build's cgroup quota, throttled periods, and " +
			"disk I/O for each step, sorted by cost.\n\n" +
			"With no BUILD_ID, lists recent builds newest-first; pass one back (or \"latest\") " +
			"to see its full step tree. CPU, throttling, and I/O are only available for steps " +
			"that ran inside a runtime pod with the erun-dind sidecar; an older runtime image " +
			"or a bare host build reports duration only.",
		Example:       "  erun build profile\n  erun build profile latest\n  erun build profile build-20260115T101500.000000000Z",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) > 0 {
				id = args[0]
			}
			return runBuildProfileCommand(commandContext(cmd), id, limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of recent builds to list (newest-first); 0 lists all retained records")
	return cmd
}

func runBuildProfileCommand(ctx common.Context, id string, limit int) error {
	if id == "" {
		summaries, err := common.ListTimingRecords(buildProfileCommandName, limit)
		if err != nil {
			return err
		}
		if ctx.Output == common.OutputJSON {
			return ctx.WriteResult(summaries)
		}
		return writeBuildProfileListText(ctx, summaries)
	}
	record, err := common.LoadTimingRecord(buildProfileCommandName, id)
	if err != nil {
		return err
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(record)
	}
	return writeBuildProfileDetailText(ctx, record)
}

func writeBuildProfileListText(ctx common.Context, summaries []common.TimingRecordSummary) error {
	if len(summaries) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "No recorded builds found in ~/.erun/timing.")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Recent builds (newest first):"); err != nil {
		return err
	}
	for _, s := range summaries {
		status := "ok"
		if s.Failed {
			status = "failed"
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s  %s  %.1fs  %s\n", s.ID, s.StartedAt, s.DurationSeconds, status); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(ctx.Stdout, "\nRun `erun build profile <BUILD_ID>` to see the step tree.")
	return err
}

func writeBuildProfileDetailText(ctx common.Context, record common.TimingRecord) error {
	for _, row := range common.RenderTimingRecordRows(record) {
		if _, err := fmt.Fprintln(ctx.Stdout, row); err != nil {
			return err
		}
	}
	return nil
}
