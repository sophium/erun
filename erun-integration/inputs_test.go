package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// stubKubectlUploadAccepts answers the upload exec by draining stdin (the
// piped local file, discarded — the stub never really lands it in a pod) and
// reporting the canned size/sha256 a caller expects for that fixed payload.
// sha is the sha256 of the exact bytes the scenario writes as the local file.
func stubKubectlUploadAccepts(t *testing.T, stubs string, size int, sha string) {
	t.Helper()
	fixture.StubBinaryWithScript(t, stubs, "kubectl",
		"cat > /dev/null\n"+
			"printf '%s\\t%s\\n' '"+strconv.Itoa(size)+"' '"+sha+"'\n")
}

func TestInputs(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"inputs", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "inputs/help", normalize.Apply(result.Combined))
	})

	t.Run("upload_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"inputs", "upload", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "inputs/upload_help", normalize.Apply(result.Combined))
	})

	t.Run("upload_dry_run", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		local := filepath.Join(setup.Cwd, "evidence.bin")
		if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
			t.Fatalf("seed local file: %v", err)
		}
		result := erun.Run(t, []string{"inputs", "upload", local, "/home/erun/.erun/outputs/evidence.bin", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "inputs/upload_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("upload_missing_local_file", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		missing := filepath.Join(setup.Cwd, "does-not-exist.bin")
		result := erun.Run(t, []string{"inputs", "upload", missing, "/home/erun/.erun/outputs/evidence.bin", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a missing local file, got 0: %s", result.Combined)
		}
		golden.Equal(t, "inputs/upload_missing_local_file", normalize.Apply(result.Combined))
	})

	t.Run("upload_remote_path_must_be_absolute", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		local := filepath.Join(setup.Cwd, "evidence.bin")
		if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
			t.Fatalf("seed local file: %v", err)
		}
		result := erun.Run(t, []string{"inputs", "upload", local, "relative/path.bin", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a relative remote path, got 0: %s", result.Combined)
		}
		golden.Equal(t, "inputs/upload_remote_path_must_be_absolute", normalize.Apply(result.Combined))
	})

	t.Run("upload_real_run", func(t *testing.T) {
		// "hello"'s sha256 is fixed, so the stub can report it without actually
		// landing the file anywhere — this locks the argv/stdin wiring and the
		// checksum-comparison success path, not a real cluster transfer.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		local := filepath.Join(setup.Cwd, "evidence.bin")
		if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
			t.Fatalf("seed local file: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		stubKubectlUploadAccepts(t, stubs, 5, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"inputs", "upload", local, "/home/erun/.erun/outputs/evidence.bin"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "inputs/upload_real_run", normalize.Apply(result.Combined))
	})

	t.Run("upload_real_run_checksum_mismatch", func(t *testing.T) {
		// The stub reports a checksum that doesn't match the bytes sent, which
		// must fail the command clearly rather than report success.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		local := filepath.Join(setup.Cwd, "evidence.bin")
		if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
			t.Fatalf("seed local file: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		stubKubectlUploadAccepts(t, stubs, 5, "0000000000000000000000000000000000000000000000000000000000000000")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"inputs", "upload", local, "/home/erun/.erun/outputs/evidence.bin"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a checksum mismatch, got 0: %s", result.Combined)
		}
		golden.Equal(t, "inputs/upload_real_run_checksum_mismatch", normalize.Apply(result.Combined))
	})

	t.Run("upload_real_run_destination_not_writable", func(t *testing.T) {
		// The stub simulates the remote script's own refusal for an unwritable
		// destination directory, matching the exit code/stderr shape the real
		// script emits.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		local := filepath.Join(setup.Cwd, "evidence.bin")
		if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
			t.Fatalf("seed local file: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			"cat > /dev/null\n"+
				"echo 'erun-inputs: destination directory is not writable: /readonly' >&2\n"+
				"exit 3\n")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"inputs", "upload", local, "/readonly/evidence.bin"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an unwritable destination, got 0: %s", result.Combined)
		}
		golden.Equal(t, "inputs/upload_real_run_destination_not_writable", normalize.Apply(result.Combined))
	})
}
