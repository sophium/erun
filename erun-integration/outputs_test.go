package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand/v2"
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

// podOutputsDir is where the pod keeps agent outputs. The emulator resolves a
// pod path under a stand-in filesystem root, so a scenario stages its payload
// at exactly the path erun will ask the pod for.
const podOutputsDir = "home/erun/.erun/outputs"

// stubPodExecDownload stages one payload inside a stand-in pod filesystem and
// routes erun's kubectl at the emulator that serves it. A fixed-stdout stub
// cannot answer a download: it asks the pod what it would send, then reads the
// payload one bounded range at a time.
func stubPodExecDownload(t *testing.T, setup env.Setup, stubs, name string, payload []byte, spec fixture.PodExecStubSpec) []string {
	t.Helper()
	spec.Root = filepath.Join(setup.Cwd, "pod")
	staged := filepath.Join(spec.Root, filepath.FromSlash(podOutputsDir), name)
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatalf("stage the pod outputs directory: %v", err)
	}
	if err := os.WriteFile(staged, payload, 0o644); err != nil {
		t.Fatalf("stage the pod payload: %v", err)
	}
	fixture.StubPodExec(t, stubs, spec)
	return fixture.StubEnv(stubs, "kubectl")
}

// stubPodExecDownloadDir stages a folder rather than a file, so the download
// takes the branch that archives it in the pod first.
func stubPodExecDownloadDir(t *testing.T, setup env.Setup, stubs, name string, files map[string]string) []string {
	t.Helper()
	root := filepath.Join(setup.Cwd, "pod")
	staged := filepath.Join(root, filepath.FromSlash(podOutputsDir), name)
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatalf("stage the pod outputs folder: %v", err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(staged, file), []byte(body), 0o644); err != nil {
			t.Fatalf("stage %s: %v", file, err)
		}
	}
	fixture.StubPodExec(t, stubs, fixture.PodExecStubSpec{Root: root})
	return fixture.StubEnv(stubs, "kubectl")
}

// incompressiblePayload is the shape the bug report probed with: bytes gzip
// cannot shrink, so a range's stream carries its full base64 expansion and the
// transfer has no compression to hide behind.
func incompressiblePayload(size int) []byte {
	payload := make([]byte, size)
	source := rand.NewChaCha8([32]byte{'e', 'r', 'u', 'n'})
	_, _ = source.Read(payload)
	return payload
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// localCodesignIdentity and localCodesignKeychainFile mirror the constants
// erun-common declares (this module imports nothing, by design). A host that
// carries the keychain has erun's stable signing identity, which is what makes a
// macOS privacy grant survive the next build.
const (
	localCodesignIdentity     = "ERun Local Development"
	localCodesignKeychainFile = "erun-local-signing.keychain-db"
)

// seedLocalCodesignIdentity puts the local signing keychain on the scenario's
// host, the way a developer's first desktop build would.
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

func requireArchiveEntries(t *testing.T, path string, want []string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the downloaded archive: %v", err)
	}
	defer func() { _ = file.Close() }()
	zip, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("read the downloaded archive: %v", err)
	}
	reader := tar.NewReader(zip)
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("walk the downloaded archive: %v", err)
		}
		names = append(names, header.Name)
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
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
	t.Parallel()
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
		envVars := append(setup.Env(), stubPodExecDownload(t, setup, stubs, "report.pdf", []byte("hello"), fixture.PodExecStubSpec{})...)
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
		envVars := append(setup.Env(), stubPodExecDownloadDir(t, setup, stubs, "results", map[string]string{"summary.txt": "done\n"})...)
		result := erun.Run(t, []string{"outputs", "download", "results"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The archive is what proves the pod staged the folder and the download
		// read that staged copy, not the folder itself.
		requireArchiveEntries(t, filepath.Join(setup.Cwd, "results.tar.gz"), []string{"results/", "results/summary.txt"})
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
		stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
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

	// macOS pins a privacy grant to the identity that signed the code, and an
	// ad-hoc signature has none — so it pins the code-directory hash instead and
	// the next build silently drops the grant. A host that already carries erun's
	// local signing identity therefore signs with it, and says which signer it
	// used, because a grant that vanishes is otherwise unattributable.
	t.Run("download_darwin_signs_with_the_local_identity", func(t *testing.T) {
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
	})

	// A keychain that turns out not to hold the identity must not cost the
	// artifact its signature: macOS SIGKILLs an unsigned Mach-O, so ad-hoc is the
	// floor even when the stable identity is unusable.
	t.Run("download_darwin_falls_back_to_adhoc_when_the_identity_fails", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", machOPayload, fixture.PodExecStubSpec{})
		codesignLog := filepath.Join(stubs, "codesign-calls.log")
		fixture.StubBinaryWithScript(t, stubs, "codesign",
			"printf '%s\\n' \"$*\" >> '"+filepath.ToSlash(codesignLog)+"'\n"+
				"case \"$1\" in -d) exit 1 ;; esac\n"+
				// The signer is the second argument: `-` is ad-hoc and succeeds,
				// the local identity is the one this host cannot actually use.
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
	})

	t.Run("download_darwin_signs_universal_macho", func(t *testing.T) {
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
	})

	// An artifact somebody signed properly must not be quietly re-signed ad-hoc.
	t.Run("download_darwin_leaves_a_signed_macho_alone", func(t *testing.T) {
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
	})

	// codesign exists only on macOS, and so does the problem. No codesign stub is
	// declared, so reaching for one would fail the scenario.
	t.Run("download_linux_host_does_not_sign", func(t *testing.T) {
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
	})

	// A codesign that fails is a diagnosable state, not a reason to lose the file:
	// the download still succeeds and the trace names the command that repairs it.
	t.Run("download_darwin_codesign_failure_keeps_the_artifact", func(t *testing.T) {
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
	})

	// The structured result carries the same outcome as the human trace, so an
	// orchestrator can see that the artifact it just pulled was made runnable.
	t.Run("download_darwin_signs_unsigned_macho_json", func(t *testing.T) {
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
	})

	// The regression this file exists to hold: a single exec stream breaks as the
	// volume it carries grows, so a whole-file transfer of a real cross-built
	// binary never completes. Measured on the reporting host, a 12 MB payload
	// transferred 6/6 and a 14 MB one 1/4, so the emulator refuses to carry more
	// than 12 MiB in one call — the largest amount observed to be reliable. A 20
	// MB payload (0/6 there) therefore cannot arrive in one stream and can only
	// arrive at all if the download reads it in bounded ranges.
	t.Run("download_large_file_arrives_when_one_stream_cannot_carry_it", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		payload := incompressiblePayload(20 * 1000 * 1000)
		envVars := append(setup.Env(), stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", payload, fixture.PodExecStubSpec{
			MaxStreamBytes: 12 * 1024 * 1024,
		})...)
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Normalization collapses the digest the command prints, so the file's
		// own hash is the only thing that can prove the bytes are the pod's.
		want := sha256.Sum256(payload)
		if got := fileSHA256(t, filepath.Join(setup.Cwd, "erun-darwin-arm64")); got != hex.EncodeToString(want[:]) {
			t.Fatalf("downloaded sha256 = %s, want %s", got, hex.EncodeToString(want[:]))
		}
		golden.Equal(t, "outputs/download_large_file_arrives_when_one_stream_cannot_carry_it", normalize.Apply(result.Combined))
	})

	// A broken stream is a transport fault, not a fact about the bytes, so the
	// range is re-read rather than the download lost. The emulator breaks the
	// first read of the second range only, which no single-stream transfer could
	// have recovered from at all.
	t.Run("download_retries_a_range_whose_stream_breaks", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		payload := incompressiblePayload(10 * 1000 * 1000)
		envVars := append(setup.Env(), stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", payload, fixture.PodExecStubSpec{
			MaxStreamBytes: 12 * 1024 * 1024,
			FailOnceAt:     []int64{8 * 1024 * 1024},
		})...)
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		want := sha256.Sum256(payload)
		if got := fileSHA256(t, filepath.Join(setup.Cwd, "erun-darwin-arm64")); got != hex.EncodeToString(want[:]) {
			t.Fatalf("downloaded sha256 = %s, want %s", got, hex.EncodeToString(want[:]))
		}
		golden.Equal(t, "outputs/download_retries_a_range_whose_stream_breaks", normalize.Apply(result.Combined))
	})

	// The reported failure was silent about its cause: a bare stream EOF reads
	// like a stale tunnel and sends an operator to diagnose the wrong thing. A
	// range that never arrives must therefore say how much of the transfer had
	// already succeeded, and must not leave a partial file behind.
	t.Run("download_reports_how_far_it_got_when_a_range_never_arrives", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		envVars := append(setup.Env(), stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", incompressiblePayload(10*1000*1000), fixture.PodExecStubSpec{
			FailAlwaysAt: []int64{8 * 1024 * 1024},
		})...)
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit when a range never arrives:\n%s", result.Combined)
		}
		if _, err := os.Stat(filepath.Join(setup.Cwd, "erun-darwin-arm64")); !os.IsNotExist(err) {
			t.Fatalf("a failed download must leave no partial file, stat gave %v", err)
		}
		golden.Equal(t, "outputs/download_reports_how_far_it_got_when_a_range_never_arrives", normalize.Apply(result.Combined))
	})

	// The pod says how it encoded each range, so a pod without gzip still serves
	// a download — it just sends more bytes for the same payload.
	t.Run("download_large_file_arrives_from_a_pod_without_gzip", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		payload := incompressiblePayload(10 * 1000 * 1000)
		envVars := append(setup.Env(), stubPodExecDownload(t, setup, stubs, "erun-darwin-arm64", payload, fixture.PodExecStubSpec{
			MaxStreamBytes: 12 * 1024 * 1024,
			RawEncoding:    true,
		})...)
		result := erun.Run(t, []string{"outputs", "download", "erun-darwin-arm64"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		want := sha256.Sum256(payload)
		if got := fileSHA256(t, filepath.Join(setup.Cwd, "erun-darwin-arm64")); got != hex.EncodeToString(want[:]) {
			t.Fatalf("downloaded sha256 = %s, want %s", got, hex.EncodeToString(want[:]))
		}
		golden.Equal(t, "outputs/download_large_file_arrives_from_a_pod_without_gzip", normalize.Apply(result.Combined))
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
