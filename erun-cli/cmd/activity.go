package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newActivityCmd(store common.OpenStore, resolveOpen OpenResolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "activity",
		Short:         "Record and inspect environment activity",
		SilenceErrors: true,
		SilenceUsage:  true,
		Hidden:        true,
	}
	cmd.AddCommand(
		newActivityTouchCmd(),
		newActivityLeaseCmd(resolveOpen),
		newActivitySampleCmd(),
		newActivityStatusCmd(store),
		newActivityStopReadyCmd(store),
		newActivityCancelStopPendingCmd(),
		newActivityRecordStopCmd(),
		newActivitySSHProxyCmd(),
	)
	return cmd
}

func newActivityTouchCmd() *cobra.Command {
	var tenant string
	var environment string
	var kind string
	var seen bool
	var bytes int64
	var clientAddress string
	var clientBytes int64
	cmd := &cobra.Command{
		Use:  "touch",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := common.EnvironmentActivityParams{
				Tenant:      tenant,
				Environment: environment,
				Kind:        kind,
				Seen:        seen,
				Bytes:       bytes,
			}
			if strings.TrimSpace(clientAddress) != "" {
				params.ClientUpdates = []common.EnvironmentActivityClientUpdate{
					{Address: clientAddress, Bytes: clientBytes},
				}
			}
			return common.RecordEnvironmentActivity(params)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&kind, "kind", "", "Activity kind")
	cmd.Flags().BoolVar(&seen, "seen", false, "Record process heartbeat without user activity")
	cmd.Flags().Int64Var(&bytes, "bytes", 0, "Traffic bytes observed since the previous sample")
	cmd.Flags().StringVar(&clientAddress, "client-address", "", "Remote address whose bytes to attribute (SSH proxy testing)")
	cmd.Flags().Int64Var(&clientBytes, "client-bytes", 0, "Bytes attributed to --client-address since the previous sample")
	return cmd
}

func newActivityStatusCmd(store common.OpenStore) *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := resolveActivityStatus(store, tenant, environment)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(commandContext(cmd).Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(status)
			}
			return writeActivityStatus(commandContext(cmd), status)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output")
	return cmd
}

// newActivityStopReadyCmd drives the idle grace-period decision. Its
// exit code is a contract with the in-pod monitor's entrypoint.sh:
// exit 0 only on Fire, meaning the caller may now stop the instance;
// every other grace state (Skip/Arm/Wait) exits non-zero so the env
// stays running.
func newActivityStopReadyCmd(store common.OpenStore) *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	var cloudContextName string
	cmd := &cobra.Command{
		Use:  "stop-ready",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityStopReady(cmd, store, tenant, environment, cloudContextName, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write a JSON summary of the stop decision to stdout regardless of exit code")
	cmd.Flags().StringVar(&cloudContextName, "cloud-context", "", "Cloud context name to record on the pending entry (informational)")
	return cmd
}

func runActivityStopReady(cmd *cobra.Command, store common.OpenStore, tenant, environment, cloudContextName string, jsonOutput bool) error {
	status, err := resolveActivityStatus(store, tenant, environment)
	if err != nil {
		return err
	}
	result, err := common.MaybeArmOrFireIdleStop(common.MaybeArmOrFireIdleStopParams{
		Tenant:           tenant,
		Environment:      environment,
		Status:           status,
		CloudContextName: cloudContextName,
		ReasonSummary:    common.IdleStopReasonSummary(status),
		Now:              time.Now(),
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		if err := emitStopReadyJSON(commandContext(cmd).Stdout, status, result); err != nil {
			return err
		}
	}
	return stopReadyExitForAction(status, result)
}

// emitStopReadyJSON writes the stop-ready decision for the in-pod
// monitor's heartbeat log. On Fire the pending file is already gone
// (Fire clears it for crash safety), so this payload is the only way
// record-stop can recover the pending state for the audit row.
func emitStopReadyJSON(stdout io.Writer, status common.EnvironmentIdleStatus, result common.MaybeArmOrFireIdleStopResult) error {
	payload := stopReadyJSON{
		StopEligible:     status.StopEligible,
		BlockedReason:    strings.TrimSpace(status.StopBlockedReason),
		Action:           result.Action,
		SecondsRemaining: result.SecondsRemaining,
		GraceSeconds:     status.GracePeriodSeconds,
		ReasonSummary:    common.IdleStopReasonSummary(status),
	}
	if !result.State.Since.IsZero() {
		payload.PendingSince = result.State.Since.UTC().Format(time.RFC3339)
	}
	if result.Action == common.IdleStopActionFire {
		state := result.State
		payload.PendingState = &state
	}
	encoder := json.NewEncoder(stdout)
	return encoder.Encode(payload)
}

func stopReadyExitForAction(status common.EnvironmentIdleStatus, result common.MaybeArmOrFireIdleStopResult) error {
	switch result.Action {
	case common.IdleStopActionFire:
		return nil
	case common.IdleStopActionArm:
		return fmt.Errorf("auto-stop armed: %d seconds of grace before forced stop", result.SecondsRemaining)
	case common.IdleStopActionWait:
		return fmt.Errorf("auto-stop pending: %d seconds remaining in grace", result.SecondsRemaining)
	default:
		if strings.TrimSpace(status.StopBlockedReason) != "" {
			return fmt.Errorf("environment is not stop eligible: %s", status.StopBlockedReason)
		}
		return fmt.Errorf("environment is not idle")
	}
}

// stopReadyJSON is the --json wire contract: the in-pod entrypoint
// script logs it and, on Fire, pipes it into record-stop --state-stdin
// so the audit row keeps the per-marker breakdown.
type stopReadyJSON struct {
	StopEligible     bool                           `json:"stopEligible"`
	BlockedReason    string                         `json:"blockedReason,omitempty"`
	Action           string                         `json:"action"`
	PendingSince     string                         `json:"pendingSince,omitempty"`
	SecondsRemaining int64                          `json:"secondsRemaining"`
	GraceSeconds     int64                          `json:"graceSeconds"`
	ReasonSummary    string                         `json:"reasonSummary,omitempty"`
	PendingState     *common.EnvironmentStopPending `json:"pendingState,omitempty"`
}

// newActivityCancelStopPendingCmd backs the desktop's Cancel button
// (via the cancelStopPending MCP tool, which shells out here) and
// manual operator recovery. Idempotent: a missing pending file is not
// an error.
func newActivityCancelStopPendingCmd() *cobra.Command {
	var tenant string
	var environment string
	cmd := &cobra.Command{
		Use:  "cancel-stop-pending",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenant = strings.TrimSpace(tenant)
			environment = strings.TrimSpace(environment)
			if err := validateActivityTarget(tenant, environment); err != nil {
				return err
			}
			return common.ClearEnvironmentStopPending(tenant, environment)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	return cmd
}

// newActivityRecordStopCmd appends a stop entry to the env's audit
// history. The in-pod monitor calls it after stopping the host
// (--source pod-monitor); the desktop's manual Stop button calls it
// via the idle_stop_record MCP tool (--source host-manual). The
// source flag distinguishes the two on the History tab.
func newActivityRecordStopCmd() *cobra.Command {
	var tenant string
	var environment string
	var reason string
	var cloudContextName string
	var source string
	var stateStdin bool
	cmd := &cobra.Command{
		Use:  "record-stop",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityRecordStop(cmd, tenant, environment, reason, cloudContextName, source, stateStdin)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&reason, "reason", "", "Reason summary to record (defaults to the pending entry's reason)")
	cmd.Flags().StringVar(&cloudContextName, "cloud-context", "", "Cloud context name to record (defaults to the pending entry's value)")
	cmd.Flags().StringVar(&source, "source", "", "Where the stop originated: 'pod-monitor' (in-pod idle loop) or 'host-manual' (desktop Stop button)")
	cmd.Flags().BoolVar(&stateStdin, "state-stdin", false, "Read a stop-ready --json blob from stdin to recover the per-marker breakdown the in-pod monitor just consumed")
	return cmd
}

func runActivityRecordStop(cmd *cobra.Command, tenant, environment, reason, cloudContextName, source string, stateStdin bool) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	source = normalizeStopHistorySource(source)
	pending, havePending, err := loadPendingForRecord(commandContext(cmd).Stdin, stateStdin, tenant, environment)
	if err != nil {
		return err
	}
	entry := buildStopHistoryEntry(time.Now().UTC(), source, reason, cloudContextName, pending, havePending)
	if err := common.AppendStopHistoryEntry(tenant, environment, entry); err != nil {
		return err
	}
	// Clear the pending file once we've persisted the
	// audit record so a follow-up `stop-ready` call after
	// the env restarts arms a fresh grace window from
	// scratch rather than reusing the stale entry.
	return common.ClearEnvironmentStopPending(tenant, environment)
}

// loadPendingForRecord prefers stdin over the on-disk pending file
// because the Fire branch clears that file before record-stop runs.
// ok=false with no error is the expected case for a manual stop with
// no prior grace, not a failure.
func loadPendingForRecord(stdin io.Reader, stateStdin bool, tenant, environment string) (common.EnvironmentStopPending, bool, error) {
	if stateStdin {
		body, err := io.ReadAll(stdin)
		if err != nil {
			return common.EnvironmentStopPending{}, false, fmt.Errorf("read --state-stdin: %w", err)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return common.EnvironmentStopPending{}, false, nil
		}
		var payload stopReadyJSON
		if err := json.Unmarshal(body, &payload); err != nil {
			return common.EnvironmentStopPending{}, false, fmt.Errorf("parse --state-stdin: %w", err)
		}
		if payload.PendingState == nil {
			return common.EnvironmentStopPending{}, false, nil
		}
		return *payload.PendingState, true, nil
	}
	pending, ok, err := common.LoadEnvironmentStopPending(tenant, environment)
	if err != nil {
		return common.EnvironmentStopPending{}, false, err
	}
	return pending, ok, nil
}

func buildStopHistoryEntry(now time.Time, source, reason, cloudContextName string, pending common.EnvironmentStopPending, havePending bool) common.EnvironmentStopHistoryEntry {
	entry := common.EnvironmentStopHistoryEntry{
		StoppedAt:        now,
		Source:           source,
		Reason:           strings.TrimSpace(reason),
		CloudContextName: strings.TrimSpace(cloudContextName),
	}
	if !havePending {
		return entry
	}
	entry.GraceSeconds = pending.GraceSeconds
	entry.ArmedAt = pending.Since
	entry.Policy = pending.Policy
	if entry.Reason == "" {
		entry.Reason = pending.ReasonSummary
	}
	if entry.CloudContextName == "" {
		entry.CloudContextName = pending.CloudContextName
	}
	for _, marker := range pending.Markers {
		entry.Markers = append(entry.Markers, common.EnvironmentStopHistoryMarker{
			Name:           marker.Name,
			Idle:           marker.Idle,
			Reason:         marker.Reason,
			SecondsIdleFor: secondsIdleForMarker(marker, pending.Since),
		})
	}
	return entry
}

// normalizeStopHistorySource coerces any unrecognized value to empty
// so old runtime images that omit --source do not break the audit
// append.
func normalizeStopHistorySource(source string) string {
	source = strings.TrimSpace(source)
	switch source {
	case common.StopHistorySourcePodMonitor, common.StopHistorySourceHostManual:
		return source
	default:
		return ""
	}
}

func secondsIdleForMarker(marker common.EnvironmentIdleMarker, since time.Time) int64 {
	if marker.LastActivity.IsZero() {
		return 0
	}
	delta := int64(since.Sub(marker.LastActivity).Seconds())
	if delta < 0 {
		return 0
	}
	return delta
}

func addActivityTargetFlags(cmd *cobra.Command, tenant, environment *string) {
	cmd.Flags().StringVar(tenant, "tenant", "", "Tenant")
	cmd.Flags().StringVar(environment, "environment", "", "Environment")
}

func resolveActivityStatus(store common.OpenStore, tenant, environment string) (common.EnvironmentIdleStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := validateActivityTarget(tenant, environment); err != nil {
		return common.EnvironmentIdleStatus{}, err
	}
	return common.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, time.Now())
}

func writeActivityStatus(ctx common.Context, status common.EnvironmentIdleStatus) error {
	if err := writeLabeledValue(ctx, "stop eligible", enabledDisabledLabel(status.StopEligible)); err != nil {
		return err
	}
	if strings.TrimSpace(status.StopBlockedReason) != "" {
		if err := writeLabeledValue(ctx, "stop blocked", status.StopBlockedReason); err != nil {
			return err
		}
	}
	for _, marker := range status.Markers {
		value := "active"
		if marker.Idle {
			value = "idle"
		}
		if strings.TrimSpace(marker.Reason) != "" {
			value += " (" + marker.Reason + ")"
		}
		if err := writeLabeledValue(ctx, marker.Name, value); err != nil {
			return err
		}
	}
	return nil
}
