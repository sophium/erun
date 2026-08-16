package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// securityStubBody is the default stubbed `security`. It materialises and
// removes the keychain file for real, because "is there already an identity" is
// the branch the whole first-use-versus-reuse behaviour turns on.
const securityStubBody = "case \"$1\" in\n" +
	"  create-keychain) : > \"$4\" ;;\n" +
	"  delete-keychain) rm -f \"$2\" ;;\n" +
	"esac\n" +
	"exit 0\n"

// codesignScriptHarness stubs codesign, security, and openssl so the script's
// decisions can be observed from a host that is not macOS.
type codesignScriptHarness struct {
	home             string
	stubs            string
	securityBody     string
	codesignExitCode string
	identity         string
	hostOS           string
}

// codesignScriptRun is one invocation: what it told the operator, and every call
// it made to each stubbed tool.
type codesignScriptRun struct {
	exitCode int
	output   string
	codesign string
	security string
	openssl  string
}

func (r codesignScriptRun) toolsUsed() bool {
	return r.codesign != "" || r.security != "" || r.openssl != ""
}

func newCodesignScriptHarness(t *testing.T) *codesignScriptHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("codesign.sh and its stubs are POSIX shell")
	}
	harness := &codesignScriptHarness{
		home:             t.TempDir(),
		stubs:            t.TempDir(),
		securityBody:     securityStubBody,
		codesignExitCode: "0",
		hostOS:           "darwin",
	}
	// Every macOS home has this; the temp one standing in for it must too, or the
	// stub is answering a question the real host never asks.
	if err := os.MkdirAll(filepath.Dir(harness.keychain()), 0o755); err != nil {
		t.Fatalf("stage the keychain directory: %v", err)
	}
	return harness
}

func (h *codesignScriptHarness) keychain() string {
	return filepath.Join(h.home, "Library", "Keychains", eruncommon.LocalCodesignKeychainFile)
}

func (h *codesignScriptHarness) logPath(tool string) string {
	return filepath.Join(h.stubs, tool+"-calls.log")
}

func (h *codesignScriptHarness) writeStub(t *testing.T, tool, body string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + h.logPath(tool) + "'\n" + body
	if err := os.WriteFile(filepath.Join(h.stubs, tool), []byte(script), 0o755); err != nil {
		t.Fatalf("write %s stub: %v", tool, err)
	}
}

func (h *codesignScriptHarness) readLog(t *testing.T, tool string) string {
	t.Helper()
	body, err := os.ReadFile(h.logPath(tool))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s call log: %v", tool, err)
	}
	return string(body)
}

// artifact writes a file for the script to sign and returns its path.
func (h *codesignScriptHarness) artifact(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(h.home, name)
	if err := os.WriteFile(path, []byte("artifact"), 0o755); err != nil {
		t.Fatalf("write artifact %s: %v", name, err)
	}
	return path
}

func (h *codesignScriptHarness) run(t *testing.T, artifacts ...string) codesignScriptRun {
	t.Helper()
	h.writeStub(t, "security", h.securityBody)
	h.writeStub(t, "openssl", "exit 0\n")
	h.writeStub(t, "codesign", "exit "+h.codesignExitCode+"\n")
	// Each run answers for itself: a log carrying the previous build's calls
	// would read as a rebuild re-creating an identity it actually reused.
	for _, tool := range []string{"security", "openssl", "codesign"} {
		if err := os.Remove(h.logPath(tool)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear %s call log: %v", tool, err)
		}
	}

	cmd := exec.Command("sh", append([]string{"codesign.sh"}, artifacts...)...)
	cmd.Env = append(os.Environ(),
		"HOME="+h.home,
		"ERUN_HOST_OS_OVERRIDE="+h.hostOS,
		"ERUN_CODESIGN_BIN="+filepath.Join(h.stubs, "codesign"),
		"ERUN_SECURITY_BIN="+filepath.Join(h.stubs, "security"),
		"ERUN_OPENSSL_BIN="+filepath.Join(h.stubs, "openssl"),
		"ERUN_CODESIGN_IDENTITY="+h.identity,
	)
	combined, err := cmd.CombinedOutput()
	run := codesignScriptRun{
		output:   string(combined),
		codesign: h.readLog(t, "codesign"),
		security: h.readLog(t, "security"),
		openssl:  h.readLog(t, "openssl"),
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		run.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("run codesign.sh: %v (%s)", err, run.output)
	}
	return run
}

// build.sh runs on Linux too — that is where its gates run — so the signing step
// has to be a no-op there rather than a failure, and it has to say which it was:
// a build that printed nothing about signing is a build nobody can attribute a
// vanished privacy grant to.
func TestCodesignScriptIsANoOpOffMacOS(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	harness.hostOS = "linux"

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0 off macOS, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.output, "code signing: skipped, linux is not macOS") {
		t.Fatalf("expected the skip to be visible in the output, got:\n%s", run.output)
	}
	if run.toolsUsed() {
		t.Fatalf("expected no signing tool to run off macOS, got codesign=%q security=%q openssl=%q", run.codesign, run.security, run.openssl)
	}
}

// The whole point of the identity is that it outlives a rebuild, so the first
// build mints it and every build after that reuses the one already there.
func TestCodesignScriptCreatesTheLocalIdentityOnce(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	artifact := harness.artifact(t, "erun-app")

	run := harness.run(t, artifact)

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.output, "code signing: creating identity "+eruncommon.LocalCodesignIdentity) {
		t.Fatalf("expected the creation to be visible in the output, got:\n%s", run.output)
	}
	for _, call := range []string{"create-keychain", "import", "set-key-partition-list"} {
		if !strings.Contains(run.security, call) {
			t.Fatalf("expected the identity created with %s, got security calls:\n%s", call, run.security)
		}
	}
	if !strings.Contains(run.codesign, "--sign "+eruncommon.LocalCodesignIdentity) {
		t.Fatalf("expected the artifact signed with the local identity, got codesign calls:\n%s", run.codesign)
	}
	if !strings.Contains(run.codesign, "--keychain "+harness.keychain()) {
		t.Fatalf("expected the identity taken from erun's own keychain, got codesign calls:\n%s", run.codesign)
	}
	if !strings.Contains(run.output, "code signing: signed "+artifact+" as "+eruncommon.LocalCodesignIdentity) {
		t.Fatalf("expected the signer named per artifact, got:\n%s", run.output)
	}
}

// An identity already on the developer's machine is never replaced: a new
// certificate is a new signer, which is the grant loss this exists to end.
func TestCodesignScriptReusesAnIdentityThatAlreadyExists(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	artifact := harness.artifact(t, "erun-app")
	harness.run(t, artifact)

	run := harness.run(t, artifact)

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0 on the second build, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.output, "code signing: using identity "+eruncommon.LocalCodesignIdentity) {
		t.Fatalf("expected the second build to reuse the identity, got:\n%s", run.output)
	}
	if strings.Contains(run.security, "create-keychain") {
		t.Fatalf("expected no second create-keychain, got security calls:\n%s", run.security)
	}
	if strings.Contains(run.output, "creating identity") {
		t.Fatalf("expected no second creation, got:\n%s", run.output)
	}
}

// A keychain left behind by a half-finished creation would be read as holding
// the identity forever after, so every later build would sign against a
// certificate that is not there.
func TestCodesignScriptRemovesAKeychainItCouldNotFinish(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	// Fail at the import, after create-keychain has already made the file.
	harness.securityBody = "case \"$1\" in\n" +
		"  create-keychain) : > \"$4\" ;;\n" +
		"  delete-keychain) rm -f \"$2\" ;;\n" +
		"  import) exit 1 ;;\n" +
		"esac\n" +
		"exit 0\n"

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode == 0 {
		t.Fatalf("expected a non-zero exit when the identity could not be created:\n%s", run.output)
	}
	if !strings.Contains(run.output, "could not create identity") {
		t.Fatalf("expected the failure named, got:\n%s", run.output)
	}
	if !strings.Contains(run.output, "tccutil reset") {
		t.Fatalf("expected the recovery named, got:\n%s", run.output)
	}
	if _, err := os.Stat(harness.keychain()); !os.IsNotExist(err) {
		t.Fatalf("expected the half-made keychain removed, stat says %v", err)
	}
}

// Signing trouble is not a build failure — the artifact runs either way — but it
// is a privacy grant that will vanish, so the cost and the recovery are printed.
func TestCodesignScriptReportsAFailedSignatureWithItsRecovery(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	harness.codesignExitCode = "1"

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode == 0 {
		t.Fatalf("expected a non-zero exit when signing failed:\n%s", run.output)
	}
	if !strings.Contains(run.output, "code signing: failed to sign") {
		t.Fatalf("expected the failure named, got:\n%s", run.output)
	}
	if !strings.Contains(run.output, "tccutil reset <service> com.sophium.erun") {
		t.Fatalf("expected the recovery named, got:\n%s", run.output)
	}
}

// A real Developer ID belongs to the operator, not to erun: it is already in a
// keychain the search list reaches, and erun neither creates nor replaces it.
func TestCodesignScriptDefersToAnOperatorSuppliedIdentity(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	harness.identity = "Developer ID Application: Someone (TEAMID)"

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.output, "using identity "+harness.identity+" from the keychain search list") {
		t.Fatalf("expected the operator identity named, got:\n%s", run.output)
	}
	if run.security != "" {
		t.Fatalf("expected no keychain of erun's own to be touched, got security calls:\n%s", run.security)
	}
	if strings.Contains(run.codesign, "--keychain") {
		t.Fatalf("expected the search list to resolve the identity, got codesign calls:\n%s", run.codesign)
	}
}

// The script and erun-common's host-side signing have to name the same identity,
// keychain, and password, or a desktop build and an artifact landing beside it
// would carry different signers — the same silent grant loss by another route.
func TestLocalSigningIdentityMatchesTheSharedContract(t *testing.T) {
	script := readBuildScript(t, "codesign.sh")
	for _, value := range []string{
		eruncommon.LocalCodesignIdentity,
		eruncommon.LocalCodesignKeychainFile,
		eruncommon.LocalCodesignKeychainPassword,
	} {
		if !strings.Contains(script, value) {
			t.Errorf("codesign.sh does not use %q; the desktop build and erun-common's host signing would sign with different identities", value)
		}
	}
}

// A signing step build.sh never calls leaves every build ad-hoc signed, which is
// the state this exists to end.
func TestDesktopBuildScriptSignsWhatItProduces(t *testing.T) {
	if !strings.Contains(readBuildScript(t, "build.sh"), "codesign.sh") {
		t.Fatal("build.sh does not run codesign.sh; its artifacts keep the linker's ad-hoc signature")
	}
}
