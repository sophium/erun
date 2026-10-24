package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorRemoteInitScenarios(t *testing.T) {
	t.Run("finish_remote_init_real_run_prompts_url_and_retries_clone", testDoctorFinishRemoteInitRealRunPromptsUrlAndRetriesClone)
	t.Run("finish_remote_init_codecommit_real_run_prompts_key_id", testDoctorFinishRemoteInitCodecommitRealRunPromptsKeyId)
}

func testDoctorFinishRemoteInitRealRunPromptsUrlAndRetriesClone(t *testing.T) {
	// Real-run only: these arms depend on side effects. One prompt per subprocess,
	// since readline read-ahead eats the rest of stdin.
	setup := env.New(t)
	projectRoot := filepath.Join(setup.Home, "git", "team")
	seedRemoteInitMarker(t, setup, "team", "dev",
		"tenant: team\n"+
			"environment: dev\n"+
			"project_root: "+projectRoot+"\n"+
			"bootstrap_complete: false\n")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubSSHKeygenWritingKeypair(t, stubs)
	stubGitForRemoteInit(t, stubs, true)
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true",
		"ERUN_TENANT=team",
		"ERUN_ENVIRONMENT=dev",
		"ERUN_REPO_PATH="+projectRoot,
	)
	envVars = append(envVars, fixture.StubEnv(stubs, "ssh-keygen", "git")...)
	result := erun.Run(t, []string{"doctor", "--finish-remote-init"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "git@example.com:team/repo.git\n",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/finish_remote_init_real_run_prompts_url_and_retries_clone", normalize.Apply(result.Combined))
	marker := readFileForTest(t, filepath.Join(setup.Home, ".erun", "team", "dev", "bootstrap.yaml"))
	for _, want := range []string{
		"repository_url: git@example.com:team/repo.git",
		"bootstrap_complete: true",
	} {
		if !strings.Contains(marker, want) {
			t.Errorf("expected marker to contain %q, got:\n%s", want, marker)
		}
	}
	assertFileMode(t, filepath.Join(setup.Home, ".ssh", "id_ed25519"), 0o600)
	assertFileMode(t, filepath.Join(setup.Home, ".ssh", "id_ed25519.pub"), 0o644)
}

func testDoctorFinishRemoteInitCodecommitRealRunPromptsKeyId(t *testing.T) {
	// The CodeCommit key-ID prompt is reachable only here: in init it is always the
	// second prompt.
	setup := env.New(t)
	projectRoot := filepath.Join(setup.Home, "git", "petios")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	sshDir := filepath.Join(setup.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("seeded\n"), 0o600); err != nil {
		t.Fatalf("write ssh key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAASEEDPUB user@example\n"), 0o644); err != nil {
		t.Fatalf("write ssh pub key: %v", err)
	}
	seedRemoteInitMarker(t, setup, "petios", "dev",
		"tenant: petios\n"+
			"environment: dev\n"+
			"project_root: "+projectRoot+"\n"+
			"bootstrap_complete: false\n")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubSSHKeygenWritingKeypair(t, stubs)
	stubGitForRemoteInit(t, stubs, false)
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true",
		"ERUN_TENANT=petios",
		"ERUN_ENVIRONMENT=dev",
		"ERUN_REPO_PATH="+projectRoot,
	)
	envVars = append(envVars, fixture.StubEnv(stubs, "ssh-keygen", "git")...)
	result := erun.Run(t, []string{
		"doctor", "--finish-remote-init",
		"--remote-repository-url", "ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios",
	}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "APKAEXAMPLEKEYID\n",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/finish_remote_init_codecommit_real_run_prompts_key_id", normalize.Apply(result.Combined))
	marker := readFileForTest(t, filepath.Join(setup.Home, ".erun", "petios", "dev", "bootstrap.yaml"))
	for _, want := range []string{
		"repository_url: ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios",
		"codecommit_host: git-codecommit.eu-west-1.amazonaws.com",
		"codecommit_ssh_key_id: APKAEXAMPLEKEYID",
		"bootstrap_complete: true",
	} {
		if !strings.Contains(marker, want) {
			t.Errorf("expected marker to contain %q, got:\n%s", want, marker)
		}
	}
	sshConfig := readFileForTest(t, filepath.Join(sshDir, "config"))
	for _, want := range []string{
		"Host git-codecommit.eu-west-1.amazonaws.com",
		"User APKAEXAMPLEKEYID",
		"IdentityFile ~/.ssh/id_rsa_codecommit",
	} {
		if !strings.Contains(sshConfig, want) {
			t.Errorf("expected ~/.ssh/config to contain %q, got:\n%s", want, sshConfig)
		}
	}
	assertFileMode(t, filepath.Join(sshDir, "id_rsa_codecommit"), 0o600)
}

// stubGitForRemoteInit always clones; with failFirstLsRemote the first ls-remote
// fails once, tracked in a marker file.
func stubGitForRemoteInit(t *testing.T, stubsDir string, failFirstLsRemote bool) {
	t.Helper()
	lines := []string{`case "$*" in`, `  *"ls-remote"*)`}
	if failFirstLsRemote {
		attempted := filepath.Join(stubsDir, "ls-remote-attempted")
		lines = append(lines,
			`    if [ ! -f '`+attempted+`' ]; then`,
			`      : > '`+attempted+`'`,
			`      echo 'fatal: Could not read from remote repository.' >&2`,
			`      exit 128`,
			`    fi`,
		)
	}
	lines = append(lines,
		`    printf 'stub-ref\tHEAD\n'`,
		`    exit 0`,
		`    ;;`,
		`esac`,
		`exit 0`,
	)
	fixture.StubBinaryWithScript(t, stubsDir, "git", strings.Join(lines, "\n"))
}

// stubSSHKeygenWritingKeypair writes the keypair at -f, with the key type
// selecting the public-key text.
func stubSSHKeygenWritingKeypair(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`for arg in "$@"; do keyfile="$arg"; done`,
		`mkdir -p "$(dirname "$keyfile")"`,
		`printf 'stub-private-key\n' > "$keyfile"`,
		`case "$*" in`,
		`  *" rsa "*) printf 'ssh-rsa AAAASTUBRSAKEY erun-doctor-test\n' > "$keyfile.pub" ;;`,
		`  *) printf 'ssh-ed25519 AAAASTUBEDKEY erun-doctor-test\n' > "$keyfile.pub" ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "ssh-keygen", script)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	// Windows has no Unix permission bits.
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("stat %s: %v", path, err)
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("expected %s mode %o, got %o", path, want, got)
	}
}
