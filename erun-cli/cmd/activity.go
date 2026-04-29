package cmd

import (
	"encoding/json"
	"fmt"
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
	cmd.AddCommand(newActivityTouchCmd(), newActivityStatusCmd(store), newActivityStopReadyCmd(store), newActivitySSHProxyCmd())
	return cmd
}

func newActivityTouchCmd() *cobra.Command {
	var tenant string
	var environment string
	var kind string
	var seen bool
	var bytes int64
	cmd := &cobra.Command{
		Use:  "touch",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return common.RecordEnvironmentActivity(common.EnvironmentActivityParams{
				Tenant:      tenant,
				Environment: environment,
				Kind:        kind,
				Seen:        seen,
				Bytes:       bytes,
			})
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&kind, "kind", "", "Activity kind")
	cmd.Flags().BoolVar(&seen, "seen", false, "Record process heartbeat without user activity")
	cmd.Flags().Int64Var(&bytes, "bytes", 0, "Traffic bytes observed since the previous sample")
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

func newActivityStopReadyCmd(store common.OpenStore) *cobra.Command {
	var tenant string
	var environment string
	cmd := &cobra.Command{
		Use:  "stop-ready",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := resolveActivityStatus(store, tenant, environment)
			if err != nil {
				return err
			}
			if !status.StopEligible {
				if strings.TrimSpace(status.StopBlockedReason) != "" {
					return fmt.Errorf("environment is not stop eligible: %s", status.StopBlockedReason)
				}
				return fmt.Errorf("environment is not idle")
			}
			return nil
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	return cmd
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
