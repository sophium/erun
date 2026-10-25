package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envcall"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/infra/activity/records"
	"github.com/sophium/erun/erun-common/infra/activity/sampling"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
	"github.com/spf13/cobra"
)

// Lease verbs let a long job announce itself so the environment does not read
// as idle; off-environment they act through its edge.

func newActivityLeaseCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "lease",
		Short:         "Hold, release, and inspect activity leases",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(newActivityLeaseTakeCmd(resolveOpen), newActivityLeaseReleaseCmd(resolveOpen), newActivityLeaseListCmd(resolveOpen))
	return cmd
}

func newActivityLeaseTakeCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var pid int
	var ttl time.Duration
	var exclusive bool
	var scope string
	var orchestrator string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "take",
		Short: "Take or renew a named lease so the environment reports it as busy",
		Long: "Hold a lease for the lifetime of a long job. While it is held the environment\n" +
			"reports as busy with the lease's name, and idle-stop will not stop it.\n\n" +
			"Taking an existing id renews it, so a wrapper can refresh on a timer. A lease\n" +
			"expires without renewal, and one whose --pid is gone is reclaimed on the next\n" +
			"read, so a crashed job cannot keep an environment awake.\n\n" +
			"Pass --exclusive before any mutating work in a target environment (erun#1245):\n" +
			"at most one exclusive holder is allowed per --scope (default \"worktree\"), so a\n" +
			"second exclusive take in that scope is refused and named in the error, while a\n" +
			"holder in a different scope - a separate clone in the same pod - is\n" +
			"unaffected. An exclusive take is also refused while an operator's own SSH\n" +
			"session is active in the environment.\n\n" +
			"Which work a claim refuses once it is held depends on its scope: only an\n" +
			"\"environment\" claim refuses other job starts here, while a \"worktree\" claim\n" +
			"- the default - is refused by erun exec gate-merge, which rewrites that one\n" +
			"shared worktree. A claim at another scope refuses neither.",
		Example: "  # From inside the environment, wrap a long build so it stays busy for the build.\n" +
			"  erun activity lease take --tenant team --environment dev --name gradle-build --pid $$\n" +
			"  trap 'erun activity lease release --tenant team --environment dev --id gradle-build' EXIT\n\n" +
			"  # Before mutating work: claim exclusivity over the worktree, refusing if another\n" +
			"  # job or orchestrator already holds it.\n" +
			"  erun activity lease take --tenant team --environment dev --name job-fix-1245 \\\n" +
			"    --exclusive --orchestrator \"$ERUN_ORCHESTRATOR_ID\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if exclusive && !cmd.Flags().Changed("ttl") {
				ttl = activitylease.DefaultExclusiveEnvironmentActivityLeaseTTL
			}
			return runActivityLeaseTake(cmd, resolveOpen, activitylease.TakeEnvironmentActivityLeaseParams{
				Tenant:      tenant,
				Environment: environment,
				Name:        name,
				ID:          id,
				PID:         pid,
				TTL:         ttl,
				Scope:       scope,
				Exclusive:   exclusive,
				Holder:      model.EnvironmentActivityLeaseHolder{Orchestrator: orchestrator},
			}, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "What the lease is holding the environment for (shown to the operator)")
	cmd.Flags().StringVar(&id, "id", "", "Lease id to take or renew (defaults to the name)")
	cmd.Flags().IntVar(&pid, "pid", 0, "Holder process in the environment to reconcile the lease against; the lease is reclaimed once it exits")
	cmd.Flags().DurationVar(&ttl, "ttl", activitylease.DefaultEnvironmentActivityLeaseTTL, "How long the lease holds without a renewal (defaults to 5m instead when --exclusive is set)")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "Claim exclusivity over --scope instead of plain presence; a second exclusive take in the same scope is refused and told who holds it")
	cmd.Flags().StringVar(&scope, "scope", "", "The resource this exclusive claim protects (default \"worktree\"); only meaningful with --exclusive")
	cmd.Flags().StringVar(&orchestrator, "orchestrator", "", "The calling orchestrator's own id, recorded on the lease so a refusal can name who to go ask")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the lease as JSON (alias for --output json)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runActivityLeaseTake(cmd *cobra.Command, resolveOpen envscope.OpenResolver, params activitylease.TakeEnvironmentActivityLeaseParams, jsonOutput bool) error {
	if err := validateActivityTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := clicmd.CommandContext(cmd)
	lease, resolved, err := takeLease(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if clicmd.CommandWantsJSON(ctx, jsonOutput) {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(lease)
	}
	_, err = fmt.Fprintf(ctx.Stdout, "lease held: %s (id %s), expires in %s\n", lease.Name, lease.ID, formatLeaseRemaining(lease, time.Now()))
	return err
}

func takeLease(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, params activitylease.TakeEnvironmentActivityLeaseParams) (model.EnvironmentActivityLease, bool, error) {
	if !envcall.EnvironmentTargetsItself() {
		return takeLeaseInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	if commandCtx.DryRun {
		if params.Exclusive {
			scope := activitylease.NormalizeExclusiveEnvironmentActivityLeaseScope(params.Scope)
			commandCtx.TraceCommand("", "activity", "lease-take", params.Tenant, params.Environment, params.Name, "--exclusive", "--scope", scope)
		} else {
			commandCtx.TraceCommand("", "activity", "lease-take", params.Tenant, params.Environment, params.Name)
		}
		return model.EnvironmentActivityLease{}, false, nil
	}
	lease, err := activitylease.TakeEnvironmentActivityLease(params)
	if err != nil {
		return model.EnvironmentActivityLease{}, false, err
	}
	return lease, true, nil
}

func newActivityLeaseReleaseCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	var exclusive bool
	var scope string
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Release a held lease so the environment can go idle again",
		Long: "Releasing a lease that was never taken, or has already expired, succeeds — so a\n" +
			"wrapper's exit trap never fails a job that finished cleanly — but the report\n" +
			"says whether anything was actually held, so a caller can tell the two apart\n" +
			"instead of reading the same success either way.\n\n" +
			"Pass --exclusive and the same --scope used at take time to release an\n" +
			"exclusive claim. Only the id that took it can release it: if the scope is held\n" +
			"by a different id, the release fails and names the actual holder rather than\n" +
			"silently leaving it in place.",
		Example: "  erun activity lease release --tenant team --environment dev --id gradle-build\n" +
			"  erun activity lease release --tenant team --environment dev --id job-fix-1245 --exclusive",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityLeaseRelease(cmd, resolveOpen, tenant, environment, id, scope, exclusive)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Lease id to release")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "Release an exclusive claim rather than a plain lease; must match how it was taken")
	cmd.Flags().StringVar(&scope, "scope", "", "The scope the exclusive claim was taken on (default \"worktree\"); only meaningful with --exclusive")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runActivityLeaseRelease(cmd *cobra.Command, resolveOpen envscope.OpenResolver, tenant, environment, id, scope string, exclusive bool) error {
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("lease id is required")
	}
	ctx := clicmd.CommandContext(cmd)
	outcome, resolved, err := releaseLease(cmd.Context(), ctx, resolveOpen, tenant, environment, id, scope, exclusive)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	// An unreadable note falls back to the plain summary; the release already succeeded.
	note := ""
	if outcome == model.EnvironmentActivityLeaseHeldElsewhere {
		note, _ = activitylease.EnvironmentActivityLeaseHeldElsewhereNote(tenant, environment, id, exclusive)
	}
	_, err = fmt.Fprintf(ctx.Stdout, "%s\n", releaseLeaseSummary(outcome, id, note))
	return err
}

// releaseLeaseSummary distinguishes "removed" from "nothing held", and names the
// matching release when the id holds a lease of the other shape.
func releaseLeaseSummary(outcome model.EnvironmentActivityLeaseReleaseOutcome, id, note string) string {
	trimmed := strings.TrimSpace(id)
	switch outcome {
	case model.EnvironmentActivityLeaseReleased:
		return fmt.Sprintf("lease released: %s", trimmed)
	case model.EnvironmentActivityLeaseHeldElsewhere:
		if note != "" {
			return fmt.Sprintf("lease not released: %s is %s", trimmed, note)
		}
	case model.EnvironmentActivityLeaseNotHeld:
	}
	return fmt.Sprintf("lease not held: %s", trimmed)
}

func releaseLease(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, tenant, environment, id, scope string, exclusive bool) (model.EnvironmentActivityLeaseReleaseOutcome, bool, error) {
	if !envcall.EnvironmentTargetsItself() {
		return releaseLeaseInEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id, scope, exclusive)
	}
	if commandCtx.DryRun {
		if exclusive {
			resolvedScope := activitylease.NormalizeExclusiveEnvironmentActivityLeaseScope(scope)
			commandCtx.TraceCommand("", "activity", "lease-release", tenant, environment, id, "--exclusive", "--scope", resolvedScope)
		} else {
			commandCtx.TraceCommand("", "activity", "lease-release", tenant, environment, id)
		}
		return model.EnvironmentActivityLeaseNotHeld, false, nil
	}
	if exclusive {
		outcome, err := activitylease.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, scope, id)
		if err != nil {
			return model.EnvironmentActivityLeaseNotHeld, false, err
		}
		return outcome, true, nil
	}
	outcome, err := activitylease.ReleaseEnvironmentActivityLease(tenant, environment, id)
	if err != nil {
		return model.EnvironmentActivityLeaseNotHeld, false, err
	}
	return outcome, true, nil
}

func newActivityLeaseListCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
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
			return runActivityLeaseList(cmd, resolveOpen, tenant, environment, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the leases as JSON (alias for --output json)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runActivityLeaseList(cmd *cobra.Command, resolveOpen envscope.OpenResolver, tenant, environment string, jsonOutput bool) error {
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	ctx := clicmd.CommandContext(cmd)
	leases, resolved, err := listLeases(cmd.Context(), ctx, resolveOpen, tenant, environment)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	return WriteActivityLeases(ctx, leases, time.Now(), jsonOutput)
}

func listLeases(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, tenant, environment string) ([]model.EnvironmentActivityLease, bool, error) {
	if !envcall.EnvironmentTargetsItself() {
		return envcall.ListLeasesInEnvironment(ctx, commandCtx, resolveOpen, tenant, environment)
	}
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "activity", "lease-list", tenant, environment)
		return nil, false, nil
	}
	leases, err := activitylease.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		return nil, false, err
	}
	return leases, true, nil
}

func WriteActivityLeases(ctx core.Context, leases []model.EnvironmentActivityLease, now time.Time, jsonOutput bool) error {
	if clicmd.CommandWantsJSON(ctx, jsonOutput) {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		if leases == nil {
			leases = []model.EnvironmentActivityLease{}
		}
		return encoder.Encode(leases)
	}
	if len(leases) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no leases held")
		return err
	}
	for _, lease := range leases {
		if err := textfmt.WriteLabeledValue(ctx, lease.ID, activityLeaseSummary(lease, now)); err != nil {
			return err
		}
	}
	return nil
}

// activityLeaseSummary renders one held claim, marking exclusive ones.
func activityLeaseSummary(lease model.EnvironmentActivityLease, now time.Time) string {
	value := fmt.Sprintf("%s, expires in %s", lease.Name, formatLeaseRemaining(lease, now))
	if lease.Exclusive {
		value = fmt.Sprintf("%s, exclusive on %s, held by %s, expires in %s",
			lease.Name,
			activitylease.NormalizeExclusiveEnvironmentActivityLeaseScope(lease.Scope),
			lease.Holder.String(),
			formatLeaseRemaining(lease, now))
	}
	if lease.PID > 0 {
		value += fmt.Sprintf(", pid %d", lease.PID)
	}
	return value
}

func formatLeaseRemaining(lease model.EnvironmentActivityLease, now time.Time) string {
	remaining := lease.ExpiresAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("%ds", int64(remaining.Round(time.Second)/time.Second))
}

// newActivitySampleCmd records activity from CPU use for work that took no lease,
// and retains a resource-usage reading on the same tick.
func newActivitySampleCmd() *cobra.Command {
	var tenant string
	var environment string
	var procRoot string
	var cgroupRoot string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "sample",
		Short: "Sample resident build and agent processes and record activity and resource usage",
		Long:  "Records activity only when a matched process burned CPU since the previous\nsample, so an agent parked at a prompt does not keep the environment awake.\nAlso records SSH activity when an sshd child process holds an allocated\npseudo-terminal, so a real interactive session reads as active while\nport-forward re-establishment and background sync traffic do not. Also\nretains the container's own cgroup CPU and memory counters, which is what\nlets erun recommend a size for this environment from what it has actually done.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivitySample(cmd, tenant, environment, procRoot, cgroupRoot, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&procRoot, "proc-root", sampling.DefaultProcRoot, "Process filesystem to sample")
	cmd.Flags().StringVar(&cgroupRoot, "cgroup-root", runtimeresources.DefaultCgroupRoot, "Cgroup filesystem to read this container's own CPU and memory counters from")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the sample verdict as JSON (alias for --output json)")
	return cmd
}

func recordActivityIf(found bool, tenant, environment, kind string) error {
	if !found {
		return nil
	}
	return records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant:      tenant,
		Environment: environment,
		Kind:        kind,
	})
}

func runActivitySample(cmd *cobra.Command, tenant, environment, procRoot, cgroupRoot string, jsonOutput bool) error {
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	previous, err := sampling.LoadResidentActivitySample(tenant, environment)
	if err != nil {
		return err
	}
	result, err := sampling.ScanResidentActivity(procRoot, os.Getpid(), previous, time.Now())
	if err != nil {
		return err
	}
	if err := sampling.SaveResidentActivitySample(tenant, environment, result.Sample); err != nil {
		return err
	}
	if err := retainRuntimeUsage(tenant, environment, cgroupRoot); err != nil {
		return err
	}
	if err := recordActivityIf(result.Busy, tenant, environment, records.ActivityKindProcess); err != nil {
		return err
	}
	interactiveSSH, err := sampling.ScanInteractiveSSHSession(procRoot)
	if err != nil {
		return err
	}
	if err := recordActivityIf(interactiveSSH, tenant, environment, config.ActivityKindSSH); err != nil {
		return err
	}
	return WriteActivitySampleResult(clicmd.CommandContext(cmd), result, jsonOutput)
}

func WriteActivitySampleResult(ctx core.Context, result model.ResidentActivityResult, jsonOutput bool) error {
	if clicmd.CommandWantsJSON(ctx, jsonOutput) {
		return json.NewEncoder(ctx.Stdout).Encode(result)
	}
	if !result.Busy {
		_, err := fmt.Fprintln(ctx.Stdout, "no working build or agent processes")
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "working: %s\n", strings.Join(result.Processes, ", "))
	return err
}

// retainRuntimeUsage records nothing when the host supplies no cgroup counters.
func retainRuntimeUsage(tenant, environment, cgroupRoot string) error {
	sample := runtimeresources.ReadLocalRuntimeUsage(cgroupRoot)
	if !sample.HasCounters() {
		return nil
	}
	history, err := runtimeusage.LoadRuntimeUsageHistory(tenant, environment)
	if err != nil {
		return err
	}
	return podruntime.SaveRuntimeUsageHistory(tenant, environment, podruntime.AppendRuntimeUsageSample(history, sample, time.Now()))
}

func validateActivityTarget(tenant, environment string) error {
	missing := envcall.MissingTenantOrEnvironmentFlags(tenant, environment)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s not set: erun activity commands run outside any resolved environment and never read the ambient config -- pass --tenant and --environment explicitly",
		strings.Join(missing, " and "),
	)
}
