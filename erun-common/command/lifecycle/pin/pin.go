package pin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeimage"
)

// PinSiteSummary is the phrase every re-pin surface uses to name a site kind.
type PinSiteSummary struct {
	Kind   model.PinSiteKind
	Phrase string
}

// PinSiteSummaries enumerates what a re-pin rewrites; every surface listing re-pin sites must
// carry each phrase (TestEverySurfaceDescribingARepinNamesEveryPinSiteKind).
var PinSiteSummaries = []PinSiteSummary{
	{Kind: model.PinSiteTerraformRef, Phrase: "Terraform module"},
	{Kind: model.PinSiteImageReference, Phrase: "dns01_webhook_image"},
	{Kind: model.PinSiteHelmDependency, Phrase: "umbrella"},
	{Kind: model.PinSiteRuntimeImage, Phrase: "build-env image"},
	{Kind: model.PinSiteRuntimeChart, Phrase: "own stock"},
	{Kind: model.PinSiteRuntimeVersion, Phrase: "runtime version"},
}

// Only erun's own module sources are rewritten.
var erunTerraformRefPattern = regexp.MustCompile(`(github\.com/sophium/erun\.git//[^"'\s?]*\?ref=)v?([0-9][^"'\s]*)`)

// The repository line anchors it so a tenant's own charts are left alone.
var erunHelmDependencyPattern = regexp.MustCompile(`(?ms)(-\s+name:\s*(\S+)[^\n]*\n(?:\s+[^\n]*\n)*?\s+repository:\s*[^\n]*sophium[^\n]*\n(?:\s+[^\n]*\n)*?\s+version:\s*)(\S+)`)

// The version class excludes `\` so an escaped quote (as in HCL) is never captured.
var erunDevopsImagePattern = regexp.MustCompile(`(erun-devops:)([0-9][^\s"'\\]*)`)

// Anchored on `=\s*"` and an unescaped closing quote so mentions inside description prose
// never match. erun-devops is covered by erunDevopsImagePattern.
var erunImageReferencePattern = regexp.MustCompile(`(=\s*")(ghcr\.io/sophium/(erun-[a-zA-Z0-9_-]+):)([0-9][^\s"'\\]*)"`)

// An unresolved tenant/environment must fail here rather than yield a real-looking runtime-version row.
func normalizePinPlanInputs(tenant, environment, projectRoot, target string) (string, string, string, string, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	projectRoot = strings.TrimSpace(projectRoot)
	target = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), "v"))
	if tenant == "" || environment == "" {
		return "", "", "", "", fmt.Errorf("pin: tenant and environment must both be resolved before a plan can be built (got tenant=%q environment=%q)", tenant, environment)
	}
	if projectRoot == "" {
		return "", "", "", "", fmt.Errorf("a project root is required to resolve the pin sites")
	}
	if target == "" {
		return "", "", "", "", fmt.Errorf("a target erun version is required")
	}
	return tenant, environment, projectRoot, target, nil
}

// ResolvePinPlan reports what moving projectRoot's pin sites to target would change, without writing.
func ResolvePinPlan(projectRoot, tenant, environment string, env config.EnvConfig, target string) (model.PinPlan, error) {
	if env.ResolvedType() == config.EnvironmentTypeHost {
		return model.PinPlan{}, fmt.Errorf("pin %s/%s: %s is a host environment — it has no pod and no runtime version to pin", tenant, environment, environment)
	}
	tenant, environment, projectRoot, target, err := normalizePinPlanInputs(tenant, environment, projectRoot, target)
	if err != nil {
		return model.PinPlan{}, err
	}

	plan := model.PinPlan{
		Tenant:      tenant,
		Environment: environment,
		Target:      target,
		ProjectRoot: projectRoot,
		Previous:    strings.TrimSpace(env.RuntimeVersion),
	}
	versionSites, versionSkipped := resolveRuntimeVersionAndImagePinSites(tenant, environment, target, env)
	plan.Sites = append(plan.Sites, versionSites...)
	plan.Skipped = append(plan.Skipped, versionSkipped...)
	if chartSite, chartSkipped := resolveRuntimeChartPinSite(strings.TrimSpace(env.RuntimeChart), target); chartSite != nil {
		plan.Sites = append(plan.Sites, *chartSite)
	} else if chartSkipped != "" {
		plan.Skipped = append(plan.Skipped, chartSkipped)
	}

	fileSites, err := scanPinFiles(projectRoot, target)
	if err != nil {
		return model.PinPlan{}, err
	}
	plan.Sites = append(plan.Sites, fileSites...)
	return plan, nil
}

// Origin names the config field the reference came from; empty for runtimeimage.
type runtimeImageStatement struct {
	Reference string
	Origin    string
}

// ownLineImageStatements returns every config statement placing the runtime image on a line
// other than erun's; any one means runtimeversion must not move to the erun target. Lines are
// classified by name, never version number, and unclassifiable references are skipped.
func ownLineImageStatements(tenant string, env config.EnvConfig) []runtimeImageStatement {
	var statements []runtimeImageStatement
	for _, candidate := range []runtimeImageStatement{
		{Reference: strings.TrimSpace(env.RuntimeImage)},
		{
			Reference: strings.TrimSpace(env.RuntimeRunningImage),
			Origin:    "from runtimerunningimage, the image this environment's last deploy confirmed running",
		},
	} {
		if referenceIsOnOwnLine(candidate.Reference) {
			statements = append(statements, candidate)
		}
	}
	if chart := ownLineChartStatement(env.RuntimeChart); chart != nil {
		statements = append(statements, *chart)
	}
	if len(statements) == 0 {
		if resolved := defaultDeployImageStatement(tenant, env); resolved != nil {
			statements = append(statements, *resolved)
		}
	}
	return statements
}

// An unclassifiable reference is not on its own line.
func referenceIsOnOwnLine(reference string) bool {
	line, ok := config.RuntimeImageReleaseLine(reference)
	return ok && line != "erun"
}

// A stated non-stock chart runs its own image on its own line.
func ownLineChartStatement(chart string) *runtimeImageStatement {
	chart = strings.TrimSpace(chart)
	if chart == "" {
		return nil
	}
	reference, _ := chartref.SplitChartReferenceVersion(chart)
	name := chartref.ChartName(reference)
	if name == "" || name == runtimetarget.DevopsComponentName {
		return nil
	}
	return &runtimeImageStatement{
		Reference: chart,
		Origin:    "from runtimechart, the " + name + " chart publishes its own runtime image, not erun's stock " + runtimetarget.DevopsComponentName,
	}
}

// defaultDeployImageStatement is what a deploy installs when the env states no image; nil when
// that is the stock erun-devops image.
func defaultDeployImageStatement(tenant string, env config.EnvConfig) *runtimeImageStatement {
	if strings.TrimSpace(env.RuntimeImage) != "" || strings.TrimSpace(env.RuntimeRunningImage) != "" {
		return nil
	}
	chartReference, _ := chartref.SplitChartReferenceVersion(strings.TrimSpace(env.RuntimeChart))
	name := runtimeimage.DefaultDeployRuntimeImageBareName(tenant, chartref.ChartName(chartReference))
	if name == "" || name == runtimetarget.DevopsComponentName {
		return nil
	}
	return &runtimeImageStatement{
		Reference: name,
		Origin:    "the image a deploy of this environment resolves by default, neither runtimeimage nor runtimerunningimage being recorded",
	}
}

func ownLineRuntimeVersionSkipNote(tenant, environment string, statements []runtimeImageStatement) string {
	reasons := make([]string, 0, len(statements))
	for _, statement := range statements {
		reason := statement.Reference + "'s own release line"
		if statement.Origin != "" {
			reason += " (" + statement.Origin + ")"
		}
		reasons = append(reasons, reason)
	}
	return "runtimeversion " + tenant + "/" + environment + " rides " + strings.Join(reasons, ", and ") +
		", not erun's, so pin leaves it alone; it moves on the tenant's own next build/release"
}

// runtimeversion and a stock runtimeimage's tag follow erun's line only when the runtime image
// does; otherwise they are skipped, since the erun target tag does not exist on that line.
func resolveRuntimeVersionAndImagePinSites(tenant, environment, target string, env config.EnvConfig) ([]model.PinSite, []string) {
	var sites []model.PinSite
	var skipped []string
	image := strings.TrimSpace(env.RuntimeImage)
	if ownLine := ownLineImageStatements(tenant, env); len(ownLine) > 0 {
		skipped = append(skipped, ownLineRuntimeVersionSkipNote(tenant, environment, ownLine))
	} else {
		sites = append(sites, model.PinSite{
			Kind:    model.PinSiteRuntimeVersion,
			Detail:  tenant + "/" + environment,
			Current: strings.TrimSpace(env.RuntimeVersion),
			Target:  target,
		})
	}
	if image == "" {
		return sites, skipped
	}
	_, version, ok := chartref.SplitImageTag(image)
	if !ok {
		return sites, skipped
	}
	// A tenant's own <tenant>-devops tag follows the tenant's release line.
	if !runtimeimage.RuntimeImageIsStockDevops(image) {
		skipped = append(skipped, "runtimeimage "+image+" is not the stock erun-devops image; its tag rides the tenant's own release line, not erun's, so pin leaves it alone")
		return sites, skipped
	}
	sites = append(sites, model.PinSite{
		Kind:    model.PinSiteRuntimeImage,
		Detail:  image,
		Current: version,
		Target:  target,
	})
	return sites, skipped
}

// resolveRuntimeChartPinSite moves only erun's stock chart with an explicit version; anything
// else yields a skip note and never blocks.
func resolveRuntimeChartPinSite(chart, target string) (*model.PinSite, string) {
	if chart == "" {
		return nil, ""
	}
	reference, chartVersion := chartref.SplitChartReferenceVersion(chart)
	chartName := chartref.ChartName(reference)
	if chartName != runtimetarget.DevopsComponentName {
		reason := "names " + chartName + ", not the stock " + runtimetarget.DevopsComponentName + " chart, so it rides its own release line"
		if chartName == "" {
			reason = "could not be read to find its chart name"
		}
		return nil, "runtimechart " + chart + " " + reason + "; pin leaves it alone"
	}
	if chartVersion == "" {
		return nil, ""
	}
	return &model.PinSite{
		Kind:    model.PinSiteRuntimeChart,
		Detail:  chart,
		Current: chartVersion,
		Target:  target,
	}, ""
}

// DescribeCwdFallbackProjectRoot names the fallback root's HEAD and warns when its pins disagree
// with what is deployed. Best-effort: an unreadable HEAD never fails the plan.
func DescribeCwdFallbackProjectRoot(ctx core.Context, plan model.PinPlan) string {
	note := "project root resolved from the caller's working directory, not a known checkout of this environment or its tenant"
	if head, err := gitx.GitShortCommit(ctx, plan.ProjectRoot); err == nil && strings.TrimSpace(head) != "" {
		note += "; HEAD is " + strings.TrimSpace(head)
	}
	deployed := strings.TrimSpace(plan.Previous)
	if deployed == "" {
		return note
	}
	for _, site := range plan.Sites {
		if site.Kind == model.PinSiteRuntimeVersion {
			continue
		}
		current := strings.TrimSpace(site.Current)
		if current != "" && current != deployed {
			return note + fmt.Sprintf("; this tree's own %s reference is at %s, which disagrees with %s/%s's deployed version (%s) — it may be stale or may not be this environment's repo at all",
				site.Kind, current, plan.Tenant, plan.Environment, deployed)
		}
	}
	return note
}

// ApplyPinPlan rewrites every file site; env-config sites are applied via ApplyPinnedEnvConfig.
func ApplyPinPlan(plan model.PinPlan) error {
	byPath := map[string]struct{}{}
	for _, site := range plan.Changes() {
		if site.Path == "" {
			continue
		}
		byPath[site.Path] = struct{}{}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, relative := range paths {
		full := filepath.Join(plan.ProjectRoot, filepath.FromSlash(relative))
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("read %s: %w", relative, err)
		}
		info, err := os.Stat(full)
		if err != nil {
			return fmt.Errorf("stat %s: %w", relative, err)
		}
		updated := rewritePinnedVersions(string(data), plan.Target)
		if updated == string(data) {
			continue
		}
		if err := os.WriteFile(full, []byte(updated), info.Mode().Perm()); err != nil {
			return fmt.Errorf("write %s: %w", relative, err)
		}
	}
	return nil
}

// ApplyPinnedEnvConfig returns the env config with every env-config site applied, so the caller
// saves once; separate saves would overwrite each other since a save writes the whole struct.
func ApplyPinnedEnvConfig(env config.EnvConfig, plan model.PinPlan) (config.EnvConfig, bool) {
	updated := env
	changed := false
	if planHasSite(plan, model.PinSiteRuntimeVersion) && strings.TrimSpace(updated.RuntimeVersion) != strings.TrimSpace(plan.Target) {
		updated.RuntimeVersion = plan.Target
		changed = true
	}
	if planHasEnvConfigSite(plan, model.PinSiteRuntimeImage) {
		if rewritten := rewritePinnedReferenceVersion(updated.RuntimeImage, plan.Target); rewritten != strings.TrimSpace(updated.RuntimeImage) {
			updated.RuntimeImage = rewritten
			changed = true
		}
	}
	if planHasSite(plan, model.PinSiteRuntimeChart) {
		if rewritten := rewritePinnedReferenceVersion(updated.RuntimeChart, plan.Target); rewritten != strings.TrimSpace(updated.RuntimeChart) {
			updated.RuntimeChart = rewritten
			changed = true
		}
	}
	return updated, changed
}

func planHasSite(plan model.PinPlan, kind model.PinSiteKind) bool {
	for _, site := range plan.Sites {
		if site.Kind == kind {
			return true
		}
	}
	return false
}

// Only sites from the env config (empty Path) are persisted here; file sites are rewritten by ApplyPinPlan.
func planHasEnvConfigSite(plan model.PinPlan, kind model.PinSiteKind) bool {
	for _, site := range plan.Sites {
		if site.Kind == kind && strings.TrimSpace(site.Path) == "" {
			return true
		}
	}
	return false
}

func rewritePinnedReferenceVersion(raw, target string) string {
	raw = strings.TrimSpace(raw)
	cut := strings.LastIndex(raw, "/")
	if cut < 0 {
		return raw
	}
	last := raw[cut+1:]
	sep := strings.LastIndex(last, ":")
	if sep <= 0 {
		return raw
	}
	return raw[:cut+1] + last[:sep] + ":" + target
}

// rewritePinnedVersions changes only erun version references in content.
func rewritePinnedVersions(content, target string) string {
	content = erunTerraformRefPattern.ReplaceAllString(content, "${1}v"+target)
	content = erunHelmDependencyPattern.ReplaceAllString(content, "${1}"+target)
	content = erunDevopsImagePattern.ReplaceAllString(content, "${1}"+target)
	content = erunImageReferencePattern.ReplaceAllString(content, "${1}${2}"+target+`"`)
	return content
}

// `helm dependency build` refuses a lock that disagrees with its chart, so every re-pinned chart's
// lock is regenerated.

// HelmDependencyUpdater regenerates one chart's Chart.lock and charts/.
type HelmDependencyUpdater func(ctx core.Context, chartDir string) error

// RunHelmDependencyUpdate is the real updater.
func RunHelmDependencyUpdate(ctx core.Context, chartDir string) error {
	ctx.TraceCommand(chartDir, "helm", "dependency", "update")
	if ctx.DryRun {
		return nil
	}
	cmd := core.Command("helm", "dependency", "update")
	cmd.Dir = chartDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helm dependency update in %s: %w: %s", chartDir, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// PinnedChartDirs lists each chart directory a plan rewrote, once.
func PinnedChartDirs(plan model.PinPlan) []string {
	seen := map[string]struct{}{}
	dirs := make([]string, 0, len(plan.Sites))
	for _, site := range plan.Changes() {
		if site.Kind != model.PinSiteHelmDependency || strings.TrimSpace(site.Path) == "" {
			continue
		}
		dir := filepath.Dir(filepath.Join(plan.ProjectRoot, filepath.FromSlash(site.Path)))
		if _, already := seen[dir]; already {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

// RefreshPinnedChartLocks regenerates each re-pinned chart's lock, stopping at the first failure.
func RefreshPinnedChartLocks(ctx core.Context, plan model.PinPlan, update HelmDependencyUpdater) error {
	if update == nil {
		update = RunHelmDependencyUpdate
	}
	for _, dir := range PinnedChartDirs(plan) {
		if err := update(ctx, dir); err != nil {
			return err
		}
	}
	return nil
}
