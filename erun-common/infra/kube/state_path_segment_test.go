package kube

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// displayShapedStateNames are tenant/environment pairs as rendered for humans.
var displayShapedStateNames = []string{
	"erun ux",
	"erun build",
	"erun local-ideas",
	"frs local",
	"frs prod 17300",
	"frs local 17700",
	"petios rihards-review",
}

var unusableStatePathNames = []string{
	"frs\tlocal",
	"frs\nlocal",
	"../frs",
	"../..",
	"frs/local",
	`frs\local`,
	".",
	"..",
	"a\x00b",
}

// Call the builders directly; "no directory appeared" alone would pass without exercising them.
func TestStatePathsRefuseDisplayShapedTenantAndEnvironmentNames(t *testing.T) {
	redirectConfigHomeForTest(t)
	root := configHomeForTest(t)

	for _, name := range refusedStatePathNames() {
		t.Run(name, func(t *testing.T) {
			assertStatePathBuildersRefuse(t, name)
			assertNoStatePathComponentCarriesWhitespace(t, root)
		})
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read config root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("refused names still left %d entries under the config root: %v", len(entries), entries)
	}
}

func refusedStatePathNames() []string {
	names := make([]string, 0, len(displayShapedStateNames)+len(unusableStatePathNames))
	names = append(names, displayShapedStateNames...)
	return append(names, unusableStatePathNames...)
}

func assertStatePathBuildersRefuse(t *testing.T, name string) {
	t.Helper()
	for _, slot := range []struct {
		what  string
		build func() (string, error)
	}{
		{"EnvConfigPath tenant", func() (string, error) { return config.EnvConfigPath(name, "local") }},
		{"EnvConfigPath environment", func() (string, error) { return config.EnvConfigPath("frs", name) }},
		{"PortForwardStatePath tenant", func() (string, error) { return PortForwardStatePath("mcp", name, "local") }},
		{"PortForwardStatePath environment", func() (string, error) { return PortForwardStatePath("mcp", "frs", name) }},
	} {
		if path, err := slot.build(); err == nil {
			t.Errorf("%s built %s for %q, which is not a path segment", slot.what, path, name)
		}
	}

	for _, write := range []struct {
		what string
		err  error
	}{
		{"SaveEnvConfig tenant", config.SaveEnvConfig(name, config.EnvConfig{Name: "local"})},
		{"SaveEnvConfig environment", config.SaveEnvConfig("frs", config.EnvConfig{Name: name})},
		{"SaveTenantConfig", config.SaveTenantConfig(config.TenantConfig{Name: name})},
		{"DeleteEnvConfig tenant", config.DeleteEnvConfig(name, "local")},
		{"DeleteEnvConfig environment", config.DeleteEnvConfig("frs", name)},
		{"DeleteTenantConfig", config.DeleteTenantConfig(name)},
	} {
		if write.err == nil {
			t.Errorf("%s accepted %q", write.what, name)
		}
	}

	// A read reports not configured, so listing skips an unusable name.
	if _, _, err := config.LoadEnvConfig(name, "local"); !errors.Is(err, config.ErrNotInitialized) {
		t.Errorf("LoadEnvConfig(%q, ...) = %v, want ErrNotInitialized", name, err)
	}
	if _, _, err := config.LoadTenantConfig(name); !errors.Is(err, config.ErrNotInitialized) {
		t.Errorf("LoadTenantConfig(%q) = %v, want ErrNotInitialized", name, err)
	}
}

func TestStatePathsAcceptAnOrdinaryTenantAndEnvironment(t *testing.T) {
	redirectConfigHomeForTest(t)
	root := configHomeForTest(t)

	if err := config.SaveTenantConfig(config.TenantConfig{Name: "frs"}); err != nil {
		t.Fatalf("SaveTenantConfig(frs): %v", err)
	}
	if err := config.SaveEnvConfig("frs", config.EnvConfig{Name: "local"}); err != nil {
		t.Fatalf("SaveEnvConfig(frs, local): %v", err)
	}

	path, err := config.EnvConfigPath("frs", "local")
	if err != nil {
		t.Fatalf("EnvConfigPath(frs, local): %v", err)
	}
	if want := filepath.Join(root, "erun", "frs", "local", "config.yaml"); path != want {
		t.Fatalf("EnvConfigPath = %s, want %s", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	assertNoStatePathComponentCarriesWhitespace(t, root)
}

func TestStatePathSegmentValidatesTheShapeOfAName(t *testing.T) {
	for _, value := range []string{"frs", "frs-local", "frs_local", "frs123", "FRS"} {
		if err := config.ValidateStatePathSegment("tenant", value); err != nil {
			t.Errorf("validateStatePathSegment(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range append(append([]string{}, displayShapedStateNames...), unusableStatePathNames...) {
		err := config.ValidateStatePathSegment("tenant", value)
		if err == nil {
			t.Errorf("validateStatePathSegment(%q) = nil, want a refusal", value)
			continue
		}
		if !errors.Is(err, config.ErrUnusableStateName) {
			t.Errorf("validateStatePathSegment(%q) = %v, want ErrUnusableStateName", value, err)
		}
	}
	if err := config.ValidateStatePathSegment("tenant", ""); !errors.Is(err, config.ErrUnusableStateName) {
		t.Errorf("validateStatePathSegment(\"\") = %v, want ErrUnusableStateName", err)
	}
}

// The tenant store trims surrounding whitespace rather than refusing the name.
func TestSaveTenantConfigNormalizesWhitespaceAroundANameThatIsOtherwiseUsable(t *testing.T) {
	redirectConfigHomeForTest(t)
	root := configHomeForTest(t)

	if err := config.SaveTenantConfig(config.TenantConfig{Name: " frs "}); err != nil {
		t.Fatalf("SaveTenantConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "erun", "frs", "config.yaml")); err != nil {
		t.Fatalf("stat trimmed tenant config: %v", err)
	}
	assertNoStatePathComponentCarriesWhitespace(t, root)
}

func configHomeForTest(t *testing.T) string {
	t.Helper()
	root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if root == "" {
		t.Fatal("XDG_CONFIG_HOME is unset; call redirectConfigHomeForTest first")
	}
	return root
}

// assertNoStatePathComponentCarriesWhitespace checks directories too, since empty ones are the residue.
func assertNoStatePathComponentCarriesWhitespace(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.ContainsAny(component, " \t\n\r\v\f") {
				t.Errorf("%s carries whitespace in a path component, so the state tree holds a display-shaped name", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
