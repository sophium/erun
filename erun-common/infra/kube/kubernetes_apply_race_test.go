package kube

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Parallel deploy components apply one shared Secret; a kubectl stub forces the create race.
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

// kubectlStubScript models get/create/update with an AlreadyExists refusal. All
// participants rendezvous before creating so the overlap is deterministic; `set -C`
// gives an O_EXCL create and each invocation publishes its own manifest file.
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

// writeStatefulKubectlStub points ERUN_KUBECTL_BIN at the rendered script and returns its state dir.
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

// isolateKubectlSecretApplyMode forces the subprocess path regardless of local config.
func isolateKubectlSecretApplyMode(t *testing.T) {
	t.Helper()
	restore := setConfigHomeForModeTest(t)
	t.Cleanup(restore)
}

// kubectlStubRefusals counts refused creates; a missing file means zero.
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

func applySharedSecretFromTwoComponents(t *testing.T) string {
	t.Helper()
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, func(dir string) string { return kubectlStubScript(dir, 2) })
	args := KubectlApplyStdinArgs("team-dev", "orbstack")

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = ApplySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, args)
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

// assertSharedSecretApplied expects exactly one refusal: the loser's retry takes the update path.
func assertSharedSecretApplied(t *testing.T, dir string) {
	t.Helper()
	// Guards against passing without ever racing.
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

func TestConcurrentAppliesOfOneSharedSecretBothSucceed(t *testing.T) {
	assertSharedSecretApplied(t, applySharedSecretFromTwoComponents(t))
}

// AlreadyExists is not success on its own: a changed credential must still land.
func TestSecretApplyUpdatesAnExistingSharedSecret(t *testing.T) {
	isolateKubectlSecretApplyMode(t)
	dir := writeStatefulKubectlStub(t, func(dir string) string { return kubectlStubScript(dir, 1) })
	stale := strings.Replace(sharedSecretManifest, "shared-token", "stale-token", 1)
	if err := os.WriteFile(filepath.Join(dir, "object"), []byte(stale), 0o600); err != nil {
		t.Fatalf("seed the existing secret: %v", err)
	}

	if err := ApplySecretManifest("orbstack", "team-dev", "gateway credentials secret", sharedSecretManifest, KubectlApplyStdinArgs("team-dev", "orbstack")); err != nil {
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
