package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// staleAppBuiltAt and freshAppBuiltAt are the two modification times the
// reported machine showed for its desktop app copies: one left beside the CLI
// by an earlier build, and one ./erun-ui/build.sh had just written into the
// checkout. Naming them keeps the freshness decisions below readable.
var (
	staleAppBuiltAt = time.Date(2026, 9, 20, 10, 43, 40, 0, time.UTC)
	freshAppBuiltAt = time.Date(2026, 9, 20, 23, 48, 58, 0, time.UTC)
)

// cliBinDir is where the CLI binary sits in the build tree these tests model.
func cliBinDir(root string) string { return filepath.Join(root, "erun-cli", "bin") }

// checkoutBinDir is where a local ./erun-ui/build.sh writes its rebuild, and
// so the second layout resolveAppExecutableNear derives from the CLI's own
// location.
func checkoutBinDir(root string) string { return filepath.Join(root, "erun-ui", "bin") }

// appCopyPath is the path a launch has to name for a copy staged in dir: on
// macOS the enclosing .app bundle, elsewhere the binary itself.
func appCopyPath(dir string, hostOS eruncommon.HostOS, executableName string) string {
	if hostOS == eruncommon.HostOSDarwin {
		return filepath.Join(dir, desktopAppBundleName)
	}
	return filepath.Join(dir, executableName)
}

// bundleExecutablePath is the executable inside a staged macOS bundle, the
// one a rebuild that does not recreate the bundle replaces in place.
func bundleExecutablePath(bundle, executableName string) string {
	return filepath.Join(bundle, "Contents", "MacOS", executableName)
}

// cliExecutable writes the CLI binary the resolution starts from. Only its
// directory decides anything, but writing it keeps the staged tree the same
// shape as the one being modelled.
func cliExecutable(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(cliBinDir(root), "erun")
	if err := os.MkdirAll(cliBinDir(root), 0o755); err != nil {
		t.Fatalf("create %s: %v", cliBinDir(root), err)
	}
	if err := os.WriteFile(path, []byte("cli"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// stageAppCopy writes one desktop app copy into dir in the shape hostOS
// launches, stamped with builtAt so a test controls which copy looks fresher.
func stageAppCopy(t *testing.T, dir string, hostOS eruncommon.HostOS, executableName string, builtAt time.Time) string {
	t.Helper()
	launch := appCopyPath(dir, hostOS, executableName)
	if hostOS == eruncommon.HostOSDarwin {
		binary := bundleExecutablePath(launch, executableName)
		if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
			t.Fatalf("create app bundle %s: %v", launch, err)
		}
		if err := os.WriteFile(binary, []byte("desktop app"), 0o755); err != nil {
			t.Fatalf("write %s: %v", binary, err)
		}
		setAppModTime(t, binary, builtAt)
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
		if err := os.WriteFile(launch, []byte("desktop app"), 0o755); err != nil {
			t.Fatalf("write %s: %v", launch, err)
		}
	}
	setAppModTime(t, launch, builtAt)
	return launch
}

func setAppModTime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("stamp %s with %s: %v", path, at, err)
	}
}

// TestResolveAppExecutableNearLaunchesTheFreshRebuildOverTheStaleCopy is the
// reproduction of the reported relaunch-a-stale-bundle failure: with a copy
// left beside the CLI and a newer rebuild in erun-ui/bin, both real
// directories, the launch has to pick the rebuild. Taking the first copy that
// exists picked the stale one instead, so the desktop silently kept running an
// old build and every restart after it relaunched that same copy, because a
// desktop restarts from the copy it is running from.
func TestResolveAppExecutableNearLaunchesTheFreshRebuildOverTheStaleCopy(t *testing.T) {
	t.Parallel()

	const executableName = "erun-app"

	for _, hostOS := range []eruncommon.HostOS{eruncommon.HostOSDarwin, eruncommon.HostOSLinux} {
		t.Run(string(hostOS), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			staleCopy := stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
			freshCopy := stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, freshAppBuiltAt)

			chosen, trace := resolveAppExecutableNear(cliExecutable(t, root), executableName, hostOS)

			if chosen != freshCopy {
				t.Errorf("resolved %q, want the fresh rebuild %q (the stale copy beside the CLI was %q)", chosen, freshCopy, staleCopy)
			}
			if !strings.Contains(trace, freshCopy) {
				t.Errorf("the reported choice %q does not name the copy that will launch (%q)", trace, freshCopy)
			}
			if !strings.Contains(trace, staleCopy) {
				t.Errorf("the reported choice %q does not name the copy it passed over (%q)", trace, staleCopy)
			}
		})
	}
}

// copyLocation names which of the two layouts a resolution is expected to
// launch.
type copyLocation int

const (
	noCopy copyLocation = iota
	besideCLI
	inCheckout
)

// desktopAppCopyCase is one freshness decision, staged as the two layouts a
// build tree can hold at once.
type desktopAppCopyCase struct {
	name string
	// darwinOnly marks a case staged in the .app bundle shape alone, where a
	// rebuild can replace the bundle's executable without recreating it.
	darwinOnly bool
	stage      func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string)
	want       copyLocation
}

var desktopAppCopyCases = []desktopAppCopyCase{
	{
		name: "a_newer_copy_beside_the_cli_wins",
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, freshAppBuiltAt)
			stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, staleAppBuiltAt)
		},
		want: besideCLI,
	},
	{
		name:       "a_rebuild_that_replaces_the_bundle_executable_in_place_wins",
		darwinOnly: true,
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
			bundle := stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, staleAppBuiltAt)
			setAppModTime(t, bundleExecutablePath(bundle, executableName), freshAppBuiltAt)
		},
		want: inCheckout,
	},
	{
		name: "equal_timestamps_keep_the_copy_nearest_the_cli",
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
			stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, staleAppBuiltAt)
		},
		want: besideCLI,
	},
	{
		name: "only_the_checkout_rebuild_present",
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {
			stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, freshAppBuiltAt)
		},
		want: inCheckout,
	},
	{
		name: "only_the_copy_beside_the_cli_present",
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
		},
		want: besideCLI,
	},
	{
		name:  "no_copy_anywhere_resolves_nothing",
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {},
		want:  noCopy,
	},
	{
		// A file where a bundle belongs on macOS, and a directory where a
		// binary belongs elsewhere: neither is a copy any launch can start.
		name: "a_layout_of_the_wrong_shape_is_not_a_copy",
		stage: func(t *testing.T, root string, hostOS eruncommon.HostOS, executableName string) {
			path := appCopyPath(cliBinDir(root), hostOS, executableName)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create %s: %v", filepath.Dir(path), err)
			}
			if hostOS == eruncommon.HostOSDarwin {
				// A bare file named ERun.app is not a bundle.
				if err := os.WriteFile(path, []byte("placeholder"), 0o644); err != nil {
					t.Fatalf("stage %s: %v", path, err)
				}
				return
			}
			// A directory does not stand in for the binary.
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("stage %s: %v", path, err)
			}
		},
		want: noCopy,
	},
}

// TestResolveAppExecutableNearChoosesAmongCopies covers the rest of the
// freshness rule: the inverse of the reported shadowing, the case where the
// rebuild replaced the bundle's executable in place, what a tie does, and what
// is not a copy at all.
func TestResolveAppExecutableNearChoosesAmongCopies(t *testing.T) {
	t.Parallel()

	const executableName = "erun-app"

	for _, hostOS := range []eruncommon.HostOS{eruncommon.HostOSDarwin, eruncommon.HostOSLinux} {
		t.Run(string(hostOS), func(t *testing.T) {
			t.Parallel()
			for _, tc := range desktopAppCopyCases {
				if tc.darwinOnly && hostOS != eruncommon.HostOSDarwin {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					tc.stage(t, root, hostOS, executableName)

					want := ""
					switch tc.want {
					case besideCLI:
						want = appCopyPath(cliBinDir(root), hostOS, executableName)
					case inCheckout:
						want = appCopyPath(checkoutBinDir(root), hostOS, executableName)
					}
					chosen, trace := resolveAppExecutableNear(cliExecutable(t, root), executableName, hostOS)
					if chosen != want {
						t.Fatalf("resolved %q, want %q", chosen, want)
					}
					if want == "" && trace != "" {
						t.Errorf("resolved nothing yet reported %q", trace)
					}
				})
			}
		})
	}
}
