package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

// reentryHelpers are the processes this test binary can be re-executed as.
// Each helper is a real process on purpose, because each stands in for
// something only a real process can be: a stub `ssh` answers a PATH lookup,
// and a supervisor helper gives the job store a pid that genuinely ran and
// genuinely exited. Only one TestMain is allowed per test binary, so the file
// that defines a helper registers it here from an init, and the helper moves
// with that file. A helper returns false when this is not the process it
// serves.
var reentryHelpers []func() (int, bool)

// runReentryHelper runs the helper this process was re-executed as, if any.
func runReentryHelper() (int, bool) {
	for _, reenter := range reentryHelpers {
		if code, reentered := reenter(); reentered {
			return code, true
		}
	}
	return 0, false
}

func TestMain(m *testing.M) {
	if code, reentered := runReentryHelper(); reentered {
		os.Exit(code)
	}
	// This suite must never depend on the invoking shell's own environment:
	// running `go test` from inside an actual runtime pod (as this repo's own
	// agent environments do) otherwise leaves ERUN_TENANT/ERUN_ENVIRONMENT set,
	// which makes inInjectedRuntimePod() true for every test in the package --
	// stepTiming's cgroup sampling (build_cgroup_metrics.go) would then attempt
	// a real `kubectl exec` per step on every timing test, turning millisecond
	// tests into multi-second ones and making them depend on cluster access
	// nothing here should need. Any test that means to exercise the
	// injected-pod path sets these explicitly via t.Setenv.
	if err := os.Unsetenv("ERUN_TENANT"); err != nil {
		fmt.Fprintf(os.Stderr, "unsetenv ERUN_TENANT: %v\n", err)
		os.Exit(1)
	}
	if err := os.Unsetenv("ERUN_ENVIRONMENT"); err != nil {
		fmt.Fprintf(os.Stderr, "unsetenv ERUN_ENVIRONMENT: %v\n", err)
		os.Exit(1)
	}
	// Timing history is the operator's, not the suite's: this package reaches
	// writeTimingRecord through the build/deploy paths its tests drive, so
	// without this the binary appends microsecond records for commands that
	// never ran to the operator's real ~/.erun/timing -- and, because retention
	// prunes on write, evicts a genuine record to make room. Redirected for the
	// whole binary rather than per test, so a test added later is isolated by
	// default. A caller who set the variable explicitly keeps their value.
	var timingTempDir string
	if _, ok := os.LookupEnv(core.TimingRecordDirEnv); !ok {
		dir, err := os.MkdirTemp("", "erun-timing-test-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "create timing temp dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.Setenv(core.TimingRecordDirEnv, dir); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", core.TimingRecordDirEnv, err)
			os.Exit(1)
		}
		timingTempDir = dir
	}
	// No test may reach the operator's real browser or file opener: the OIDC
	// login opens the authorize URL as its first act, and on a desktop that is
	// a stray tab at a fixture hostname. Every platform's opener is pointed at
	// an inert binary unless a test names its own.
	if err := pointBrowserOpenersAtInertBinary(); err != nil {
		fmt.Fprintf(os.Stderr, "stub browser openers: %v\n", err)
		os.Exit(1)
	}
	if err := disableGitMaintenanceForSuite(); err != nil {
		fmt.Fprintf(os.Stderr, "turn off git maintenance: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if timingTempDir != "" {
		_ = os.RemoveAll(timingTempDir)
	}
	os.Exit(code)
}

// browserOpenerOverrideEnvs are the ERUN_<NAME>_BIN seams of every platform's
// opener erunLoginBrowserCommand can name.
var browserOpenerOverrideEnvs = []string{"ERUN_OPEN_BIN", "ERUN_XDG_OPEN_BIN", "ERUN_CMD_BIN"}

func pointBrowserOpenersAtInertBinary() error {
	inert, err := exec.LookPath("true")
	if err != nil {
		return err
	}
	for _, name := range browserOpenerOverrideEnvs {
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		if err := os.Setenv(name, inert); err != nil {
			return err
		}
	}
	return nil
}
