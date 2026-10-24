package pin

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/opentarget"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
	"github.com/spf13/cobra"
)

// NewPinCmd re-pins every recorded erun version for an environment. It only edits
// sources; terraform apply and deploy stay separate steps.
func NewPinCmd(prepareContext func(core.Context) core.Context, resolveOpen func(model.OpenParams) (config.OpenResult, error), saveEnvConfig func(string, config.EnvConfig) error, listEnvConfigs func(string) ([]config.EnvConfig, error), findProjectRoot projectseam.ProjectFinderFunc) *cobra.Command {
	var version string
	var latest, revert, list bool
	target := model.OpenParams{}

	cmd := &cobra.Command{
		Use:   "pin [TENANT] [ENVIRONMENT]",
		Short: "Re-pin every erun version reference for an environment",
		Long: "Re-pin every erun version reference for an environment: the Terraform module refs, " +
			"an erun image reference set directly in Terraform variables (e.g. the cluster-edge " +
			"module's dns01_webhook_image), each umbrella chart's erun dependencies, the build-env " +
			"image tag, a stated runtime chart or runtime image naming erun's own stock release, " +
			"and the environment's own runtime version when that environment runs erun's own runtime image.\n\n" +
			"Idempotent, and a no-op once aligned. It rewrites the source of truth only — realizing " +
			"the new version (terraform apply, deploy) stays a separate explicit step.",
		Example: "  erun pin --list\n  erun pin acme dev --dry-run\n  erun pin acme dev --version 1.0.175\n  erun pin acme dev --revert",
		Args:    cobra.MaximumNArgs(2),

		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			// --list reads only the registry, so it must work without a resolvable tenant.
			if list {
				return runPinListCommand(cmd.Context(), ctx)
			}
			params, err := opentarget.ResolveOpenParams(cmd.CommandPath(), args, target)
			if err != nil {
				return err
			}
			result, err := resolveOpen(params)
			if err != nil {
				return err
			}
			return runPinCommand(cmd.Context(), ctx, result, pinRequest{
				Version: version,
				Latest:  latest,
				Revert:  revert,
			}, saveEnvConfig, listEnvConfigs, findProjectRoot)
		},
	}
	clicmd.AddDryRunFlag(cmd)
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

// runPinListCommand lists published versions; it needs no tenant, environment, or project root.
func runPinListCommand(ctx context.Context, cmdCtx core.Context) error {
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

func runPinCommand(ctx context.Context, cmdCtx core.Context, result config.OpenResult, request pinRequest, saveEnvConfig func(string, config.EnvConfig) error, listEnvConfigs func(string) ([]config.EnvConfig, error), findProjectRoot projectseam.ProjectFinderFunc) error {
	projectRoot, viaCwdFallback, err := resolvePinProjectRoot(result, listEnvConfigs, findProjectRoot)
	if err != nil {
		return err
	}
	targetVersion, err := resolvePinTarget(ctx, cmdCtx, result, request, projectRoot)
	if err != nil {
		return err
	}

	plan, err := lifecycle.ResolvePinPlan(projectRoot, result.Tenant, result.Environment, result.EnvConfig, targetVersion)
	if err != nil {
		return err
	}
	if viaCwdFallback {
		plan.ProjectRootNote = lifecycle.DescribeCwdFallbackProjectRoot(cmdCtx, plan)
	}
	lifecycle.AnnotatePinPlanBaseFreshness(cmdCtx, &plan)
	tracePinPlan(cmdCtx, plan)

	if plan.Aligned() {
		cmdCtx.Info(fmt.Sprintf("%s/%s is already pinned to %s — nothing to change.", result.Tenant, result.Environment, plan.Target))
		return cmdCtx.WriteResult(plan)
	}
	if cmdCtx.DryRun {
		return nil
	}
	return applyPinCommand(cmdCtx, projectRoot, result, plan, saveEnvConfig)
}

// applyPinCommand writes a resolved, drifted plan.
func applyPinCommand(cmdCtx core.Context, projectRoot string, result config.OpenResult, plan model.PinPlan, saveEnvConfig func(string, config.EnvConfig) error) error {
	// Recorded first so a revert has a target even if the rewrite fails partway.
	if err := lifecycle.RecordPinPrevious(projectRoot, result.Tenant, result.Environment, plan.Previous); err != nil {
		return err
	}
	if err := lifecycle.ApplyPinPlan(plan); err != nil {
		return err
	}
	if err := savePinnedEnvConfig(result, plan, saveEnvConfig); err != nil {
		return err
	}
	// Rewritten Chart.yaml files and their locks must agree or the next deploy fails.
	if err := lifecycle.RefreshPinnedChartLocks(cmdCtx, plan, nil); err != nil {
		return err
	}

	cmdCtx.Info(fmt.Sprintf("Pinned %s/%s to %s across %d references (was %s).",
		result.Tenant, result.Environment, plan.Target, len(plan.Changes()), orNone(plan.Previous)))
	cmdCtx.Info("Nothing is deployed yet: run `erun terraform apply` and `erun deploy` to realize it.")
	return cmdCtx.WriteResult(plan)
}

// resolvePinTarget refuses an unpublished version, which would only fail later at terraform init or chart pull.
func resolvePinTarget(ctx context.Context, cmdCtx core.Context, result config.OpenResult, request pinRequest, projectRoot string) (string, error) {
	if request.Revert {
		previous, ok := lifecycle.PinPrevious(projectRoot, result.Tenant, result.Environment)
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
		// An explicit target does not fail closed on an unreadable registry.
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

// verifyPinTargetPublished refuses a target the registry lacks. An empty listing means "cannot check".
func verifyPinTargetPublished(target string, versions model.RuntimeRegistryVersions) error {
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

func resolvePinRegistryVersions(ctx context.Context) (model.RuntimeRegistryVersions, error) {
	return registry.ResolveDefaultRuntimeRegistryVersions(ctx)
}

// resolvePinProjectRoot prefers the env's own repo, then a sibling env of the same tenant,
// then the cwd; viaCwdFallback reports the last case so the plan can say it is unverified.
func resolvePinProjectRoot(result config.OpenResult, listEnvConfigs func(string) ([]config.EnvConfig, error), findProjectRoot projectseam.ProjectFinderFunc) (string, bool, error) {
	if path := strings.TrimSpace(result.RepoPath); path != "" && !result.RemoteRepo() {
		return path, false, nil
	}
	if listEnvConfigs != nil {
		if envs, err := listEnvConfigs(result.Tenant); err == nil {
			if root, ok := config.TenantLocalCheckoutRoot(envs); ok {
				return root, false, nil
			}
		}
	}
	if findProjectRoot != nil {
		if _, root, err := findProjectRoot(); err == nil && strings.TrimSpace(root) != "" {
			return root, true, nil
		}
	}
	return "", false, fmt.Errorf("%s/%s has no local checkout of its repo on this machine, and no other %s environment does either — pin rewrites files in that checkout, so run it from a machine (or shell) that has the tenant repo checked out",
		result.Tenant, result.Environment, result.Tenant)
}

// savePinnedEnvConfig persists only fields the plan has sites for; skipped
// tenant-owned values must stay untouched.
func savePinnedEnvConfig(result config.OpenResult, plan model.PinPlan, saveEnvConfig func(string, config.EnvConfig) error) error {
	if saveEnvConfig == nil {
		return nil
	}
	updated, changed := lifecycle.ApplyPinnedEnvConfig(result.EnvConfig, plan)
	if !changed {
		return nil
	}
	return saveEnvConfig(result.Tenant, updated)
}

// tracePinPlan renders every site's old→new value, on real runs too.
func tracePinPlan(cmdCtx core.Context, plan model.PinPlan) {
	cmdCtx.Trace(fmt.Sprintf("pin %s/%s -> %s (project root %s)", plan.Tenant, plan.Environment, plan.Target, plan.ProjectRoot))
	if plan.ProjectRootNote != "" {
		cmdCtx.Trace("  " + plan.ProjectRootNote)
	}
	if note := plan.BaseFreshnessNote(); note != "" {
		cmdCtx.Trace("  " + note)
	}
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

func pinSiteLabel(site model.PinSite) string {
	if strings.TrimSpace(site.Path) == "" {
		return site.Detail
	}
	if strings.TrimSpace(site.Detail) == "" {
		return site.Path
	}
	return site.Path + " (" + site.Detail + ")"
}
