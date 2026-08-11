package integration

import (
	"bytes"
	"encoding/base64"
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

// outputsListing is the canned `find` output the stubbed kubectl returns. Its
// input order deliberately differs from the golden's newest-first order, so the
// golden proves the resolver sorts.
const outputsListing = "f\t1024\t1700000200.0\treport.pdf\n" +
	"d\t4096\t1700000300.5\tresults\n" +
	"f\t512\t1700000100.0\tnotes.txt\n"

// stubKubectlPrints makes the outputs commands deterministic without a cluster:
// their only kubectl call is the remote find/tar/base64 exec, so one fixed
// stdout regardless of args is enough.
func stubKubectlPrints(t *testing.T, stubs, stdout string) {
	t.Helper()
	fixture.StubBinaryWithScript(t, stubs, "kubectl", "cat <<'EOF'\n"+stdout+"EOF\n")
}

// The download path classifies a payload by content, not by name, so these are
// the byte prefixes it decides on. A little-endian 64-bit Mach-O is what a
// darwin/arm64 cross-build produces; the universal header and the Java class
// both open 0xCAFEBABE and are told apart by the four bytes after it.
var (
	machOPayload          = append([]byte{0xCF, 0xFA, 0xED, 0xFE}, []byte("erun darwin arm64 artifact")...)
	universalMachOPayload = append([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00, 0x00, 0x02}, []byte("two slices")...)
	javaClassPayload      = append([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00, 0x00, 0x34}, []byte("not a mach-o")...)
)

// stubKubectlDownloads answers the download exec with one file payload.
func stubKubectlDownloads(t *testing.T, stubs string, payload []byte) {
	t.Helper()
	stubKubectlPrints(t, stubs, "file\n"+base64.StdEncoding.EncodeToString(payload)+"\n")
}

func requireDownloadedFile(t *testing.T, path string, want []byte) os.FileInfo {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("downloaded bytes = %q, want %q", data, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat downloaded file: %v", err)
	}
	return info
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

func TestOutputs(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"outputs", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/help", normalize.Apply(result.Combined))
	})

	t.Run("list_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"outputs", "list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/list_help", normalize.Apply(result.Combined))
	})

	t.Run("download_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"outputs", "download", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/download_help", normalize.Apply(result.Combined))
	})

	t.Run("list_dry_run", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"outputs", "list", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/list_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("list_dry_run_json", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"outputs", "list", "--dry-run", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/list_dry_run_json", normalize.Apply(result.Combined))
	})

	t.Run("list_real_run", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlPrints(t, stubs, outputsListing)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"outputs", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/list_real_run", normalize.Apply(result.Combined))
	})

	t.Run("list_real_run_json", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlPrints(t, stubs, outputsListing)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"outputs", "list", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/list_real_run_json", normalize.Apply(result.Combined))
	})

	t.Run("download_dry_run", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"outputs", "download", "report.pdf", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/download_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("download_real_run_file", func(t *testing.T) {
		// The reported sha256 normalizes to <HEX>, so the golden can't prove the
		// bytes; the file read is what confirms "hello" round-tripped.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlPrints(t, stubs, "file\n"+base64.StdEncoding.EncodeToString([]byte("hello"))+"\n")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"outputs", "download", "report.pdf"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		data, err := os.ReadFile(filepath.Join(setup.Cwd, "report.pdf"))
		if err != nil {
			t.Fatalf("read downloaded file: %v", err)
		}
		if string(data) != "hello" {
			t.Fatalf("downloaded bytes = %q, want %q", string(data), "hello")
		}
		golden.Equal(t, "outputs/download_real_run_file", normalize.Apply(result.Combined))
	})

	t.Run("download_real_run_dir", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlPrints(t, stubs, "dir\n"+base64.StdEncoding.EncodeToString([]byte("tarball-bytes"))+"\n")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"outputs", "download", "results"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(filepath.Join(setup.Cwd, "results.tar.gz")); err != nil {
			t.Fatalf("expected results.tar.gz to be written: %v", err)
		}
		golden.Equal(t, "outputs/download_real_run_dir", normalize.Apply(result.Combined))
	})

	// arm64 macOS SIGKILLs a Mach-O that carries no signature at all, silently, so
	// a darwin binary cross-built in the Linux pod has to be ad-hoc signed where
	// it lands. ERUN_HOST_OS_OVERRIDE pins the darwin branch so the scenario runs
	// everywhere; codesign is reached through its ERUN_CODESIGN_BIN seam.
	t.Run("download_darwin_signs_unsigned_macho", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, machOPayload)
		codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The artifact keeps its bytes and gains the execute bit a 0644 download
		// otherwise lacks — neither is visible in the captured streams.
		info := requireDownloadedFile(t, filepath.Join(setup.Cwd, "erun-darwin-arm64"), machOPayload)
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("expected the signed artifact to be executable, got mode %v", info.Mode().Perm())
		}
		if calls := readCodesignCalls(t, codesignLog); !strings.Contains(calls, "-s - -f") {
			t.Fatalf("expected an ad-hoc signing call, got codesign calls:\n%s", calls)
		}
		golden.Equal(t, "outputs/download_darwin_signs_unsigned_macho", normalize.Apply(result.Combined))
	})

	t.Run("download_darwin_signs_universal_macho", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, universalMachOPayload)
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
	})

	// An artifact somebody signed properly must not be quietly re-signed ad-hoc.
	t.Run("download_darwin_leaves_a_signed_macho_alone", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, machOPayload)
		codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{AlreadySigned: true})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The absence of a signing call is the whole point and leaves no trace in
		// the captured streams, so the stub's own log is the only witness.
		calls := readCodesignCalls(t, codesignLog)
		if strings.Contains(calls, "-s - -f") {
			t.Fatalf("an already-signed artifact must not be re-signed, got codesign calls:\n%s", calls)
		}
		if !strings.Contains(calls, "-d ") {
			t.Fatalf("expected the display probe that decided the artifact was signed, got:\n%s", calls)
		}
		golden.Equal(t, "outputs/download_darwin_leaves_a_signed_macho_alone", normalize.Apply(result.Combined))
	})

	// codesign has no business seeing a tarball, a text file, or a Java class —
	// and no codesign stub is declared here, so any attempt to run one fails the
	// scenario on the scrubbed PATH rather than passing silently.
	t.Run("download_darwin_skips_a_non_macho_payload", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, javaClassPayload)
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
	})

	// codesign exists only on macOS, and so does the problem. No codesign stub is
	// declared, so reaching for one would fail the scenario.
	t.Run("download_linux_host_does_not_sign", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, machOPayload)
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
	})

	// A codesign that fails is a diagnosable state, not a reason to lose the file:
	// the download still succeeds and the trace names the command that repairs it.
	t.Run("download_darwin_codesign_failure_keeps_the_artifact", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, machOPayload)
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
	})

	// The structured result carries the same outcome as the human trace, so an
	// orchestrator can see that the artifact it just pulled was made runnable.
	t.Run("download_darwin_signs_unsigned_macho_json", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubKubectlDownloads(t, stubs, machOPayload)
		fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "codesign")...)
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "outputs/download_darwin_signs_unsigned_macho_json", normalize.Apply(result.Combined))
	})

	t.Run("download_traversal_neutralized", func(t *testing.T) {
		// A parent-traversal entry name must resolve inside the outputs dir and
		// can never reach /etc/passwd.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"outputs", "download", "../../etc/passwd", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The audit line echoes the raw arg the operator typed — that is input,
		// not the resolved target — so /etc/passwd in the golden is expected.
		golden.Equal(t, "outputs/download_traversal_neutralized", normalize.Apply(result.Combined))
	})

	t.Run("download_invalid_name_rejected", func(t *testing.T) {
		// A bare ".." is not a valid entry name and is rejected before any pod
		// contact.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"outputs", "download", "..", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for invalid name, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "outputs/download_invalid_name_rejected", normalize.Apply(result.Combined))
	})
}
