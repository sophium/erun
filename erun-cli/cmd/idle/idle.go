package idle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/spf13/cobra"
)

func NewIdleCmd(store config.OpenStore, resolveOpen shared.OpenResolver) *cobra.Command {
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
			return runIdleCommand(cmd.Context(), shared.CommandContext(cmd), store, resolveOpen, tenant, environment, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Tenant")
	cmd.Flags().StringVar(&environment, "environment", "", "Environment")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output (alias for --output json)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runIdleCommand(ctx context.Context, commandCtx core.Context, store config.OpenStore, resolveOpen shared.OpenResolver, tenant, environment string, jsonOutput bool) error {
	status, resolved, err := resolveEnvironmentIdleStatus(ctx, commandCtx, store, resolveOpen, tenant, environment)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	return WriteIdleResult(commandCtx, status, jsonOutput)
}

// WriteIdleResult picks the wire form once, so the documented global
// --output json and the command's own --json alias cannot diverge.
func WriteIdleResult(commandCtx core.Context, status activity.EnvironmentIdleStatus, jsonOutput bool) error {
	if shared.CommandWantsJSON(commandCtx, jsonOutput) {
		encoder := json.NewEncoder(commandCtx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	return writeIdleStatus(commandCtx, status)
}

// resolveEnvironmentIdleStatus reads the local store only when this process is
// the environment being asked about; anywhere else the environment's own edge
// is the only thing that knows.
func resolveEnvironmentIdleStatus(ctx context.Context, commandCtx core.Context, store config.OpenStore, resolveOpen shared.OpenResolver, tenant, environment string) (activity.EnvironmentIdleStatus, bool, error) {
	if shared.EnvironmentTargetsItself() {
		status, err := activity.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, time.Now())
		return status, err == nil, err
	}
	return shared.CallEnvironmentTool[activity.EnvironmentIdleStatus](ctx, commandCtx, resolveOpen, tenant, environment, "idle", nil, true)
}

func writeIdleStatus(ctx core.Context, status activity.EnvironmentIdleStatus) error {
	if err := shared.WriteLabeledValue(ctx, "timeout", fmt.Sprintf("%d seconds", int64(status.Policy.Timeout/time.Second))); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "seconds until stop", fmt.Sprintf("%d", status.SecondsUntilStop)); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "stop eligible", shared.EnabledDisabledLabel(status.StopEligible)); err != nil {
		return err
	}
	if err := writeOptionalIdleValue(ctx, "stop blocked", status.StopBlockedReason); err != nil {
		return err
	}
	if err := writeOptionalIdleValue(ctx, "stop error", status.StopError); err != nil {
		return err
	}
	for _, marker := range status.Markers {
		if err := shared.WriteLabeledValue(ctx, marker.Name, idleMarkerValue(marker)); err != nil {
			return err
		}
	}
	return nil
}

func writeOptionalIdleValue(ctx core.Context, label, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return shared.WriteLabeledValue(ctx, label, value)
}

func idleMarkerValue(marker activity.EnvironmentIdleMarker) string {
	value := "active"
	if marker.Idle {
		value = "idle"
	}
	if marker.SecondsRemaining > 0 {
		value += fmt.Sprintf(" (%ds)", marker.SecondsRemaining)
	}
	return value
}
