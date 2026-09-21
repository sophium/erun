package eruncommon

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The state ERun keeps under the user's config directory is two trees, not
// one, and they are deliberately spelled differently. The user's config tree
// is lowercase "erun" -- configRoot, the root `erun doctor` prints as
// <config-root>, and the tree every tenant config and secret lives in. The
// desktop's own per-installation state -- its identity key, app log, and
// orchestrator bookkeeping -- is the capitalized "ERun" tree beside it.
//
// On macOS APFS, case-insensitive by default, those two names resolve to one
// directory, which hides the distinction completely; on Linux and on
// case-sensitive APFS they are two directories, and always have been. That
// makes the casing look like a typo waiting to be "fixed", so these tests
// exist because the tempting fix is the harmful one: re-spelling the config
// tree to "ERun" moves every tenant config, secret and ACME record, while
// re-spelling the desktop tree to "erun" moves the identity key deployed
// edges already trust. Either direction leaves real state on disk that the
// code no longer reads, and neither is a rename an operator can undo
// silently. Flipping either spelling must fail here first.

// stateDirLiteralPattern matches either spelling of a state-directory name
// written as its own string literal, which is the shape the port-forward path
// used to duplicate the config root with.
var stateDirLiteralPattern = regexp.MustCompile(`"(?:ERun|erun)"`)

// goFuncBody returns the source of one function, so a test can assert on the
// name the code resolved through rather than on what ends up on disk.
func goFuncBody(t *testing.T, fileName, funcName string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), fileName))
	if err != nil {
		t.Fatalf("read %s: %v", fileName, err)
	}
	lines := strings.Split(string(source), "\n")
	signature := "func " + funcName + "("
	for i, line := range lines {
		if !strings.HasPrefix(line, signature) {
			continue
		}
		for j := i; j < len(lines); j++ {
			if lines[j] == "}" {
				return strings.Join(lines[i:j+1], "\n")
			}
		}
	}
	t.Fatalf("%s: no %s declaration found", fileName, signature)
	return ""
}

// TestPortForwardStatePathNamesTheConfigRootThroughItsOneConstant is the
// reproduction for the split-state-directory report. Port-forward state is
// written inside the user's config tree, whose one name is configRoot, but
// this path spelled that root out as a second literal -- the only copy of the
// config tree's name anywhere outside configRoot's own definition. A copy does
// not follow its original: whichever of the two a later edit changed, the
// port-forward tree would resolve somewhere other than the rest of the config
// store, and every forward already on disk would stop being found, taking the
// recorded local port and the log beside it out of reach with it.
//
// Deliberately structural, reading the source rather than the filesystem: on a
// case-insensitive volume -- the macOS default, where this whole split is
// invisible -- two differently cased paths resolve to the same directory, so
// no assertion about what exists on disk can tell the two names apart. What
// can be checked is which name the code resolved through.
func TestPortForwardStatePathNamesTheConfigRootThroughItsOneConstant(t *testing.T) {
	body := goFuncBody(t, "port_forward_state.go", "PortForwardStatePath")
	if !strings.Contains(body, "configRoot") {
		t.Errorf("PortForwardStatePath must resolve the config tree through configRoot rather than a name of its own:\n%s", body)
	}
	if literal := stateDirLiteralPattern.FindString(body); literal != "" {
		t.Errorf("PortForwardStatePath spells the state directory out as the literal %s; resolving through configRoot is what keeps the port-forward tree from drifting away from the config tree it lives in", literal)
	}
}

// TestPortForwardStateLivesInsideTheConfigTree pins which of the two trees
// port-forward state belongs to. It is part of the config tree, not the
// desktop's ERun tree: LoadPortForwardState validates a state file against the
// tenant/environment config sitting beside it, and the file records the local
// port and log path `erun open` reuses, so it has to move with the configs it
// is checked against rather than with the desktop's own state.
func TestPortForwardStateLivesInsideTheConfigTree(t *testing.T) {
	redirectConfigHomeForTest(t)
	configTree, err := ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir: %v", err)
	}
	statePath, err := PortForwardStatePath("mcp", "acme", "dev")
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	want := filepath.Join(configTree, "portforward", "mcp", "acme", "dev.json")
	if statePath != want {
		t.Fatalf("port-forward state must sit in the config tree:\n got %q\nwant %q", statePath, want)
	}

	desktop := DefaultDesktopIdentityDir()
	if desktop == "" {
		t.Fatal("DefaultDesktopIdentityDir returned empty")
	}
	if strings.HasPrefix(statePath, desktop+string(filepath.Separator)) {
		t.Fatalf("port-forward state resolved into the desktop's own state tree %q, but it belongs to the config tree %q", desktop, configTree)
	}
}

// TestTheDesktopStateTreeAndTheConfigTreeKeepTheirOwnSpellings is the guard
// against re-spelling either tree to match the other. The two sit side by side
// under one config home and differ only in case, which on a case-insensitive
// volume is invisible -- so the change that "unifies" them looks free and is
// not. Pointing either name at the other's spelling hides every file the
// losing tree already holds, on exactly the case-sensitive volumes where the
// two are distinguishable at all.
func TestTheDesktopStateTreeAndTheConfigTreeKeepTheirOwnSpellings(t *testing.T) {
	redirectConfigHomeForTest(t)
	configTree, err := ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir: %v", err)
	}
	desktop := DefaultDesktopIdentityDir()
	if desktop == "" {
		t.Fatal("DefaultDesktopIdentityDir returned empty")
	}

	if base := filepath.Base(configTree); base != configRoot {
		t.Errorf("config tree base = %q, want configRoot %q", base, configRoot)
	}
	// The published contract: the desktop identity is read from
	// <user config dir>/ERun/desktopid.pub.
	if base := filepath.Base(desktop); base != "ERun" {
		t.Errorf("desktop state tree base = %q, want %q", base, "ERun")
	}
	if filepath.Dir(configTree) != filepath.Dir(desktop) {
		t.Fatalf("both trees must share one config home, got %q and %q", configTree, desktop)
	}
	if filepath.Base(configTree) == filepath.Base(desktop) {
		t.Fatalf("the config tree and the desktop state tree collapsed onto one name %q; that silently orphans whichever tree already exists under the other spelling", filepath.Base(configTree))
	}
}
