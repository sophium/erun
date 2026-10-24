package activity

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/spf13/cobra"
)

func NewActivityCmd(store config.OpenStore, resolveOpen shared.OpenResolver) *cobra.Command {
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
		newActivityAISessionCmd(),
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
			params := activity.EnvironmentActivityParams{
				Tenant:      tenant,
				Environment: environment,
				Kind:        kind,
				Seen:        seen,
				Bytes:       bytes,
			}
			if strings.TrimSpace(clientAddress) != "" {
				params.ClientUpdates = []activity.EnvironmentActivityClientUpdate{
					{Address: clientAddress, Bytes: clientBytes},
				}
			}
			return activity.RecordEnvironmentActivity(params)
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

func newActivityStatusCmd(store config.OpenStore) *cobra.Command {
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
			commandCtx := shared.CommandContext(cmd)
			if shared.CommandWantsJSON(commandCtx, jsonOutput) {
				encoder := json.NewEncoder(commandCtx.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(status)
			}
			return writeActivityStatus(shared.CommandContext(cmd), status)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output (alias for --output json)")
	return cmd
}

// newActivityStopReadyCmd exits 0 only on Fire; startRuntimeEnvironmentMonitor stops the
// instance on that exit code.
func newActivityStopReadyCmd(store config.OpenStore) *cobra.Command {
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
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write a JSON summary of the stop decision to stdout regardless of exit code (alias for --output json)")
	cmd.Flags().StringVar(&cloudContextName, "cloud-context", "", "Cloud context name to record on the pending entry (informational)")
	return cmd
}

func runActivityStopReady(cmd *cobra.Command, store config.OpenStore, tenant, environment, cloudContextName string, jsonOutput bool) error {
	status, err := resolveActivityStatus(store, tenant, environment)
	if err != nil {
		return err
	}
	result, err := activity.MaybeArmOrFireIdleStop(activity.MaybeArmOrFireIdleStopParams{
		Tenant:           tenant,
		Environment:      environment,
		Status:           status,
		CloudContextName: cloudContextName,
		ReasonSummary:    activity.IdleStopReasonSummary(status),
		Now:              time.Now(),
	})
	if err != nil {
		return err
	}
	if shared.CommandWantsJSON(shared.CommandContext(cmd), jsonOutput) {
		if err := emitStopReadyJSON(shared.CommandContext(cmd).Stdout, status, result); err != nil {
			return err
		}
	}
	return stopReadyExitForAction(status, result)
}

// On Fire the pending file is already cleared, so this payload is how record-stop recovers it.
func emitStopReadyJSON(stdout io.Writer, status activity.EnvironmentIdleStatus, result activity.MaybeArmOrFireIdleStopResult) error {
	payload := stopReadyJSON{
		StopEligible:     status.StopEligible,
		BlockedReason:    strings.TrimSpace(status.StopBlockedReason),
		Action:           result.Action,
		SecondsRemaining: result.SecondsRemaining,
		GraceSeconds:     status.GracePeriodSeconds,
		ReasonSummary:    activity.IdleStopReasonSummary(status),
	}
	if !result.State.Since.IsZero() {
		payload.PendingSince = result.State.Since.UTC().Format(time.RFC3339)
	}
	if result.Action == activity.IdleStopActionFire {
		state := result.State
		payload.PendingState = &state
	}
	encoder := json.NewEncoder(stdout)
	return encoder.Encode(payload)
}

func stopReadyExitForAction(status activity.EnvironmentIdleStatus, result activity.MaybeArmOrFireIdleStopResult) error {
	switch result.Action {
	case activity.IdleStopActionFire:
		return nil
	case activity.IdleStopActionArm:
		return fmt.Errorf("auto-stop armed: %d seconds of grace before forced stop", result.SecondsRemaining)
	case activity.IdleStopActionWait:
		return fmt.Errorf("auto-stop pending: %d seconds remaining in grace", result.SecondsRemaining)
	default:
		if strings.TrimSpace(status.StopBlockedReason) != "" {
			return fmt.Errorf("environment is not stop eligible: %s", status.StopBlockedReason)
		}
		return fmt.Errorf("environment is not idle")
	}
}

// stopReadyJSON is piped into record-stop --state-stdin by the in-pod entrypoint on Fire.
type stopReadyJSON struct {
	StopEligible     bool                             `json:"stopEligible"`
	BlockedReason    string                           `json:"blockedReason,omitempty"`
	Action           string                           `json:"action"`
	PendingSince     string                           `json:"pendingSince,omitempty"`
	SecondsRemaining int64                            `json:"secondsRemaining"`
	GraceSeconds     int64                            `json:"graceSeconds"`
	ReasonSummary    string                           `json:"reasonSummary,omitempty"`
	PendingState     *activity.EnvironmentStopPending `json:"pendingState,omitempty"`
}

// newActivityCancelStopPendingCmd is idempotent: a missing pending file is not an error.
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
			return activity.ClearEnvironmentStopPending(tenant, environment)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	return cmd
}

// newActivityRecordStopCmd appends a stop entry; --source distinguishes pod-monitor
// from host-manual.
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
	pending, havePending, err := loadPendingForRecord(shared.CommandContext(cmd).Stdin, stateStdin, tenant, environment)
	if err != nil {
		return err
	}
	entry := buildStopHistoryEntry(time.Now().UTC(), source, reason, cloudContextName, pending, havePending)
	if err := activity.AppendStopHistoryEntry(tenant, environment, entry); err != nil {
		return err
	}
	// Cleared after persisting so the next stop-ready arms a fresh grace window.
	return activity.ClearEnvironmentStopPending(tenant, environment)
}

// Stdin wins because Fire clears the pending file; ok=false is normal for a manual stop.
func loadPendingForRecord(stdin io.Reader, stateStdin bool, tenant, environment string) (activity.EnvironmentStopPending, bool, error) {
	if stateStdin {
		body, err := io.ReadAll(stdin)
		if err != nil {
			return activity.EnvironmentStopPending{}, false, fmt.Errorf("read --state-stdin: %w", err)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return activity.EnvironmentStopPending{}, false, nil
		}
		var payload stopReadyJSON
		if err := json.Unmarshal(body, &payload); err != nil {
			return activity.EnvironmentStopPending{}, false, fmt.Errorf("parse --state-stdin: %w", err)
		}
		if payload.PendingState == nil {
			return activity.EnvironmentStopPending{}, false, nil
		}
		return *payload.PendingState, true, nil
	}
	pending, ok, err := activity.LoadEnvironmentStopPending(tenant, environment)
	if err != nil {
		return activity.EnvironmentStopPending{}, false, err
	}
	return pending, ok, nil
}

func buildStopHistoryEntry(now time.Time, source, reason, cloudContextName string, pending activity.EnvironmentStopPending, havePending bool) activity.EnvironmentStopHistoryEntry {
	entry := activity.EnvironmentStopHistoryEntry{
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
		entry.Markers = append(entry.Markers, activity.EnvironmentStopHistoryMarker{
			Name:           marker.Name,
			Idle:           marker.Idle,
			Reason:         marker.Reason,
			SecondsIdleFor: secondsIdleForMarker(marker, pending.Since),
		})
	}
	return entry
}

// Unknown values coerce to empty so older runtime images still append.
func normalizeStopHistorySource(source string) string {
	source = strings.TrimSpace(source)
	switch source {
	case activity.StopHistorySourcePodMonitor, activity.StopHistorySourceHostManual:
		return source
	default:
		return ""
	}
}

func secondsIdleForMarker(marker activity.EnvironmentIdleMarker, since time.Time) int64 {
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

func resolveActivityStatus(store config.OpenStore, tenant, environment string) (activity.EnvironmentIdleStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := validateActivityTarget(tenant, environment); err != nil {
		return activity.EnvironmentIdleStatus{}, err
	}
	return activity.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, time.Now())
}

func writeActivityStatus(ctx core.Context, status activity.EnvironmentIdleStatus) error {
	if err := shared.WriteLabeledValue(ctx, "stop eligible", shared.EnabledDisabledLabel(status.StopEligible)); err != nil {
		return err
	}
	if strings.TrimSpace(status.StopBlockedReason) != "" {
		if err := shared.WriteLabeledValue(ctx, "stop blocked", status.StopBlockedReason); err != nil {
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
		if err := shared.WriteLabeledValue(ctx, marker.Name, value); err != nil {
			return err
		}
	}
	return nil
}
