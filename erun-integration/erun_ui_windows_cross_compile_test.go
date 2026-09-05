package integration

import (
	"strings"
	"testing"
)

// TestCheckGateRunsWindowsDesktopCrossCompile locks in check-gate's only
// compile check for a platform erun-ui actually ships (Windows, via Scoop):
// the webkit2gtk-tagged Playwright build never touches the windows-only
// build-constrained source, so a break confined to it would otherwise pass
// the gate silently — the same blind spot the webkit2gtk-only build leaves
// for macOS, which has no automated compile check at all (see
// erun-ui/AGENTS.md's "End-to-end UI tests" section for why).
func TestCheckGateRunsWindowsDesktopCrossCompile(t *testing.T) {
	t.Parallel()
	root, ok := findFullCheckoutRoot()
	if !ok {
		t.Skip("full source tree not present (partial in-build build context)")
	}

	makefile := mustReadRepoFile(t, root, "Makefile")
	checkGate := lineContaining(makefile, "check-gate:")
	if checkGate == "" {
		t.Fatalf("Makefile has no check-gate target")
	}
	if !strings.Contains(checkGate, "test-erun-ui-windows-build") {
		t.Fatalf("check-gate no longer runs test-erun-ui-windows-build; the desktop gate would silently drop its only compile check for the Windows build erun-ui actually ships")
	}
}

func lineContaining(text, substr string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}
