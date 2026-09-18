package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlaywrightSuite is one discovered erun e2e target: a Playwright project
// directory, optionally scoped to the component name it sits under.
type PlaywrightSuite struct {
	Dir       string
	Component string
}

// playwrightConfigPath returns the suite's own Playwright config file path,
// or "" when dir holds neither the .ts nor the .js variant.
func playwrightConfigPath(dir string) (string, error) {
	for _, name := range []string{"playwright.config.ts", "playwright.config.js"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

func isPlaywrightSuiteDir(dir string) (bool, error) {
	path, err := playwrightConfigPath(dir)
	if err != nil {
		return false, err
	}
	return path != "", nil
}

// PlaywrightSuitesUnderDir enumerates the per-component suites directly under
// a playwright/ root -- the same "one config file per subdirectory" shape
// docker/ (Dockerfile) and k8s/ (Chart.yaml) already use, applied to a
// playwright.config file.
func PlaywrightSuitesUnderDir(dir string) ([]PlaywrightSuite, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	suites := make([]PlaywrightSuite, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		componentDir := filepath.Join(dir, entry.Name())
		ok, err := isPlaywrightSuiteDir(componentDir)
		if err != nil {
			return nil, err
		}
		if ok {
			suites = append(suites, PlaywrightSuite{Dir: componentDir, Component: entry.Name()})
		}
	}
	return suites, nil
}

// isPlaywrightModuleDir reports whether dir is a usable playwright/ module: a
// suite directly, or a folder whose subdirectories hold per-component suites.
func isPlaywrightModuleDir(dir string) (bool, error) {
	if filepath.Base(dir) != "playwright" {
		return false, nil
	}
	if ok, err := isPlaywrightSuiteDir(dir); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	suites, err := PlaywrightSuitesUnderDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return len(suites) > 0, nil
}

// resolveAndValidatePlaywrightDir mirrors resolveAndValidateDockerDir: a
// configured override must resolve to a real playwright module, or the
// misconfiguration fails loudly rather than silently falling back to
// convention.
func resolveAndValidatePlaywrightDir(projectRoot, override, configKey string) (string, bool, error) {
	dir := resolveProjectPath(projectRoot, override)
	if dir == "" {
		return "", false, nil
	}
	ok, err := isPlaywrightModuleDir(dir)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, fmt.Errorf("configured playwright path %q (.erun/config.yaml %s) is not a playwright suite module: expected a directory named \"playwright\" holding a Playwright project or per-component suites", strings.TrimSpace(override), configKey)
	}
	return dir, true, nil
}

func configuredPlaywrightDir(projectRoot string) (string, bool, error) {
	paths, err := loadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	return resolveAndValidatePlaywrightDir(projectRoot, paths.Playwright, "paths.playwright")
}

// resolveComponentAwarePlaywrightDir mirrors resolveComponentAwareDockerDir: a
// selected components: entry wins over the project-global paths.playwright
// override.
func resolveComponentAwarePlaywrightDir(projectRoot, selectedComponent string) (string, bool, error) {
	name, paths, ok, err := resolveProjectComponent(projectRoot, selectedComponent)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return configuredPlaywrightDir(projectRoot)
	}
	return resolveAndValidatePlaywrightDir(projectRoot, paths.Playwright, fmt.Sprintf("components.%s.playwright", name))
}

func resolveProjectRootDevopsPlaywrightDir(projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	candidates, err := findDevopsPlaywrightDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops playwright directories found under project root")
	}
}

func findDevopsPlaywrightDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		playwrightDir := filepath.Join(projectRoot, entry.Name(), "playwright")
		ok, err := isPlaywrightModuleDir(playwrightDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, playwrightDir)
		}
	}
	return candidates, nil
}

// ResolveCurrentPlaywrightSuite resolves the one e2e suite erun e2e should run
// for the current project: the configured paths.playwright override (or a
// selected components: entry) when set, else the <tenant>-devops/playwright
// convention. A project with no playwright/ folder at all returns (nil, nil),
// not an error, so an undiscovered suite never blocks build or deploy --
// mirroring docker/'s ErrDockerBuildContextNotFound tolerance. When more than
// one per-component suite exists under the resolved root and component does
// not select one, the error names every discovered component so an operator
// can pass --component.
func ResolveCurrentPlaywrightSuite(findProjectRoot ProjectFinderFunc, component string) (*PlaywrightSuite, error) {
	component = strings.TrimSpace(component)

	playwrightDir, err := resolveCurrentPlaywrightDir(findProjectRoot, component)
	if err != nil || playwrightDir == "" {
		return nil, err
	}
	return selectPlaywrightSuite(playwrightDir, component)
}

// resolveCurrentPlaywrightDir resolves the playwright/ root for the current
// project: the configured paths.playwright override (or a selected
// components: entry) when set, else the <tenant>-devops/playwright
// convention. Empty, non-error return means no playwright/ folder resolves at
// all -- mirroring docker/'s ErrDockerBuildContextNotFound tolerance.
func resolveCurrentPlaywrightDir(findProjectRoot ProjectFinderFunc, component string) (string, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
	if err != nil || projectRoot == "" {
		return "", err
	}

	playwrightDir, ok, err := resolveComponentAwarePlaywrightDir(projectRoot, component)
	if err != nil {
		return "", err
	}
	if ok {
		return playwrightDir, nil
	}

	playwrightDir, ok, err = resolveProjectRootDevopsPlaywrightDir(projectRoot)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return playwrightDir, nil
}

// selectPlaywrightSuite picks the one suite erun e2e should run out of a
// resolved playwright/ root: the root itself when it is a suite directly, or
// the matching (or lone) per-component subdirectory otherwise. When more than
// one per-component suite exists and component does not select one, the error
// names every discovered component so an operator can pass --component.
func selectPlaywrightSuite(playwrightDir, component string) (*PlaywrightSuite, error) {
	if suiteOK, err := isPlaywrightSuiteDir(playwrightDir); err != nil {
		return nil, err
	} else if suiteOK {
		componentName := component
		if componentName == "" && filepath.Base(filepath.Dir(playwrightDir)) == "playwright" {
			componentName = filepath.Base(playwrightDir)
		}
		return &PlaywrightSuite{Dir: playwrightDir, Component: componentName}, nil
	}

	suites, err := PlaywrightSuitesUnderDir(playwrightDir)
	if err != nil {
		return nil, err
	}
	if component != "" {
		return selectPlaywrightSuiteByComponent(suites, component)
	}
	return selectSolePlaywrightSuite(suites)
}

func selectPlaywrightSuiteByComponent(suites []PlaywrightSuite, component string) (*PlaywrightSuite, error) {
	for i := range suites {
		if suites[i].Component == component {
			return &suites[i], nil
		}
	}
	return nil, fmt.Errorf("playwright suite not found for component %q", component)
}

func selectSolePlaywrightSuite(suites []PlaywrightSuite) (*PlaywrightSuite, error) {
	switch len(suites) {
	case 0:
		return nil, nil
	case 1:
		return &suites[0], nil
	default:
		names := make([]string, len(suites))
		for i, suite := range suites {
			names[i] = suite.Component
		}
		return nil, fmt.Errorf("more than one playwright suite found (%s); pass --component", strings.Join(names, ", "))
	}
}
