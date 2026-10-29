package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// Download classifies by content; the universal header and a Java class share 0xCAFEBABE.
var (
	machOPayload          = append([]byte{0xCF, 0xFA, 0xED, 0xFE}, []byte("erun darwin arm64 artifact")...)
	universalMachOPayload = append([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00, 0x00, 0x02}, []byte("two slices")...)
	javaClassPayload      = append([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00, 0x00, 0x34}, []byte("not a mach-o")...)
)

// Mirrors erun-common's constants (this module imports nothing).
const (
	localCodesignIdentity     = "ERun Local Development"
	localCodesignKeychainFile = "erun-local-signing.keychain-db"
)

func seedLocalCodesignIdentity(t *testing.T, home string) string {
	t.Helper()
	keychain := filepath.Join(home, "Library", "Keychains", localCodesignKeychainFile)
	if err := os.MkdirAll(filepath.Dir(keychain), 0o755); err != nil {
		t.Fatalf("stage the keychain directory: %v", err)
	}
	if err := os.WriteFile(keychain, []byte("keychain"), 0o600); err != nil {
		t.Fatalf("seed the local signing keychain: %v", err)
	}
	return keychain
}

func readCodesignCalls(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read codesign call log: %v", err)
	}
	return string(data)
}

// arm64 macOS silently SIGKILLs an unsigned Mach-O, so a cross-built darwin binary is
// ad-hoc signed on download.
func testOutputsDownloadDarwinSignsUnsignedMacho(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	info := requireDownloadedFile(t, filepath.Join(setup.Cwd, "erun-darwin-arm64"), machOPayload)
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected the signed artifact to be executable, got mode %v", info.Mode().Perm())
	}
	if calls := readCodesignCalls(t, codesignLog); !strings.Contains(calls, "-s - -f") {
		t.Fatalf("expected an ad-hoc signing call, got codesign calls:\n%s", calls)
	}
	golden.Equal(t, "outputs/download_darwin_signs_unsigned_macho", normalize.Apply(result.Combined))
}

// An ad-hoc signature pins the cdhash, so the next build drops privacy grants; a host with
// erun's local identity signs with it and names the signer.
func testOutputsDownloadDarwinSignsWithTheLocalIdentity(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
	fixture.StubBinaryWithScript(t, stubs, "security", "exit 0")
	keychain := seedLocalCodesignIdentity(t, setup.Home)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign", "security")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	calls := readCodesignCalls(t, codesignLog)
	if !strings.Contains(calls, "-s "+localCodesignIdentity+" --keychain "+keychain) {
		t.Fatalf("expected the artifact signed with the local identity, got codesign calls:\n%s", calls)
	}
	if strings.Contains(calls, "-s - -f") {
		t.Fatalf("expected no ad-hoc fallback once the identity signed it, got codesign calls:\n%s", calls)
	}
	golden.Equal(t, "outputs/download_darwin_signs_with_the_local_identity", normalize.Apply(result.Combined))
}

// Ad-hoc is the floor when the stable identity is unusable.
func testOutputsDownloadDarwinFallsBackToAdhocWhenTheIdentityFails(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	codesignLog := filepath.Join(stubs, "codesign-calls.log")
	fixture.StubBinaryWithScript(t, stubs, "codesign",
		"printf '%s\\n' \"$*\" >> '"+filepath.ToSlash(codesignLog)+"'\n"+
			"case \"$1\" in -d) exit 1 ;; esac\n"+
			// $2 is the signer: `-` (ad-hoc) succeeds, the local identity fails.
			"case \"$2\" in -) exit 0 ;; esac\n"+
			"exit 1\n")
	fixture.StubBinaryWithScript(t, stubs, "security", "exit 0")
	seedLocalCodesignIdentity(t, setup.Home)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign", "security")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if calls := readCodesignCalls(t, codesignLog); !strings.Contains(calls, "-s - -f") {
		t.Fatalf("expected the ad-hoc fallback, got codesign calls:\n%s", calls)
	}
	golden.Equal(t, "outputs/download_darwin_falls_back_to_adhoc_when_the_identity_fails", normalize.Apply(result.Combined))
}

func testOutputsDownloadDarwinSignsUniversalMacho(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-universal", universalMachOPayload, fixture.PodExecStubSpec{})
	codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-universal"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if calls := readCodesignCalls(t, codesignLog); !strings.Contains(calls, "-s - -f") {
		t.Fatalf("expected an ad-hoc signing call, got codesign calls:\n%s", calls)
	}
	golden.Equal(t, "outputs/download_darwin_signs_universal_macho", normalize.Apply(result.Combined))
}

func testOutputsDownloadDarwinLeavesASignedMachoAlone(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{AlreadySigned: true})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	calls := readCodesignCalls(t, codesignLog)
	if strings.Contains(calls, "-s - -f") {
		t.Fatalf("an already-signed artifact must not be re-signed, got codesign calls:\n%s", calls)
	}
	if !strings.Contains(calls, "-d ") {
		t.Fatalf("expected the display probe that decided the artifact was signed, got:\n%s", calls)
	}
	golden.Equal(t, "outputs/download_darwin_leaves_a_signed_macho_alone", normalize.Apply(result.Combined))
}

// No codesign stub is declared, so any attempt to run one fails on the scrubbed PATH.
func testOutputsDownloadDarwinSkipsANonMachoPayload(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "Report.class", javaClassPayload, fixture.PodExecStubSpec{})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "Report.class"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	info := requireDownloadedFile(t, filepath.Join(setup.Cwd, "Report.class"), javaClassPayload)
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("a non-Mach-O payload must not be made executable, got mode %v", info.Mode().Perm())
	}
	golden.Equal(t, "outputs/download_darwin_skips_a_non_macho_payload", normalize.Apply(result.Combined))
}

// No codesign stub is declared, so any attempt to run one fails.
func testOutputsDownloadLinuxHostDoesNotSign(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=linux")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	info := requireDownloadedFile(t, filepath.Join(setup.Cwd, "erun-darwin-arm64"), machOPayload)
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("a non-darwin host must leave the artifact untouched, got mode %v", info.Mode().Perm())
	}
	golden.Equal(t, "outputs/download_linux_host_does_not_sign", normalize.Apply(result.Combined))
}

// A failed codesign keeps the file and names the repair command.
func testOutputsDownloadDarwinCodesignFailureKeepsTheArtifact(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{
		SignExitCode: 1,
		SignStderr:   "codesign: erun-darwin-arm64: no identity found",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("a signing failure must not fail the download, exit %d: %s", result.ExitCode, result.Combined)
	}
	requireDownloadedFile(t, filepath.Join(setup.Cwd, "erun-darwin-arm64"), machOPayload)
	golden.Equal(t, "outputs/download_darwin_codesign_failure_keeps_the_artifact", normalize.Apply(result.Combined))
}

func testOutputsDownloadDarwinSignsUnsignedMachoJSON(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
	fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/download_darwin_signs_unsigned_macho_json", normalize.Apply(result.Combined))
}
