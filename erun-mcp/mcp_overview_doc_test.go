package erunmcp

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

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

var overviewIndexToolPattern = regexp.MustCompile("`([a-z][a-z_-]*)`")

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
