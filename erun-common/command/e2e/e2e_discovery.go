package e2e

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// E2ESuite is one suite under <tenant>-devops/e2e/<component>/; Component is
// also the exposed service it targets.
type E2ESuite struct {
	Component  string `json:"component"`
	Dir        string `json:"dir"`
	Dockerfile string `json:"dockerfile"`
}

// E2EPlan is the project root (every suite's build context) and the suites to run.
type E2EPlan struct {
	ProjectRoot string     `json:"projectRoot"`
	Suites      []E2ESuite `json:"suites"`
}

// Only an exact `AS e2e` counts: erun builds `--target e2e`, so `e2e-deps` must not match.
var dockerfileE2EStagePattern = regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+e2e(?:\s|$)`)

const e2eMigrationRemedy = "move each suite to <tenant>-devops/e2e/<component>/Dockerfile, whose `AS e2e` stage installs the suite's dependencies and runs it (for example `npx playwright test`) against the ERUN_E2E_BASE_URL build arg"

// ResolveE2EPlan discovers the suites to run; an empty component runs them all.
func ResolveE2EPlan(findProjectRoot shared.ProjectFinderFunc, component string) (E2EPlan, error) {
	component = strings.TrimSpace(component)
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, shared.DockerCommandTarget{})
	if err != nil {
		return E2EPlan{}, err
	}
	if projectRoot == "" {
		return E2EPlan{}, e2eComponentNotFound(component, nil)
	}
	projectRoot = filepath.Clean(projectRoot)

	cfg, _, err := shared.LoadProjectConfigForDockerSecrets(projectRoot)
	if err != nil {
		return E2EPlan{}, err
	}
	if err := refuseRetiredE2EConfig(cfg); err != nil {
		return E2EPlan{}, err
	}
	if err := refuseHostRunPlaywrightSuites(projectRoot); err != nil {
		return E2EPlan{}, err
	}

	e2eDir, err := resolveE2EDir(projectRoot, cfg.Paths.E2E)
	if err != nil {
		return E2EPlan{}, err
	}
	plan := E2EPlan{ProjectRoot: projectRoot}
	if e2eDir == "" {
		return plan, e2eComponentNotFound(component, nil)
	}
	suites, err := e2eSuitesUnderDir(e2eDir)
	if err != nil {
		return E2EPlan{}, err
	}
	plan.Suites, err = selectE2ESuites(suites, component)
	if err != nil {
		return E2EPlan{}, err
	}
	return plan, nil
}

func selectE2ESuites(suites []E2ESuite, component string) ([]E2ESuite, error) {
	if component == "" {
		return suites, nil
	}
	for _, suite := range suites {
		if suite.Component == component {
			return []E2ESuite{suite}, nil
		}
	}
	return nil, e2eComponentNotFound(component, suites)
}

func e2eComponentNotFound(component string, suites []E2ESuite) error {
	if component == "" {
		return nil
	}
	names := make([]string, len(suites))
	for i, suite := range suites {
		names[i] = suite.Component
	}
	found := "none"
	if len(names) > 0 {
		found = strings.Join(names, ", ")
	}
	return fmt.Errorf("e2e suite not found for component %q (discovered: %s)", component, found)
}

func refuseRetiredE2EConfig(cfg config.ProjectConfig) error {
	if strings.TrimSpace(cfg.Paths.Playwright) != "" {
		return fmt.Errorf("erun e2e: .erun/config.yaml paths.playwright names a host-run Playwright suite, which erun e2e no longer runs: %s, then replace the key with paths.e2e", e2eMigrationRemedy)
	}
	names := make([]string, 0, len(cfg.Components))
	for name := range cfg.Components {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		paths := cfg.Components[name]
		if strings.TrimSpace(paths.Playwright) != "" {
			return fmt.Errorf("erun e2e: .erun/config.yaml components.%s.playwright names a host-run Playwright suite, which erun e2e no longer runs: %s, then remove the key", name, e2eMigrationRemedy)
		}
		if strings.TrimSpace(paths.E2E) != "" {
			return fmt.Errorf("erun e2e: .erun/config.yaml components.%s.e2e is not read: e2e suites are discovered project-wide, so set paths.e2e instead", name)
		}
	}
	return nil
}

func refuseHostRunPlaywrightSuites(projectRoot string) error {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		legacy := filepath.Join(projectRoot, entry.Name(), "playwright")
		info, err := os.Stat(legacy)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("erun e2e: %s is a host-run Playwright suite, which erun e2e no longer runs: %s", legacy, e2eMigrationRemedy)
		}
	}
	return nil
}

// A configured paths.e2e must exist rather than silently falling back to convention.
func resolveE2EDir(projectRoot, override string) (string, error) {
	if dir := config.ResolveProjectPath(projectRoot, override); dir != "" {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("configured e2e path %q (.erun/config.yaml paths.e2e) is not a directory: expected one subdirectory per suite, each holding a Dockerfile with an `e2e` stage", strings.TrimSpace(override))
		}
		return dir, nil
	}

	candidates, err := devopsE2EDirs(projectRoot)
	if err != nil {
		return "", err
	}
	switch len(candidates) {
	case 0:
		return "", nil
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple devops e2e directories found under project root (%s); set .erun/config.yaml paths.e2e", strings.Join(candidates, ", "))
	}
}

func devopsE2EDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		dir := filepath.Join(projectRoot, entry.Name(), "e2e")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// A Dockerfile without an `e2e` stage is refused now rather than failing at
// `--target e2e` later.
func e2eSuitesUnderDir(dir string) ([]E2ESuite, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	suites := make([]E2ESuite, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		suiteDir := filepath.Join(dir, entry.Name())
		dockerfile := filepath.Join(suiteDir, "Dockerfile")
		data, err := os.ReadFile(dockerfile)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if !dockerfileE2EStagePattern.Match(data) {
			return nil, fmt.Errorf("erun e2e: %s declares no `e2e` stage: erun e2e builds `--target e2e`, so the stage that runs the suite must be named exactly e2e (`FROM <base> AS e2e`)", dockerfile)
		}
		suites = append(suites, E2ESuite{Component: entry.Name(), Dir: suiteDir, Dockerfile: dockerfile})
	}
	return suites, nil
}
