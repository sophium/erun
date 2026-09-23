package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	cmd.AddCommand(newActivityAISessionReportCmd(), newActivityAISessionHookCmd(), newActivityAISessionStatusCmd())
	return cmd
}

// newActivityAISessionHookCmd is the entry point an installed hook invokes: the
// AI tool runs it at a turn boundary with its own event payload on stdin, and
// the command records the model event the settings bound it to. It exists
// separately from `report` because a hook is never given a session id as an
// argv token -- the tool tells it, on stdin, which conversation it is -- while
// `report` addresses a session the caller already knows.
func newActivityAISessionHookCmd() *cobra.Command {
	var tenant string
	var environment string
	var tool string
	var event string
	var sessionID string
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Record a turn-boundary event from an AI tool's own hook payload",
		Long:  "Record the state an AI tool reports about itself, read from the hook payload\nthe tool writes to stdin. --event names the model event this invocation reports\nand is chosen by whichever hook the settings installed the command on, not by\nthe payload: turn-start, tool-use, turn-end (control returned to the human --\nreads as awaiting-input), notify (blocked on a permission or a question mid-turn\n-- also awaiting-input), or exit.\n\nAn installed hook needs no flags beyond --event and --tool: the environment is\ntaken from ERUN_TENANT and ERUN_ENVIRONMENT, which a runtime pod already sets.\nA hook invocation that can resolve neither refuses rather than recording the\nevent against an environment it guessed.",
		Example: "  # What a settings entry installs (Claude Code's Stop hook):\n" +
			"  erun activity ai-session hook --event turn-end --tool claude\n\n" +
			"  # By hand, against one environment:\n" +
			"  echo '{\"session_id\":\"5f2c\"}' | erun activity ai-session hook \\\n" +
			"    --event turn-end --tool claude --tenant team --environment dev",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityAISessionHook(cmd, tenant, environment, tool, strings.TrimSpace(event), sessionID)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&tool, "tool", "", "AI tool name, e.g. claude or codex")
	cmd.Flags().StringVar(&event, "event", "", "Event kind this hook reports: turn-start, tool-use, turn-end, notify, or exit")
	cmd.Flags().StringVar(&sessionID, "session", "", "AI session id; omit to read it from the hook payload on stdin")
	return cmd
}

func runActivityAISessionHook(cmd *cobra.Command, tenant, environment, tool, event, sessionID string) error {
	if !validAISessionHookEvent(event) {
		return fmt.Errorf("unsupported --event %q: pass turn-start, tool-use, turn-end, notify, or exit", event)
	}
	resolvedTenant, resolvedEnvironment, err := resolveAISessionHookTarget(cmd, tenant, environment)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		payload, readErr := io.ReadAll(cmd.InOrStdin())
		if readErr != nil {
			return fmt.Errorf("read the hook payload from stdin: %w", readErr)
		}
		sessionID, err = common.AISessionHookSessionID(payload)
		if err != nil {
			return fmt.Errorf("%s hook: %w", toolLabelForHook(tool), err)
		}
	}
	return common.RecordAISessionEvent(common.AISessionEventParams{
		Tenant:      resolvedTenant,
		Environment: resolvedEnvironment,
		SessionID:   sessionID,
		Tool:        tool,
		Event:       common.AISessionEventKind(event),
	})
}

// resolveAISessionHookTarget resolves the environment a hook reports against:
// the flags when a caller passed them, and otherwise the environment the
// session is already running in. A runtime pod exports both, which is what lets
// an installed hook be a bare command with no per-environment arguments baked
// into a settings file every environment shares.
func resolveAISessionHookTarget(cmd *cobra.Command, tenant, environment string) (string, string, error) {
	tenant, environment = strings.TrimSpace(tenant), strings.TrimSpace(environment)
	if tenant == "" {
		tenant = strings.TrimSpace(os.Getenv("ERUN_TENANT"))
	}
	if environment == "" {
		environment = strings.TrimSpace(os.Getenv("ERUN_ENVIRONMENT"))
	}
	if tenant != "" && environment != "" {
		return tenant, environment, nil
	}
	missing := missingTenantOrEnvironmentFlags(tenant, environment)
	hint := "ERUN_TENANT and ERUN_ENVIRONMENT are both set, which a runtime pod already does"
	if len(missing) == 1 {
		hint = "ERUN_" + strings.ToUpper(missing[0]) + " is set, which a runtime pod already does"
	}
	return "", "", fmt.Errorf(
		"%s not set and not in the environment: pass --tenant and --environment, or run this where %s",
		strings.Join(missing, " and "), hint,
	)
}

// validAISessionHookEvent accepts only the model events a hook can report, so a
// typo in a settings file is refused rather than recorded as an unsupported
// event on every turn boundary.
func validAISessionHookEvent(event string) bool {
	for _, kind := range common.AISessionHookBindings() {
		if string(kind.ModelEvent) == event {
			return true
		}
	}
	return false
}

// toolLabelForHook names the reporting tool in a refusal, falling back to the
// generic form when a settings entry left --tool off.
func toolLabelForHook(tool string) string {
	if tool = strings.TrimSpace(tool); tool != "" {
		return tool
	}
	return "AI tool"
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
		Example: "  # For a Claude Code hook, use the hook verb instead: it reads the session id\n" +
			"  # the tool reports on stdin, and takes the environment from the pod's own\n" +
			"  # ERUN_TENANT/ERUN_ENVIRONMENT. This verb addresses a session you already\n" +
			"  # know the id of -- a shell wrapper, a script, or an exit observed elsewhere.\n" +
			"  erun activity ai-session report --tenant team --environment dev \\\n" +
			"    --session 5f2c1a2b-e2e --tool claude --event turn-end",
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
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output (alias for --output json)")
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
	ctx := commandContext(cmd)
	if commandWantsJSON(ctx, jsonOutput) {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(statuses)
	}
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
