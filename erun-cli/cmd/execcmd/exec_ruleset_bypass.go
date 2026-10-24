package execcmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

func newExecReconcileBypassCmd(store config.CloudReadStore, deps cloud.CloudDependencies) *cobra.Command {
	var (
		remoteURL      string
		rulesetID      int64
		targetBranch   string
		since          string
		expectedActors []string
		alias          string
	)
	cmd := &cobra.Command{
		Use:   "reconcile-bypass",
		Short: "Check every ruleset-bypassed push against a real successful build",
		Long: "Cross-reference GitHub's own bypass ledger (`GET .../rulesets/rule-suites`) for --ruleset-id on " +
			"--target-branch against erun's recorded builds: every push that bypassed --ruleset-id is reported next to " +
			"what accounts for it. A push is RECONCILED when a successful build is recorded for one of the commits it " +
			"landed, RELEASE when a tag in the repository points at one of them (a release stamps, tags and then " +
			"pushes, so its own commits were never built as a merge), and UNRECONCILED when nothing accounts for " +
			"it at all.\n\n" +
			"--expected-actor names the identities allowed to hold the bypass grant. Any other actor's bypass is " +
			"reported UNEXPECTED_ACTOR even when the content it landed was really built -- that is what makes " +
			"narrowing the grant to one non-human identity (`erun exec plan-ruleset-bypass`) observable rather " +
			"than merely configured.\n\n" +
			"Exits non-zero, after printing the full report, when any push is unaccounted for or any unnamed " +
			"identity bypassed -- a bypass nobody can explain is loud, never silent.\n\n" +
			"--dry-run traces the GitHub and platform lookups without sending them.",
		Example: "  erun exec reconcile-bypass --ruleset-id 11081432 --target-branch main \\\n" +
			"    --expected-actor erun-merge-queue",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecReconcileBypassCommand(shared.CommandContext(cmd), store, alias, review.ReconcileBypassParams{
				RemoteURL:      remoteURL,
				RulesetID:      rulesetID,
				TargetBranch:   targetBranch,
				Since:          since,
				ExpectedActors: expectedActors,
			}, deps)
		},
	}
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The github.com remote the ruleset lives on (defaults to the current checkout's origin)")
	cmd.Flags().Int64Var(&rulesetID, "ruleset-id", 0, "The ruleset to check bypasses against (required)")
	cmd.Flags().StringVar(&targetBranch, "target-branch", "", "The ruleset's protected branch (required)")
	cmd.Flags().StringVar(&since, "since", "", "Narrow the GitHub lookup window: hour, day, week, or month (defaults to GitHub's own window)")
	cmd.Flags().StringArrayVar(&expectedActors, "expected-actor", nil, "An identity allowed to hold the bypass grant; repeatable. Any other actor's bypass is reported UNEXPECTED_ACTOR")
	cmd.Flags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

// newExecPlanRulesetBypassCmd builds `erun exec plan-ruleset-bypass`: the
// read-only half of narrowing a protected branch's bypass grant to one
// non-human identity. It resolves the edit from the live ruleset, proves the
// identity can already push, and writes the payload each stage and the
// rollback apply — it never changes repository settings itself.
func newExecPlanRulesetBypassCmd() *cobra.Command {
	var params review.PlanRulesetBypassParams
	cmd := &cobra.Command{
		Use:   "plan-ruleset-bypass",
		Short: "Plan narrowing a ruleset's bypass grant to one non-human queue identity",
		Long: "A merge queue's push is itself a direct push, so it must bypass the branch's pull-request rule. " +
			"That is only accountable if exactly one nameable, non-human identity can bypass at all -- and " +
			"GitHub's bypass is per-actor, not per-rule, so adding a required status check on top of a broad " +
			"`always` grant changes nothing for whoever holds it.\n\n" +
			"This resolves the exact edit from the ruleset as it actually is, in two stages that never leave the " +
			"branch unmergeable: stage 1 grants --queue-actor an `always` bypass alongside today's actors (both " +
			"paths open, so the queue can be proven under the new identity first), stage 2 demotes every other " +
			"`always` actor to `pull_request` (an emergency lever that still requires a pull request). It refuses " +
			"up front when the queue identity cannot already push, when GitHub will not show the ruleset's bypass " +
			"actors (plan with an admin token), or when --target-branch is not a branch this ruleset governs.\n\n" +
			"Writes three payload files -- stage1, stage2 and rollback (today's bypass list, exactly) -- and " +
			"prints the `gh api` calls that apply, verify and revert them. It never writes to GitHub: a ruleset " +
			"governs every contributor's merges, so applying the edit stays a deliberate, human step.\n\n" +
			"--dry-run traces the GitHub lookups and the files it would write without sending or writing anything.",
		Example: "  erun exec plan-ruleset-bypass --ruleset-id 11081432 --target-branch main \\\n" +
			"    --queue-actor erun-merge-queue --out-dir .erun/ruleset-plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecPlanRulesetBypassCommand(shared.CommandContext(cmd), params)
		},
	}
	cmd.Flags().StringVar(&params.RemoteURL, "remote-url", "", "The github.com remote the ruleset lives on (defaults to the current checkout's origin)")
	cmd.Flags().Int64Var(&params.RulesetID, "ruleset-id", 0, "The ruleset whose bypass grant is being narrowed (required)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "The protected branch the ruleset must govern, checked against its own conditions")
	cmd.Flags().StringVar(&params.QueueActorType, "queue-actor-type", "User", "The queue identity's GitHub actor type: User, Integration, Team, RepositoryRole, OrganizationAdmin, or DeployKey")
	cmd.Flags().StringVar(&params.QueueActor, "queue-actor", "", "The queue identity: a login for User, otherwise the numeric actor id (required)")
	cmd.Flags().StringVar(&params.OutDir, "out-dir", ".", "Directory the stage1, stage2 and rollback payload files are written to")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runExecPlanRulesetBypassCommand(ctx core.Context, params review.PlanRulesetBypassParams) error {
	result, err := review.PlanRulesetBypass(ctx, params, review.PlanRulesetBypassDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec plan-ruleset-bypass planned.")
		return err
	}
	if ctx.Output != core.OutputJSON {
		if err := writeRulesetBypassPlan(ctx, result); err != nil {
			return err
		}
	}
	return ctx.WriteResult(result)
}

// writeRulesetBypassPlan renders the plan as the ordered set of commands an
// operator runs: apply, verify, and — if any of it goes wrong — revert. A plan
// that named the edit without naming its verification and its way back would
// be exactly the dead end this command exists to avoid.
func writeRulesetBypassPlan(ctx core.Context, result review.PlanRulesetBypassResult) error {
	out := new(strings.Builder)
	fmt.Fprintf(out, "Ruleset %d (%q) on %s/%s\n", result.RulesetID, result.RulesetName, result.Owner, result.Repo)
	fmt.Fprintf(out, "  bypass actors today (%d):\n", len(result.CurrentBypassActors))
	for _, actor := range result.CurrentBypassActors {
		fmt.Fprintf(out, "    %-18s %-8s %s\n", actor.ActorType, formatRulesetActorID(actor.ActorID), actor.BypassMode)
	}
	fmt.Fprintf(out, "  this token's own bypass: %s\n", orUnknown(result.CallerCanBypass))
	fmt.Fprintf(out, "  queue identity: %s %s (actor id %d)%s\n", result.QueueActorType, result.QueueActor,
		result.QueueActorID, formatQueueActorAccess(result.QueueActorPushAccess))

	apply := "gh api --method PUT repos/" + result.Owner + "/" + result.Repo + "/rulesets/" +
		strconv.FormatInt(result.RulesetID, 10) + " --input "
	fmt.Fprintf(out, "\nStage 1 -- grant the queue identity bypass alongside today's actors:\n  %s%s\n", apply, result.Stage1File)
	fmt.Fprintf(out, "Then prove the queue under the new identity: run one real queued merge as %s before stage 2.\n", result.QueueActor)
	fmt.Fprintf(out, "\nStage 2 -- demote every other always-bypass actor to pull_request:\n  %s%s\n", apply, result.Stage2File)

	read := "gh api repos/" + result.Owner + "/" + result.Repo + "/rulesets/" +
		strconv.FormatInt(result.RulesetID, 10) + " --jq .current_user_can_bypass"
	fmt.Fprintf(out, "\nVerify (GitHub answers this per token, so run it as each identity):\n")
	fmt.Fprintf(out, "  GH_TOKEN=<%s's token> %s   # want: always\n", result.QueueActor, read)
	fmt.Fprintf(out, "  GH_TOKEN=<a human's token> %s   # want: pull_requests_only after stage 2\n", read)
	fmt.Fprintf(out, "  erun exec reconcile-bypass --ruleset-id %d --target-branch %s --expected-actor %s\n",
		result.RulesetID, orUnknown(result.TargetBranch), result.QueueActor)
	fmt.Fprintf(out, "\nRollback (restores today's bypass list exactly):\n  %s%s\n", apply, result.RollbackFile)
	_, err := fmt.Fprint(ctx.Stdout, out.String())
	return err
}

func formatRulesetActorID(actorID *int64) string {
	if actorID == nil {
		return "-"
	}
	return strconv.FormatInt(*actorID, 10)
}

func formatQueueActorAccess(access string) string {
	if access == "" {
		return ", push access not verifiable for this actor type"
	}
	return ", " + access + " access"
}

// orUnknown keeps a plan readable when GitHub answered nothing for a field
// rather than printing a bare blank the reader cannot interpret.
func orUnknown(value string) string {
	if value == "" {
		return "(unknown)"
	}
	return value
}

// newExecRouteCheckCmd builds `erun exec route-check`: proves every route
// erun-backend-api's router registers is actually reachable on a deployed
// plane. A route can merge, get unit-tested, and close its issue while
// still 404ing on the live control plane for months, because nothing but a
// human running the CLI by hand had ever exercised the deployed route.
func newExecRouteCheckCmd(store config.CloudReadStore, deps cloud.CloudDependencies) *cobra.Command {
	var (
		routesDir string
		alias     string
	)
	cmd := &cobra.Command{
		Use:   "route-check",
		Short: "Prove every registered API route is reachable on a deployed plane",
		Long: "Reads erun-backend-api's own registered routes straight out of its source (never a hand-maintained " +
			"list) and GETs each one against the plane --erun-alias resolves. It first sanity-probes GET /v1/whoami " +
			"and refuses outright if that does not answer, so a down or misconfigured plane is never reported as " +
			"every route missing. Every probe is a plain GET regardless of a route's own registered method -- Go's " +
			"router reports 405 for a path it knows under a different method, so this never risks creating, " +
			"updating, or deleting anything -- and only the plane's own unmodified \"404 page not found\" means a " +
			"route was never registered at all; an application-level 404 (a well-formed request for an id that " +
			"doesn't exist) always looks different and is reported reachable.\n\n" +
			"Exits non-zero, after printing the full report, when the plane cannot be reached at all or when any " +
			"registered route comes back missing.\n\n" +
			"--dry-run traces the resolved plane and the route inventory without sending any request.",
		Example:      "  erun exec route-check --erun-alias erun+api.erunpaas.com@erun",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecRouteCheckCommand(shared.CommandContext(cmd), store, alias, expose.RouteCheckParams{
				RoutesDir: routesDir,
			}, deps)
		},
	}
	cmd.Flags().StringVar(&routesDir, "routes-dir", "",
		"Path to erun-backend-api/internal/routes (defaults to that path under the current checkout's project root)")
	cmd.Flags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runExecRouteCheckCommand(ctx core.Context, store config.CloudReadStore, alias string, params expose.RouteCheckParams, deps cloud.CloudDependencies) error {
	result, err := expose.RunRouteCheck(ctx, store, alias, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec route-check planned.")
		return err
	}
	if ctx.Output != core.OutputJSON {
		if err := writeRouteCheckReport(ctx, result); err != nil {
			return err
		}
	}
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	return routeCheckExitError(result)
}

// writeRouteCheckReport prints one line per missing route -- the loud
// failure this command exists to surface -- and a summary line always, so a
// clean run is visibly clean rather than silent.
func writeRouteCheckReport(ctx core.Context, result expose.RouteCheckResult) error {
	if !result.PlaneReachable {
		_, err := fmt.Fprintf(ctx.Stdout, "%s did not answer the sanity probe (%s); no routes were checked.\n",
			result.APIURL, result.UnreachableReason)
		return err
	}
	for _, missing := range result.Missing {
		if _, err := fmt.Fprintf(ctx.Stdout, "MISSING  %-7s %s\n", missing.Method, missing.Path); err != nil {
			return err
		}
	}
	for _, probeErr := range result.Errors {
		if _, err := fmt.Fprintf(ctx.Stdout, "ERROR    %-7s %s: %s\n", probeErr.Method, probeErr.Path, probeErr.Detail); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%d/%d route(s) reachable on %s.\n",
		result.RoutesChecked-len(result.Missing)-len(result.Errors), result.RoutesChecked, result.APIURL)
	return err
}

// routeCheckExitError makes a missing route (or a probe that could not even
// complete) a non-zero exit, after the full report has already printed --
// the same "print everything, then fail loudly" shape reconcileBypassExitError
// uses.
func routeCheckExitError(result expose.RouteCheckResult) error {
	if !result.PlaneReachable {
		return fmt.Errorf("route-check: %s did not answer the sanity probe (%s); refusing to report on an unreachable plane",
			result.APIURL, result.UnreachableReason)
	}
	var problems []string
	if len(result.Missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d registered route(s) are missing on the deployed plane", len(result.Missing)))
	}
	if len(result.Errors) > 0 {
		problems = append(problems, fmt.Sprintf("%d route probe(s) could not complete", len(result.Errors)))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("of %d route(s) checked on %s: %s", result.RoutesChecked, result.APIURL, strings.Join(problems, "; "))
}

func runExecReconcileBypassCommand(ctx core.Context, store config.CloudReadStore, alias string, params review.ReconcileBypassParams, deps cloud.CloudDependencies) error {
	result, err := review.ReconcileBypass(ctx, store, alias, params, deps, review.ReconcileBypassDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec reconcile-bypass planned.")
		return err
	}
	if ctx.Output != core.OutputJSON {
		if err := writeReconcileBypassReport(ctx, result); err != nil {
			return err
		}
	}
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	return reconcileBypassExitError(result)
}

// reconcileBypassExitError turns the report's two distinct failures into one
// non-zero exit, naming both rather than collapsing them: content nobody
// built and an identity nobody expected are different problems with different
// remedies.
func reconcileBypassExitError(result review.ReconcileBypassResult) error {
	var problems []string
	if result.Unreconciled > 0 {
		problems = append(problems, fmt.Sprintf("%d have no successful build and no release tag accounting for them",
			result.Unreconciled))
	}
	if result.UnexpectedActors > 0 {
		problems = append(problems, fmt.Sprintf("%d were bypassed by an unexpected identity", result.UnexpectedActors))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("of %d bypassed push(es) on %s: %s",
		len(result.Pushes), result.TargetBranch, strings.Join(problems, "; "))
}

func writeReconcileBypassReport(ctx core.Context, result review.ReconcileBypassResult) error {
	if len(result.Pushes) == 0 {
		_, err := fmt.Fprintf(ctx.Stdout, "No bypassed pushes found for %s/%s ruleset %d on %s.\n",
			result.Owner, result.Repo, result.RulesetID, result.TargetBranch)
		return err
	}
	for _, push := range result.Pushes {
		detail := push.Reason
		if detail == "" {
			detail = "build " + push.BuildID
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "%-17s %s  %s  bypassed %s by %s (%s)\n",
			push.Verdict, push.Commit, push.PushedAt, push.BypassedRule, push.Actor, detail); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%d/%d unaccounted for, %d by an unexpected identity.\n",
		result.Unreconciled, len(result.Pushes), result.UnexpectedActors)
	return err
}
