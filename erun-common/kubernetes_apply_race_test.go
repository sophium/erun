package eruncommon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Deploy runs the components of one step in parallel, and a Secret the
// components share -- the gateway credential, the Cloudflare token, the
// image-pull credential -- is applied by each of them into the same namespace.
// On first apply two components therefore both read "absent" and both take the
// create path, and the API server refuses the loser with AlreadyExists. That
// refusal used to fail the losing component, and with it the environment's
// upgrade, leaving the environment half-rolled -- one component at the new
// version and its sibling not. These tests stand a kubectl in for the API
// server so the overlap is forced rather than hoped for.
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

// kubectlStubScript is a kubectl stand-in that models the apply behaviour this
// file needs: read the manifest from stdin, GET (the object file), take the
// create path when it is absent, and report the API server's AlreadyExists
// refusal when a sibling invocation claimed the create first. The update path
// is modelled too, so a re-apply of an existing object still lands its content
// -- which is what distinguishes a retry from treating the refusal as success.
//
// participants is how many invocations must have reached the create before any
// of them may create. The rendezvous makes the overlap deterministic instead of
// timing-dependent: every invocation announces itself, then waits for the
// others, so no invocation can win the create before its sibling has also read
// "absent". The wait is bounded so a broken test cannot hang.
func kubectlStubScript(dir string, participants int) string {
	return `#!/bin/sh
set -u
dir="` + dir + `"
participants=` + strconv.Itoa(participants) + `
cat > "$dir/manifest"
echo x >> "$dir/attempts"
if [ -f "$dir/object" ]; then
  cp "$dir/manifest" "$dir/object"
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
if mkdir "$dir/claimed" 2>/dev/null; then
  cp "$dir/manifest" "$dir/object"
  exit 0
fi
echo x >> "$dir/refusals"
echo 'Error from server (AlreadyExists): error when creating "STDIN": secrets "erun-claude-gateway" already exists' >&2
exit 1
`
}

// writeStatefulKubectlStub points the ERUN_KUBECTL_BIN seam at the script script
// renders for the directory its state lives in, and returns that directory.
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

// isolateKubectlSecretApplyMode keeps the apply on the subprocess path these
// tests stand a kubectl in for, whatever the machine running them has in its
// own erun config.
func isolateKubectlSecretApplyMode(t *testing.T) {
	t.Helper()
	restore := setConfigHomeForModeTest(t)
	t.Cleanup(restore)
}

// kubectlStubRefusals is how many creates the stub refused, which is how a
// test proves the overlap it meant to force really happened rather than
// passing because the two applies never met. A missing file is zero, not a
// read failure: it is the no-overlap case this counter exists to report, and
// the caller's own message is the one that explains it.
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

// kubectlStubAttempts is how many times the stub was invoked, which is how a
// test sees whether an apply was retried.
func kubectlStubAttempts(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "attempts"))
	if err != nil {
		t.Fatalf("read stub attempts: %v", err)
	}
	return len(strings.Fields(string(raw)))
}

// TestConcurrentAppliesOfOneSharedSecretBothSucceed is the reported failure:
// two components of one parallel deploy step apply the same shared secret, the
// loser of the create race is refused with AlreadyExists, and the component --
// and with it the environment's upgrade -- fails while its sibling succeeds.
//
// The overlap is forced by the stub's rendezvous, so the loser is guaranteed
// to lose before the fix and to be retried into the update path after it.
func TestConcurrentAppliesOfOneSharedSecretBothSucceed(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, func(dir string) string { return kubectlStubScript(dir, 2) })
	args := kubectlApplyStdinArgs("team-dev", "orbstack")

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = applySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, args)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("component %d failed on a shared secret its sibling also applied: %v", i+1, err)
		}
	}
	// Without this the test could pass by never racing at all, which is the
	// one way a test for a race can quietly stop testing anything.
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

// A refusal that never stops being a refusal is still an error, reported after
// the bounded number of attempts. This is what separates a retry from treating
// AlreadyExists as success: an object that never becomes what the manifest asks
// for must fail the component rather than be reported as applied.
func TestSecretApplySurfacesACreateRefusalThatNeverStops(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, alwaysRefusingKubectlStub(`Error from server (AlreadyExists): error when creating "STDIN": secrets "erun-claude-gateway" already exists`))

	err := applySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, kubectlApplyStdinArgs("team-dev", "orbstack"))
	if err == nil {
		t.Fatal("a create refusal that every attempt hit was reported as success")
	}
	if !strings.Contains(err.Error(), "AlreadyExists") {
		t.Fatalf("the refusal was not reported: %v", err)
	}
	if got := kubectlStubAttempts(t, dir); got != secretApplyCreateRaceAttempts {
		t.Fatalf("attempts = %d, want %d", got, secretApplyCreateRaceAttempts)
	}
}

// alwaysRefusingKubectlStub is a kubectl that counts its invocations and then
// fails with message on every one of them.
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

// Any other refusal is a genuine failure of this apply and must surface on its
// first occurrence, not be retried behind the operator's back.
func TestSecretApplyDoesNotRetryAnUnrelatedFailure(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, alwaysRefusingKubectlStub(`Error from server (Forbidden): error when creating "STDIN": secrets "erun-claude-gateway" is forbidden: User cannot create resource`))

	err := applySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, kubectlApplyStdinArgs("team-dev", "orbstack"))
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

// An apply of a Secret that already exists must still land this manifest's
// content. "Already exists" is not success on its own: when the credential has
// changed, the object has to change with it.
func TestSecretApplyUpdatesAnExistingSharedSecret(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, func(dir string) string { return kubectlStubScript(dir, 1) })
	stale := strings.Replace(sharedSecretManifest, "shared-token", "stale-token", 1)
	if err := os.WriteFile(filepath.Join(dir, "object"), []byte(stale), 0o600); err != nil {
		t.Fatalf("seed the existing secret: %v", err)
	}

	if err := applySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, kubectlApplyStdinArgs("team-dev", "orbstack")); err != nil {
		t.Fatalf("apply onto an existing secret: %v", err)
	}
	applied, err := os.ReadFile(filepath.Join(dir, "object"))
	if err != nil {
		t.Fatalf("read the applied secret: %v", err)
	}
	if string(applied) != sharedSecretManifest {
		t.Fatalf("the existing secret kept its stale content:\n got: %s\nwant: %s", applied, sharedSecretManifest)
	}
}
