package eruncommon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// displayShapedStateNames are the strings a tenant and an environment take on
// when they are rendered for a human instead of joined into a path: the pair,
// and the pair with the port an operator sees beside it. Directories named this
// way accumulated under the state root, empty, one per pair somebody had seen
// displayed.
var displayShapedStateNames = []string{
	"erun ux",
	"erun build",
	"erun local-ideas",
	"frs local",
	"frs prod 17300",
	"frs local 17700",
	"petios rihards-review",
}

// unusableStatePathNames are the shapes a path builder must never accept: a
// display label with whitespace of any kind, a value that walks out of the
// state tree, and a value that names no directory at all.
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

// TestStatePathsRefuseDisplayShapedTenantAndEnvironmentNames is the invariant
// behind the stray state directories: a tenant or environment is one path
// segment, so a display-shaped string is refused where the path is built rather
// than joined into one -- nothing is resolved, and nothing is created. Asserting
// only "no directory appeared" would pass without exercising the builder; these
// cases call the builders themselves.
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

// refusedStatePathNames is every name the store must turn away: the
// display-shaped ones this invariant is about, plus the shapes that would name
// no directory or walk out of the tree.
func refusedStatePathNames() []string {
	names := make([]string, 0, len(displayShapedStateNames)+len(unusableStatePathNames))
	names = append(names, displayShapedStateNames...)
	return append(names, unusableStatePathNames...)
}

// assertStatePathBuildersRefuseName puts one unusable name through every
// builder, in the tenant slot and in the environment slot, and through the
// writers and readers that resolve the same segments.
func assertStatePathBuildersRefuse(t *testing.T, name string) {
	t.Helper()
	for _, slot := range []struct {
		what  string
		build func() (string, error)
	}{
		{"EnvConfigPath tenant", func() (string, error) { return EnvConfigPath(name, "local") }},
		{"EnvConfigPath environment", func() (string, error) { return EnvConfigPath("frs", name) }},
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
		{"SaveEnvConfig tenant", SaveEnvConfig(name, EnvConfig{Name: "local"})},
		{"SaveEnvConfig environment", SaveEnvConfig("frs", EnvConfig{Name: name})},
		{"SaveTenantConfig", SaveTenantConfig(TenantConfig{Name: name})},
		{"DeleteEnvConfig tenant", DeleteEnvConfig(name, "local")},
		{"DeleteEnvConfig environment", DeleteEnvConfig("frs", name)},
		{"DeleteTenantConfig", DeleteTenantConfig(name)},
	} {
		if write.err == nil {
			t.Errorf("%s accepted %q", write.what, name)
		}
	}

	// A read answers "not configured", the way an environment that was never
	// created does, so listing the tree skips a name it cannot hold instead of
	// failing on it or resolving outside the tree.
	if _, _, err := LoadEnvConfig(name, "local"); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("LoadEnvConfig(%q, ...) = %v, want ErrNotInitialized", name, err)
	}
	if _, _, err := LoadTenantConfig(name); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("LoadTenantConfig(%q) = %v, want ErrNotInitialized", name, err)
	}
}

// TestStatePathsAcceptAnOrdinaryTenantAndEnvironment is the other half of the
// invariant: the refusal is about the shape of a name, not about the store
// having become unwilling to write. An ordinary pair resolves to the nested
// layout, with no component carrying whitespace.
func TestStatePathsAcceptAnOrdinaryTenantAndEnvironment(t *testing.T) {
	redirectConfigHomeForTest(t)
	root := configHomeForTest(t)

	if err := SaveTenantConfig(TenantConfig{Name: "frs"}); err != nil {
		t.Fatalf("SaveTenantConfig(frs): %v", err)
	}
	if err := SaveEnvConfig("frs", EnvConfig{Name: "local"}); err != nil {
		t.Fatalf("SaveEnvConfig(frs, local): %v", err)
	}

	path, err := EnvConfigPath("frs", "local")
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

// TestStatePathSegmentValidatesTheShapeOfAName pins the validator itself, so a
// future caller can rely on what "unusable" means without re-reading the store.
func TestStatePathSegmentValidatesTheShapeOfAName(t *testing.T) {
	for _, value := range []string{"frs", "frs-local", "frs_local", "frs123", "FRS"} {
		if err := validateStatePathSegment("tenant", value); err != nil {
			t.Errorf("validateStatePathSegment(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range append(append([]string{}, displayShapedStateNames...), unusableStatePathNames...) {
		err := validateStatePathSegment("tenant", value)
		if err == nil {
			t.Errorf("validateStatePathSegment(%q) = nil, want a refusal", value)
			continue
		}
		if !errors.Is(err, ErrUnusableStateName) {
			t.Errorf("validateStatePathSegment(%q) = %v, want ErrUnusableStateName", value, err)
		}
	}
	if err := validateStatePathSegment("tenant", ""); !errors.Is(err, ErrUnusableStateName) {
		t.Errorf("validateStatePathSegment(\"\") = %v, want ErrUnusableStateName", err)
	}
}

// TestBootstrapDryRunTracesNoWriteItWouldRefuse keeps the dry-run trace honest:
// a bootstrap dry run reports the mkdir and write-yaml the real run would
// perform, so a name the real save refuses must produce neither. Tracing it
// anyway would show the operator a plan that cannot run -- and, for the shape
// this guards, a path with a space in it.
func TestBootstrapDryRunTracesNoWriteItWouldRefuse(t *testing.T) {
	redirectConfigHomeForTest(t)

	for _, name := range displayShapedStateNames {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			store := tracedBootstrapStore{
				ctx: Context{Logger: NewLoggerWithWriters(VerbosityTrace, &stdout, &stderr), DryRun: true},
			}
			if err := store.SaveEnvConfig("frs", EnvConfig{Name: name}); !errors.Is(err, ErrUnusableStateName) {
				t.Fatalf("SaveEnvConfig environment %q = %v, want ErrUnusableStateName", name, err)
			}
			if err := store.SaveTenantConfig(TenantConfig{Name: name}); !errors.Is(err, ErrUnusableStateName) {
				t.Fatalf("SaveTenantConfig %q = %v, want ErrUnusableStateName", name, err)
			}
			traced := stdout.String() + stderr.String()
			if strings.Contains(traced, "mkdir") || strings.Contains(traced, "write-yaml") {
				t.Fatalf("a dry run traced a write the real run would refuse:\n%s", traced)
			}
		})
	}
}

// TestSaveTenantConfigNormalizesWhitespaceAroundANameThatIsOtherwiseUsable
// records the one shape that is normalized rather than refused: the tenant
// store has always trimmed the name it writes, and it writes that same trimmed
// name, so the directory and the recorded name still agree.
func TestSaveTenantConfigNormalizesWhitespaceAroundANameThatIsOtherwiseUsable(t *testing.T) {
	redirectConfigHomeForTest(t)
	root := configHomeForTest(t)

	if err := SaveTenantConfig(TenantConfig{Name: " frs "}); err != nil {
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

// assertNoStatePathComponentCarriesWhitespace walks the state tree and fails on
// any component with whitespace in it, whatever created it. A directory that
// merely came to exist is the residue being guarded against, so this checks
// directories as well as files.
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
