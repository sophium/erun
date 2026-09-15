package eruncommon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubKubectlScript answers by namespace so each case below is one namespace's
// real shape: two that list Secrets, one whose access is denied, and one that
// does not exist.
const stubKubectlScript = `#!/bin/sh
ns=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--namespace" ]; then ns="$a"; fi
  prev="$a"
done
case "$ns" in
  team-alpha)
    printf '%s' '{"items":[
      {"metadata":{"name":"erun-claude-gateway"},"data":{"token":"aGVsbG8="}},
      {"metadata":{"name":"tenant-claude-gateway"},"data":{"token":"aGVsbG8="},"stringData":{"refresh":"x"}}
    ]}'
    ;;
  team-beta)
    printf '%s' '{"items":[{"metadata":{"name":"erun-claude-gateway"},"data":{"token":"aGVsbG8="}}]}'
    ;;
  team-denied)
    printf '%s\n' 'Error from server (Forbidden): secrets is forbidden' >&2
    exit 1
    ;;
  *)
    printf '%s\n' "Error from server (NotFound): namespaces \"$ns\" not found" >&2
    exit 1
    ;;
esac
`

// stubKubectl puts a fake kubectl first on PATH for the duration of the test.
func stubKubectl(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a POSIX shell script; the read itself is platform-neutral")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(path, []byte(stubKubectlScript), 0o755); err != nil {
		t.Fatalf("write the kubectl stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListGatewayCredentialCandidatesUnionsNamespaces(t *testing.T) {
	stubKubectl(t)
	got, problems := ListGatewayCredentialCandidates([]SecretNamespace{
		{Namespace: "team-alpha", Context: "ctx-a"},
		{Namespace: "team-beta", Context: "ctx-b"},
	})
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	// Sorted by name, so a picker presents the same list every time.
	if got[0].Name != "erun-claude-gateway" || got[1].Name != "tenant-claude-gateway" {
		t.Fatalf("candidates not name-ordered: %+v", got)
	}
	// A Secret sitting in both namespaces names both: the catalog is erun-level,
	// so the name has to exist wherever it is used.
	if strings.Join(got[0].Namespaces, ",") != "team-alpha,team-beta" {
		t.Fatalf("first candidate namespaces = %v", got[0].Namespaces)
	}
	if strings.Join(got[1].Namespaces, ",") != "team-alpha" {
		t.Fatalf("second candidate namespaces = %v", got[1].Namespaces)
	}
	// Key names are offered so the token's key can be picked, and they are
	// collected across both data and stringData.
	if strings.Join(got[1].Keys, ",") != "refresh,token" {
		t.Fatalf("second candidate keys = %v", got[1].Keys)
	}
}

func TestListGatewayCredentialCandidatesReportsUnreadableNamespaces(t *testing.T) {
	stubKubectl(t)
	got, problems := ListGatewayCredentialCandidates([]SecretNamespace{
		{Namespace: "team-alpha"},
		{Namespace: "team-denied"},
	})
	// The readable namespace still contributes: one denial must not empty the list.
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	// A Secret whose access is denied must not look like a Secret that is
	// missing, so the namespace is named.
	if len(problems) != 1 || !strings.Contains(problems[0], "team-denied") {
		t.Fatalf("problems = %v, want one naming team-denied", problems)
	}
	if !strings.Contains(problems[0], "Forbidden") {
		t.Fatalf("the reason was dropped: %v", problems[0])
	}
}

func TestListGatewayCredentialCandidatesTreatsMissingNamespaceAsEmpty(t *testing.T) {
	stubKubectl(t)
	got, problems := ListGatewayCredentialCandidates([]SecretNamespace{
		{Namespace: "team-absent"},
		{Namespace: "   "},
	})
	// An environment may be configured before it is deployed, so a namespace
	// that does not exist is not an error.
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}
