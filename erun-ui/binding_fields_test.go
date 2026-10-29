package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

const bindingFieldListsGolden = "testdata/binding_field_lists.golden"

// TestBindingFieldListsAreUnchanged runs the Wails binding generator the way
// `go tool wails generate module` does (build with the bindings tag, run it
// against a wails.json) into a temp dir, and compares the generated field and
// function lists with every type name normalized away. A Go type moving
// between packages renames its TS class, which is expected; a field or
// signature changing is not.
func TestBindingFieldListsAreUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the desktop with the bindings tag")
	}
	models, app := generateWailsBindings(t)
	got := normalizedBindingFieldLists(models, app)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(bindingFieldListsGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(bindingFieldListsGolden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("generated binding field lists changed:\n%s", lineDiff(string(want), got))
	}
}

// generateWailsBindings returns the generated models.ts and main/App.d.ts.
func generateWailsBindings(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "wailsbindings")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// go test puts its own GOROOT/bin first on PATH.
	build := exec.Command("go", "build", "-buildvcs=false", "-tags", "bindings", "-o", bin, ".")
	// Under the test HOME the go command would fill a fresh, read-only module
	// cache in a temp dir that is never removed.
	build.Env = append(append(withoutEnvKeys(os.Environ(), testHomeKeys), realHomeEnv...), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bindings generator: %v\n%s", err, out)
	}
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "wails.json"), []byte(`{"name":"bindings","wailsjsdir":"./out"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := exec.Command(bin)
	gen.Dir = project
	gen.Env = append(os.Environ(), "tsoutputtype=classes")
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate bindings: %v\n%s", err, out)
	}
	goDir := filepath.Join(project, "out", "wailsjs", "go")
	models, err := os.ReadFile(filepath.Join(goDir, "models.ts"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(filepath.Join(goDir, "main", "App.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(models), string(app)
}

var (
	tsClassStart = regexp.MustCompile(`^\s*export class \w+ \{$`)
	tsField      = regexp.MustCompile(`^\s+(\w+\??): (.+);$`)
	tsFunction   = regexp.MustCompile(`^export function (\w+)\((.*)\):(.+);$`)
	tsParamName  = regexp.MustCompile(`\barg\d+:`)
	tsTypeName   = regexp.MustCompile(`[A-Za-z_$][\w$]*(\.[A-Za-z_$][\w$]*)*`)
)

var tsBuiltinTypes = map[string]bool{
	"string": true, "number": true, "boolean": true, "any": true, "void": true,
	"Promise": true, "Array": true, "Record": true, "key": true, "null": true,
	"undefined": true, "Error": true,
}

func normalizeTSType(s string) string {
	return tsTypeName.ReplaceAllStringFunc(s, func(name string) string {
		if tsBuiltinTypes[name] {
			return name
		}
		return "#"
	})
}

// normalizedBindingFieldLists reduces models.ts to one line per class (its
// fields in order) and App.d.ts to one line per function, sorted, with every
// class name replaced by "#".
func normalizedBindingFieldLists(models, app string) string {
	var classes []string
	var fields []string
	inClass := false
	for _, line := range strings.Split(models, "\n") {
		switch {
		case tsClassStart.MatchString(line):
			inClass, fields = true, nil
		case inClass && strings.Contains(line, "static createFrom"):
			classes = append(classes, "class {"+strings.Join(fields, "; ")+"}")
			inClass = false
		case inClass:
			if m := tsField.FindStringSubmatch(line); m != nil {
				fields = append(fields, m[1]+": "+normalizeTSType(m[2]))
			}
		}
	}
	sort.Strings(classes)
	var functions []string
	for _, line := range strings.Split(app, "\n") {
		if m := tsFunction.FindStringSubmatch(line); m != nil {
			params := tsParamName.ReplaceAllString(m[2], "")
			functions = append(functions, m[1]+"("+normalizeTSType(params)+"): "+normalizeTSType(m[3]))
		}
	}
	sort.Strings(functions)
	return "# functions\n" + strings.Join(functions, "\n") + "\n\n# classes\n" + strings.Join(classes, "\n") + "\n"
}

func withoutEnvKeys(env, keys []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(keys, name) {
			out = append(out, entry)
		}
	}
	return out
}
