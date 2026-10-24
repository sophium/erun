package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/cmd/shared/desktop"
	"github.com/sophium/erun/erun-common/base/model"
)

var (
	staleAppBuiltAt = time.Date(2026, 9, 20, 10, 43, 40, 0, time.UTC)
	freshAppBuiltAt = time.Date(2026, 9, 20, 23, 48, 58, 0, time.UTC)
)

func cliBinDir(root string) string { return filepath.Join(root, "erun-cli", "bin") }

func checkoutBinDir(root string) string { return filepath.Join(root, "erun-ui", "bin") }

// appCopyPath is the .app bundle on macOS, the binary elsewhere.
func appCopyPath(dir string, hostOS model.HostOS, executableName string) string {
	if hostOS == model.HostOSDarwin {
		return filepath.Join(dir, desktop.DesktopAppBundleName)
	}
	return filepath.Join(dir, executableName)
}

func bundleExecutablePath(bundle, executableName string) string {
	return filepath.Join(bundle, "Contents", "MacOS", executableName)
}

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

func stageAppCopy(t *testing.T, dir string, hostOS model.HostOS, executableName string, builtAt time.Time) string {
	t.Helper()
	launch := appCopyPath(dir, hostOS, executableName)
	if hostOS == model.HostOSDarwin {
		stageAppBundleExecutable(t, launch, executableName, builtAt)
	} else {
		stagePlainAppExecutable(t, dir, launch)
	}
	setAppModTime(t, launch, builtAt)
	return launch
}

func stageAppBundleExecutable(t *testing.T, launch, executableName string, builtAt time.Time) {
	t.Helper()
	binary := bundleExecutablePath(launch, executableName)
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("create app bundle %s: %v", launch, err)
	}
	if err := os.WriteFile(binary, []byte("desktop app"), 0o755); err != nil {
		t.Fatalf("write %s: %v", binary, err)
	}
	setAppModTime(t, binary, builtAt)
}

func stagePlainAppExecutable(t *testing.T, dir, launch string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	if err := os.WriteFile(launch, []byte("desktop app"), 0o755); err != nil {
		t.Fatalf("write %s: %v", launch, err)
	}
}

func setAppModTime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("stamp %s with %s: %v", path, at, err)
	}
}

// A desktop restarts from the copy it runs, so picking the stale copy would stick.
func TestResolveAppExecutableNearLaunchesTheFreshRebuildOverTheStaleCopy(t *testing.T) {
	t.Parallel()

	const executableName = "erun-app"

	for _, hostOS := range []model.HostOS{model.HostOSDarwin, model.HostOSLinux} {
		t.Run(string(hostOS), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			staleCopy := stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
			freshCopy := stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, freshAppBuiltAt)

			chosen, trace := desktop.ResolveAppExecutableNear(cliExecutable(t, root), executableName, hostOS)

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

type copyLocation int

const (
	noCopy copyLocation = iota
	besideCLI
	inCheckout
)

type desktopAppCopyCase struct {
	name string
	// darwinOnly: a rebuild can replace the bundle's executable without recreating it.
	darwinOnly bool
	stage      func(t *testing.T, root string, hostOS model.HostOS, executableName string)
	want       copyLocation
}

var desktopAppCopyCases = []desktopAppCopyCase{
	{
		name: "a_newer_copy_beside_the_cli_wins",
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, freshAppBuiltAt)
			stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, staleAppBuiltAt)
		},
		want: besideCLI,
	},
	{
		name:       "a_rebuild_that_replaces_the_bundle_executable_in_place_wins",
		darwinOnly: true,
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
			bundle := stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, staleAppBuiltAt)
			setAppModTime(t, bundleExecutablePath(bundle, executableName), freshAppBuiltAt)
		},
		want: inCheckout,
	},
	{
		name: "equal_timestamps_keep_the_copy_nearest_the_cli",
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
			stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, staleAppBuiltAt)
		},
		want: besideCLI,
	},
	{
		name: "only_the_checkout_rebuild_present",
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {
			stageAppCopy(t, checkoutBinDir(root), hostOS, executableName, freshAppBuiltAt)
		},
		want: inCheckout,
	},
	{
		name: "only_the_copy_beside_the_cli_present",
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {
			stageAppCopy(t, cliBinDir(root), hostOS, executableName, staleAppBuiltAt)
		},
		want: besideCLI,
	},
	{
		name:  "no_copy_anywhere_resolves_nothing",
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {},
		want:  noCopy,
	},
	{
		name: "a_layout_of_the_wrong_shape_is_not_a_copy",
		stage: func(t *testing.T, root string, hostOS model.HostOS, executableName string) {
			path := appCopyPath(cliBinDir(root), hostOS, executableName)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create %s: %v", filepath.Dir(path), err)
			}
			if hostOS == model.HostOSDarwin {
				if err := os.WriteFile(path, []byte("placeholder"), 0o644); err != nil {
					t.Fatalf("stage %s: %v", path, err)
				}
				return
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("stage %s: %v", path, err)
			}
		},
		want: noCopy,
	},
}

func expectedAppCopyPath(root string, hostOS model.HostOS, executableName string, location copyLocation) string {
	switch location {
	case besideCLI:
		return appCopyPath(cliBinDir(root), hostOS, executableName)
	case inCheckout:
		return appCopyPath(checkoutBinDir(root), hostOS, executableName)
	case noCopy:
	}
	return ""
}

func TestResolveAppExecutableNearChoosesAmongCopies(t *testing.T) {
	t.Parallel()

	const executableName = "erun-app"

	for _, hostOS := range []model.HostOS{model.HostOSDarwin, model.HostOSLinux} {
		t.Run(string(hostOS), func(t *testing.T) {
			t.Parallel()
			for _, tc := range desktopAppCopyCases {
				if tc.darwinOnly && hostOS != model.HostOSDarwin {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					tc.stage(t, root, hostOS, executableName)

					want := expectedAppCopyPath(root, hostOS, executableName, tc.want)
					chosen, trace := desktop.ResolveAppExecutableNear(cliExecutable(t, root), executableName, hostOS)
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
