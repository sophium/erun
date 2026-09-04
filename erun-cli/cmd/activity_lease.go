package cmd

import (
	"context"
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
// that holds one across a detached run. A lease belongs to the environment it
// holds, so off-environment these act through its edge.

func newActivityLeaseCmd(resolveOpen OpenResolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "lease",
		Short:         "Hold, release, and inspect activity leases",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(newActivityLeaseTakeCmd(resolveOpen), newActivityLeaseReleaseCmd(resolveOpen), newActivityLeaseListCmd(resolveOpen))
	return cmd
}

func newActivityLeaseTakeCmd(resolveOpen OpenResolver) *cobra.Command {
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
			"second agent job or orchestrator already working the same worktree is refused\n" +
			"and named in the error, while a job in a different scope - a separate clone in\n" +
			"the same pod - is unaffected. An exclusive take is also refused while an\n" +
			"operator's own SSH session is active in the environment.",
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
				ttl = common.DefaultExclusiveEnvironmentActivityLeaseTTL
			}
			return runActivityLeaseTake(cmd, resolveOpen, common.TakeEnvironmentActivityLeaseParams{
				Tenant:      tenant,
				Environment: environment,
				Name:        name,
				ID:          id,
				PID:         pid,
				TTL:         ttl,
				Scope:       scope,
				Exclusive:   exclusive,
				Holder:      common.EnvironmentActivityLeaseHolder{Orchestrator: orchestrator},
			}, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "What the lease is holding the environment for (shown to the operator)")
	cmd.Flags().StringVar(&id, "id", "", "Lease id to take or renew (defaults to the name)")
	cmd.Flags().IntVar(&pid, "pid", 0, "Holder process in the environment to reconcile the lease against; the lease is reclaimed once it exits")
	cmd.Flags().DurationVar(&ttl, "ttl", common.DefaultEnvironmentActivityLeaseTTL, "How long the lease holds without a renewal (defaults to 5m instead when --exclusive is set)")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "Claim exclusivity over --scope instead of plain presence; a second exclusive take in the same scope is refused and told who holds it")
	cmd.Flags().StringVar(&scope, "scope", "", "The resource this exclusive claim protects (default \"worktree\"); only meaningful with --exclusive")
	cmd.Flags().StringVar(&orchestrator, "orchestrator", "", "The calling orchestrator's own id, recorded on the lease so a refusal can name who to go ask")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the lease as JSON")
	addDryRunFlag(cmd)
	return cmd
}

func runActivityLeaseTake(cmd *cobra.Command, resolveOpen OpenResolver, params common.TakeEnvironmentActivityLeaseParams, jsonOutput bool) error {
	if err := validateActivityTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	lease, resolved, err := takeLease(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if jsonOutput {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(lease)
	}
	_, err = fmt.Fprintf(ctx.Stdout, "lease held: %s (id %s), expires in %s\n", lease.Name, lease.ID, formatLeaseRemaining(lease, time.Now()))
	return err
}

func takeLease(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.TakeEnvironmentActivityLeaseParams) (common.EnvironmentActivityLease, bool, error) {
	if !environmentTargetsItself() {
		return takeLeaseInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	if commandCtx.DryRun {
		if params.Exclusive {
			scope := common.NormalizeExclusiveEnvironmentActivityLeaseScope(params.Scope)
			commandCtx.TraceCommand("", "activity", "lease-take", params.Tenant, params.Environment, params.Name, "--exclusive", "--scope", scope)
		} else {
			commandCtx.TraceCommand("", "activity", "lease-take", params.Tenant, params.Environment, params.Name)
		}
		return common.EnvironmentActivityLease{}, false, nil
	}
	lease, err := common.TakeEnvironmentActivityLease(params)
	if err != nil {
		return common.EnvironmentActivityLease{}, false, err
	}
	return lease, true, nil
}

func newActivityLeaseReleaseCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	var exclusive bool
	var scope string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Release a held lease so the environment can go idle again",
		Long: "Releasing a lease that was never taken, or has already expired, always exits 0 —\n" +
			"so a wrapper's exit trap never fails a job that finished cleanly. But a release\n" +
			"that matched nothing is reported honestly rather than as \"lease released\": when\n" +
			"the id is actually held under a different shape than asked (a plain lease vs an\n" +
			"--exclusive claim, or a different --scope), the message names it and the flags\n" +
			"that will actually release it.\n\n" +
			"Pass --exclusive and the same --scope used at take time to release an\n" +
			"exclusive claim; only the id that took it can release it.",
		Example: "  erun activity lease release --tenant team --environment dev --id gradle-build\n" +
			"  erun activity lease release --tenant team --environment dev --id job-fix-1245 --exclusive",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivityLeaseRelease(cmd, resolveOpen, tenant, environment, id, scope, exclusive, jsonOutput)
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Lease id to release")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "Release an exclusive claim rather than a plain lease; must match how it was taken")
	cmd.Flags().StringVar(&scope, "scope", "", "The scope the exclusive claim was taken on (default \"worktree\"); only meaningful with --exclusive")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the release result (id, released, note) as JSON")
	addDryRunFlag(cmd)
	return cmd
}

func runActivityLeaseRelease(cmd *cobra.Command, resolveOpen OpenResolver, tenant, environment, id, scope string, exclusive, jsonOutput bool) error {
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return fmt.Errorf("lease id is required")
	}
	ctx := commandContext(cmd)
	result, resolved, err := releaseLease(cmd.Context(), ctx, resolveOpen, tenant, environment, id, scope, exclusive)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	return writeActivityLeaseReleaseResult(ctx, trimmedID, result, jsonOutput)
}

// writeActivityLeaseReleaseResult reports what the release actually did.
// erun#2115: a no-match release used to print the identical "lease released"
// success text a real release does, hiding a still-held exclusive claim from
// an operator who released it with the wrong flags. The exit code stays 0
// either way — a wrapper's cleanup trap must never fail over this — but the
// message (and --json's released field) now says plainly whether anything
// was removed, and names the fix when the id is held under a different shape.
func writeActivityLeaseReleaseResult(ctx common.Context, id string, result common.ReleaseEnvironmentActivityLeaseResult, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(struct {
			ID       string `json:"id"`
			Released bool   `json:"released"`
			Note     string `json:"note,omitempty"`
		}{ID: id, Released: result.Released, Note: result.Note})
	}
	if result.Released {
		_, err := fmt.Fprintf(ctx.Stdout, "lease released: %s\n", id)
		return err
	}
	if result.Note != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "no lease released: %s (%s)\n", id, result.Note)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "no lease released: %s (nothing was held with that id)\n", id)
	return err
}

func releaseLease(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, id, scope string, exclusive bool) (common.ReleaseEnvironmentActivityLeaseResult, bool, error) {
	if !environmentTargetsItself() {
		return releaseLeaseInEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id, scope, exclusive)
	}
	if commandCtx.DryRun {
		if exclusive {
			resolvedScope := common.NormalizeExclusiveEnvironmentActivityLeaseScope(scope)
			commandCtx.TraceCommand("", "activity", "lease-release", tenant, environment, id, "--exclusive", "--scope", resolvedScope)
		} else {
			commandCtx.TraceCommand("", "activity", "lease-release", tenant, environment, id)
		}
		return common.ReleaseEnvironmentActivityLeaseResult{}, false, nil
	}
	if exclusive {
		result, err := common.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, scope, id)
		if err != nil {
			return common.ReleaseEnvironmentActivityLeaseResult{}, false, err
		}
		return result, true, nil
	}
	result, err := common.ReleaseEnvironmentActivityLease(tenant, environment, id)
	if err != nil {
		return common.ReleaseEnvironmentActivityLeaseResult{}, false, err
	}
	return result, true, nil
}

func newActivityLeaseListCmd(resolveOpen OpenResolver) *cobra.Command {
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
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the leases as JSON")
	addDryRunFlag(cmd)
	return cmd
}

func runActivityLeaseList(cmd *cobra.Command, resolveOpen OpenResolver, tenant, environment string, jsonOutput bool) error {
	if err := validateActivityTarget(tenant, environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	leases, resolved, err := listLeases(cmd.Context(), ctx, resolveOpen, tenant, environment)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	return writeActivityLeases(ctx, leases, time.Now(), jsonOutput)
}

func listLeases(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment string) ([]common.EnvironmentActivityLease, bool, error) {
	if !environmentTargetsItself() {
		return listLeasesInEnvironment(ctx, commandCtx, resolveOpen, tenant, environment)
	}
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "activity", "lease-list", tenant, environment)
		return nil, false, nil
	}
	leases, err := common.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		return nil, false, err
	}
	return leases, true, nil
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
		// Naming the exclusive scope here is what lets an operator write the
		// matching release (--exclusive --scope <scope>) from what list
		// prints, instead of guessing after a release reports no match
		// (erun#2115).
		if lease.Exclusive {
			value += fmt.Sprintf(", exclusive scope %s", lease.Scope)
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
// processes doing it burned CPU since the previous tick. The same tick retains a
// resource-usage reading, so an environment accumulates the history its sizing
// recommendation is derived from without a second scheduled job.
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
	cmd.Flags().StringVar(&procRoot, "proc-root", common.DefaultProcRoot, "Process filesystem to sample")
	cmd.Flags().StringVar(&cgroupRoot, "cgroup-root", common.DefaultCgroupRoot, "Cgroup filesystem to read this container's own CPU and memory counters from")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the sample verdict as JSON")
	return cmd
}

// recordActivityIf records the given kind only when found is true, so a
// sampler tick that detects nothing leaves the activity snapshot untouched.
func recordActivityIf(found bool, tenant, environment, kind string) error {
	if !found {
		return nil
	}
	return common.RecordEnvironmentActivity(common.EnvironmentActivityParams{
		Tenant:      tenant,
		Environment: environment,
		Kind:        kind,
	})
}

func runActivitySample(cmd *cobra.Command, tenant, environment, procRoot, cgroupRoot string, jsonOutput bool) error {
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
	if err := retainRuntimeUsage(tenant, environment, cgroupRoot); err != nil {
		return err
	}
	if err := recordActivityIf(result.Busy, tenant, environment, common.ActivityKindProcess); err != nil {
		return err
	}
	interactiveSSH, err := common.ScanInteractiveSSHSession(procRoot)
	if err != nil {
		return err
	}
	if err := recordActivityIf(interactiveSSH, tenant, environment, common.ActivityKindSSH); err != nil {
		return err
	}
	return writeActivitySampleResult(commandContext(cmd), result, jsonOutput)
}

func writeActivitySampleResult(ctx common.Context, result common.ResidentActivityResult, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(ctx.Stdout).Encode(result)
	}
	if !result.Busy {
		_, err := fmt.Fprintln(ctx.Stdout, "no working build or agent processes")
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "working: %s\n", strings.Join(result.Processes, ", "))
	return err
}

// retainRuntimeUsage folds this tick's cgroup reading into the environment's
// retained history. A host that supplies no counters records nothing: a history
// of empty samples would only ever support "no evidence", and this runs on a
// laptop as readily as in a container.
func retainRuntimeUsage(tenant, environment, cgroupRoot string) error {
	sample := common.ReadLocalRuntimeUsage(cgroupRoot)
	if !sample.HasCounters() {
		return nil
	}
	history, err := common.LoadRuntimeUsageHistory(tenant, environment)
	if err != nil {
		return err
	}
	return common.SaveRuntimeUsageHistory(tenant, environment, common.AppendRuntimeUsageSample(history, sample, time.Now()))
}

// missingTenantOrEnvironmentFlags names which of the shared --tenant/--environment
// flags a caller left empty, for validateActivityTarget/validateJobTarget to build
// an operation-specific refusal instead of a bare "tenant and environment are
// required" dead end.
func missingTenantOrEnvironmentFlags(tenant, environment string) []string {
	var missing []string
	if strings.TrimSpace(tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(environment) == "" {
		missing = append(missing, "environment")
	}
	return missing
}

func validateActivityTarget(tenant, environment string) error {
	missing := missingTenantOrEnvironmentFlags(tenant, environment)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s not set: erun activity commands run outside any resolved environment and never read the ambient config -- pass --tenant and --environment explicitly",
		strings.Join(missing, " and "),
	)
}
