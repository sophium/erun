package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDoctorInspectionDryRunTracesWaitAndDindExec(t *testing.T) {
	trace := new(bytes.Buffer)
	ctx := Context{
		Logger: NewLoggerWithWriters(2, trace, trace),
		DryRun: true,
		Stdout: trace,
		Stderr: trace,
	}
	req := ShellLaunchParams{
		Tenant:            "tenant-a",
		Environment:       "local",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	}

	if _, err := RunDoctorInspection(ctx, nil, req); err != nil {
		t.Fatalf("RunDoctorInspection failed: %v", err)
	}

	output := trace.String()
	for _, want := range []string{
		"kubectl --context cluster-local --namespace tenant-a-local wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-local --namespace tenant-a-local exec -c erun-dind deployment/tenant-a-devops -- /bin/sh -lc '<remote-script>'",
		"docker system df",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected trace to contain %q, got:\n%s", want, output)
		}
	}
}

func TestRunDoctorActionExecutesPruneScriptInDindContainer(t *testing.T) {
	kubectlDir := t.TempDir()
	logPath := filepath.Join(kubectlDir, "kubectl.log")
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	script := `#!/bin/sh
if [ "$1" = "--context" ]; then
  shift 4
fi
if [ "$1" = "wait" ]; then
  exit 0
fi
if [ "$1" = "exec" ] && [ "$2" = "-c" ] && [ "$3" = "erun-dind" ]; then
  printf '%s\n' "$@" >> "` + logPath + `"
  printf '%s' "$8" > "` + filepath.Join(kubectlDir, "script.sh") + `"
  printf 'pruned\n'
  exit 0
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := ShellLaunchParams{
		Tenant:            "tenant-a",
		Environment:       "local",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	}
	result, err := RunDoctorAction(Context{}, nil, req, DoctorActionPruneImages)
	if err != nil {
		t.Fatalf("RunDoctorAction failed: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "pruned" {
		t.Fatalf("unexpected stdout: %q", result.Stdout)
	}
	scriptData, err := os.ReadFile(filepath.Join(kubectlDir, "script.sh"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(scriptData), "docker image prune -a -f") {
		t.Fatalf("expected prune script, got:\n%s", scriptData)
	}
}

func TestDoctorActionPromptLabelIncludesTarget(t *testing.T) {
	label := DoctorActionPromptLabel(DoctorActionPruneBuildCache, OpenResult{
		Tenant:      "tenant-a",
		Environment: "dev",
	})
	if !strings.Contains(label, "tenant-a/dev") {
		t.Fatalf("expected target in prompt label, got %q", label)
	}
}

func TestRunDoctorInspectionReportsNamespaceLookupInconsistency(t *testing.T) {
	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	script := `#!/bin/sh
while [ "$1" = "--context" ] || [ "$1" = "--namespace" ]; do
  shift 2
done
if [ "$1" = "wait" ]; then
  echo 'Error from server (NotFound): namespaces "tenant-a-local" not found' >&2
  exit 1
fi
if [ "$1" = "get" ] && [ "$2" = "namespaces" ] && [ "$3" = "-o" ] && [ "$4" = "name" ]; then
  printf 'namespace/default\nnamespace/tenant-a-local\n'
  exit 0
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := ShellLaunchParams{
		Tenant:            "tenant-a",
		Environment:       "local",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	}

	_, err := RunDoctorInspection(Context{}, nil, req)
	if err == nil {
		t.Fatal("expected RunDoctorInspection to fail")
	}
	if !strings.Contains(err.Error(), `runtime namespace "tenant-a-local" is listed, but direct Kubernetes API access is failing`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDoctorActionReportsDiskIOIssue(t *testing.T) {
	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	script := `#!/bin/sh
while [ "$1" = "--context" ] || [ "$1" = "--namespace" ]; do
  shift 2
done
if [ "$1" = "wait" ]; then
  exit 0
fi
if [ "$1" = "exec" ] && [ "$2" = "-c" ] && [ "$3" = "erun-dind" ]; then
  echo 'Error from server: rpc error: code = Unknown desc = disk I/O error: input/output error' >&2
  exit 1
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := ShellLaunchParams{
		Tenant:            "tenant-a",
		Environment:       "local",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	}
	_, err := RunDoctorAction(Context{}, nil, req, DoctorActionPruneImages)
	if err == nil {
		t.Fatal("expected RunDoctorAction to fail")
	}
	if !strings.Contains(err.Error(), "local Kubernetes storage for tenant-a/local is unhealthy") {
		t.Fatalf("unexpected error: %v", err)
	}
}
