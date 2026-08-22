package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// `erun pin` re-pins every place an environment records its erun version, in one
// motion. Those places only work when they agree, and nothing kept them in step:
// a repo was found with Terraform on one version, its charts on a second and the
// running binary on a third.
//
// It edits the source of truth and stops there. Realizing the new version —
// terraform apply, deploy — stays a separate explicit step, so changing a pin is
// never a rollout by accident.
func newPinCmd(prepareContext func(common.Context) common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var version string
	var latest, revert, list bool
	target := common.OpenParams{}

	cmd := &cobra.Command{
		Use:   "pin [TENANT] [ENVIRONMENT]",
		Short: "Re-pin every erun version reference for an environment",
		Long: "Re-pin every erun version reference for an environment: the Terraform module refs, " +
			"an erun image reference set directly in Terraform variables (e.g. the cluster-edge " +
			"module's dns01_webhook_image), each umbrella chart's erun dependencies, the build-env " +
			"image tag, and the environment's own runtime version.\n\n" +
			"Idempotent, and a no-op once aligned. It rewrites the source of truth only — realizing " +
			"the new version (terraform apply, deploy) stays a separate explicit step.",
		Example: "  erun pin --list\n  erun pin acme dev --dry-run\n  erun pin acme dev --version 1.0.175\n  erun pin acme dev --revert",
		Args:    cobra.MaximumNArgs(2),

		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			params, err := resolveOpenParams(args, target)
			if err != nil {
				return err
			}
			result, err := resolveOpen(params)
			if err != nil {
				return err
			}
			if list {
				return runPinListCommand(cmd.Context(), ctx, result)
			}
			return runPinCommand(cmd.Context(), ctx, result, pinRequest{
				Version: version,
				Latest:  latest,
				Revert:  revert,
			}, saveEnvConfig, findProjectRoot)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Re-pin a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Re-pin a specific environment")
	cmd.Flags().StringVar(&version, "version", "", "Pin to this erun version")
	cmd.Flags().BoolVar(&latest, "latest", false, "Pin to the latest published stable release")
	cmd.Flags().BoolVar(&revert, "revert", false, "Pin back to the version recorded before the last re-pin")
	cmd.Flags().BoolVar(&list, "list", false, "List the published erun versions available to pin to")
	return cmd
}

type pinRequest struct {
	Version string
	Latest  bool
	Revert  bool
}

// runPinListCommand answers "what can I pin to" from the registry, so choosing a
// version is recognition rather than recall.
func runPinListCommand(ctx context.Context, cmdCtx common.Context, result common.OpenResult) error {
	versions, err := resolvePinRegistryVersions(ctx)
	if err != nil {
		return err
	}
	available := slices.Clone(versions.Tags)
	slices.Sort(available)
	slices.Reverse(available)
	cmdCtx.Info(fmt.Sprintf("latest stable: %s", orNone(versions.LatestStable)))
	cmdCtx.Info(fmt.Sprintf("latest snapshot: %s", orNone(versions.LatestSnapshot)))
	for _, tag := range available {
		cmdCtx.Info("  " + tag)
	}
	return cmdCtx.WriteResult(versions)
}

func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none published)"
	}
	return value
}

func runPinCommand(ctx context.Context, cmdCtx common.Context, result common.OpenResult, request pinRequest, saveEnvConfig func(string, common.EnvConfig) error, findProjectRoot common.ProjectFinderFunc) error {
	projectRoot, err := resolvePinProjectRoot(result, findProjectRoot)
	if err != nil {
		return err
	}
	targetVersion, err := resolvePinTarget(ctx, cmdCtx, result, request, projectRoot)
	if err != nil {
		return err
	}

	plan, err := common.ResolvePinPlan(projectRoot, result.Tenant, result.Environment, result.EnvConfig, targetVersion)
	if err != nil {
		return err
	}
	tracePinPlan(cmdCtx, plan)

	if plan.Aligned() {
		cmdCtx.Info(fmt.Sprintf("%s/%s is already pinned to %s — nothing to change.", result.Tenant, result.Environment, plan.Target))
		return cmdCtx.WriteResult(plan)
	}
	if cmdCtx.DryRun {
		return nil
	}

	// Recorded before anything moves, so a revert has somewhere to go even if the
	// rewrite fails partway.
	if err := common.RecordPinPrevious(projectRoot, result.Tenant, result.Environment, plan.Previous); err != nil {
		return err
	}
	if err := common.ApplyPinPlan(plan); err != nil {
		return err
	}
	if err := savePinnedRuntimeVersion(result, plan.Target, saveEnvConfig); err != nil {
		return err
	}
	// The rewritten Chart.yaml and the lock beside it have to agree, or the next
	// deploy fails on a tree this command just called aligned.
	if err := common.RefreshPinnedChartLocks(cmdCtx, plan, nil); err != nil {
		return err
	}

	cmdCtx.Info(fmt.Sprintf("Pinned %s/%s to %s across %d references (was %s).",
		result.Tenant, result.Environment, plan.Target, len(plan.Changes()), orNone(plan.Previous)))
	cmdCtx.Info("Nothing is deployed yet: run `erun terraform apply` and `erun deploy` to realize it.")
	return cmdCtx.WriteResult(plan)
}

// resolvePinTarget answers which version to pin to, and refuses a version that
// is not actually published. Pinning to something unpublished produces a tree
// that only fails later, at terraform init or a chart pull, far from the cause.
func resolvePinTarget(ctx context.Context, cmdCtx common.Context, result common.OpenResult, request pinRequest, projectRoot string) (string, error) {
	if request.Revert {
		previous, ok := common.PinPrevious(projectRoot, result.Tenant, result.Environment)
		if !ok {
			return "", fmt.Errorf("no previous pin recorded for %s/%s — nothing to revert to", result.Tenant, result.Environment)
		}
		cmdCtx.Trace("reverting to the version recorded before the last re-pin: " + previous)
		return previous, nil
	}
	explicit := strings.TrimSpace(request.Version)
	if explicit != "" && request.Latest {
		return "", fmt.Errorf("--version and --latest both name a target; pass one")
	}
	versions, err := resolvePinRegistryVersions(ctx)
	if err != nil {
		// Choosing the latest needs the registry; verifying an explicit target
		// does not have to fail closed on an unreadable one.
		if explicit == "" {
			return "", fmt.Errorf("could not read the published erun versions to resolve the latest: %w", err)
		}
		cmdCtx.Trace("could not read the published versions, so " + explicit + " is pinned unverified: " + err.Error())
		return explicit, nil
	}
	if explicit == "" {
		if strings.TrimSpace(versions.LatestStable) == "" {
			return "", fmt.Errorf("no published stable release was found to pin to")
		}
		cmdCtx.Trace("resolved the latest published stable release: " + versions.LatestStable)
		return versions.LatestStable, nil
	}
	if err := verifyPinTargetPublished(explicit, versions); err != nil {
		return "", err
	}
	return explicit, nil
}

// verifyPinTargetPublished refuses a target the registry does not carry. An
// empty tag listing is not proof of absence, so it is treated as "cannot check"
// rather than "not there".
func verifyPinTargetPublished(target string, versions common.RuntimeRegistryVersions) error {
	wanted := strings.TrimPrefix(strings.TrimSpace(target), "v")
	if len(versions.Tags) == 0 {
		return nil
	}
	for _, tag := range versions.Tags {
		if strings.TrimPrefix(strings.TrimSpace(tag), "v") == wanted {
			return nil
		}
	}
	return fmt.Errorf("erun version %s is not published in %s — pin to a released version (see `erun pin --list`)", target, versions.Image)
}

// resolvePinRegistryVersions reads what erun has actually published. A pin is
// only meaningful against a released version, so the registry — not a local
// guess — is what decides which targets exist.
func resolvePinRegistryVersions(ctx context.Context) (common.RuntimeRegistryVersions, error) {
	return common.ResolveDefaultRuntimeRegistryVersions(ctx)
}

// resolvePinProjectRoot finds the tree whose pins are being rewritten. The
// environment's repo path is authoritative when it has one; otherwise the
// discovered project root is.
func resolvePinProjectRoot(result common.OpenResult, findProjectRoot common.ProjectFinderFunc) (string, error) {
	if path := strings.TrimSpace(result.RepoPath); path != "" && !result.RemoteRepo() {
		return path, nil
	}
	if findProjectRoot != nil {
		if _, root, err := findProjectRoot(); err == nil && strings.TrimSpace(root) != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("could not resolve the project root whose erun references would be re-pinned; run from inside the tenant repository")
}

func savePinnedRuntimeVersion(result common.OpenResult, target string, saveEnvConfig func(string, common.EnvConfig) error) error {
	if saveEnvConfig == nil {
		return nil
	}
	updated := result.EnvConfig
	if strings.TrimSpace(updated.RuntimeVersion) == strings.TrimSpace(target) {
		return nil
	}
	updated.RuntimeVersion = target
	return saveEnvConfig(result.Tenant, updated)
}

// tracePinPlan renders every site and its old→new value. Emitted for a real run
// too, not only a dry run: a re-pin edits files across a repo, and the operator
// should see which without diffing afterwards.
func tracePinPlan(cmdCtx common.Context, plan common.PinPlan) {
	cmdCtx.Trace(fmt.Sprintf("pin %s/%s -> %s (project root %s)", plan.Tenant, plan.Environment, plan.Target, plan.ProjectRoot))
	for _, site := range plan.Sites {
		state := "change"
		if site.Aligned() {
			state = "already"
		}
		cmdCtx.Trace(fmt.Sprintf("  %s %s %s: %s -> %s", state, site.Kind, pinSiteLabel(site), orNone(site.Current), site.Target))
	}
	for _, note := range plan.Skipped {
		cmdCtx.Trace("  skipped: " + note)
	}
}

func pinSiteLabel(site common.PinSite) string {
	if strings.TrimSpace(site.Path) == "" {
		return site.Detail
	}
	if strings.TrimSpace(site.Detail) == "" {
		return site.Path
	}
	return site.Path + " (" + site.Detail + ")"
}
