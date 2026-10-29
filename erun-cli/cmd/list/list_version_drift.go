package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
	"github.com/sophium/erun/erun-common/command/lifecycle/versiondrift"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/registry"
)

func runListVersionDrift(ctx core.Context, result list.ListResult, versionDriftTenant, gateEnvironment string, failOnDrift bool) error {
	drift, err := versiondrift.ResolveTenantVersionDrift(ctx, result, versionDriftTenant, gateEnvironment)
	if err != nil {
		return err
	}
	if ctx.Output == model.OutputJSON {
		if err := ctx.WriteResult(drift); err != nil {
			return err
		}
	} else if err := writeVersionDriftReport(ctx, drift); err != nil {
		return err
	}
	if !failOnDrift {
		return nil
	}
	return versionDriftExitError(drift)
}

func runListControlPlanes(ctx core.Context, result list.ListResult, erunAlias string, failOnDrift bool) error {
	drift, err := versiondrift.ResolveControlPlaneVersionDrift(ctx, result, erunAlias, cloud.DefaultCloudDependencies(), registry.ResolveDefaultRuntimeRegistryVersions)
	if err != nil {
		return err
	}
	if ctx.Output == model.OutputJSON {
		if err := ctx.WriteResult(drift); err != nil {
			return err
		}
	} else if err := writeControlPlaneVersionReport(ctx, drift); err != nil {
		return err
	}
	if ctx.DryRun || !failOnDrift {
		return nil
	}
	return controlPlaneVersionDriftExitError(drift)
}

// versionDriftExitError fails --fail-on-drift for behind, undeterminable, or unresolved/behind gate envs.
func versionDriftExitError(drift model.TenantVersionDrift) error {
	var problems []string
	var behind []string
	var unresolved []string
	for _, env := range drift.Environments {
		if env.BehindMax {
			behind = append(behind, env.Environment)
		}
		if env.VersionUnresolved {
			unresolved = append(unresolved, env.Environment)
		}
	}
	if len(behind) > 0 {
		problems = append(problems, fmt.Sprintf("%d environment(s) behind the tenant's max version: %s", len(behind), strings.Join(behind, ", ")))
	}
	if len(unresolved) > 0 {
		problems = append(problems, fmt.Sprintf("%d environment(s) excluded from drift because their version could not be determined: %s", len(unresolved), strings.Join(unresolved, ", ")))
	}
	if drift.GateEnvironment != "" {
		switch {
		case drift.GateVersionUnresolved:
			problems = append(problems, fmt.Sprintf("gate environment %s's own erun version could not be resolved", drift.GateEnvironment))
		case drift.GateBehind:
			problems = append(problems, fmt.Sprintf("gate environment %s is outdated relative to %s", drift.GateEnvironment, strings.Join(drift.GateOutdatedBy, ", ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("version drift for tenant %s: %s", drift.Tenant, strings.Join(problems, "; "))
}

// controlPlaneVersionDriftExitError fails for any plane not confirmed to run the published version.
func controlPlaneVersionDriftExitError(drift model.ControlPlaneVersionDrift) error {
	var problems []string
	if drift.PublishedVersionError != "" {
		problems = append(problems, "the published version could not be resolved: "+drift.PublishedVersionError)
	}
	unreachable, behind, ahead := classifyControlPlaneVersionDrift(drift.Planes)
	if len(unreachable) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) unreachable: %s", len(unreachable), strings.Join(unreachable, ", ")))
	}
	if len(behind) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) behind published: %s", len(behind), strings.Join(behind, ", ")))
	}
	if len(ahead) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) ahead of published: %s", len(ahead), strings.Join(ahead, ", ")))
	}
	if mismatched := controlPlaneAPIURLMismatches(drift.Planes); len(mismatched) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) advertising a foreign apiUrl: %s", len(mismatched), strings.Join(mismatched, ", ")))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("control plane version drift: %s", strings.Join(problems, "; "))
}

// controlPlaneAPIURLMismatches is never routine drift, so it is reported separately.
func controlPlaneAPIURLMismatches(planes []model.ControlPlaneVersionStatus) []string {
	var mismatched []string
	for _, plane := range planes {
		if plane.AdvertisedAPIURLMismatch != "" {
			mismatched = append(mismatched, plane.Alias)
		}
	}
	return mismatched
}

// classifyControlPlaneVersionDrift ignores surfaces a plane never advertised.
func classifyControlPlaneVersionDrift(planes []model.ControlPlaneVersionStatus) (unreachable, behind, ahead []string) {
	for _, plane := range planes {
		unreachable, behind, ahead = appendControlPlaneVersionVerdict(unreachable, behind, ahead, plane.Alias, plane.Reachable, plane.Behind, plane.Ahead)
		for _, surface := range linkedVersionSurfaces(plane) {
			unreachable, behind, ahead = appendControlPlaneVersionVerdict(unreachable, behind, ahead, plane.Alias+" ("+surface.label+")", surface.status.Reachable, surface.status.Behind, surface.status.Ahead)
		}
	}
	return unreachable, behind, ahead
}

func appendControlPlaneVersionVerdict(unreachable, behind, ahead []string, label string, reachable, isBehind, isAhead bool) ([]string, []string, []string) {
	switch {
	case !reachable:
		unreachable = append(unreachable, label)
	case isBehind:
		behind = append(behind, label)
	case isAhead:
		ahead = append(ahead, label)
	}
	return unreachable, behind, ahead
}

func writeVersionDriftReport(ctx core.Context, drift model.TenantVersionDrift) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Version drift for tenant %s:\n", drift.Tenant); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "max version", textfmt.ValueOrNone(drift.MaxVersion)); err != nil {
		return err
	}
	if err := writeVersionDriftEnvironments(ctx, drift.Environments); err != nil {
		return err
	}
	if drift.GateEnvironment == "" {
		return nil
	}
	return writeVersionDriftGate(ctx, drift)
}

func writeVersionDriftEnvironments(ctx core.Context, environments []model.EnvironmentVersionStatus) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "  environments:"); err != nil {
		return err
	}
	for _, env := range environments {
		line := "    - " + env.Environment + " version=" + versionDriftVersionValue(env)
		if env.BehindMax {
			line += " [behind max]"
		}
		if env.VersionUnresolved {
			line += " [excluded from drift]"
			if reason := strings.TrimSpace(env.VersionUnresolvedReason); reason != "" {
				line += " -- " + reason
			}
		}
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

// versionDriftVersionValue distinguishes "none" (nothing deployed) from "undetermined".
func versionDriftVersionValue(env model.EnvironmentVersionStatus) string {
	if env.VersionUnresolved {
		return "undetermined"
	}
	return textfmt.QuotedValueOrNone(env.Version)
}

func writeVersionDriftGate(ctx core.Context, drift model.TenantVersionDrift) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "  gate:"); err != nil {
		return err
	}
	if err := textfmt.WriteIndentedValue(ctx, 4, "environment", drift.GateEnvironment); err != nil {
		return err
	}
	if err := textfmt.WriteIndentedValue(ctx, 4, "version", textfmt.ValueOrNone(drift.GateVersion)); err != nil {
		return err
	}
	switch {
	case drift.GateVersionUnresolved:
		_, err := fmt.Fprintln(ctx.Stdout, "    behind: unknown ("+gateVersionUnresolvedMessage(drift.GateVersionUnresolvedReason)+")")
		return err
	case drift.GateBehind:
		_, err := fmt.Fprintf(ctx.Stdout, "    behind: yes -- outdated relative to %s\n", strings.Join(drift.GateOutdatedBy, ", "))
		return err
	default:
		_, err := fmt.Fprintln(ctx.Stdout, "    behind: no")
		return err
	}
}

func gateVersionUnresolvedMessage(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "gate's own erun version could not be resolved from config"
	}
	return "gate's own erun version could not be resolved: " + reason
}

func writeControlPlaneVersionReport(ctx core.Context, drift model.ControlPlaneVersionDrift) error {
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: control plane version check planned; see trace for the planes and registry lookup that would be probed.")
		return err
	}
	if drift.PublishedVersionError != "" {
		if err := textfmt.WriteLabeledValue(ctx, "published version", "unresolved ("+drift.PublishedVersionError+")"); err != nil {
			return err
		}
	} else {
		if err := textfmt.WriteLabeledValue(ctx, "published version", textfmt.ValueOrNone(drift.PublishedVersion)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(ctx.Stdout, controlPlanesSectionHeader(drift.Planes)); err != nil {
		return err
	}
	if len(drift.Planes) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, plane := range drift.Planes {
		if err := writeControlPlaneVersionEntry(ctx, plane); err != nil {
			return err
		}
	}
	return nil
}

// controlPlanesSectionHeader shows both backend and alias counts; a gap means collapsed aliases.
func controlPlanesSectionHeader(planes []model.ControlPlaneVersionStatus) string {
	if len(planes) == 0 {
		return "Control planes:"
	}
	aliasCount := 0
	for _, plane := range planes {
		aliasCount += 1 + len(plane.AdditionalAliases)
	}
	return fmt.Sprintf("Control planes (%s, %s):", pluralCount(len(planes), "backend", "backends"), pluralCount(aliasCount, "alias", "aliases"))
}

func pluralCount(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func writeControlPlaneVersionEntry(ctx core.Context, plane model.ControlPlaneVersionStatus) error {
	line := "  - " + plane.Alias + " api-url=" + textfmt.QuotedValueOrNone(plane.APIURL)
	if !plane.Reachable {
		line += " reachable=no reason=" + textfmt.QuotedValueOrNone(plane.UnreachableReason)
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
		return writeControlPlaneAdditionalAliases(ctx, plane.AdditionalAliases)
	}
	line += " reachable=yes version=" + textfmt.QuotedValueOrNone(plane.Version)
	switch {
	case plane.Behind:
		line += " [behind published -- roll it]"
	case plane.Ahead:
		line += " [ahead of published -- running an unpublished version]"
	}
	if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
		return err
	}
	if err := writeControlPlaneAdditionalAliases(ctx, plane.AdditionalAliases); err != nil {
		return err
	}
	return writeControlPlaneLinkedSurfaces(ctx, plane)
}

func writeControlPlaneLinkedSurfaces(ctx core.Context, plane model.ControlPlaneVersionStatus) error {
	if plane.AdvertisedAPIURLMismatch != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, "    [advertised apiUrl mismatch: "+plane.AdvertisedAPIURLMismatch+"]"); err != nil {
			return err
		}
	}
	for _, surface := range linkedVersionSurfaces(plane) {
		if err := writeControlPlaneVersionSurfaceEntry(ctx, surface.label, *surface.status); err != nil {
			return err
		}
	}
	return nil
}

type linkedVersionSurface struct {
	label  string
	status *model.VersionSurfaceStatus
}

// linkedVersionSurfaces feeds both the report and the classifier so they cannot disagree.
func linkedVersionSurfaces(plane model.ControlPlaneVersionStatus) []linkedVersionSurface {
	surfaces := make([]linkedVersionSurface, 0, 2)
	for _, candidate := range []linkedVersionSurface{
		{"console", plane.Console},
		{"docs site", plane.Docs},
	} {
		if candidate.status != nil {
			surfaces = append(surfaces, candidate)
		}
	}
	return surfaces
}

func writeControlPlaneAdditionalAliases(ctx core.Context, aliases []model.ControlPlaneAliasRef) error {
	for _, alias := range aliases {
		if _, err := fmt.Fprintf(ctx.Stdout, "      also reachable as: %s api-url=%s\n", alias.Alias, textfmt.QuotedValueOrNone(alias.APIURL)); err != nil {
			return err
		}
	}
	return nil
}

func writeControlPlaneVersionSurfaceEntry(ctx core.Context, label string, surface model.VersionSurfaceStatus) error {
	line := "    " + label + ": url=" + textfmt.QuotedValueOrNone(surface.URL)
	if !surface.Reachable {
		line += " reachable=no reason=" + textfmt.QuotedValueOrNone(surface.UnreachableReason)
		_, err := fmt.Fprintln(ctx.Stdout, line)
		return err
	}
	line += " reachable=yes version=" + textfmt.QuotedValueOrNone(surface.Version)
	if surface.Reason != "" {
		line += " reason=" + textfmt.QuotedValueOrNone(surface.Reason)
	}
	switch {
	case surface.Behind:
		line += " [behind published -- roll it]"
	case surface.Ahead:
		line += " [ahead of published -- running an unpublished version]"
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}
