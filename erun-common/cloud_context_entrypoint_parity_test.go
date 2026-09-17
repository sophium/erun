package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The runtime entrypoint re-derives the cloud-context name/kubernetes-context
// defaults in shell (entrypoint.sh) that this package owns in Go. Two
// implementations of one default is exactly the coupling that failed silently
// in #1662: `doctor --sync-config` reported phantom drift forever because the
// injected projection and the on-disk config disagreed on the default, and
// nothing went red.
//
// Both sides now read cloud_context_defaults.tsv, so a change to either
// fallback without the matching change to the other fails here or in
// erun-devops/docker/erun-devops/entrypoint_test.sh (which asserts the same
// table against the entrypoint's emitted config file).
func TestCloudContextDefaultsMatchEntrypoint(t *testing.T) {
	fixture := filepath.Join("..", "erun-devops", "docker", "erun-devops", "cloud_context_defaults.tsv")
	rows := readCloudContextDefaults(t, fixture)
	if len(rows) == 0 {
		t.Fatalf("%s yielded no cases; the shared fixture must not be empty", fixture)
	}

	for _, row := range rows {
		t.Run(row.label, func(t *testing.T) {
			env := map[string]string{
				"ERUN_TENANT":               "team",
				"ERUN_ENVIRONMENT":          "dev",
				"ERUN_CLOUD_PROVIDER":       "aws",
				"ERUN_CLOUD_PROVIDER_ALIAS": "operator@aws",
				"ERUN_CLOUD_REGION":         "us-east-1",
			}
			if row.cloudContextName != "-" {
				env["ERUN_CLOUD_CONTEXT_NAME"] = row.cloudContextName
			}
			if row.kubernetesContext != "-" {
				env["ERUN_KUBERNETES_CONTEXT"] = row.kubernetesContext
			}

			injected, ok := ResolveInjectedRuntimeConfig(func(key string) string { return env[key] })
			if !ok {
				t.Fatal("tenant and environment are set, so the projection must resolve")
			}
			if len(injected.Contexts) != 1 {
				t.Fatalf("expected exactly one injected cloud context, got %d", len(injected.Contexts))
			}

			context := injected.Contexts[0]
			if context.Name != row.expectedName {
				t.Errorf("cloud context name = %q, want %q (entrypoint.sh emits the same value; "+
					"update both sides and the shared fixture together)", context.Name, row.expectedName)
			}
			if context.KubernetesContext != row.expectedKubernetesContext {
				t.Errorf("kubernetes context = %q, want %q (entrypoint.sh emits the same value; "+
					"update both sides and the shared fixture together)", context.KubernetesContext, row.expectedKubernetesContext)
			}
		})
	}
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
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
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
