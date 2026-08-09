package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newIdleCmd(store common.OpenStore, resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "idle [TENANT] [ENVIRONMENT]",
		Short: "Show environment idle stop status",
		Long: "Report the environment's own idle markers: what it last saw, which leases are\n" +
			"holding it busy, and how long it has before auto-stop.\n\n" +
			"The answer comes from the environment, over its MCP edge, so it is the same\n" +
			"one the desktop's activity view shows. An edge that cannot be reached is\n" +
			"reported as such rather than as an idle environment — safe to act on before a\n" +
			"stop, a redeploy, or a delete.",
		Example:       "  erun idle --tenant team --environment dev\n  erun idle team dev --json",
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
			return runIdleCommand(cmd.Context(), commandContext(cmd), store, resolveOpen, tenant, environment, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Tenant")
	cmd.Flags().StringVar(&environment, "environment", "", "Environment")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output")
	addDryRunFlag(cmd)
	return cmd
}

func runIdleCommand(ctx context.Context, commandCtx common.Context, store common.OpenStore, resolveOpen OpenResolver, tenant, environment string, jsonOutput bool) error {
	status, resolved, err := resolveEnvironmentIdleStatus(ctx, commandCtx, store, resolveOpen, tenant, environment)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if jsonOutput {
		encoder := json.NewEncoder(commandCtx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	return writeIdleStatus(commandCtx, status)
}

// resolveEnvironmentIdleStatus reads the local store only when this process is
// the environment being asked about; anywhere else the environment's own edge
// is the only thing that knows.
func resolveEnvironmentIdleStatus(ctx context.Context, commandCtx common.Context, store common.OpenStore, resolveOpen OpenResolver, tenant, environment string) (common.EnvironmentIdleStatus, bool, error) {
	if environmentTargetsItself() {
		status, err := common.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, time.Now())
		return status, err == nil, err
	}
	return callEnvironmentTool[common.EnvironmentIdleStatus](ctx, commandCtx, resolveOpen, tenant, environment, "idle", nil)
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
	if err := writeOptionalIdleValue(ctx, "stop blocked", status.StopBlockedReason); err != nil {
		return err
	}
	if err := writeOptionalIdleValue(ctx, "stop error", status.StopError); err != nil {
		return err
	}
	for _, marker := range status.Markers {
		if err := writeLabeledValue(ctx, marker.Name, idleMarkerValue(marker)); err != nil {
			return err
		}
	}
	return nil
}

func writeOptionalIdleValue(ctx common.Context, label, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return writeLabeledValue(ctx, label, value)
}

func idleMarkerValue(marker common.EnvironmentIdleMarker) string {
	value := "active"
	if marker.Idle {
		value = "idle"
	}
	if marker.SecondsRemaining > 0 {
		value += fmt.Sprintf(" (%ds)", marker.SecondsRemaining)
	}
	return value
}
