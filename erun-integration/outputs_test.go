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
	"regexp"
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
// archiveSizePattern is the byte count `erun outputs download` reports for a
// downloaded archive.
var archiveSizePattern = regexp.MustCompile(`\(\d+B, `)

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

func TestOutputs(t *testing.T) {
	t.Parallel()
	t.Run("help", testOutputsHelp)
	t.Run("list_help", testOutputsListHelp)
	t.Run("download_help", testOutputsDownloadHelp)
	t.Run("list_dry_run", testOutputsListDryRun)
	t.Run("list_dry_run_json", testOutputsListDryRunJSON)
	t.Run("list_real_run", testOutputsListRealRun)
	t.Run("list_real_run_json", testOutputsListRealRunJSON)
	t.Run("download_dry_run", testOutputsDownloadDryRun)
	t.Run("download_real_run_file", testOutputsDownloadRealRunFile)
	t.Run("download_real_run_dir", testOutputsDownloadRealRunDir)
	t.Run("download_darwin_signs_unsigned_macho", testOutputsDownloadDarwinSignsUnsignedMacho)
	t.Run("download_darwin_signs_with_the_local_identity", testOutputsDownloadDarwinSignsWithTheLocalIdentity)
	t.Run("download_darwin_falls_back_to_adhoc_when_the_identity_fails", testOutputsDownloadDarwinFallsBackToAdhocWhenTheIdentityFails)
	t.Run("download_darwin_signs_universal_macho", testOutputsDownloadDarwinSignsUniversalMacho)
	t.Run("download_darwin_leaves_a_signed_macho_alone", testOutputsDownloadDarwinLeavesASignedMachoAlone)
	t.Run("download_darwin_skips_a_non_macho_payload", testOutputsDownloadDarwinSkipsANonMachoPayload)
	t.Run("download_linux_host_does_not_sign", testOutputsDownloadLinuxHostDoesNotSign)
	t.Run("download_darwin_codesign_failure_keeps_the_artifact", testOutputsDownloadDarwinCodesignFailureKeepsTheArtifact)
	t.Run("download_darwin_signs_unsigned_macho_json", testOutputsDownloadDarwinSignsUnsignedMachoJSON)
	t.Run("download_large_file_arrives_when_one_stream_cannot_carry_it", testOutputsDownloadLargeFileArrivesWhenOneStreamCannotCarryIt)
	t.Run("download_retries_a_range_whose_stream_breaks", testOutputsDownloadRetriesARangeWhoseStreamBreaks)
	t.Run("download_reports_how_far_it_got_when_a_range_never_arrives", testOutputsDownloadReportsHowFarItGotWhenARangeNeverArrives)
	t.Run("download_large_file_arrives_from_a_pod_without_gzip", testOutputsDownloadLargeFileArrivesFromAPodWithoutGzip)
	t.Run("download_traversal_neutralized", testOutputsDownloadTraversalNeutralized)
	t.Run("download_invalid_name_rejected", testOutputsDownloadInvalidNameRejected)
}

func testOutputsHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"outputs", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/help", normalize.Apply(result.Combined))
}

func testOutputsListHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"outputs", "list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/list_help", normalize.Apply(result.Combined))
}

func testOutputsDownloadHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"outputs", "download", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/download_help", normalize.Apply(result.Combined))
}

func testOutputsListDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"outputs", "list", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/list_dry_run", normalize.Apply(result.Combined))
}

func testOutputsListDryRunJSON(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"outputs", "list", "--dry-run", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/list_dry_run_json", normalize.Apply(result.Combined))
}

func testOutputsListRealRun(t *testing.T) {
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
}

func testOutputsListRealRunJSON(t *testing.T) {
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
}

func testOutputsDownloadDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"outputs", "download", "report.pdf", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "outputs/download_dry_run", normalize.Apply(result.Combined))
}

func testOutputsDownloadRealRunFile(t *testing.T) {
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
}

func testOutputsDownloadRealRunDir(t *testing.T) {
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
	// The archive is built by the host's own tar, and GNU and BSD tar write
	// the same entries at different sizes, so its byte count says nothing
	// about erun: the entries above are what the download is checked by.
	golden.Equal(t, "outputs/download_real_run_dir", archiveSizePattern.ReplaceAllString(normalize.Apply(result.Combined), "(<SIZE>, "))
}

// The regression this file exists to hold: a single exec stream breaks as the
// volume it carries grows, so a whole-file transfer of a real cross-built
// binary never completes. Measured on the reporting host, a 12 MB payload
// transferred 6/6 and a 14 MB one 1/4, so the emulator refuses to carry more
// than 12 MiB in one call — the largest amount observed to be reliable. A 20
// MB payload (0/6 there) therefore cannot arrive in one stream and can only
// arrive at all if the download reads it in bounded ranges.
func testOutputsDownloadLargeFileArrivesWhenOneStreamCannotCarryIt(t *testing.T) {
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
}

// A broken stream is a transport fault, not a fact about the bytes, so the
// range is re-read rather than the download lost. The emulator breaks the
// first read of the second range only, which no single-stream transfer could
// have recovered from at all.
func testOutputsDownloadRetriesARangeWhoseStreamBreaks(t *testing.T) {
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
}

// The reported failure was silent about its cause: a bare stream EOF reads
// like a stale tunnel and sends an operator to diagnose the wrong thing. A
// range that never arrives must therefore say how much of the transfer had
// already succeeded, and must not leave a partial file behind.
func testOutputsDownloadReportsHowFarItGotWhenARangeNeverArrives(t *testing.T) {
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
}

// The pod says how it encoded each range, so a pod without gzip still serves
// a download — it just sends more bytes for the same payload.
func testOutputsDownloadLargeFileArrivesFromAPodWithoutGzip(t *testing.T) {
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
}

func testOutputsDownloadTraversalNeutralized(t *testing.T) {
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
}

func testOutputsDownloadInvalidNameRejected(t *testing.T) {
	// A bare ".." is not a valid entry name and is rejected before any pod
	// contact.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"outputs", "download", "..", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for invalid name, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "outputs/download_invalid_name_rejected", normalize.Apply(result.Combined))
}
