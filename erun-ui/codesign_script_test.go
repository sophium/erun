package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	codesignpkg "github.com/sophium/erun/erun-common/infra/codesign"
)

type securityStubFailure struct {
	subcommand string
	stderr     string
}

// securityStub really creates and removes the keychain file and answers the
// search-list read, the two branches the script's behaviour turns on.
func securityStub(searchList []string, failures []securityStubFailure) string {
	var body strings.Builder
	body.WriteString("case \"$1\" in\n")
	for _, failure := range failures {
		body.WriteString("  " + failure.subcommand + ") printf '%s\\n' '" + failure.stderr + "' >&2; exit 1 ;;\n")
	}
	body.WriteString("  create-keychain) : > \"$4\" ;;\n")
	body.WriteString("  delete-keychain) rm -f \"$2\" ;;\n")
	// Three arguments is the read (`-d user`); the write adds `-s` and paths.
	body.WriteString("  list-keychains)\n    if [ \"$#\" -eq 3 ]; then\n      :\n")
	for _, keychain := range searchList {
		body.WriteString("      printf '    \"%s\"\\n' '" + keychain + "'\n")
	}
	body.WriteString("    fi ;;\n")
	body.WriteString("esac\nexit 0\n")
	return body.String()
}

// codesignScriptHarness stubs codesign, security, and openssl so non-macOS hosts can run the script.
type codesignScriptHarness struct {
	home             string
	stubs            string
	searchList       []string
	securityFailures []securityStubFailure
	codesignExitCode string
	identity         string
	hostOS           string
}

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
		codesignExitCode: "0",
		hostOS:           "darwin",
	}
	if err := os.MkdirAll(filepath.Dir(harness.keychain()), 0o755); err != nil {
		t.Fatalf("stage the keychain directory: %v", err)
	}
	harness.searchList = []string{harness.loginKeychain(), "/Library/Keychains/System.keychain"}
	return harness
}

func (h *codesignScriptHarness) keychain() string {
	return filepath.Join(h.home, "Library", "Keychains", codesignpkg.LocalCodesignKeychainFile)
}

func (h *codesignScriptHarness) loginKeychain() string {
	return filepath.Join(h.home, "Library", "Keychains", "login.keychain-db")
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
	h.writeStub(t, "security", securityStub(h.searchList, h.securityFailures))
	h.writeStub(t, "openssl", "exit 0\n")
	h.writeStub(t, "codesign", "exit "+h.codesignExitCode+"\n")
	// Reset logs so a reused identity does not read as re-created.
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

// build.sh runs on Linux too, so signing must be a reported no-op there.
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

func TestCodesignScriptCreatesTheLocalIdentityOnce(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	artifact := harness.artifact(t, "erun-app")

	run := harness.run(t, artifact)

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.output, "code signing: creating identity "+codesignpkg.LocalCodesignIdentity) {
		t.Fatalf("expected the creation to be visible in the output, got:\n%s", run.output)
	}
	for _, call := range []string{"create-keychain", "import", "set-key-partition-list"} {
		if !strings.Contains(run.security, call) {
			t.Fatalf("expected the identity created with %s, got security calls:\n%s", call, run.security)
		}
	}
	if !strings.Contains(run.codesign, "--sign "+codesignpkg.LocalCodesignIdentity) {
		t.Fatalf("expected the artifact signed with the local identity, got codesign calls:\n%s", run.codesign)
	}
	if !strings.Contains(run.codesign, "--keychain "+harness.keychain()) {
		t.Fatalf("expected the identity taken from erun's own keychain, got codesign calls:\n%s", run.codesign)
	}
	if !strings.Contains(run.output, "code signing: signed "+artifact+" as "+codesignpkg.LocalCodesignIdentity) {
		t.Fatalf("expected the signer named per artifact, got:\n%s", run.output)
	}
}

// openssl and `security import -P ""` disagree on empty PKCS#12 passwords, so both need the same non-empty one.
func TestCodesignScriptGivesThePKCS12APasswordBothToolsAgreeOn(t *testing.T) {
	harness := newCodesignScriptHarness(t)

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.openssl, "-passout pass:"+codesignpkg.LocalCodesignKeychainPassword) {
		t.Fatalf("expected the PKCS#12 exported under a non-empty password, got openssl calls:\n%s", run.openssl)
	}
	if !strings.Contains(run.security, "-P "+codesignpkg.LocalCodesignKeychainPassword) {
		t.Fatalf("expected the import to read the same password, got security calls:\n%s", run.security)
	}
	for _, empty := range []string{"-passout pass:\n", "-passout pass: ", "-P  ", "-P \n"} {
		if strings.Contains(run.openssl+run.security, empty) {
			t.Fatalf("expected no empty PKCS#12 password, %q is still passed:\nopenssl:\n%s\nsecurity:\n%s", empty, run.openssl, run.security)
		}
	}
}

// `codesign --keychain` does not resolve identities off the search list; adding
// erun's keychain must keep the existing entries.
func TestCodesignScriptAddsItsKeychainToTheSearchList(t *testing.T) {
	harness := newCodesignScriptHarness(t)

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", run.exitCode, run.output)
	}
	written := searchListWrite(t, run.security)
	if written == "" {
		t.Fatalf("expected the keychain added to the search list, got security calls:\n%s", run.security)
	}
	for _, keychain := range append(harness.searchList, harness.keychain()) {
		if !strings.Contains(written, keychain) {
			t.Fatalf("expected %s kept on the search list, got: %s", keychain, written)
		}
	}
	if !strings.Contains(run.output, "added "+harness.keychain()+" to the keychain search list") {
		t.Fatalf("expected the search-list change reported, got:\n%s", run.output)
	}
}

func TestCodesignScriptLeavesASearchListThatAlreadyHasItAlone(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	harness.searchList = append(harness.searchList, harness.keychain())

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", run.exitCode, run.output)
	}
	if written := searchListWrite(t, run.security); written != "" {
		t.Fatalf("expected the search list left alone, it was rewritten as: %s", written)
	}
	if strings.Contains(run.output, "keychain search list") {
		t.Fatalf("expected nothing reported about the search list, got:\n%s", run.output)
	}
}

// searchListWrite returns the `security list-keychains -s ...` call, or "" when none.
func searchListWrite(t *testing.T, securityCalls string) string {
	t.Helper()
	for _, call := range strings.Split(securityCalls, "\n") {
		if strings.HasPrefix(call, "list-keychains") && strings.Contains(call, " -s ") {
			return call
		}
	}
	return ""
}

func TestCodesignScriptNamesTheCreationStepThatFailed(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	const rejection = "security: SecKeychainItemImport: MAC verification failed during PKCS12 import"
	harness.securityFailures = []securityStubFailure{{subcommand: "import", stderr: rejection}}

	run := harness.run(t, harness.artifact(t, "erun-app"))

	if run.exitCode == 0 {
		t.Fatalf("expected a non-zero exit when the identity could not be created:\n%s", run.output)
	}
	if !strings.Contains(run.output, "importing the identity into the keychain failed") {
		t.Fatalf("expected the failing step named, got:\n%s", run.output)
	}
	if !strings.Contains(run.output, rejection) {
		t.Fatalf("expected what the tool said repeated, got:\n%s", run.output)
	}
}

// `find-identity -v` never reports a self-signed identity, so keychain-file presence is the reuse test.
func TestCodesignScriptNeverDecidesOnTheValidIdentityFilter(t *testing.T) {
	for _, line := range strings.Split(readBuildScript(t, "codesign.sh"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "find-identity") {
			t.Errorf("codesign.sh runs find-identity (%q); a self-signed identity is never \"valid\", so the check would miss an identity that is there", strings.TrimSpace(line))
		}
	}
}

// A new certificate is a new signer, which loses privacy grants.
func TestCodesignScriptReusesAnIdentityThatAlreadyExists(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	artifact := harness.artifact(t, "erun-app")
	harness.run(t, artifact)

	run := harness.run(t, artifact)

	if run.exitCode != 0 {
		t.Fatalf("expected exit 0 on the second build, got %d:\n%s", run.exitCode, run.output)
	}
	if !strings.Contains(run.output, "code signing: using identity "+codesignpkg.LocalCodesignIdentity) {
		t.Fatalf("expected the second build to reuse the identity, got:\n%s", run.output)
	}
	if strings.Contains(run.security, "create-keychain") {
		t.Fatalf("expected no second create-keychain, got security calls:\n%s", run.security)
	}
	if strings.Contains(run.output, "creating identity") {
		t.Fatalf("expected no second creation, got:\n%s", run.output)
	}
}

// A half-created keychain would read as holding the identity forever after.
func TestCodesignScriptRemovesAKeychainItCouldNotFinish(t *testing.T) {
	harness := newCodesignScriptHarness(t)
	// Fail at the import, after create-keychain has made the file.
	harness.securityFailures = []securityStubFailure{{subcommand: "import"}}

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

// Signing trouble does not fail the build, but the cost and recovery are printed.
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

// The script and erun-common's host-side signing must name the same identity, keychain, and password.
func TestLocalSigningIdentityMatchesTheSharedContract(t *testing.T) {
	script := readBuildScript(t, "codesign.sh")
	for _, value := range []string{
		codesignpkg.LocalCodesignIdentity,
		codesignpkg.LocalCodesignKeychainFile,
		codesignpkg.LocalCodesignKeychainPassword,
	} {
		if !strings.Contains(script, value) {
			t.Errorf("codesign.sh does not use %q; the desktop build and erun-common's host signing would sign with different identities", value)
		}
	}
}

func TestDesktopBuildScriptSignsWhatItProduces(t *testing.T) {
	if !strings.Contains(readBuildScript(t, "build.sh"), "codesign.sh") {
		t.Fatal("build.sh does not run codesign.sh; its artifacts keep the linker's ad-hoc signature")
	}
}
