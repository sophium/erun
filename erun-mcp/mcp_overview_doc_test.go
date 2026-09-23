package erunmcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// repoRootForOverviewDocTest returns the repo root. erun-mcp sits directly
// under the root, so the grandparent of this file is the root regardless of
// the test's working directory.
func repoRootForOverviewDocTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

var overviewIndexToolPattern = regexp.MustCompile("`([a-z][a-z0-9_-]*)`")

// fullToolIndexBody returns the lines between the "### Full tool index"
// heading and the next heading of any level, exclusive of both.
func fullToolIndexBody(t *testing.T, docPath string, lines []string) []string {
	t.Helper()
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "### Full tool index") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: no \"### Full tool index\" heading found", docPath)
	}

	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			end = i
			break
		}
	}
	return lines[start:end]
}

// toolCellName extracts the backtick-quoted tool name from a table row's Tool
// column (the row's second cell), or "" for a header/separator row.
func toolCellName(t *testing.T, row string) string {
	t.Helper()
	cells := strings.Split(row, "|")
	// A row is "| Family | Tool | CLI equivalent | Read/Work |", which splits
	// into ["", " Family ", " Tool ", " CLI equivalent ", " Read/Work ", ""].
	if len(cells) < 3 {
		return ""
	}
	toolCell := strings.TrimSpace(cells[2])
	if toolCell == "Tool" || strings.HasPrefix(toolCell, "---") {
		return ""
	}
	match := overviewIndexToolPattern.FindStringSubmatch(toolCell)
	if match == nil {
		t.Fatalf("table row %q has no backtick-quoted tool name in its Tool column", row)
	}
	return match[1]
}

// overviewFullToolIndexNames extracts the Tool column of the "Full tool
// index" table in erun-docs/docs/mcp/overview.md: every table row's second
// backtick-quoted cell, from the "### Full tool index" heading to the next
// heading of any level.
func overviewFullToolIndexNames(t *testing.T, docPath string) []string {
	t.Helper()
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}

	var names []string
	for _, line := range fullToolIndexBody(t, docPath, strings.Split(string(data), "\n")) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		if name := toolCellName(t, trimmed); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// toolNameSet reports duplicates via t.Errorf as it builds the set, so a
// caller gets one pass over the list.
func toolNameSet(t *testing.T, docPath string, names []string) map[string]bool {
	t.Helper()
	set := map[string]bool{}
	for _, name := range names {
		if set[name] {
			t.Errorf("%s: %q listed more than once in the Full tool index", docPath, name)
		}
		set[name] = true
	}
	return set
}

// reportSetDifference emits one t.Errorf per name in `from` that is absent
// from `in`, formatted with the given verb.
func reportSetDifference(t *testing.T, from []string, in map[string]bool, format string) {
	t.Helper()
	var diff []string
	for _, name := range from {
		if !in[name] {
			diff = append(diff, name)
		}
	}
	sort.Strings(diff)
	for _, name := range diff {
		t.Errorf(format, name)
	}
}

// TestMCPOverviewDocumentsEveryTool is the scripted check #1246 asks for: the
// "Full tool index" table in erun-docs/docs/mcp/overview.md must name exactly
// the tools erun-common's MCPToolDescriptor table registers -- no fewer (an
// undocumented tool), no more (a phantom entry like the `logs`/`open` rows
// that drifted onto the page after both tools were removed from the
// registry).
func TestMCPOverviewDocumentsEveryTool(t *testing.T) {
	root := repoRootForOverviewDocTest(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "mcp", "overview.md")

	documented := overviewFullToolIndexNames(t, docPath)
	if len(documented) == 0 {
		t.Fatal("no tool rows parsed from the Full tool index; the rest of this test would pass vacuously")
	}
	documentedSet := toolNameSet(t, docPath, documented)
	registered := eruncommon.MCPToolNames()
	registeredSet := toolNameSet(t, docPath, registered)

	reportSetDifference(t, registered, documentedSet, docPath+": registered tool %q is missing from the Full tool index")
	reportSetDifference(t, documented, registeredSet, docPath+": Full tool index names %q, which is not a registered tool")
}

// environmentExampleBody returns the JSON body of the first fenced code block
// inside the "### `environment`" section of the MCP overview doc -- the
// documented output of the environment read model tool.
func environmentExampleBody(t *testing.T, docPath string, lines []string) []string {
	t.Helper()
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "### `environment`") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: no \"### `environment`\" heading found", docPath)
	}

	fence := -1
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		if strings.HasPrefix(trimmed, "```") {
			fence = i
			break
		}
	}
	if fence < 0 {
		t.Fatalf("%s: the environment section carries no fenced example block", docPath)
	}

	for i := fence + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			return lines[fence+1 : i]
		}
	}
	t.Fatalf("%s: the environment section's fenced example block is never closed", docPath)
	return nil
}

// jsonValueKind names the JSON shape a hand-written DTO layer has to declare
// for a decoded value, which is what makes a documented example comparable to
// the real encoder's output without requiring the example's example *values*
// to match.
func jsonValueKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// compareDocumentedJSONPaths walks a documented example and reports every
// path it names that the real encoder does not emit under the same name and
// shape. It descends only into objects and ignores everything else: the
// example is an excerpt, so paths it omits are fine, while a path it *shows*
// must be one a client reading the real output can actually rely on.
func compareDocumentedJSONPaths(t *testing.T, docPath, path string, documented, actual any) {
	t.Helper()
	documentedObject, ok := documented.(map[string]any)
	if !ok {
		return
	}
	actualObject, _ := actual.(map[string]any)
	for _, key := range sortedJSONKeys(documentedObject) {
		real, present := actualObject[key]
		if !present {
			t.Errorf("%s: the environment example names %q, which the resolved model never emits; the example is the contract a hand-written client reads these names from", docPath, path+"."+key)
			continue
		}
		if documentedKind, actualKind := jsonValueKind(documentedObject[key]), jsonValueKind(real); documentedKind != actualKind {
			t.Errorf("%s: the environment example types %q as %s, but the resolved model emits %s", docPath, path+"."+key, documentedKind, actualKind)
			continue
		}
		compareDocumentedJSONPaths(t, docPath, path+"."+key, documentedObject[key], real)
	}
}

func sortedJSONKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestMCPOverviewEnvironmentExampleMatchesTheResolvedJSON pins the `environment`
// tool's documented example to what erun-common's resolved read model actually
// marshals to. The example is a public JSON contract, not prose: a native
// client that cannot import erun-common hand-writes its DTOs from it, so a
// field named there that the encoder does not emit decodes to a silent zero
// rather than an error -- the failure this check exists to make loud. It
// caught two live drifts: `health.deploy.helmStatus`, which the untagged
// DeployDiagnosisResult actually emits as `HelmStatus`, and
// `idle.policy.timeout`, documented as the string "5m0s" where the encoder
// emits a time.Duration's nanoseconds as a number.
//
// Only paths the example shows are compared, and only by name and JSON shape,
// so the example stays free to illustrate with its own values.
func TestMCPOverviewEnvironmentExampleMatchesTheResolvedJSON(t *testing.T) {
	root := repoRootForOverviewDocTest(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "mcp", "overview.md")

	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	example := strings.Join(environmentExampleBody(t, docPath, strings.Split(string(data), "\n")), "\n")

	var documented map[string]any
	if err := json.Unmarshal([]byte(example), &documented); err != nil {
		t.Fatalf("%s: the environment example is not valid JSON: %v", docPath, err)
	}

	// Every field the example names is populated here, including the ones the
	// model omits when zero: an omitempty field left at its zero value would
	// otherwise be reported below as unemitted, and this fixture is the answer
	// to that report for anyone extending the example.
	policy := eruncommon.EnvironmentIdlePolicy{Timeout: 5 * time.Minute, WorkingHours: "09:00-19:00"}
	fixture := eruncommon.EnvironmentReadModel{
		Tenant: "myapp",
		Environment: eruncommon.ListEnvironmentResult{
			Name:           "local",
			Type:           eruncommon.EnvironmentTypeLocalAgent,
			RuntimeVersion: "1.0.308",
			ManagedCloud:   true,
			IsDefault:      true,
			IsEffective:    true,
		},
		State: eruncommon.EnvironmentLifecycleRunning,
		Idle:  &eruncommon.EnvironmentIdleStatus{Policy: policy, StopEligible: true},
		Health: &eruncommon.EnvironmentHealth{
			RootConfig: eruncommon.RootConfigInspection{ConfigStatus: eruncommon.RootConfigStatusOK},
			Deploy:     eruncommon.DeployDiagnosisResult{HelmStatus: "STATUS: deployed"},
		},
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal the resolved environment read model: %v", err)
	}
	var resolved any
	if err := json.Unmarshal(encoded, &resolved); err != nil {
		t.Fatalf("decode the resolved environment read model: %v", err)
	}

	compareDocumentedJSONPaths(t, docPath, "environment", documented, resolved)
}
