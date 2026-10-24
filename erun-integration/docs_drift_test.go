package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/primitive/build/classify"
)

// buildSignatureDocStopwords are stripped before matching: the docs paraphrase
// each signature, so only its distinctive nouns are checked.
var buildSignatureDocStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "for": true,
	"on": true, "in": true, "failed": true, "fetch": true, "fetching": true,
	"resolve": true, "resolving": true, "resolution": true,
}

var docDriftWordPattern = regexp.MustCompile(`[a-z0-9]+`)

func normalizedWordSet(text string) map[string]bool {
	words := docDriftWordPattern.FindAllString(strings.ToLower(text), -1)
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

func significantWords(signature string) []string {
	words := docDriftWordPattern.FindAllString(strings.ToLower(signature), -1)
	var kept []string
	for _, w := range words {
		if !buildSignatureDocStopwords[w] {
			kept = append(kept, w)
		}
	}
	return kept
}

// configLocationsGoOSFallbacks maps each documented platform to github.com/adrg/xdg's
// default config base (not os.UserConfigDir's). Only the host GOOS row can be
// checked live; xdg resolves once at init.
var configLocationsGoOSFallbacks = map[string]string{
	"darwin":  "~/Library/Application Support/erun",
	"linux":   "~/.config/erun",
	"windows": `%LOCALAPPDATA%\erun`,
}

// resolverConfigPaths returns the global, tenant, and env config paths from
// erun-common's own resolvers.
func resolverConfigPaths(t *testing.T) (globalPath, tenantPath, envPath string) {
	t.Helper()
	_, globalPath, _ = config.LoadERunConfig()
	_, tenantPath, _ = config.LoadTenantConfig("my-tenant")
	_, envPath, _ = config.LoadEnvConfig("my-tenant", "local")
	if globalPath == "" || tenantPath == "" || envPath == "" {
		t.Fatal("erun-common resolved an empty per-user config path; the rest of this test would pass vacuously")
	}
	return globalPath, tenantPath, envPath
}

func docTreeEntries(t *testing.T, docPath, doc string) []string {
	t.Helper()
	var blocks []string
	for i, part := range strings.Split(doc, "```") {
		if i%2 == 1 {
			blocks = append(blocks, part)
		}
	}
	treeEntryPattern := regexp.MustCompile(`(?m)^[\s│]*[├└]──\s+(\S+)`)
	for _, block := range blocks {
		if !strings.Contains(block, "<config-root>") {
			continue
		}
		var entries []string
		for _, match := range treeEntryPattern.FindAllStringSubmatch(block, -1) {
			entries = append(entries, match[1])
		}
		if len(entries) == 0 {
			t.Fatalf("%s: the per-user tree block lists no entries; the parser or the tree changed shape", docPath)
		}
		return entries
	}
	t.Fatalf("%s: no fenced tree block documents the per-user config root", docPath)
	return nil
}

// hostConfigBase returns an absolute XDG_CONFIG_HOME, else the GOOS default.
func hostConfigBase(t *testing.T) string {
	t.Helper()
	if xdgHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdgHome != "" && filepath.IsAbs(xdgHome) {
		return xdgHome
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home dir: %v", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData == "" {
			t.Fatalf("LOCALAPPDATA is unset; cannot establish the config base this host resolves")
		}
		return localAppData
	default:
		return filepath.Join(home, ".config")
	}
}

// TestConfigLocationsDocMatchesResolver checks config-locations.md against the
// paths erun-common resolves; non-host rows are covered by configLocationsGoOSFallbacks.
func TestConfigLocationsDocMatchesResolver(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "reference", "config-locations.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(data)

	globalPath, tenantPath, envPath := resolverConfigPaths(t)

	configRoot, err := config.ERunConfigDir()
	if err != nil {
		t.Fatalf("erun-common could not resolve its config root: %v", err)
	}
	if got := filepath.Dir(globalPath); got != configRoot {
		t.Fatalf("the global config %s is not directly under the resolved config root %s", globalPath, configRoot)
	}

	assertConfigTreeMatchesResolver(t, docPath, doc, globalPath, tenantPath, envPath)

	if _, ok := configLocationsGoOSFallbacks[runtime.GOOS]; !ok {
		t.Fatalf("no documented config-locations row for GOOS %q; add one here and to %s", runtime.GOOS, docPath)
	}
	if got, want := filepath.Dir(configRoot), hostConfigBase(t); got != want {
		t.Errorf("erun-common resolved its config root under %s, but the %s rule this test encodes expects %s -- the test table and the resolver disagree",
			got, runtime.GOOS, want)
	}
	for goos, fallback := range configLocationsGoOSFallbacks {
		if !strings.Contains(doc, fallback) {
			t.Errorf("%s: no row documents the %s default %s", docPath, goos, fallback)
		}
	}
	if strings.Contains(doc, "~/.config/erun on Linux/macOS") {
		t.Errorf("%s: still claims ~/.config/erun for Linux and macOS together; macOS resolves %s", docPath, configLocationsGoOSFallbacks["darwin"])
	}
}

func assertConfigTreeMatchesResolver(t *testing.T, docPath, doc, globalPath, tenantPath, envPath string) {
	t.Helper()
	entries := docTreeEntries(t, docPath, doc)
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		present[entry] = true
	}
	for _, resolved := range []string{globalPath, tenantPath, envPath} {
		name := filepath.Base(resolved)
		if !present[name] {
			t.Errorf("%s: the per-user tree does not list %q, the name erun-common resolves for %s -- the tree and the resolver disagree",
				docPath, name, resolved)
		}
	}
	for _, dir := range []string{"<tenant>/", "<environment>/"} {
		if !present[dir] {
			t.Errorf("%s: the per-user tree no longer nests %s, but erun-common resolves %s", docPath, dir, tenantPath)
		}
	}
	// Checked by name so the failure names this specific mistake.
	if present["tenant.yaml"] {
		t.Errorf("%s: the per-user tree names the tenant config tenant.yaml, but erun-common resolves %s", docPath, tenantPath)
	}
}

// TestBuildInfrastructureSignaturesAreDocumented checks
// build.BuildInconclusiveSignatures against skills-spec.md in both directions.
func TestBuildInfrastructureSignaturesAreDocumented(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "agent-reference", "skills-spec.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	docWords := normalizedWordSet(string(data))

	signatures := classify.BuildInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("eruncommon.BuildInconclusiveSignatures() returned nothing; the rest of this test would pass vacuously")
	}

	var missing []string
	for _, sig := range signatures {
		for _, word := range significantWords(sig) {
			if !docWords[word] {
				missing = append(missing, fmt.Sprintf("%q (missing word %q)", sig, word))
				break
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s: known infrastructure-failure signature %s is not reflected in the classifier's spec -- "+
			"update the doc's description of BuildInconclusiveSignatures (erun-common/build_failure_classifier.go)",
			docPath, m)
	}
}

var docDriftEnvNamePattern = regexp.MustCompile(`ERUN_[A-Z0-9_]+`)

// TestOrchestratorSessionEnvVarsAreDocumented checks the ERUN_* names an
// orchestrator session receives against env-vars.md. ERUN_DEV_BIN_DIR is owned
// by erun-cli/run.sh, so it is read from the script.
func TestOrchestratorSessionEnvVarsAreDocumented(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "reference", "env-vars.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(data)

	// ERUN_OUTPUTS_DIR is also in the in-pod table, so the section itself must exist.
	const heading = "## Orchestrator-session variables"
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s: missing the %q section; a host-side orchestrator session has no pod, "+
			"so the pod-scoped table cannot describe it", docPath, heading)
	}
	section := doc[start:]
	if next := strings.Index(section[len(heading):], "\n## "); next >= 0 {
		section = section[:len(heading)+next]
	}

	names := orchestrator.OrchestratorSessionEnvVars()
	if len(names) == 0 {
		t.Fatal("eruncommon.OrchestratorSessionEnvVars() returned nothing; the rest of this test would pass vacuously")
	}

	// Read unconditionally so a moved script fails loudly.
	names = append(names, devWrapperEnvNames(t, root)...)

	var missing []string
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if !documentedAsTableRow(section, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("%s: %s is set on a host-side orchestrator session but has no row in the %q table -- "+
			"add it there (erun-common/orchestrator_session_env.go)", docPath, name, heading)
	}
}

func devWrapperEnvNames(t *testing.T, root string) []string {
	t.Helper()
	scriptPath := filepath.Join(root, "erun-cli", "run.sh")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}
	fromScript := docDriftEnvNamePattern.FindAllString(string(script), -1)
	if len(fromScript) == 0 {
		t.Fatalf("%s references no ERUN_* variable; the development wrapper this test reads for "+
			"ERUN_DEV_BIN_DIR has moved or stopped owning one", scriptPath)
	}
	return fromScript
}

// documentedAsTableRow reports whether name is an inline-code table row in
// section; the prose also names these variables.
func documentedAsTableRow(section, name string) bool {
	quoted := "`" + name + "`"
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "|") && strings.Contains(line, quoted) {
			return true
		}
	}
	return false
}
