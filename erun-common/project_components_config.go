package eruncommon

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// resolveProjectComponent selects a components: entry for a caller that must
// know exactly which root it resolved (build, push) rather than falling back
// to a whole-project walk. An empty requested name auto-selects the lone
// entry when exactly one is declared; an explicit unknown name, or an empty
// requested name with more than one entry declared, fails naming the choices
// rather than silently resolving one. A project declaring no components: map
// returns ("", zero-value, false, nil) so every existing single-service
// .erun/config.yaml keeps resolving through the unchanged project-global
// paths: block.
func resolveProjectComponent(projectRoot, requested string) (string, ProjectPathsConfig, bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", ProjectPathsConfig{}, false, nil
	}
	config, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return "", ProjectPathsConfig{}, false, nil
		}
		return "", ProjectPathsConfig{}, false, err
	}

	requested = strings.TrimSpace(requested)
	if requested != "" {
		paths, ok := config.Components[requested]
		if !ok {
			return "", ProjectPathsConfig{}, false, fmt.Errorf("component %q not declared in .erun/config.yaml components (declared: %s)", requested, componentNamesOrNone(config.Components))
		}
		return requested, paths, true, nil
	}

	switch len(config.Components) {
	case 0:
		return "", ProjectPathsConfig{}, false, nil
	case 1:
		for name, paths := range config.Components {
			return name, paths, true, nil
		}
	}
	return "", ProjectPathsConfig{}, false, fmt.Errorf("ambiguous component selection: .erun/config.yaml declares %d components (%s); pass --component <name>", len(config.Components), componentNamesOrNone(config.Components))
}

// declaredComponentPaths looks up a specific components: entry by name, with
// no selection logic — a plain map lookup for callers that already have a
// component name (from --components or the environments.<env>.k8s.deployments
// plan) and fall back to their existing whole-project discovery when the name
// is not declared, so a project with no components: map (or one that names
// only a subset) keeps resolving exactly as before this schema existed.
func declaredComponentPaths(projectRoot, componentName string) (ProjectPathsConfig, bool, error) {
	componentName = strings.TrimSpace(componentName)
	if strings.TrimSpace(projectRoot) == "" || componentName == "" {
		return ProjectPathsConfig{}, false, nil
	}
	config, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return ProjectPathsConfig{}, false, nil
		}
		return ProjectPathsConfig{}, false, err
	}
	paths, ok := config.Components[componentName]
	return paths, ok, nil
}

func componentNamesOrNone(components map[string]ProjectPathsConfig) string {
	if len(components) == 0 {
		return "none"
	}
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
