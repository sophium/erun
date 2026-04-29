package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newIdleCmd(store common.OpenStore) *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:           "idle [TENANT] [ENVIRONMENT]",
		Short:         "Show environment idle stop status",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				tenant = args[0]
			}
			if len(args) > 1 {
				environment = args[1]
			}
			status, err := common.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, time.Now())
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(commandContext(cmd).Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(status)
			}
			return writeIdleStatus(commandContext(cmd), status)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Tenant")
	cmd.Flags().StringVar(&environment, "environment", "", "Environment")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output")
	return cmd
}

func writeIdleStatus(ctx common.Context, status common.EnvironmentIdleStatus) error {
	if err := writeLabeledValue(ctx, "timeout", fmt.Sprintf("%d seconds", int64(status.Policy.Timeout/time.Second))); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "seconds until stop", fmt.Sprintf("%d", status.SecondsUntilStop)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "stop eligible", enabledDisabledLabel(status.StopEligible)); err != nil {
		return err
	}
	if strings.TrimSpace(status.StopBlockedReason) != "" {
		if err := writeLabeledValue(ctx, "stop blocked", status.StopBlockedReason); err != nil {
			return err
		}
	}
	if strings.TrimSpace(status.StopError) != "" {
		if err := writeLabeledValue(ctx, "stop error", status.StopError); err != nil {
			return err
		}
	}
	for _, marker := range status.Markers {
		value := "active"
		if marker.Idle {
			value = "idle"
		}
		if marker.SecondsRemaining > 0 {
			value += fmt.Sprintf(" (%ds)", marker.SecondsRemaining)
		}
		if err := writeLabeledValue(ctx, marker.Name, value); err != nil {
			return err
		}
	}
	return nil
}
