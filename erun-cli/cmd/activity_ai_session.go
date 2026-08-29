package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// The ai-session verbs are the CLI half of the structured AI-session status
// model: a tool's own turn-boundary hooks report what state they are in
// (turn-start/tool-use/turn-end/notify/exit), replacing a guess made from PTY
// output volume. report is the write side a hook command invokes; status is
// the read side that resolves the current state from the last report.

func newActivityAISessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ai-session",
		Short:         "Report and inspect structured AI tool session status",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(newActivityAISessionReportCmd(), newActivityAISessionStatusCmd())
	return cmd
}

func newActivityAISessionReportCmd() *cobra.Command {
	var tenant string
	var environment string
	var sessionID string
	var tool string
	var event string
	var exitCode int
	var exitReason string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Record an AI tool session's own turn-boundary event",
		Long: "Record the state an AI tool reports about itself at a turn boundary, so the\n" +
			"environment's AI-session status reflects what the tool said rather than a\n" +
			"guess from output volume or silence.\n\n" +
			"--event maps to a tool's own hook events: turn-start (control passed to the\n" +
			"tool), tool-use (still working mid-turn), turn-end (control returned to the\n" +
			"human -- reads as awaiting-input), notify (blocked on a permission or a\n" +
			"question mid-turn -- also awaiting-input), and exit (the process ended; pass\n" +
			"--exit-reason oom when the exit was an out-of-memory kill).",
		Example: "  # Wire into Claude Code hooks: report a turn ending so the session reads as\n" +
			"  # awaiting-input instead of merely quiet.\n" +
			"  erun activity ai-session report --tenant team --environment dev \\\n" +
			"    --session \"$CLAUDE_SESSION_ID\" --tool claude --event turn-end",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := common.AISessionEventParams{
				Tenant:      tenant,
				Environment: environment,
				SessionID:   sessionID,
				Tool:        tool,
				Event:       common.AISessionEventKind(strings.TrimSpace(event)),
				ExitReason:  exitReason,
			}
			if cmd.Flags().Changed("exit-code") {
				params.ExitCode = &exitCode
			}
			return common.RecordAISessionEvent(params)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&sessionID, "session", "", "AI session id (the tool's own session/conversation id)")
	cmd.Flags().StringVar(&tool, "tool", "", "AI tool name, e.g. claude or codex")
	cmd.Flags().StringVar(&event, "event", "", "Event kind: turn-start, tool-use, turn-end, notify, or exit")
	cmd.Flags().IntVar(&exitCode, "exit-code", 0, "Process exit code (only meaningful with --event exit)")
	cmd.Flags().StringVar(&exitReason, "exit-reason", "", "Exit reason; pass 'oom' when the process was killed by an out-of-memory event")
	return cmd
}

func newActivityAISessionStatusCmd() *cobra.Command {
	var tenant string
	var environment string
	var sessionID string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Resolve the current status of one or every AI tool session",
		Long: "Resolve the current AI-session state (idle, busy, awaiting-input, exited, or\n" +
			"oom-killed) from the last event reported for the session -- never from output\n" +
			"silence. Pass --session for one session; omit it to list every session\n" +
			"recorded for the environment.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityAISessionStatus(cmd, tenant, environment, sessionID, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&sessionID, "session", "", "AI session id to resolve; omit to list every recorded session")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output")
	return cmd
}

func runActivityAISessionStatus(cmd *cobra.Command, tenant, environment, sessionID string, jsonOutput bool) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		status, err := common.LoadAISessionStatus(tenant, environment, sessionID)
		if err != nil {
			return err
		}
		return writeAISessionStatuses(cmd, []common.AISessionStatus{status}, jsonOutput)
	}
	statuses, err := common.LoadAISessionStatuses(tenant, environment)
	if err != nil {
		return err
	}
	return writeAISessionStatuses(cmd, statuses, jsonOutput)
}

func writeAISessionStatuses(cmd *cobra.Command, statuses []common.AISessionStatus, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(commandContext(cmd).Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(statuses)
	}
	ctx := commandContext(cmd)
	if len(statuses) == 0 {
		return writeLabeledValue(ctx, "ai sessions", "none recorded")
	}
	for _, status := range statuses {
		value := string(status.State)
		if strings.TrimSpace(status.Tool) != "" {
			value = status.Tool + ": " + value
		}
		if strings.TrimSpace(status.Reason) != "" {
			value += fmt.Sprintf(" (%s)", status.Reason)
		}
		if err := writeLabeledValue(ctx, status.SessionID, value); err != nil {
			return err
		}
	}
	return nil
}
