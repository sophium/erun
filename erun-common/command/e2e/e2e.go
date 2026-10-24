package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// E2ESuiteResult is how one suite's run ended.
type E2ESuiteResult struct {
	Component  string `json:"component"`
	Dockerfile string `json:"dockerfile"`
	BaseURL    string `json:"baseUrl"`
	Passed     bool   `json:"passed"`
	LogPath    string `json:"logPath,omitempty"`
	Failure    string `json:"failure,omitempty"`
}

// E2EResult is what a completed run reports; Tenant and Environment are empty
// when --base-url and --version meant no environment was read.
type E2EResult struct {
	Tenant      string           `json:"tenant,omitempty"`
	Environment string           `json:"environment,omitempty"`
	BaseURL     string           `json:"baseUrl,omitempty"`
	Version     string           `json:"version"`
	Suites      []E2ESuiteResult `json:"suites"`

	versionOverridden bool
}

// e2eDeps are RunE2E's effects, injected for tests.
type e2eDeps struct {
	runBuild   func(dir string, args []string, output io.Writer) error
	lookupHost func(host string) ([]string, error)
	logPath    func(target model.E2ETarget, component string) (string, error)
}

var defaultE2EDeps = e2eDeps{
	runBuild:   runE2EDockerBuild,
	lookupHost: lookupE2EHost,
	logPath:    e2eLogPath,
}

type e2eRun struct {
	suite   model.E2ESuite
	baseURL string
	args    []string
	logPath string
}

const e2eFailureLogTailLines = 40

var hardcodedPlaywrightBaseURLPattern = regexp.MustCompile(`baseURL\s*:\s*['"]`)

// RunE2E checks every precondition before any suite runs, then runs every
// suite even after one fails. Failures are reported only: e2e is not a release gate.
func RunE2E(ctx core.Context, params model.E2EParams) (*E2EResult, error) {
	return runE2E(ctx, params, defaultE2EDeps)
}

func runE2E(ctx core.Context, params model.E2EParams, deps e2eDeps) (*E2EResult, error) {
	result, runs, err := resolveE2ERuns(ctx, params, deps)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		ctx.TraceCommand(params.Plan.ProjectRoot, "docker", run.args...)
		if ctx.DryRun {
			result.Suites = append(result.Suites, E2ESuiteResult{Component: run.suite.Component, Dockerfile: run.suite.Dockerfile, BaseURL: run.baseURL, LogPath: run.logPath})
			continue
		}
		result.Suites = append(result.Suites, runE2ESuite(ctx, params.Plan.ProjectRoot, run, deps))
	}
	if ctx.DryRun {
		return result, nil
	}
	return result, reportE2EOutcome(ctx, result)
}

func resolveE2ERuns(ctx core.Context, params model.E2EParams, deps e2eDeps) (*E2EResult, []e2eRun, error) {
	for _, suite := range params.Plan.Suites {
		if err := lintPlaywrightSuite(suite.Dir); err != nil {
			return nil, nil, err
		}
	}
	secrets, err := resolveE2ESecrets(params.Plan.ProjectRoot)
	if err != nil {
		return nil, nil, err
	}
	baseURLOverride, err := parseE2EBaseURLOverride(params.BaseURL)
	if err != nil {
		return nil, nil, err
	}

	target := params.Target
	versionOverride := strings.TrimSpace(params.Version)
	if err := refuseE2EBaseURLWithoutVersion(params, baseURLOverride, versionOverride); err != nil {
		return nil, nil, err
	}
	version, exposed, err := checkE2EPreconditions(ctx, target, params.Plan.Suites, baseURLOverride != "", versionOverride)
	if err != nil {
		return nil, nil, err
	}
	traceE2EVersion(ctx, version, versionOverride != "")
	if len(secrets) > 0 {
		ctx.Trace("e2e: secrets configured as " + shared.DescribeDockerBuildSecrets(secrets) + " (.erun/config.yaml e2e.secrets)")
	}

	runs := make([]e2eRun, 0, len(params.Plan.Suites))
	for _, suite := range params.Plan.Suites {
		run, err := resolveE2ERun(ctx, suite, target, version, baseURLOverride, exposed, secrets, deps)
		if err != nil {
			return nil, nil, err
		}
		runs = append(runs, run)
	}
	return newE2EResult(target, baseURLOverride, version, versionOverride != ""), runs, nil
}

func newE2EResult(target model.E2ETarget, baseURLOverride, version string, versionOverridden bool) *E2EResult {
	result := &E2EResult{Version: version, versionOverridden: versionOverridden}
	if baseURLOverride != "" && versionOverridden {
		result.BaseURL = baseURLOverride
		return result
	}
	result.Tenant, result.Environment = target.Tenant, target.Environment
	return result
}

func (r *E2EResult) describe() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return r.Tenant + "/" + r.Environment
}

// refuseE2EBaseURLWithoutVersion: the current scope's deployed version need not
// be what an arbitrary --base-url serves.
func refuseE2EBaseURLWithoutVersion(params model.E2EParams, baseURLOverride, versionOverride string) error {
	if baseURLOverride == "" || versionOverride != "" || params.TargetNamed {
		return nil
	}
	return fmt.Errorf("erun e2e: --base-url %s needs the version it serves: without --tenant/--environment the expected version would come from the current environment %s/%s, which need not be what that URL serves. Pass --version <the version it serves>, or name the environment it serves with --tenant/--environment",
		baseURLOverride, params.Target.Tenant, params.Target.Environment)
}

func traceE2EVersion(ctx core.Context, version string, overridden bool) {
	if overridden {
		ctx.Trace(fmt.Sprintf("e2e: expected version %s (from --version; deployed-version lookup skipped)", version))
		return
	}
	ctx.Trace(fmt.Sprintf("e2e: deployed version %s", version))
}

func resolveE2ERun(ctx core.Context, suite model.E2ESuite, target model.E2ETarget, version, baseURLOverride string, exposed []clusterread.ExposedService, secrets []config.DockerBuildSecret, deps e2eDeps) (e2eRun, error) {
	baseURL := baseURLOverride
	ctx.Trace(fmt.Sprintf("e2e: suite %s: %s", suite.Component, suite.Dockerfile))
	if baseURL != "" {
		ctx.Trace(fmt.Sprintf("e2e: base url %s (from --base-url; exposure and certificate checks skipped)", baseURL))
	} else {
		service, err := resolveE2EExposedService(exposed, suite.Component, target.Tenant, target.Environment)
		if err != nil {
			return e2eRun{}, err
		}
		baseURL = service.Scheme + "://" + service.Hostname
		ctx.Trace(fmt.Sprintf("e2e: base url %s (resolved from the expose-%s ingress)", baseURL, service.Service))
	}
	if err := refuseLoopbackE2ETarget(ctx, baseURL, deps.lookupHost); err != nil {
		return e2eRun{}, err
	}
	logPath, err := deps.logPath(target, suite.Component)
	if err != nil {
		return e2eRun{}, err
	}
	ctx.Trace(fmt.Sprintf("e2e: log %s", logPath))
	return e2eRun{
		suite:   suite,
		baseURL: baseURL,
		args:    e2eDockerBuildArgs(suite, version, baseURL, exposed, secrets, core.BuildContainerCPUCapCgroupParent()),
		logPath: logPath,
	}, nil
}

// e2eDockerBuildArgs never caches the `e2e` stage, so it re-runs every time,
// and exports cache-only: an e2e run produces no image.
func e2eDockerBuildArgs(suite model.E2ESuite, version, baseURL string, exposed []clusterread.ExposedService, secrets []config.DockerBuildSecret, cgroupParent string) []string {
	args := []string{
		"build", "--progress=plain",
		"--target", "e2e",
		"--no-cache-filter", "e2e",
		"--output", "type=cacheonly",
		"--build-arg", "ERUN_E2E_VERSION=" + version,
		"--build-arg", "ERUN_E2E_BASE_URL=" + baseURL,
	}
	sorted := slices.Clone(exposed)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Service < sorted[j].Service })
	for _, service := range sorted {
		args = append(args, "--build-arg", e2eServiceURLBuildArg(service.Service)+"="+service.Scheme+"://"+service.Hostname)
	}
	args = build.AppendCgroupParentArg(args, cgroupParent)
	args = append(args, shared.DockerSecretArgs(secrets)...)
	return append(args, "-f", suite.Dockerfile, ".")
}

var e2eBuildArgUnsafe = regexp.MustCompile(`[^A-Z0-9]+`)

// e2eServiceURLBuildArg: erun-console -> ERUN_E2E_URL_ERUN_CONSOLE.
func e2eServiceURLBuildArg(service string) string {
	return "ERUN_E2E_URL_" + strings.Trim(e2eBuildArgUnsafe.ReplaceAllString(strings.ToUpper(service), "_"), "_")
}

func runE2ESuite(ctx core.Context, projectRoot string, run e2eRun, deps e2eDeps) E2ESuiteResult {
	result := E2ESuiteResult{Component: run.suite.Component, Dockerfile: run.suite.Dockerfile, BaseURL: run.baseURL, LogPath: run.logPath}
	ctx.Info(fmt.Sprintf("e2e: running %s against %s", run.suite.Component, run.baseURL))
	if err := os.MkdirAll(filepath.Dir(run.logPath), 0o755); err != nil {
		result.Failure = "could not create the log directory: " + err.Error()
		return result
	}
	logFile, err := os.Create(run.logPath)
	if err != nil {
		result.Failure = "could not create the log: " + err.Error()
		return result
	}
	runErr := deps.runBuild(projectRoot, run.args, io.MultiWriter(logFile, ctx.Stderr))
	closeErr := logFile.Close()
	switch {
	case runErr != nil:
		result.Failure = "docker build --target e2e failed: " + runErr.Error()
	case closeErr != nil:
		result.Failure = "could not write the log: " + closeErr.Error()
	default:
		result.Passed = true
	}
	return result
}

func reportE2EOutcome(ctx core.Context, result *E2EResult) error {
	failed := make([]string, 0, len(result.Suites))
	for _, suite := range result.Suites {
		if suite.Passed {
			ctx.Info(fmt.Sprintf("e2e: passed %s against %s (version %s)", suite.Component, suite.BaseURL, result.Version))
			continue
		}
		failed = append(failed, suite.Component)
		ctx.Info(formatE2EFailure(result, suite))
	}
	if len(failed) == 0 {
		return nil
	}
	ctx.Info(fmt.Sprintf("e2e: %d of %d suites failed. erun e2e is not a release gate: nothing was blocked or rolled back, and nothing was filed. File each failure above as a bug against version %s.",
		len(failed), len(result.Suites), result.Version))
	return fmt.Errorf("erun e2e: %d of %d suites failed against %s at version %s: %s",
		len(failed), len(result.Suites), result.describe(), result.Version, strings.Join(failed, ", "))
}

func formatE2EFailure(result *E2EResult, suite E2ESuiteResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "e2e: FAILED %s\n", suite.Component)
	fmt.Fprintf(&b, "  suite:     %s\n", suite.Dockerfile)
	fmt.Fprintf(&b, "  component: %s\n", suite.Component)
	fmt.Fprintf(&b, "  target:    %s\n", suite.BaseURL)
	if result.versionOverridden {
		fmt.Fprintf(&b, "  version:   %s (expected, from --version)\n", result.Version)
	} else {
		fmt.Fprintf(&b, "  version:   %s (deployed to %s/%s)\n", result.Version, result.Tenant, result.Environment)
	}
	fmt.Fprintf(&b, "  step:      %s\n", suite.Failure)
	fmt.Fprintf(&b, "  log:       %s", suite.LogPath)
	if tail := e2eLogTail(suite.LogPath, e2eFailureLogTailLines); tail != "" {
		fmt.Fprintf(&b, "\n  last lines of the log:\n%s", tail)
	}
	return b.String()
}

func e2eLogTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	return "    " + strings.Join(lines, "\n    ")
}

// checkE2EPreconditions is read-only, so it runs even under --dry-run. With
// both --base-url and --version the environment is not read at all.
func checkE2EPreconditions(ctx core.Context, target model.E2ETarget, suites []model.E2ESuite, baseURLOverridden bool, versionOverride string) (string, []clusterread.ExposedService, error) {
	if baseURLOverridden && versionOverride != "" {
		ctx.Trace("e2e: --base-url and --version name the target; no environment is read (deployment, exposure and version checks skipped)")
		return versionOverride, nil, nil
	}
	deployed, err := deploy.CheckKubernetesDeployment(ctx, model.KubernetesDeploymentCheckParams{
		Name:              config.RuntimeReleaseName(target.Tenant),
		Namespace:         target.Namespace,
		KubernetesContext: target.KubernetesContext,
	})
	if err != nil {
		return "", nil, fmt.Errorf("erun e2e: could not confirm %s/%s is deployed: %w", target.Tenant, target.Environment, err)
	}
	if !deployed {
		return "", nil, fmt.Errorf("erun e2e: %s/%s "+deploy.KubernetesDeploymentAbsentMessageMarker+" %q not found in namespace %q); run `erun deploy %s %s` first",
			target.Tenant, target.Environment, config.RuntimeReleaseName(target.Tenant), target.Namespace, target.Tenant, target.Environment)
	}

	var exposed []clusterread.ExposedService
	if !baseURLOverridden {
		if exposed, err = checkE2EExposures(target, suites); err != nil {
			return "", nil, err
		}
	}

	if versionOverride != "" {
		return versionOverride, exposed, nil
	}
	version, err := deploy.ResolveDeployedHelmReleaseVersion(ctx, config.RuntimeReleaseName(target.Tenant), target.Namespace, target.KubernetesContext)
	if err != nil {
		return "", nil, fmt.Errorf("erun e2e: could not resolve the deployed version for %s/%s: %w", target.Tenant, target.Environment, err)
	}
	if strings.TrimSpace(version) == "" {
		return "", nil, fmt.Errorf("erun e2e: could not resolve a deployed version for %s/%s; run `erun deploy %s %s` first",
			target.Tenant, target.Environment, target.Tenant, target.Environment)
	}
	return strings.TrimSpace(version), exposed, nil
}

func checkE2EExposures(target model.E2ETarget, suites []model.E2ESuite) ([]clusterread.ExposedService, error) {
	exposed, err := clusterread.ListExposedServices(config.ShellLaunchParams{
		Tenant:            target.Tenant,
		Environment:       target.Environment,
		Namespace:         target.Namespace,
		KubernetesContext: target.KubernetesContext,
	})
	if err != nil {
		return nil, fmt.Errorf("erun e2e: could not list exposed services for %s/%s: %w", target.Tenant, target.Environment, err)
	}
	for _, suite := range suites {
		service, err := resolveE2EExposedService(exposed, suite.Component, target.Tenant, target.Environment)
		if err != nil {
			return nil, err
		}
		if service.Scheme != "https" || !service.TLSReady {
			reason := service.TLSNotReadyReason
			if reason == "" {
				reason = "the environment has no DNS-01 certificate configuration, so no certificate was ever requested"
			}
			return nil, fmt.Errorf("erun e2e: %s is not serving a valid HTTPS certificate yet: %s", service.Hostname, reason)
		}
	}
	return exposed, nil
}

func resolveE2EExposedService(services []clusterread.ExposedService, service, tenant, environment string) (clusterread.ExposedService, error) {
	for _, s := range services {
		if s.Service == service {
			return s, nil
		}
	}
	return clusterread.ExposedService{}, fmt.Errorf("erun e2e: the %q suite targets the %q service, which is not exposed in %s/%s, so there is no URL; run `erun expose %s` first, or pass --base-url", service, service, tenant, environment, service)
}

func parseE2EBaseURLOverride(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", fmt.Errorf("erun e2e: --base-url %q is not an absolute http(s) URL such as https://app.example.com", raw)
	}
	return raw, nil
}

func lookupHostAddrs(lookup func(string) ([]string, error), host string) ([]string, bool) {
	addrs, err := lookup(host)
	return addrs, err == nil
}

// refuseLoopbackE2ETarget: the suite runs in a docker build without host
// networking, where loopback is the build container. Unresolved hosts pass.
func refuseLoopbackE2ETarget(ctx core.Context, rawURL string, lookup func(string) ([]string, error)) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := parsed.Hostname()
	addrs := []string{host}
	if net.ParseIP(host) == nil {
		var resolved bool
		addrs, resolved = lookupHostAddrs(lookup, host)
		if !resolved {
			ctx.Trace(fmt.Sprintf("e2e: %s does not resolve from this machine; not checked for a loopback address", host))
			return nil
		}
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && ip.IsLoopback() {
			return fmt.Errorf("erun e2e: %s resolves to a loopback address (a local cluster's ingress), which an e2e suite cannot reach: each suite runs in a docker build without host networking, where loopback is the build container itself. Run erun e2e against an environment whose ingress has a routable address, or pass --base-url with an address a docker build can reach", host)
		}
	}
	return nil
}

func lookupE2EHost(host string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupHost(ctx, host)
}

// resolveE2ESecrets fails on a declared secret that cannot be supplied rather
// than letting the suite silently skip what it checks.
func resolveE2ESecrets(projectRoot string) ([]config.DockerBuildSecret, error) {
	cfg, ok, err := shared.LoadProjectConfigForDockerSecrets(projectRoot)
	if err != nil || !ok {
		return nil, err
	}
	secrets := make([]config.DockerBuildSecret, 0, len(cfg.E2E.Secrets))
	for i, secret := range cfg.E2E.Secrets {
		secret.ID = strings.TrimSpace(secret.ID)
		secret.Env = strings.TrimSpace(secret.Env)
		secret.Src = strings.TrimSpace(secret.Src)
		if secret.ID == "" && secret.Env == "" && secret.Src == "" {
			continue
		}
		if err := validateE2ESecret(i, secret); err != nil {
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	return secrets, nil
}

func validateE2ESecret(index int, secret config.DockerBuildSecret) error {
	switch {
	case secret.ID == "":
		return fmt.Errorf("e2e.secrets entry %d: id is required, naming the secret the e2e stage mounts with --mount=type=secret,id=<id>", index+1)
	case (secret.Env == "") == (secret.Src == ""):
		return fmt.Errorf("e2e.secrets entry %q: declare exactly one of env (the environment variable holding the credential) or src (the file holding it)", secret.ID)
	case strings.Contains(secret.ID+secret.Env+secret.Src, ","):
		return fmt.Errorf("e2e.secrets entry %q: id, env and src must not contain a comma, which docker reads as a key=value separator in --secret", secret.ID)
	case secret.Env != "" && strings.TrimSpace(os.Getenv(secret.Env)) == "":
		return fmt.Errorf("e2e.secrets entry %q needs the environment variable %s, which is not set: set it, or point the entry at a file with `src:`", secret.ID, secret.Env)
	case secret.Src != "":
		if _, err := os.Stat(secret.Src); err != nil {
			return fmt.Errorf("e2e.secrets entry %q needs %s, which cannot be read: fix the path, or point the entry at an environment variable with `env:`", secret.ID, secret.Src)
		}
	}
	return nil
}

func e2eLogPath(target model.E2ETarget, component string) (string, error) {
	if strings.TrimSpace(target.Tenant) == "" && strings.TrimSpace(target.Environment) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".erun", "e2e", component+".log"), nil
	}
	tracePath, err := EnvTraceLogPath(target.Tenant, target.Environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(tracePath), "e2e", component+".log"), nil
}

// One writer takes both streams so os/exec serializes their writes.
func runE2EDockerBuild(dir string, args []string, output io.Writer) error {
	cmd := core.Command("docker", args...)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	return cmd.Run()
}

// lintPlaywrightSuite refuses disabled TLS verification or a hardcoded baseURL,
// either of which voids the verified-HTTPS guarantee.
func lintPlaywrightSuite(dir string) error {
	for _, name := range []string{"playwright.config.ts", "playwright.config.js"} {
		configPath := filepath.Join(dir, name)
		data, err := os.ReadFile(configPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		content := string(data)
		if strings.Contains(content, "ignoreHTTPSErrors") {
			return fmt.Errorf("erun e2e: %s sets ignoreHTTPSErrors, which erun e2e forbids: the exposed certificate is real, and verifying it is part of what a deploy is checked for", configPath)
		}
		if hardcodedPlaywrightBaseURLPattern.MatchString(content) {
			return fmt.Errorf("erun e2e: %s hardcodes baseURL; erun e2e passes the resolved URL as the ERUN_E2E_BASE_URL build arg and refuses a literal that would silently ignore it", configPath)
		}
	}
	return nil
}
