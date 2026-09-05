package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// E2ETarget scopes an erun e2e run to a deployed environment (and, when the
// discovered suite is split by component, the component to test), the same
// tenant/environment/namespace/context shape open/deploy/expose already use.
type E2ETarget struct {
	Component         string
	Tenant            string
	Environment       string
	Namespace         string
	KubernetesContext string
}

// E2EResult is what a completed erun e2e run resolved and reports.
type E2EResult struct {
	Suite   PlaywrightSuite `json:"suite"`
	Service string          `json:"service,omitempty"`
	BaseURL string          `json:"baseUrl"`
	Version string          `json:"version"`
}

// PlaywrightRunnerFunc runs a discovered suite's own dependency install (when
// needed) and test command. env is appended to the process environment.
type PlaywrightRunnerFunc func(dir string, env []string, stdin io.Reader, stdout, stderr io.Writer) error

var hardcodedPlaywrightBaseURLPattern = regexp.MustCompile(`baseURL\s*:\s*['"]`)

// lintPlaywrightSuite refuses a discovered suite that disables TLS
// verification or hardcodes its own baseURL. Both silently void the guarantee
// erun e2e exists to make: a real, resolved-at-runtime HTTPS URL whose
// certificate is actually verified.
func lintPlaywrightSuite(dir string) error {
	configPath, err := playwrightConfigPath(dir)
	if err != nil {
		return err
	}
	if configPath == "" {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	content := string(data)
	if strings.Contains(content, "ignoreHTTPSErrors") {
		return fmt.Errorf("erun e2e: %s sets ignoreHTTPSErrors, which erun e2e forbids: the exposed certificate is real, and verifying it is part of what a deploy is checked for", configPath)
	}
	if hardcodedPlaywrightBaseURLPattern.MatchString(content) {
		return fmt.Errorf("erun e2e: %s hardcodes baseURL; erun e2e injects the resolved URL via the ERUN_E2E_BASE_URL environment variable and refuses a literal that would silently ignore it", configPath)
	}
	return nil
}

// RunE2E refuses before a browser starts when the target environment is not
// ready to be tested against, then runs the discovered suite once with the
// resolved HTTPS base URL and the environment's deployed version injected as
// ERUN_E2E_BASE_URL / ERUN_E2E_VERSION. run defaults to RunPlaywrightSuite.
func RunE2E(ctx Context, suite PlaywrightSuite, target E2ETarget, run PlaywrightRunnerFunc) (*E2EResult, error) {
	if err := lintPlaywrightSuite(suite.Dir); err != nil {
		return nil, err
	}

	service := strings.TrimSpace(target.Component)
	if service == "" {
		service = suite.Component
	}

	exposedService, version, err := checkE2EPreconditions(ctx, target, service)
	if err != nil {
		return nil, err
	}

	baseURL := "https://" + exposedService.Hostname
	result := &E2EResult{Suite: suite, Service: service, BaseURL: baseURL, Version: version}

	ctx.Trace(fmt.Sprintf("e2e: suite %s", suite.Dir))
	ctx.Trace(fmt.Sprintf("e2e: base url %s (resolved from the expose-%s ingress)", baseURL, exposedService.Service))
	ctx.Trace(fmt.Sprintf("e2e: deployed version %s", version))
	if ctx.DryRun {
		return result, nil
	}

	if run == nil {
		run = RunPlaywrightSuite
	}
	env := []string{"ERUN_E2E_BASE_URL=" + baseURL, "ERUN_E2E_VERSION=" + version}
	if err := run(suite.Dir, env, ctx.Stdin, ctx.Stdout, ctx.Stderr); err != nil {
		return nil, err
	}
	return result, nil
}

// checkE2EPreconditions runs every read-only check erun e2e refuses on before
// a browser starts: the environment is deployed, the service is exposed, its
// certificate is ready, and a deployed version resolves. Read-only, so it runs
// even under --dry-run -- the same "no mutation, so no reason to skip it"
// reasoning CheckKubernetesDeployment's own doc comment gives.
func checkE2EPreconditions(ctx Context, target E2ETarget, service string) (ExposedService, string, error) {
	deployed, err := CheckKubernetesDeployment(ctx, KubernetesDeploymentCheckParams{
		Name:              RuntimeReleaseName(target.Tenant),
		Namespace:         target.Namespace,
		KubernetesContext: target.KubernetesContext,
	})
	if err != nil {
		return ExposedService{}, "", fmt.Errorf("erun e2e: could not confirm %s/%s is deployed: %w", target.Tenant, target.Environment, err)
	}
	if !deployed {
		return ExposedService{}, "", fmt.Errorf("erun e2e: %s/%s "+KubernetesDeploymentAbsentMessageMarker+" %q not found in namespace %q); run `erun deploy %s %s` first",
			target.Tenant, target.Environment, RuntimeReleaseName(target.Tenant), target.Namespace, target.Tenant, target.Environment)
	}

	req := ShellLaunchParams{
		Tenant:            target.Tenant,
		Environment:       target.Environment,
		Namespace:         target.Namespace,
		KubernetesContext: target.KubernetesContext,
	}
	exposed, err := ListExposedServices(req)
	if err != nil {
		return ExposedService{}, "", fmt.Errorf("erun e2e: could not list exposed services for %s/%s: %w", target.Tenant, target.Environment, err)
	}
	exposedService, err := resolveE2EExposedService(exposed, service, target.Tenant, target.Environment)
	if err != nil {
		return ExposedService{}, "", err
	}
	if exposedService.Scheme != "https" || !exposedService.TLSReady {
		reason := exposedService.TLSNotReadyReason
		if reason == "" {
			reason = "the environment has no DNS-01 certificate configuration, so no certificate was ever requested"
		}
		return ExposedService{}, "", fmt.Errorf("erun e2e: %s is not serving a valid HTTPS certificate yet: %s", exposedService.Hostname, reason)
	}

	version, err := ResolveDeployedHelmReleaseVersion(ctx, RuntimeReleaseName(target.Tenant), target.Namespace, target.KubernetesContext)
	if err != nil {
		return ExposedService{}, "", fmt.Errorf("erun e2e: could not resolve the deployed version for %s/%s: %w", target.Tenant, target.Environment, err)
	}
	if strings.TrimSpace(version) == "" {
		return ExposedService{}, "", fmt.Errorf("erun e2e: could not resolve a deployed version for %s/%s; run `erun deploy %s %s` first",
			target.Tenant, target.Environment, target.Tenant, target.Environment)
	}
	return exposedService, version, nil
}

// resolveE2EExposedService picks the exposure erun e2e should test against: an
// explicitly named service/component, or the environment's lone exposure when
// none is named. More than one exposure with none named is ambiguous, mirroring
// resolveProjectComponent's ambiguity error.
func resolveE2EExposedService(services []ExposedService, service, tenant, environment string) (ExposedService, error) {
	if service != "" {
		for _, s := range services {
			if s.Service == service {
				return s, nil
			}
		}
		return ExposedService{}, fmt.Errorf("erun e2e: %q is not exposed in %s/%s, so there is no URL; run `erun expose %s` first", service, tenant, environment, service)
	}
	switch len(services) {
	case 0:
		return ExposedService{}, fmt.Errorf("erun e2e: %s/%s has no exposed service, so there is no URL; run `erun expose <service>` first", tenant, environment)
	case 1:
		return services[0], nil
	default:
		names := make([]string, len(services))
		for i, s := range services {
			names[i] = s.Service
		}
		return ExposedService{}, fmt.Errorf("erun e2e: %s/%s exposes more than one service (%s); pass --component", tenant, environment, strings.Join(names, ", "))
	}
}

// RunPlaywrightSuite installs the discovered suite's own dependencies when
// node_modules is missing (npm or yarn, whichever lockfile it carries), then
// runs its Playwright tests once, streaming output the same way a project
// build script does.
func RunPlaywrightSuite(dir string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); errors.Is(err, os.ErrNotExist) {
		name, args := playwrightInstallCommand(dir)
		install := Command(name, args...)
		install.Dir = dir
		install.Stdin = stdin
		install.Stdout = stdout
		install.Stderr = stderr
		if err := install.Run(); err != nil {
			return fmt.Errorf("erun e2e: installing suite dependencies: %w", err)
		}
	} else if err != nil {
		return err
	}

	test := Command("npx", "playwright", "test")
	test.Dir = dir
	test.Env = append(os.Environ(), env...)
	test.Stdin = stdin
	test.Stdout = stdout
	test.Stderr = stderr
	return test.Run()
}

func playwrightInstallCommand(dir string) (string, []string) {
	if _, err := os.Stat(filepath.Join(dir, "yarn.lock")); err == nil {
		return "yarn", []string{"install", "--frozen-lockfile"}
	}
	return "npm", []string{"ci"}
}
