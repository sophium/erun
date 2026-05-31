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

func newActivityCmd(store common.OpenStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "activity",
		Short:         "Record and inspect environment activity",
		SilenceErrors: true,
		SilenceUsage:  true,
		Hidden:        true,
	}
	cmd.AddCommand(
		newActivityTouchCmd(),
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

// newActivityStopReadyCmd drives the grace-period state machine
// shared with the desktop. The exit-code contract the in-pod monitor
// (erun-devops/docker/erun-devops/entrypoint.sh) relies on:
//
//   - exit 0 only on Fire — the caller may now invoke
//     `ec2:StopInstances` for this env
//   - exit non-zero on Skip / Arm / Wait — the env is not (yet) ready
//     to stop; the action distinguishes "no warning yet" from
//     "grace armed" from "still in grace"
//
// The first eligible call writes the pending file and exits non-zero
// (Arm). Subsequent eligible calls keep exiting non-zero until the
// grace window elapses (Wait → Fire). Activity resuming clears the
// pending file (Skip).
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

// runActivityStopReady is the body of `erun activity stop-ready`. It
// resolves the status, runs the shared decision function, optionally
// emits the JSON payload for the in-pod monitor's heartbeat log, and
// maps the result.Action to the exit-code contract documented on
// newActivityStopReadyCmd. Extracted from the RunE closure to keep
// the closure under the eslint-style cyclop ceiling.
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

// emitStopReadyJSON serializes the stop-ready decision to stdout in
// the structured shape consumed by the in-pod monitor's heartbeat
// log line.
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
	encoder := json.NewEncoder(stdout)
	return encoder.Encode(payload)
}

// stopReadyExitForAction maps the shared decision action to the
// exit-code-bearing error the bash monitor loop checks. Only Fire
// returns nil — everything else exits non-zero with a message
// identifying which grace-state branch was taken so an operator
// reading idle-monitor.log can tell at a glance.
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

// stopReadyJSON is the structured stdout payload emitted when
// --json is set. The desktop reads this through the MCP `idle` tool
// (which surfaces the same fields on EnvironmentIdleStatus); the
// CLI form is used by the in-pod entrypoint script for monitor.log.
type stopReadyJSON struct {
	StopEligible     bool   `json:"stopEligible"`
	BlockedReason    string `json:"blockedReason,omitempty"`
	Action           string `json:"action"`
	PendingSince     string `json:"pendingSince,omitempty"`
	SecondsRemaining int64  `json:"secondsRemaining"`
	GraceSeconds     int64  `json:"graceSeconds"`
	ReasonSummary    string `json:"reasonSummary,omitempty"`
}

// newActivityCancelStopPendingCmd removes the stop-pending.json file
// for the named env, dismissing the grace-period warning. Exposed
// for the desktop's Cancel button (called through the MCP
// `cancelStopPending` tool, which shells out to this command) and
// for operator troubleshooting from a remote shell. Idempotent: a
// missing file is not an error.
func newActivityCancelStopPendingCmd() *cobra.Command {
	var tenant string
	var environment string
	cmd := &cobra.Command{
		Use:  "cancel-stop-pending",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenant = strings.TrimSpace(tenant)
			environment = strings.TrimSpace(environment)
			if tenant == "" || environment == "" {
				return fmt.Errorf("tenant and environment are required")
			}
			return common.ClearEnvironmentStopPending(tenant, environment)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	return cmd
}

// newActivityRecordStopCmd appends a stop entry to stop-history.json
// for the named env. Called by the in-pod monitor's entrypoint.sh
// after `stop_cloud_host` succeeds, and by the desktop's manual
// Stop button after it observes the env transition to stopped.
// Either caller passes the snapshot from stop-pending.json so the
// per-marker breakdown survives the round-trip; if the file is
// missing (e.g. a manual stop without prior grace), the record
// carries an empty markers list and the reason flag value.
func newActivityRecordStopCmd() *cobra.Command {
	var tenant string
	var environment string
	var reason string
	var cloudContextName string
	cmd := &cobra.Command{
		Use:  "record-stop",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenant = strings.TrimSpace(tenant)
			environment = strings.TrimSpace(environment)
			if tenant == "" || environment == "" {
				return fmt.Errorf("tenant and environment are required")
			}
			now := time.Now().UTC()
			entry := common.EnvironmentStopHistoryEntry{
				StoppedAt:        now,
				Reason:           strings.TrimSpace(reason),
				CloudContextName: strings.TrimSpace(cloudContextName),
			}
			pending, ok, err := common.LoadEnvironmentStopPending(tenant, environment)
			if err != nil {
				return err
			}
			if ok {
				entry.GraceSeconds = pending.GraceSeconds
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
			}
			if err := common.AppendStopHistoryEntry(tenant, environment, entry); err != nil {
				return err
			}
			// Clear the pending file once we've persisted the
			// audit record so a follow-up `stop-ready` call after
			// the env restarts arms a fresh grace window from
			// scratch rather than reusing the stale entry.
			return common.ClearEnvironmentStopPending(tenant, environment)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&reason, "reason", "", "Reason summary to record (defaults to the pending entry's reason)")
	cmd.Flags().StringVar(&cloudContextName, "cloud-context", "", "Cloud context name to record (defaults to the pending entry's value)")
	return cmd
}

// secondsIdleForMarker reports how long marker has been idle as of
// the time the grace window was armed. Returns 0 when the marker's
// LastActivity timestamp is unset (e.g., never recorded activity).
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
	if tenant == "" || environment == "" {
		return common.EnvironmentIdleStatus{}, fmt.Errorf("tenant and environment are required")
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
