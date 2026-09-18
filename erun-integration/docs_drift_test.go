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

	eruncommon "github.com/sophium/erun/erun-common"
)

// gateRunSignatureDocStopwords are filler and verb words stripped before
// checking that a known infrastructure-failure signature is reflected in the
// docs. erun-docs/docs/agent-reference/skills-spec.md paraphrases each
// signature in flowing prose rather than quoting it verbatim -- e.g. the code
// string "failed to fetch oauth token" appears in the doc as "a failed oauth
// token fetch" -- so this test checks for a signature's distinctive nouns
// rather than an exact substring, which a verbatim match would reject.
var gateRunSignatureDocStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "for": true,
	"on": true, "in": true, "failed": true, "fetch": true, "fetching": true,
	"resolve": true, "resolving": true, "resolution": true,
}

var docDriftWordPattern = regexp.MustCompile(`[a-z0-9]+`)

// normalizedWordSet lowercases text and splits it into a set of alphanumeric
// words, so hyphens, punctuation, and markdown formatting around a word never
// stop it from matching.
func normalizedWordSet(text string) map[string]bool {
	words := docDriftWordPattern.FindAllString(strings.ToLower(text), -1)
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// significantWords returns signature's words with the stopwords in
// gateRunSignatureDocStopwords removed, leaving the nouns a paraphrase cannot
// drop without losing the signature's meaning.
func significantWords(signature string) []string {
	words := docDriftWordPattern.FindAllString(strings.ToLower(signature), -1)
	var kept []string
	for _, w := range words {
		if !gateRunSignatureDocStopwords[w] {
			kept = append(kept, w)
		}
	}
	return kept
}

// TestGateRunInfrastructureSignaturesAreDocumented cross-checks erun-common's
// own known-infrastructure-failure signature list
// (eruncommon.GateRunInconclusiveSignatures, the exact strings
// RunGateRunStart/RunGateRunReport match before silently reclassifying a
// caller-reported FAILED verdict to INCONCLUSIVE) against
// erun-docs/docs/agent-reference/skills-spec.md, the one page that specs the
// classifier's exact trigger condition end to end (a doc-drift sweep found
// three *other* pages -- cli/exec.md, cli/review.md, mcp/overview.md -- that
// described the surrounding FAILED-report behavior with no mention of this
// reclassification at all, which this test cannot see: it only knows how to
// compare a code-owned list against one designated spec page's prose, not to
// judge whether every page that touches the topic says enough about it).
// What it does catch: the classifier's own trigger
// condition -- which failures get silently reclassified -- silently drifting
// from what the canonical spec page says it is, in either direction (a
// signature added to the code with no corresponding update to the doc, or a
// signature whose wording changed enough that the doc's description no
// longer matches).
// configLocationsGoOSFallbacks maps each platform the config-locations page
// documents to the default base directory ERun's config root sits in when
// XDG_CONFIG_HOME is unset.
//
// The values are the ones github.com/adrg/xdg applies per GOOS -- the library
// erun-common reads ConfigHome from -- not what os.UserConfigDir happens to
// return: paths_darwin.go falls back to ~/Library/Application Support,
// paths_unix.go to ~/.config, and paths_windows.go to %LOCALAPPDATA% (the
// per-platform table this page carried previously said %AppData%, which is
// what os.UserConfigDir uses on Windows and is not where ERun looks).
//
// Only the entry for the running GOOS can be exercised live: GOOS is fixed at
// process start and xdg computes ConfigHome once in package init, so a Linux
// gate cannot execute the darwin rule and a darwin host cannot execute the
// windows one. The doc row for the host platform is checked against the live
// resolver by TestConfigLocationsDocMatchesResolver; the other rows are
// checked against this table, and are re-checked live when the suite runs on
// their platform.
var configLocationsGoOSFallbacks = map[string]string{
	"darwin":  "~/Library/Application Support/erun",
	"linux":   "~/.config/erun",
	"windows": `%LOCALAPPDATA%\erun`,
}

// resolverConfigPaths returns the three per-user config file paths erun-common
// resolves for the global config, one tenant config, and one env config. It
// uses the exported resolvers themselves rather than rebuilding the paths, so
// a rename in erun-common is what the documentation is compared against.
func resolverConfigPaths(t *testing.T) (globalPath, tenantPath, envPath string) {
	t.Helper()
	_, globalPath, _ = eruncommon.LoadERunConfig()
	_, tenantPath, _ = eruncommon.LoadTenantConfig("my-tenant")
	_, envPath, _ = eruncommon.LoadEnvConfig("my-tenant", "local")
	if globalPath == "" || tenantPath == "" || envPath == "" {
		t.Fatal("erun-common resolved an empty per-user config path; the rest of this test would pass vacuously")
	}
	return globalPath, tenantPath, envPath
}

// docTreeEntries returns the file and directory names a fenced config-tree
// block lists, taken from the block that documents the per-user tree.
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

// hostConfigBase returns the base directory the running host resolves its
// config root under, following the XDG rule erun-common's ConfigHome uses: an
// absolute XDG_CONFIG_HOME wins, otherwise the GOOS default.
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

// TestConfigLocationsDocMatchesResolver cross-checks the per-user config tree
// documented in erun-docs/docs/reference/config-locations.md against the paths
// erun-common actually resolves. The page previously placed the tree at
// "~/.config/erun on Linux/macOS" and named the tenant config tenant.yaml;
// both were wrong, and both are the kind of detail that rots silently because
// nothing compared the prose to the resolver that produces it.
//
// What this catches: the tree's file and directory names drifting from the
// resolver (a renamed config file, a tenant config under a different name),
// and a platform row drifting from the root ERun resolves on the platform
// this test runs on. What it cannot catch: the rows for non-host platforms,
// which no single machine can execute -- see configLocationsGoOSFallbacks.
func TestConfigLocationsDocMatchesResolver(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "reference", "config-locations.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(data)

	globalPath, tenantPath, envPath := resolverConfigPaths(t)

	configRoot, err := eruncommon.ERunConfigDir()
	if err != nil {
		t.Fatalf("erun-common could not resolve its config root: %v", err)
	}
	if got := filepath.Dir(globalPath); got != configRoot {
		t.Fatalf("the global config %s is not directly under the resolved config root %s", globalPath, configRoot)
	}

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
	// The specific regression this page carried: a tenant config under a name
	// erun never reads. Checked by name as well as through the resolver above,
	// so the failure reads as the historical bug rather than a generic miss.
	if present["tenant.yaml"] {
		t.Errorf("%s: the per-user tree names the tenant config tenant.yaml, but erun-common resolves %s", docPath, tenantPath)
	}

	// The host platform's row must match the root this process resolves.
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
	// The wording this page rotted into: one Linux-shaped path claimed for
	// Linux and macOS together.
	if strings.Contains(doc, "~/.config/erun on Linux/macOS") {
		t.Errorf("%s: still claims ~/.config/erun for Linux and macOS together; macOS resolves %s", docPath, configLocationsGoOSFallbacks["darwin"])
	}
}

func TestGateRunInfrastructureSignaturesAreDocumented(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "agent-reference", "skills-spec.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	docWords := normalizedWordSet(string(data))

	signatures := eruncommon.GateRunInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("eruncommon.GateRunInconclusiveSignatures() returned nothing; the rest of this test would pass vacuously")
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
			"update the doc's description of gateRunInconclusiveSignatures (erun-common/gate_run_failure_classifier.go)",
			docPath, m)
	}
}

// docDriftEnvNamePattern extracts ERUN_* variable names from a shell script, so
// a variable the script reads can be checked against the page that documents it
// without a hand-kept list in between.
var docDriftEnvNamePattern = regexp.MustCompile(`ERUN_[A-Z0-9_]+`)

// TestOrchestratorSessionEnvVarsAreDocumented cross-checks the ERUN_* names a
// host-side orchestrator session receives against
// erun-docs/docs/reference/env-vars.md, the one page that enumerates what erun
// reads out of the environment.
//
// That page was hand-maintained and covered only the environment/pod-scoped
// names. A host-side orchestrator session has no pod, so it found every
// variable on the page and none of its own -- including ERUN_ORCHESTRATOR_ID,
// which the shared orchestrator contract makes the session's identity: scope is
// resolved by matching it against the orchestrators: list in config.yaml, and
// RESUME-NOTE.<id>.md is named from it. An undocumented identity variable is
// one an orchestrator cannot look up when it needs to know what it is, and
// nothing but prose said the page had to list it.
//
// Two sources, because the names have two owners. The launcher-set list is
// code-owned: eruncommon.OrchestratorSessionEnvVars is what erun-ui's spawn site
// reads, so a variable added to a session without being documented fails here
// instead of shipping. ERUN_DEV_BIN_DIR belongs to the development wrapper
// erun-cli/run.sh, which no Go list can see, so it is read from the script.
func TestOrchestratorSessionEnvVarsAreDocumented(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "reference", "env-vars.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(data)

	// ERUN_OUTPUTS_DIR is already in the in-pod table, so a name being present
	// somewhere on the page does not by itself prove the session's own section
	// exists. Without this the section could be deleted and the names could
	// still pass by matching the pod's table.
	const heading = "## Orchestrator-session variables"
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s: missing the %q section; a host-side orchestrator session has no pod, "+
			"so the pod-scoped table cannot describe it", docPath, heading)
	}
	// Only this section counts. ERUN_OUTPUTS_DIR is already a row in the in-pod
	// table and ERUN_ORCHESTRATOR_ID is named in the surrounding prose, so
	// searching the whole page would pass for a name whose session-table row had
	// been deleted -- the entry a reader needs is the one in this section.
	section := doc[start:]
	if next := strings.Index(section[len(heading):], "\n## "); next >= 0 {
		section = section[:len(heading)+next]
	}

	names := eruncommon.OrchestratorSessionEnvVars()
	if len(names) == 0 {
		t.Fatal("eruncommon.OrchestratorSessionEnvVars() returned nothing; the rest of this test would pass vacuously")
	}

	// The script is read unconditionally, before any name is judged, so a moved
	// or renamed script fails loudly rather than silently dropping the one name
	// it owns from the check.
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
	names = append(names, fromScript...)

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

// documentedAsTableRow reports whether name appears in a markdown table row of
// section, formatted as inline code. The section's prose also names these
// variables -- ERUN_ORCHESTRATOR_ID is discussed in a sentence below the table --
// so a plain substring search would pass for a variable whose row had been
// deleted, which is the one thing this test exists to catch.
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
