package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/kube"
)

// Parallel components apply a shared Secret, so one create loses with AlreadyExists and must
// retry as an update. A stub kubectl forces the overlap.
const sharedSecretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: erun-claude-gateway
  namespace: team-dev
  labels:
    app.kubernetes.io/managed-by: erun-deploy
type: Opaque
stringData:
  token: "shared-token"
`

// kubectlStubScript models get/create/update with AlreadyExists. Invocations rendezvous until
// participants have read "absent", making the overlap deterministic. `set -C` (O_EXCL) makes
// the object visible the instant one create wins, as on a real API server.
func kubectlStubScript(dir string, participants int) string {
	return `#!/bin/sh
set -u
dir="` + dir + `"
participants=` + strconv.Itoa(participants) + `
manifest="$dir/manifest.$$"
cat > "$manifest"
echo x >> "$dir/attempts"
if [ -f "$dir/object" ]; then
  cp "$manifest" "$dir/object"
  exit 0
fi
: > "$dir/entered-$$"
i=0
while :; do
  count=0
  for f in "$dir"/entered-*; do
    [ -e "$f" ] && count=$((count+1))
  done
  [ "$count" -ge "$participants" ] && break
  i=$((i+1))
  [ "$i" -gt 3000 ] && break
  sleep 0.01
done
if (set -C; cat "$manifest" > "$dir/object") 2>/dev/null; then
  exit 0
fi
echo x >> "$dir/refusals"
echo 'Error from server (AlreadyExists): error when creating "STDIN": secrets "erun-claude-gateway" already exists' >&2
exit 1
`
}

func writeStatefulKubectlStub(t *testing.T, script func(dir string) string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl-stub")
	if err := os.WriteFile(path, []byte(script(dir)), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
	return dir
}

// isolateKubectlSecretApplyMode keeps the apply on the subprocess path regardless of local config.
func isolateKubectlSecretApplyMode(t *testing.T) {
	t.Helper()
	restore := setConfigHomeForModeTest(t)
	t.Cleanup(restore)
}

// kubectlStubRefusals proves the forced overlap happened; a missing file is zero.
func kubectlStubRefusals(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "refusals"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read stub refusals: %v", err)
	}
	return len(strings.Fields(string(raw)))
}

func kubectlStubAttempts(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "attempts"))
	if err != nil {
		t.Fatalf("read stub attempts: %v", err)
	}
	return len(strings.Fields(string(raw)))
}

func applySharedSecretFromTwoComponents(t *testing.T) string {
	t.Helper()
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, func(dir string) string { return kubectlStubScript(dir, 2) })
	args := kube.KubectlApplyStdinArgs("team-dev", "orbstack")

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = kube.ApplySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, args)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("component %d failed on a shared secret its sibling also applied: %v", i+1, err)
		}
	}
	return dir
}

// Exactly one refusal is the property: the loser's retry reads the object and updates.
func assertSharedSecretApplied(t *testing.T, dir string) {
	t.Helper()
	// Otherwise the test could pass without racing at all.
	if got := kubectlStubRefusals(t, dir); got != 1 {
		t.Fatalf("the stub refused %d creates, want exactly 1: the two applies did not overlap", got)
	}
	applied, err := os.ReadFile(filepath.Join(dir, "object"))
	if err != nil {
		t.Fatalf("the shared secret was never applied: %v", err)
	}
	if string(applied) != sharedSecretManifest {
		t.Fatalf("the shared secret does not hold the applied manifest:\n got: %s\nwant: %s", applied, sharedSecretManifest)
	}
}

// TestSharedSecretApplyIsNotRefusedWhileTheWinnersCreateIsStillPublishing: forces the loser's retry
// into the window between the winner's create and its publish.
func TestSharedSecretApplyIsNotRefusedWhileTheWinnersCreateIsStillPublishing(t *testing.T) {
	slowStubPublish(t, 200*time.Millisecond)
	assertSharedSecretApplied(t, applySharedSecretFromTwoComponents(t))
}

// slowStubPublish shims a sleeping `cp` onto PATH to hold the stub between create and publish.
func slowStubPublish(t *testing.T, hold time.Duration) {
	t.Helper()
	realCP, err := exec.LookPath("cp")
	if err != nil {
		t.Fatalf("locate cp: %v", err)
	}
	dir := t.TempDir()
	shim := "#!/bin/sh\nsleep " + strconv.FormatFloat(hold.Seconds(), 'f', 3, 64) + "\nexec " + shellSingleQuote(realCP) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "cp"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write cp shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSecretApplySurfacesACreateRefusalThatNeverStops(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, alwaysRefusingKubectlStub(`Error from server (AlreadyExists): error when creating "STDIN": secrets "erun-claude-gateway" already exists`))

	err := kube.ApplySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, kube.KubectlApplyStdinArgs("team-dev", "orbstack"))
	if err == nil {
		t.Fatal("a create refusal that every attempt hit was reported as success")
	}
	if !strings.Contains(err.Error(), "AlreadyExists") {
		t.Fatalf("the refusal was not reported: %v", err)
	}
	if got := kubectlStubAttempts(t, dir); got != kube.SecretApplyCreateRaceAttempts {
		t.Fatalf("attempts = %d, want %d", got, kube.SecretApplyCreateRaceAttempts)
	}
}

func alwaysRefusingKubectlStub(message string) func(dir string) string {
	return func(dir string) string {
		return `#!/bin/sh
cat > "` + dir + `/manifest"
echo x >> "` + dir + `/attempts"
echo ` + shellSingleQuote(message) + ` >&2
exit 1
`
	}
}

func TestSecretApplyDoesNotRetryAnUnrelatedFailure(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, alwaysRefusingKubectlStub(`Error from server (Forbidden): error when creating "STDIN": secrets "erun-claude-gateway" is forbidden: User cannot create resource`))

	err := kube.ApplySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, kube.KubectlApplyStdinArgs("team-dev", "orbstack"))
	if err == nil {
		t.Fatal("a forbidden apply was reported as success")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("the refusal was not reported: %v", err)
	}
	if got := kubectlStubAttempts(t, dir); got != 1 {
		t.Fatalf("attempts = %d, want 1: an unrelated failure was retried", got)
	}
}
