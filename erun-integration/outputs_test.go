package integration

import (
	"encoding/base64"
	"os"
	"path/filepath"
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
