package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// The lease verbs are the CLI half of the activity lease: a long job announces
// what it is, and the environment reports that instead of reading as untouched.
// Shaped for a shell wrapper first — take, trap the release, let the recorded
// pid reclaim the lease if the wrapper dies — and usable directly by an agent
// that holds one across a detached run.

func newActivityLeaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "lease",
		Short:         "Hold, release, and inspect activity leases",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(newActivityLeaseTakeCmd(), newActivityLeaseReleaseCmd(), newActivityLeaseListCmd())
	return cmd
}

func newActivityLeaseTakeCmd() *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var pid int
	var ttl time.Duration
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "take",
		Short: "Take or renew a named lease so the environment reports it as busy",
		Long: "Hold a lease for the lifetime of a long job. While it is held the environment\n" +
			"reports as busy with the lease's name, and idle-stop will not stop it.\n\n" +
			"Taking an existing id renews it, so a wrapper can refresh on a timer. A lease\n" +
			"expires without renewal, and one whose --pid is gone is reclaimed on the next\n" +
			"read, so a crashed job cannot keep an environment awake.",
		Example: "  # Wrap a long build so the environment stays busy for its lifetime.\n" +
			"  erun activity lease take --tenant team --environment dev --name gradle-build --pid $$\n" +
			"  trap 'erun activity lease release --tenant team --environment dev --id gradle-build' EXIT",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityLeaseTake(cmd, common.TakeEnvironmentActivityLeaseParams{
				Tenant:      tenant,
				Environment: environment,
				Name:        name,
				ID:          id,
				PID:         pid,
				TTL:         ttl,
			}, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "What the lease is holding the environment for (shown to the operator)")
	cmd.Flags().StringVar(&id, "id", "", "Lease id to take or renew (defaults to the name)")
	cmd.Flags().IntVar(&pid, "pid", 0, "Holder process to reconcile the lease against; the lease is reclaimed once it exits")
	cmd.Flags().DurationVar(&ttl, "ttl", common.DefaultEnvironmentActivityLeaseTTL, "How long the lease holds without a renewal")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the lease as JSON")
	return cmd
}

func runActivityLeaseTake(cmd *cobra.Command, params common.TakeEnvironmentActivityLeaseParams, jsonOutput bool) error {
	if err := validateActivityTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	lease, err := common.TakeEnvironmentActivityLease(params)
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)
	if jsonOutput {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(lease)
	}
	_, err = fmt.Fprintf(ctx.Stdout, "lease held: %s (id %s), expires in %s\n", lease.Name, lease.ID, formatLeaseRemaining(lease, time.Now()))
	return err
}

func newActivityLeaseReleaseCmd() *cobra.Command {
	var tenant string
	var environment string
	var id string
	cmd := &cobra.Command{
		Use:     "release",
		Short:   "Release a held lease so the environment can go idle again",
		Long:    "Releasing a lease that was never taken, or has already expired, succeeds — so a\nwrapper's exit trap never fails a job that finished cleanly.",
		Example: "  erun activity lease release --tenant team --environment dev --id gradle-build",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateActivityTarget(tenant, environment); err != nil {
				return err
			}
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("lease id is required")
			}
			if err := common.ReleaseEnvironmentActivityLease(tenant, environment, id); err != nil {
				return err
			}
			_, err := fmt.Fprintf(commandContext(cmd).Stdout, "lease released: %s\n", strings.TrimSpace(id))
			return err
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Lease id to release")
	return cmd
}

func newActivityLeaseListCmd() *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List the leases currently holding the environment busy",
		Long:    "Reading the list also reclaims leases that expired or whose holder process is\ngone, so what it prints is what is actually deferring idle-stop.",
		Example: "  erun activity lease list --tenant team --environment dev",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateActivityTarget(tenant, environment); err != nil {
				return err
			}
			now := time.Now()
			leases, err := common.LoadEnvironmentActivityLeases(tenant, environment, now)
			if err != nil {
				return err
			}
			return writeActivityLeases(commandContext(cmd), leases, now, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the leases as JSON")
	return cmd
}

func writeActivityLeases(ctx common.Context, leases []common.EnvironmentActivityLease, now time.Time, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		if leases == nil {
			leases = []common.EnvironmentActivityLease{}
		}
		return encoder.Encode(leases)
	}
	if len(leases) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no leases held")
		return err
	}
	for _, lease := range leases {
		value := fmt.Sprintf("%s, expires in %s", lease.Name, formatLeaseRemaining(lease, now))
		if lease.PID > 0 {
			value += fmt.Sprintf(", pid %d", lease.PID)
		}
		if err := writeLabeledValue(ctx, lease.ID, value); err != nil {
			return err
		}
	}
	return nil
}

func formatLeaseRemaining(lease common.EnvironmentActivityLease, now time.Time) string {
	remaining := lease.ExpiresAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("%ds", int64(remaining.Round(time.Second)/time.Second))
}

// newActivitySampleCmd is the uninstrumented-work half. The environment monitor
// calls it on its tick; work that took no lease still registers because the
// processes doing it burned CPU since the previous tick.
func newActivitySampleCmd() *cobra.Command {
	var tenant string
	var environment string
	var procRoot string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "sample",
		Short: "Sample resident build and agent processes and record activity when they are working",
		Long:  "Records activity only when a matched process burned CPU since the previous\nsample, so an agent parked at a prompt does not keep the environment awake.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivitySample(cmd, tenant, environment, procRoot, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&procRoot, "proc-root", common.DefaultProcRoot, "Process filesystem to sample")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the sample verdict as JSON")
	return cmd
}

func runActivitySample(cmd *cobra.Command, tenant, environment, procRoot string, jsonOutput bool) error {
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	previous, err := common.LoadResidentActivitySample(tenant, environment)
	if err != nil {
		return err
	}
	result, err := common.ScanResidentActivity(procRoot, os.Getpid(), previous, time.Now())
	if err != nil {
		return err
	}
	if err := common.SaveResidentActivitySample(tenant, environment, result.Sample); err != nil {
		return err
	}
	if result.Busy {
		if err := common.RecordEnvironmentActivity(common.EnvironmentActivityParams{
			Tenant:      tenant,
			Environment: environment,
			Kind:        common.ActivityKindProcess,
		}); err != nil {
			return err
		}
	}
	ctx := commandContext(cmd)
	if jsonOutput {
		return json.NewEncoder(ctx.Stdout).Encode(result)
	}
	if !result.Busy {
		_, err := fmt.Fprintln(ctx.Stdout, "no working build or agent processes")
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "working: %s\n", strings.Join(result.Processes, ", "))
	return err
}

func validateActivityTarget(tenant, environment string) error {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	return nil
}
