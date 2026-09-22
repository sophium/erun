package eruncommon

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	entrypointPath  = "../erun-devops/docker/erun-devops/entrypoint.sh"
	cloudContextTSV = "../erun-devops/docker/erun-devops/cloud_context_defaults.tsv"
)

// The entrypoint's shell twin of the cloud-context defaults. These match the
// expressions that write the runtime config's `cloudcontexts[0]` entry; the
// test evaluates them in a real shell rather than re-deriving them, so it fails
// if the entrypoint changes rather than only if this package does.
var (
	cloudContextNameExpression = regexp.MustCompile(`(?m)^\s*cloud_context_name=(.+)$`)
	kubernetesContextField     = regexp.MustCompile(`(?m)^\s*kubernetescontext: (.+)$`)
)

// The runtime entrypoint re-derives in shell the cloud-context name and
// kubernetes-context defaults that this package owns through
// ResolveInjectedRuntimeConfig and NormalizeCloudContextConfig. Two
// implementations of one default is the coupling that failed silently once:
// `doctor --sync-config` reported phantom drift on every run, never
// reached InSync, and nothing went red -- the tool simply never converged.
//
// This test pins both sides to the shared expectations table in
// cloud_context_defaults.tsv. The entrypoint's expressions are not restated
// here; they are read out of entrypoint.sh and executed, so editing the shell
// fallback alone fails this test too, not just entrypoint_test.sh's
// end-to-end case (which asserts the same table against the config file the
// entrypoint actually writes).
func TestCloudContextDefaultsMatchEntrypoint(t *testing.T) {
	rows := readCloudContextDefaults(t, cloudContextTSV)
	if len(rows) == 0 {
		t.Fatalf("%s yielded no cases; the shared fixture must not be empty", cloudContextTSV)
	}

	for _, row := range rows {
		t.Run(row.label, func(t *testing.T) {
			env := cloudContextEnv(row)
			assertEntrypointExpressions(t, row, env)
			assertInjectedProjection(t, row, env)
		})
	}
}

// cloudContextEnv is the ERUN_* environment the entrypoint and the injected
// projection both read. The provider, alias, and region are only there to make
// the Go side emit a cloud context at all.
func cloudContextEnv(row cloudContextDefaultRow) map[string]string {
	env := map[string]string{
		"ERUN_TENANT":               "team",
		"ERUN_ENVIRONMENT":          "dev",
		"ERUN_CLOUD_PROVIDER":       "aws",
		"ERUN_CLOUD_PROVIDER_ALIAS": "operator@aws",
		"ERUN_CLOUD_REGION":         "us-east-1",
	}
	// "-" is the fixture's "unset": the variable is absent, not empty, because
	// `:-` treats an empty value the same but a future `-` fallback would not.
	if row.cloudContextName != "-" {
		env["ERUN_CLOUD_CONTEXT_NAME"] = row.cloudContextName
	}
	if row.kubernetesContext != "-" {
		env["ERUN_KUBERNETES_CONTEXT"] = row.kubernetesContext
	}
	return env
}

// assertEntrypointExpressions checks the shell twin as the entrypoint actually
// writes it, rather than against a restatement of the fallback.
func assertEntrypointExpressions(t *testing.T, row cloudContextDefaultRow, env map[string]string) {
	t.Helper()
	if got := evalEntrypointExpression(t, cloudContextNameExpression, env); got != row.expectedName {
		t.Errorf("entrypoint cloud_context_name = %q, want %q", got, row.expectedName)
	}
	if got := evalEntrypointExpression(t, kubernetesContextField, env); got != row.expectedKubernetesContext {
		t.Errorf("entrypoint kubernetescontext = %q, want %q", got, row.expectedKubernetesContext)
	}
}

// assertInjectedProjection checks the Go path the runtime config is synced
// against, which is the side the drift was fixed on.
func assertInjectedProjection(t *testing.T, row cloudContextDefaultRow, env map[string]string) {
	t.Helper()
	injected, ok := ResolveInjectedRuntimeConfig(func(key string) string { return env[key] })
	if !ok {
		t.Fatal("tenant and environment are set, so the projection must resolve")
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("expected exactly one injected cloud context, got %d", len(injected.Contexts))
	}
	if got := injected.Contexts[0].Name; got != row.expectedName {
		t.Errorf("injected cloud context name = %q, want %q", got, row.expectedName)
	}
	if got := injected.Contexts[0].KubernetesContext; got != row.expectedKubernetesContext {
		t.Errorf("injected kubernetes context = %q, want %q", got, row.expectedKubernetesContext)
	}
}

// evalEntrypointExpression pulls the expression the entrypoint assigns for this
// value out of entrypoint.sh and evaluates it in a real shell with exactly the
// given environment. It deliberately does not restate the fallback: a shell
// edit that changes the default has to change this result.
func evalEntrypointExpression(t *testing.T, pattern *regexp.Regexp, env map[string]string) string {
	t.Helper()

	raw, err := os.ReadFile(entrypointPath)
	if err != nil {
		t.Fatalf("read the entrypoint: %v", err)
	}
	match := pattern.FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("%s no longer contains an expression matching %s; if the entrypoint moved or "+
			"renamed it, update this pattern in the same change", entrypointPath, pattern)
	}

	script := filepath.Join(t.TempDir(), "eval.sh")
	if err := os.WriteFile(script, []byte("set -eu\nprintf '%s' "+match[1]+"\n"), 0o600); err != nil {
		t.Fatalf("write the evaluation script: %v", err)
	}

	// cmd.Env is set explicitly rather than inherited, so an unset fixture
	// column is genuinely unset for the shell expansion.
	cmdEnv := make([]string, 0, len(env))
	for key, value := range env {
		cmdEnv = append(cmdEnv, key+"="+value)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = cmdEnv

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("evaluate %q from the entrypoint: %v", match[1], err)
	}
	return string(out)
}

type cloudContextDefaultRow struct {
	label                     string
	cloudContextName          string
	kubernetesContext         string
	expectedName              string
	expectedKubernetesContext string
}

// readCloudContextDefaults parses the fixture shared with the entrypoint test.
// Blank lines and #-prefixed comments are skipped; a row that does not carry
// all five columns is reported rather than silently truncated.
func readCloudContextDefaults(t *testing.T, path string) []cloudContextDefaultRow {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared cloud-context fixture: %v", err)
	}

	var rows []cloudContextDefaultRow
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("%s:%d: expected 5 tab-separated columns, got %d: %q", path, i+1, len(fields), line)
		}
		rows = append(rows, cloudContextDefaultRow{
			label:                     fields[0],
			cloudContextName:          fields[1],
			kubernetesContext:         fields[2],
			expectedName:              fields[3],
			expectedKubernetesContext: fields[4],
		})
	}
	return rows
}
