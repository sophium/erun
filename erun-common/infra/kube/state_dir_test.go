package kube

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// The config tree "erun" and the desktop tree "ERun" are deliberately distinct. They
// coincide on case-insensitive volumes, so "fixing" the casing looks free but strands
// real state on case-sensitive ones.

var stateDirLiteralPattern = regexp.MustCompile(`"(?:ERun|erun)"`)

// goFuncBody returns one function's source.
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

// Structural on purpose: on a case-insensitive volume the filesystem cannot tell the two spellings apart.
func TestPortForwardStatePathNamesTheConfigRootThroughItsOneConstant(t *testing.T) {
	body := goFuncBody(t, "port_forward_state.go", "PortForwardStatePath")
	if !strings.Contains(body, "ConfigRoot") {
		t.Errorf("PortForwardStatePath must resolve the config tree through ConfigRoot rather than a name of its own:\n%s", body)
	}
	if literal := stateDirLiteralPattern.FindString(body); literal != "" {
		t.Errorf("PortForwardStatePath spells the state directory out as the literal %s; resolving through configRoot is what keeps the port-forward tree from drifting away from the config tree it lives in", literal)
	}
}

// LoadPortForwardState validates state against the config beside it, so state belongs in the config tree.
func TestPortForwardStateLivesInsideTheConfigTree(t *testing.T) {
	redirectConfigHomeForTest(t)
	configTree, err := config.ERunConfigDir()
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

	desktop := core.DefaultDesktopIdentityDir()
	if desktop == "" {
		t.Fatal("DefaultDesktopIdentityDir returned empty")
	}
	if strings.HasPrefix(statePath, desktop+string(filepath.Separator)) {
		t.Fatalf("port-forward state resolved into the desktop's own state tree %q, but it belongs to the config tree %q", desktop, configTree)
	}
}

func TestTheDesktopStateTreeAndTheConfigTreeKeepTheirOwnSpellings(t *testing.T) {
	redirectConfigHomeForTest(t)
	// macOS os.UserConfigDir ignores XDG_CONFIG_HOME; share one home as a default install does.
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	xdg.Reload()
	configTree, err := config.ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir: %v", err)
	}
	desktop := core.DefaultDesktopIdentityDir()
	if desktop == "" {
		t.Fatal("DefaultDesktopIdentityDir returned empty")
	}

	if base := filepath.Base(configTree); base != config.ConfigRoot {
		t.Errorf("config tree base = %q, want configRoot %q", base, config.ConfigRoot)
	}
	// The desktop identity contract is <user config dir>/ERun/desktopid.pub.
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
