package erunmcp

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/infra/mcptools"
)

func repoRootForOverviewDocTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

var overviewIndexToolPattern = regexp.MustCompile("`([a-z][a-z0-9_-]*)`")

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

// toolCellName returns the backtick-quoted name in a row's Tool cell, or "".
func toolCellName(t *testing.T, row string) string {
	t.Helper()
	cells := strings.Split(row, "|")
	// "| Family | Tool | ... |" splits with an empty first cell.
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

func TestMCPOverviewDocumentsEveryTool(t *testing.T) {
	root := repoRootForOverviewDocTest(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "mcp", "overview.md")

	documented := overviewFullToolIndexNames(t, docPath)
	if len(documented) == 0 {
		t.Fatal("no tool rows parsed from the Full tool index; the rest of this test would pass vacuously")
	}
	documentedSet := toolNameSet(t, docPath, documented)
	registered := mcptools.MCPToolNames()
	registeredSet := toolNameSet(t, docPath, registered)

	reportSetDifference(t, registered, documentedSet, docPath+": registered tool %q is missing from the Full tool index")
	reportSetDifference(t, documented, registeredSet, docPath+": Full tool index names %q, which is not a registered tool")
}
