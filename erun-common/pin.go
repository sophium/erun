package eruncommon

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// One erun version is written down in several places in a tenant repo — the
// Terraform module refs, an erun image reference a tenant's own Terraform
// variables set directly (e.g. the cluster-edge module's dns01_webhook_image),
// every umbrella chart's erun dependencies, the build-env image tag, and the
// environment's own runtimeversion — and they only work when they agree.
// Nothing enforced that, so they drifted: a repo was found pinned to three
// different versions at once, and realigning it meant editing seven files by
// hand.
//
// The sites are found by pattern rather than by walking a fixed layout. The
// Terraform root alone has three legitimate locations, and a tenant is free to
// add modules and umbrellas, so a scan that recognises an erun reference wherever
// it appears is the thing that stays true as a repo grows.

// PinSiteKind names what kind of reference a site is, so a plan can be read
// without knowing the repo's layout.
type PinSiteKind string

const (
	PinSiteRuntimeVersion PinSiteKind = "runtime-version"
	PinSiteTerraformRef   PinSiteKind = "terraform-ref"
	PinSiteHelmDependency PinSiteKind = "helm-dependency"
	PinSiteRuntimeImage   PinSiteKind = "runtime-image"
	PinSiteImageReference PinSiteKind = "image-reference"
)

// PinSite is one place a version is recorded, and what it would become.
type PinSite struct {
	Kind PinSiteKind `json:"kind"`
	// Path is repo-relative, or empty for the environment's own config, which
	// is not a file in the repo.
	Path    string `json:"path,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Current string `json:"current"`
	Target  string `json:"target"`
}

// Aligned reports whether this site already holds the target, which is what
// makes a re-pin safe to re-run.
func (s PinSite) Aligned() bool {
	return strings.TrimSpace(s.Current) == strings.TrimSpace(s.Target)
}

// PinPlan is every site a re-pin would touch, resolved before anything is
// written so it can be rendered and refused as a whole.
type PinPlan struct {
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	Target      string    `json:"target"`
	Previous    string    `json:"previous,omitempty"`
	ProjectRoot string    `json:"projectRoot"`
	Sites       []PinSite `json:"sites"`
	// Skipped explains, in order, why something that looked like a version
	// reference was deliberately left out of Sites, so a caller reading the
	// plan is not left assuming it was covered.
	Skipped []string `json:"skipped,omitempty"`
	// ProjectRootNote is set only when ProjectRoot was not read from the
	// environment's own recorded repo path or a sibling environment's — i.e. it
	// fell back to the caller's working directory, which may or may not be the
	// environment's real repo at all. It names that tree's HEAD and, when the
	// tree's own pinned versions disagree with what the environment has
	// deployed, warns that the plan may not describe the environment's real
	// drift. Empty whenever ProjectRoot came from a known checkout.
	ProjectRootNote string `json:"projectRootNote,omitempty"`
}

// Changes is the sites that would actually move. A plan whose changes are empty
// is a no-op, which is the expected result of running a re-pin twice.
func (p PinPlan) Changes() []PinSite {
	changes := make([]PinSite, 0, len(p.Sites))
	for _, site := range p.Sites {
		if !site.Aligned() {
			changes = append(changes, site)
		}
	}
	return changes
}

func (p PinPlan) Aligned() bool { return len(p.Changes()) == 0 }

// erunTerraformRefPattern matches an erun Terraform module reference and
// captures the tag it is pinned to. Only erun's own repository is rewritten: a
// tenant's other module sources have nothing to do with the erun version.
var erunTerraformRefPattern = regexp.MustCompile(`(github\.com/sophium/erun\.git//[^"'\s?]*\?ref=)v?([0-9][^"'\s]*)`)

// erunHelmDependencyPattern matches one erun OCI chart dependency's version line
// within a Chart.yaml. The repository line anchors it so a tenant's own charts,
// which are versioned independently, are left alone.
var erunHelmDependencyPattern = regexp.MustCompile(`(?ms)(-\s+name:\s*(\S+)[^\n]*\n(?:\s+[^\n]*\n)*?\s+repository:\s*[^\n]*sophium[^\n]*\n(?:\s+[^\n]*\n)*?\s+version:\s*)(\S+)`)

// erunDevopsImagePattern matches a build-env base image tag. The version
// class excludes `\` so an escaped quote right after it (as in an HCL string)
// is never swallowed into the captured version.
var erunDevopsImagePattern = regexp.MustCompile(`(erun-devops:)([0-9][^\s"'\\]*)`)

// erunImageReferencePattern matches a published erun image reference
// (ghcr.io/sophium/erun-<component>:<version>) that is itself a whole quoted
// assignment value — anchored on `=\s*"` immediately before the reference and
// a plain, unescaped closing `"` immediately after the version. A tenant's own
// terraform can set one of these directly — the cluster-edge module's
// dns01_webhook_image is the reported case — and it names an erun release
// exactly like the module ref above it, just as a variable's value instead of
// a module source string. The anchor is what keeps this from matching the same
// reference when it only appears as an example inside a variable's
// `description` prose: prose has other text between the `=` and the quoted
// reference, and its escaped inner quotes (`\"`) leave a `\` between the
// version and the next `"` rather than the version butting straight up
// against it. Excluding `\` from the version class also means a match can
// never consume that escape backslash, so a rewrite can never turn `\"` into a
// bare `"` and break the surrounding HCL string. erun-devops is excluded: a
// bare build-env tag is already the dedicated site above, and matching it here
// too would report the same reference twice.
var erunImageReferencePattern = regexp.MustCompile(`(=\s*")(ghcr\.io/sophium/(erun-[a-zA-Z0-9_-]+):)([0-9][^\s"'\\]*)"`)

// normalizePinPlanInputs trims and validates the inputs a plan is built from.
// An unresolved tenant/environment must refuse here rather than flow into a
// plan: the runtime-version site below would otherwise report a real-looking
// row (current "", detail "/") for an environment nobody actually named,
// which a caller could mistake for an honest reading rather than a resolution
// failure.
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

// ResolvePinPlan reads every pin site under projectRoot and reports what moving
// to target would change. It never writes, so a caller can render the plan,
// refuse it, or apply it.
func ResolvePinPlan(projectRoot, tenant, environment string, env EnvConfig, target string) (PinPlan, error) {
	if env.ResolvedType() == EnvironmentTypeHost {
		return PinPlan{}, fmt.Errorf("pin %s/%s: %s is a host environment — it has no pod and no runtime version to pin", tenant, environment, environment)
	}
	tenant, environment, projectRoot, target, err := normalizePinPlanInputs(tenant, environment, projectRoot, target)
	if err != nil {
		return PinPlan{}, err
	}

	plan := PinPlan{
		Tenant:      tenant,
		Environment: environment,
		Target:      target,
		ProjectRoot: projectRoot,
		Previous:    strings.TrimSpace(env.RuntimeVersion),
	}
	image := strings.TrimSpace(env.RuntimeImage)
	// A tenant-imaged env's runtimeversion is versioned on that image's own
	// release line, not erun's (frs/prod on ghcr.io/sophium/frs-devops:1.0.76 is
	// the reported case) — rewriting it to the erun target names a tag that line
	// never publishes, guaranteeing an ImagePullBackOff on the next deploy.
	if image != "" && !runtimeImageIsStockDevops(image) {
		plan.Skipped = append(plan.Skipped, "runtimeversion "+tenant+"/"+environment+" rides "+image+"'s own release line, not erun's, so pin leaves it alone; it moves on the tenant's own next build/release")
	} else {
		plan.Sites = append(plan.Sites, PinSite{
			Kind:    PinSiteRuntimeVersion,
			Detail:  tenant + "/" + environment,
			Current: strings.TrimSpace(env.RuntimeVersion),
			Target:  target,
		})
	}
	if image != "" {
		if _, version, ok := splitImageTag(image); ok {
			// Only the stock erun-devops image's tag is an erun version; a
			// tenant's own <tenant>-devops image rides the tenant's own release
			// line, and rewriting its tag to an erun version would name a tag
			// that line never publishes.
			if runtimeImageIsStockDevops(image) {
				plan.Sites = append(plan.Sites, PinSite{
					Kind:    PinSiteRuntimeImage,
					Detail:  image,
					Current: version,
					Target:  target,
				})
			} else {
				plan.Skipped = append(plan.Skipped, "runtimeimage "+image+" is not the stock erun-devops image; its tag rides the tenant's own release line, not erun's, so pin leaves it alone")
			}
		}
	}

	fileSites, err := scanPinFiles(projectRoot, target)
	if err != nil {
		return PinPlan{}, err
	}
	plan.Sites = append(plan.Sites, fileSites...)
	return plan, nil
}

// DescribeCwdFallbackProjectRoot names a resolved project root's HEAD and
// warns when the tree's own pinned versions disagree with what the
// environment has deployed. Callers use it only when ProjectRoot came from
// falling back to the caller's working directory rather than a checkout known
// to belong to this environment or its tenant, since that tree's identity was
// never actually verified. Best-effort: a HEAD that cannot be read still
// leaves the note naming the fallback, and an unreadable HEAD never fails the
// plan this only annotates.
func DescribeCwdFallbackProjectRoot(ctx Context, plan PinPlan) string {
	note := "project root resolved from the caller's working directory, not a known checkout of this environment or its tenant"
	if head, err := GitShortCommit(ctx, plan.ProjectRoot); err == nil && strings.TrimSpace(head) != "" {
		note += "; HEAD is " + strings.TrimSpace(head)
	}
	deployed := strings.TrimSpace(plan.Previous)
	if deployed == "" {
		return note
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeVersion {
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

func splitImageTag(image string) (repository, tag string, ok bool) {
	index := strings.LastIndex(image, ":")
	if index <= 0 || index == len(image)-1 {
		return "", "", false
	}
	// A registry port is not a tag separator.
	if strings.Contains(image[index+1:], "/") {
		return "", "", false
	}
	return image[:index], image[index+1:], true
}

// scanPinFiles walks the repo once and recognises each erun reference it finds.
// Vendored trees are skipped: a chart pulled into charts/ is a build artifact of
// a pin, not a pin, and rewriting it would edit something the next
// `helm dependency update` regenerates anyway.
func scanPinFiles(projectRoot, target string) ([]PinSite, error) {
	var sites []PinSite
	walkErr := filepath.WalkDir(projectRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if skipPinScanDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		relative, relErr := filepath.Rel(projectRoot, path)
		if relErr != nil {
			return nil
		}
		found, readErr := pinSitesInFile(path, filepath.ToSlash(relative), entry.Name(), target)
		if readErr != nil {
			return nil
		}
		sites = append(sites, found...)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].Path == sites[j].Path {
			return sites[i].Detail < sites[j].Detail
		}
		return sites[i].Path < sites[j].Path
	})
	return sites, nil
}

func skipPinScanDir(name string) bool {
	switch name {
	case ".git", "node_modules", "charts", ".terraform", "vendor", "build", "dist":
		return true
	}
	return false
}

func pinSitesInFile(path, relative, name, target string) ([]PinSite, error) {
	if !pinScannableFile(name) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)

	var sites []PinSite
	for _, match := range erunTerraformRefPattern.FindAllStringSubmatch(content, -1) {
		sites = append(sites, PinSite{Kind: PinSiteTerraformRef, Path: relative, Detail: match[0], Current: match[2], Target: target})
	}
	for _, match := range erunHelmDependencyPattern.FindAllStringSubmatch(content, -1) {
		sites = append(sites, PinSite{Kind: PinSiteHelmDependency, Path: relative, Detail: match[2], Current: match[3], Target: target})
	}
	for _, match := range erunDevopsImagePattern.FindAllStringSubmatch(content, -1) {
		sites = append(sites, PinSite{Kind: PinSiteRuntimeImage, Path: relative, Detail: "erun-devops", Current: match[2], Target: target})
	}
	if pinTerraformFile(name) {
		for _, match := range erunImageReferencePattern.FindAllStringSubmatch(content, -1) {
			if match[3] == "erun-devops" {
				continue
			}
			sites = append(sites, PinSite{Kind: PinSiteImageReference, Path: relative, Detail: match[3], Current: match[4], Target: target})
		}
	}
	return sites, nil
}

func pinScannableFile(name string) bool {
	switch {
	case pinTerraformFile(name):
		return true
	case name == "Chart.yaml":
		return true
	case name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile."):
		return true
	}
	return false
}

// pinTerraformFile reports whether name is a Terraform configuration or
// variables file — the two places a tenant's own terraform can carry an erun
// image reference directly, alongside a module ref.
func pinTerraformFile(name string) bool {
	return strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tfvars")
}

// ApplyPinPlan rewrites every file site to the target. The environment's own
// runtimeversion is not written here: it lives in erun's config store, which the
// caller owns, so the transport applies that half and this owns the repo half.
func ApplyPinPlan(plan PinPlan) error {
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

// rewritePinnedVersions moves every erun reference in one file to the target,
// and touches nothing else — a re-pin edits pins, never content.
func rewritePinnedVersions(content, target string) string {
	content = erunTerraformRefPattern.ReplaceAllString(content, "${1}v"+target)
	content = erunHelmDependencyPattern.ReplaceAllString(content, "${1}"+target)
	content = erunDevopsImagePattern.ReplaceAllString(content, "${1}"+target)
	content = erunImageReferencePattern.ReplaceAllString(content, "${1}${2}"+target+`"`)
	return content
}

// A rewritten Chart.yaml is only half a re-pin: the lock beside it still names
// the versions that were there before, and `helm dependency build` — which the
// deploy path runs — refuses a lock that disagrees with its chart. So the chart
// directories a re-pin touched have their locks regenerated, or the very next
// deploy fails on a tree the operator was told was aligned.

// HelmDependencyUpdater regenerates one chart's Chart.lock and charts/. It is a
// seam so a scenario can prove the pass runs — and runs once per chart — without
// a helm binary or a reachable registry.
type HelmDependencyUpdater func(ctx Context, chartDir string) error

// RunHelmDependencyUpdate is the real updater.
func RunHelmDependencyUpdate(ctx Context, chartDir string) error {
	ctx.TraceCommand(chartDir, "helm", "dependency", "update")
	if ctx.DryRun {
		return nil
	}
	cmd := Command("helm", "dependency", "update")
	cmd.Dir = chartDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helm dependency update in %s: %w: %s", chartDir, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// PinnedChartDirs lists the chart directories a plan rewrote a dependency in,
// deduplicated and ordered, so each is refreshed exactly once however many
// dependencies inside it moved.
func PinnedChartDirs(plan PinPlan) []string {
	seen := map[string]struct{}{}
	dirs := make([]string, 0, len(plan.Sites))
	for _, site := range plan.Changes() {
		if site.Kind != PinSiteHelmDependency || strings.TrimSpace(site.Path) == "" {
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

// RefreshPinnedChartLocks regenerates the lock of every chart the re-pin moved.
// A failure names the chart and stops: leaving some locks refreshed and others
// stale is a worse tree than the one we started from, and the operator needs to
// know which one to look at.
func RefreshPinnedChartLocks(ctx Context, plan PinPlan, update HelmDependencyUpdater) error {
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

// pinHistoryFileName records what the repo was pinned to before the last
// re-pin, so reverting is one motion rather than remembering a number. It lives
// beside the project's erun config because it describes this tree.
const pinHistoryFileName = "pin-history.json"

type pinHistory struct {
	Previous map[string]string `json:"previous"`
}

func pinHistoryPath(projectRoot string) string {
	return filepath.Join(projectRoot, projectConfigDir, pinHistoryFileName)
}

func pinHistoryKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

// RecordPinPrevious remembers the version this environment was on before a
// re-pin. A re-pin that changes nothing does not overwrite the record: reverting
// twice must still reach the version you came from, not the one you are on.
func RecordPinPrevious(projectRoot, tenant, environment, previous string) error {
	previous = strings.TrimSpace(previous)
	if previous == "" {
		return nil
	}
	path := pinHistoryPath(projectRoot)
	history := readPinHistory(path)
	if history.Previous == nil {
		history.Previous = map[string]string{}
	}
	history.Previous[pinHistoryKey(tenant, environment)] = previous
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// PinPrevious reports the version recorded before the last re-pin, which is what
// a revert targets.
func PinPrevious(projectRoot, tenant, environment string) (string, bool) {
	history := readPinHistory(pinHistoryPath(projectRoot))
	previous, ok := history.Previous[pinHistoryKey(tenant, environment)]
	return strings.TrimSpace(previous), ok && strings.TrimSpace(previous) != ""
}

func readPinHistory(path string) pinHistory {
	data, err := os.ReadFile(path)
	if err != nil {
		return pinHistory{}
	}
	var history pinHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return pinHistory{}
	}
	return history
}
