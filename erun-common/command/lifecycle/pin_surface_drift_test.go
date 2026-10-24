package lifecycle

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// repinSurfaces are the descriptions that enumerate what a re-pin rewrites. Each words it
// independently, so only this test keeps them complete. The desktop sidebar's one-line
// affordance names only some kinds and is deliberately not listed.
var repinSurfaces = []string{
	"erun-cli/cmd/pin/pin.go",
	"erun-mcp/server_review_delivery_tools.go",
	"erun-ui/frontend/src/components/app/PinVersionDialog.tsx",
	"erun-skills/skills/erun-pin-version/SKILL.md",
	"erun-docs/docs/cli/pin.md",
}

// A declared kind without a PinSiteSummaries phrase fails here.
var pinKindDeclarationPattern = regexp.MustCompile(`(?m)^\s*PinSite\w+\s+PinSiteKind\s*=\s*"([^"]+)"`)

// goStringConcatPattern rejoins Go string literals wrapped with `+`.
var goStringConcatPattern = regexp.MustCompile(`"\s*\+\s*"`)

// normalizeRepinSurfaceText ignores line wrapping, typographic apostrophes, and case.
func normalizeRepinSurfaceText(raw string) string {
	raw = strings.ReplaceAll(raw, "’", "'")
	raw = goStringConcatPattern.ReplaceAllString(raw, "")
	return strings.Join(strings.Fields(strings.ToLower(raw)), " ")
}

func TestPinSiteSummariesCoverEveryDeclaredKind(t *testing.T) {
	source, err := os.ReadFile("../../base/model/pin.go")
	if err != nil {
		t.Fatalf("read base/model/pin.go: %v", err)
	}
	declared := pinKindDeclarationPattern.FindAllStringSubmatch(string(source), -1)
	if len(declared) == 0 {
		t.Fatal("no PinSiteKind declarations found in base/model/pin.go -- this guard is no longer reading what it thinks it is")
	}
	summarized := map[model.PinSiteKind]bool{}
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
		if !summarized[model.PinSiteKind(match[1])] {
			t.Errorf("PinSiteKind %q is declared in pin.go but has no PinSiteSummaries phrase -- add one, then name that kind on every surface that enumerates re-pin sites", match[1])
		}
	}
}

func TestEverySurfaceDescribingARepinNamesEveryPinSiteKind(t *testing.T) {
	if len(PinSiteSummaries) == 0 {
		t.Fatal("PinSiteSummaries is empty; the rest of this test would pass vacuously")
	}
	root := erunRepoRoot(t)
	for _, path := range repinSurfaces {
		data, err := os.ReadFile(filepath.Join(root, path))
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
