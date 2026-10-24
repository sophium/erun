package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/registry"
)

func runListVersionDrift(ctx core.Context, result lifecycle.ListResult, versionDriftTenant, gateEnvironment string, failOnDrift bool) error {
	drift, err := lifecycle.ResolveTenantVersionDrift(ctx, result, versionDriftTenant, gateEnvironment)
	if err != nil {
		return err
	}
	if ctx.Output == core.OutputJSON {
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

func runListControlPlanes(ctx core.Context, result lifecycle.ListResult, erunAlias string, failOnDrift bool) error {
	drift, err := lifecycle.ResolveControlPlaneVersionDrift(ctx, result, erunAlias, cloud.DefaultCloudDependencies(), registry.ResolveDefaultRuntimeRegistryVersions)
	if err != nil {
		return err
	}
	if ctx.Output == core.OutputJSON {
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

// versionDriftExitError makes tenant version drift a non-zero exit when
// --fail-on-drift asks for it, after the full report has already printed:
// any environment behind the tenant's own max, any environment excluded from
// that computation because its version could not be determined at all (a
// gap in the check is itself something to fail on, not something to pass
// silently), or a gate environment whose own behind verdict is unresolved or
// true -- see erun-cli/AGENTS.md § "Exit-Code Contract: Reporting Commands
// Vs Gating Checks" for why this is opt-in rather than the command's default.
func versionDriftExitError(drift lifecycle.TenantVersionDrift) error {
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

// controlPlaneVersionDriftExitError makes control-plane version drift a
// non-zero exit when --fail-on-drift asks for it: any plane behind or ahead
// of the published version, any plane erun could not reach, any plane
// advertising a foreign apiUrl, or a baseline erun could not even resolve --
// none of those confirm a plane is running what erun actually published.
func controlPlaneVersionDriftExitError(drift lifecycle.ControlPlaneVersionDrift) error {
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

// controlPlaneAPIURLMismatches names every plane whose own discovery document
// advertised an apiUrl resolving to a different backend than the one erun
// actually reached -- the one condition here that is never routine drift, so
// --fail-on-drift reports it alongside behind/ahead rather than folding it
// into either.
func controlPlaneAPIURLMismatches(planes []lifecycle.ControlPlaneVersionStatus) []string {
	var mismatched []string
	for _, plane := range planes {
		if plane.AdvertisedAPIURLMismatch != "" {
			mismatched = append(mismatched, plane.Alias)
		}
	}
	return mismatched
}

// classifyControlPlaneVersionDrift buckets every plane, and each surface it
// links to when one was checked, into unreachable/behind/ahead -- a surface's
// own label gets a " (<surface>)" suffix so the plane and its console and docs
// site stay distinguishable in the --fail-on-drift summary. A surface the
// plane never advertised contributes nothing: an absent docs site is not a
// drift finding, it is an absence.
func classifyControlPlaneVersionDrift(planes []lifecycle.ControlPlaneVersionStatus) (unreachable, behind, ahead []string) {
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

func writeVersionDriftReport(ctx core.Context, drift lifecycle.TenantVersionDrift) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Version drift for tenant %s:\n", drift.Tenant); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "max version", shared.ValueOrNone(drift.MaxVersion)); err != nil {
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

func writeVersionDriftEnvironments(ctx core.Context, environments []lifecycle.EnvironmentVersionStatus) error {
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

// versionDriftVersionValue renders one environment's resolved version,
// distinguishing "none" (a confirmed absence -- nothing is deployed) from
// "undetermined" (VersionUnresolved -- neither its cached config nor a live
// check of its cluster could tell) rather than collapsing both into the same
// bare token.
func versionDriftVersionValue(env lifecycle.EnvironmentVersionStatus) string {
	if env.VersionUnresolved {
		return "undetermined"
	}
	return shared.QuotedValueOrNone(env.Version)
}

func writeVersionDriftGate(ctx core.Context, drift lifecycle.TenantVersionDrift) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "  gate:"); err != nil {
		return err
	}
	if err := shared.WriteIndentedValue(ctx, 4, "environment", drift.GateEnvironment); err != nil {
		return err
	}
	if err := shared.WriteIndentedValue(ctx, 4, "version", shared.ValueOrNone(drift.GateVersion)); err != nil {
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

// gateVersionUnresolvedMessage names why the gate's own version could not be
// resolved. A reason from a live check (or a confirmed absence) is reported
// verbatim; the older config-only case (a gate version that parsed but not as
// plain semver) has no reason to attach, so it keeps its original wording.
func gateVersionUnresolvedMessage(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "gate's own erun version could not be resolved from config"
	}
	return "gate's own erun version could not be resolved: " + reason
}

func writeControlPlaneVersionReport(ctx core.Context, drift lifecycle.ControlPlaneVersionDrift) error {
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: control plane version check planned; see trace for the planes and registry lookup that would be probed.")
		return err
	}
	if drift.PublishedVersionError != "" {
		if err := shared.WriteLabeledValue(ctx, "published version", "unresolved ("+drift.PublishedVersionError+")"); err != nil {
			return err
		}
	} else {
		if err := shared.WriteLabeledValue(ctx, "published version", shared.ValueOrNone(drift.PublishedVersion)); err != nil {
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

// controlPlanesSectionHeader names both the number of distinct backends
// found and the number of configured aliases that reach them -- when the two
// differ, that gap is exactly the signal that two or more aliases were
// collapsed into one plane, and a bare "Control planes:" header
// would hide it.
func controlPlanesSectionHeader(planes []lifecycle.ControlPlaneVersionStatus) string {
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

func writeControlPlaneVersionEntry(ctx core.Context, plane lifecycle.ControlPlaneVersionStatus) error {
	line := "  - " + plane.Alias + " api-url=" + shared.QuotedValueOrNone(plane.APIURL)
	if !plane.Reachable {
		line += " reachable=no reason=" + shared.QuotedValueOrNone(plane.UnreachableReason)
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
		return writeControlPlaneAdditionalAliases(ctx, plane.AdditionalAliases)
	}
	line += " reachable=yes version=" + shared.QuotedValueOrNone(plane.Version)
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

// writeControlPlaneLinkedSurfaces renders the plane's own advertised-apiUrl
// note and every surface it named, nested beneath the plane they were
// discovered from.
func writeControlPlaneLinkedSurfaces(ctx core.Context, plane lifecycle.ControlPlaneVersionStatus) error {
	// Above the nested surfaces: this is a property of the plane itself, so it
	// reads with the plane's own line rather than under one of them.
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

// linkedVersionSurface is one surface a plane's own discovery document names,
// paired with the label it is reported and classified under.
type linkedVersionSurface struct {
	label  string
	status *lifecycle.VersionSurfaceStatus
}

// linkedVersionSurfaces lists the surfaces a plane advertises, in the order
// its discovery document names them, skipping the ones it never named at all.
// One list serves both the report and the --fail-on-drift classifier, so the
// two cannot disagree about which surfaces a plane has or what they are
// called -- an absent surface is absent from both, never defaulted into
// either.
func linkedVersionSurfaces(plane lifecycle.ControlPlaneVersionStatus) []linkedVersionSurface {
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

// writeControlPlaneAdditionalAliases names every other configured alias that
// resolved to this same backend, so collapsing duplicate aliases into one
// plane never hides which aliases are actually configured.
func writeControlPlaneAdditionalAliases(ctx core.Context, aliases []lifecycle.ControlPlaneAliasRef) error {
	for _, alias := range aliases {
		if _, err := fmt.Fprintf(ctx.Stdout, "      also reachable as: %s api-url=%s\n", alias.Alias, shared.QuotedValueOrNone(alias.APIURL)); err != nil {
			return err
		}
	}
	return nil
}

// writeControlPlaneVersionSurfaceEntry renders one linked surface -- a plane's
// console or its docs site -- under the plane that advertised it. The two are
// the same shape deliberately: they answer the same question the same way, and
// an operator reading the report should not have to learn two of them.
func writeControlPlaneVersionSurfaceEntry(ctx core.Context, label string, surface lifecycle.VersionSurfaceStatus) error {
	line := "    " + label + ": url=" + shared.QuotedValueOrNone(surface.URL)
	if !surface.Reachable {
		line += " reachable=no reason=" + shared.QuotedValueOrNone(surface.UnreachableReason)
		_, err := fmt.Fprintln(ctx.Stdout, line)
		return err
	}
	line += " reachable=yes version=" + shared.QuotedValueOrNone(surface.Version)
	if surface.Reason != "" {
		line += " reason=" + shared.QuotedValueOrNone(surface.Reason)
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
