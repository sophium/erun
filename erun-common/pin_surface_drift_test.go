package eruncommon

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// repinSurfaces are the operator-facing descriptions that enumerate what a
// re-pin rewrites. It is the enumerations, not every passing mention of a
// re-pin site: the desktop's one-line sidebar affordance names a few of them
// without claiming to name them all, and is deliberately not listed here.
//
// Only erun-common owns the canonical PinSiteKind set, and none of these is
// generated from it -- the CLI and MCP descriptions, the desktop dialog, the
// skill, and the public pin page each say it in their own words -- so nothing
// but this test keeps them enumerating the same thing. The regression it
// exists to stop is a surface that says "every erun reference" and then lists
// a subset: that reads as complete, and it understates what a re-pin touched
// in exactly the operation whose blast radius is the question being asked.
var repinSurfaces = []string{
	"../erun-cli/cmd/pin.go",
	"../erun-mcp/server.go",
	"../erun-ui/frontend/src/components/app/PinVersionDialog.tsx",
	"../erun-skills/skills/erun-pin-version/SKILL.md",
	"../erun-docs/docs/cli/pin.md",
}

// pinKindDeclarationPattern reads the PinSiteKind constants out of the source
// that declares them, so a kind added to the engine without a PinSiteSummaries
// phrase fails here instead of shipping named by no surface at all.
var pinKindDeclarationPattern = regexp.MustCompile(`(?m)^\s*PinSite\w+\s+PinSiteKind\s*=\s*"([^"]+)"`)

// goStringConcatPattern rejoins a Go string literal split across lines with
// `+`. The compiler allows that wrap anywhere, including through the middle of
// a phrase this test is looking for.
var goStringConcatPattern = regexp.MustCompile(`"\s*\+\s*"`)

// normalizeRepinSurfaceText folds away the differences between these surfaces
// that are not content: `+` line wrapping in Go string literals, JSX text
// broken across lines, the dialog's typographic apostrophe, and the docs'
// sentence-case table headings.
func normalizeRepinSurfaceText(raw string) string {
	raw = strings.ReplaceAll(raw, "’", "'")
	raw = goStringConcatPattern.ReplaceAllString(raw, "")
	return strings.Join(strings.Fields(strings.ToLower(raw)), " ")
}

func TestPinSiteSummariesCoverEveryDeclaredKind(t *testing.T) {
	source, err := os.ReadFile("pin.go")
	if err != nil {
		t.Fatalf("read pin.go: %v", err)
	}
	declared := pinKindDeclarationPattern.FindAllStringSubmatch(string(source), -1)
	if len(declared) == 0 {
		t.Fatal("no PinSiteKind declarations found in pin.go -- this guard is no longer reading what it thinks it is")
	}
	summarized := map[PinSiteKind]bool{}
	for _, summary := range PinSiteSummaries {
		if strings.TrimSpace(summary.Phrase) == "" {
			t.Errorf("pin site kind %q has an empty surface phrase, so no surface is required to name it", summary.Kind)
		}
		if summarized[summary.Kind] {
			t.Errorf("pin site kind %q is summarized twice", summary.Kind)
		}
		summarized[summary.Kind] = true
	}
	for _, match := range declared {
		if !summarized[PinSiteKind(match[1])] {
			t.Errorf("PinSiteKind %q is declared in pin.go but has no PinSiteSummaries phrase -- add one, then name that kind on every surface that enumerates re-pin sites", match[1])
		}
	}
}

func TestEverySurfaceDescribingARepinNamesEveryPinSiteKind(t *testing.T) {
	if len(PinSiteSummaries) == 0 {
		t.Fatal("PinSiteSummaries is empty; the rest of this test would pass vacuously")
	}
	for _, path := range repinSurfaces {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		normalized := normalizeRepinSurfaceText(string(data))
		for _, summary := range PinSiteSummaries {
			if !strings.Contains(normalized, normalizeRepinSurfaceText(summary.Phrase)) {
				t.Errorf("%s never names the %q re-pin site (no %q in its text): a surface that enumerates what a re-pin rewrites has to name every kind, or stop reading as though it named them all",
					path, summary.Kind, summary.Phrase)
			}
		}
	}
}
