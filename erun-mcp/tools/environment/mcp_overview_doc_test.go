package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

func repoRootForOverviewDocTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	for dir := filepath.Dir(file); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "erun-docs")); err == nil {
			return dir
		}
	}
	t.Fatal("no erun-docs directory above " + file)
	return ""
}

func environmentExampleBody(t *testing.T, docPath string, lines []string) []string {
	t.Helper()
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "### `environment`") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: no \"### `environment`\" heading found", docPath)
	}

	fence := -1
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		if strings.HasPrefix(trimmed, "```") {
			fence = i
			break
		}
	}
	if fence < 0 {
		t.Fatalf("%s: the environment section carries no fenced example block", docPath)
	}

	for i := fence + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			return lines[fence+1 : i]
		}
	}
	t.Fatalf("%s: the environment section's fenced example block is never closed", docPath)
	return nil
}

// jsonValueKind compares documented examples by shape, not value.
func jsonValueKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// compareDocumentedJSONPaths reports paths the example shows that the encoder
// does not emit with the same name and shape; omitted paths are fine.
func compareDocumentedJSONPaths(t *testing.T, docPath, path string, documented, actual any) {
	t.Helper()
	documentedObject, ok := documented.(map[string]any)
	if !ok {
		return
	}
	actualObject, _ := actual.(map[string]any)
	for _, key := range sortedJSONKeys(documentedObject) {
		actualValue, present := actualObject[key]
		if !present {
			t.Errorf("%s: the environment example names %q, which the resolved model never emits; the example is the contract a hand-written client reads these names from", docPath, path+"."+key)
			continue
		}
		if documentedKind, actualKind := jsonValueKind(documentedObject[key]), jsonValueKind(actualValue); documentedKind != actualKind {
			t.Errorf("%s: the environment example types %q as %s, but the resolved model emits %s", docPath, path+"."+key, documentedKind, actualKind)
			continue
		}
		compareDocumentedJSONPaths(t, docPath, path+"."+key, documentedObject[key], actualValue)
	}
}

func sortedJSONKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Native clients hand-write DTOs from this example, so a documented field the
// encoder does not emit decodes silently to zero.
func TestMCPOverviewEnvironmentExampleMatchesTheResolvedJSON(t *testing.T) {
	root := repoRootForOverviewDocTest(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "mcp", "overview.md")

	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	example := strings.Join(environmentExampleBody(t, docPath, strings.Split(string(data), "\n")), "\n")

	var documented map[string]any
	if err := json.Unmarshal([]byte(example), &documented); err != nil {
		t.Fatalf("%s: the environment example is not valid JSON: %v", docPath, err)
	}

	// Populate every field the example names, including omitempty ones.
	policy := model.EnvironmentIdlePolicy{Timeout: 5 * time.Minute, WorkingHours: "09:00-19:00"}
	fixture := lifecycle.EnvironmentReadModel{
		Tenant: "myapp",
		Environment: lifecycle.ListEnvironmentResult{
			Name:           "local",
			Type:           config.EnvironmentTypeLocalAgent,
			RuntimeVersion: "1.0.308",
			ManagedCloud:   true,
			IsDefault:      true,
			IsEffective:    true,
		},
		State: model.EnvironmentLifecycleRunning,
		Idle:  &model.EnvironmentIdleStatus{Policy: policy, StopEligible: true},
		Health: &model.EnvironmentHealth{
			RootConfig: model.RootConfigInspection{ConfigStatus: model.RootConfigStatusOK},
			Deploy:     model.DeployDiagnosisResult{HelmStatus: "STATUS: deployed"},
		},
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal the resolved environment read model: %v", err)
	}
	var resolved any
	if err := json.Unmarshal(encoded, &resolved); err != nil {
		t.Fatalf("decode the resolved environment read model: %v", err)
	}

	compareDocumentedJSONPaths(t, docPath, "environment", documented, resolved)
}
