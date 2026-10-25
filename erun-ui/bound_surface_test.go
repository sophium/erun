package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const boundSurfaceGolden = "testdata/bound_surface.golden"

// TestBoundSurfaceIsUnchanged pins what Wails and the headless server bind:
// every exported *App method, by name, with the JSON shape of each argument and
// result. Shapes are structural and struct ids are content hashes, so moving a
// type to another package or renaming it passes, while any change a frontend
// could observe fails. Regenerate with UPDATE_GOLDEN=1 only for an intended
// contract change.
func TestBoundSurfaceIsUnchanged(t *testing.T) {
	got := describeBoundSurface(reflect.TypeOf(&App{}))
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(boundSurfaceGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(boundSurfaceGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(boundSurfaceGolden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("the bound *App surface changed; Wails method names and JSON shapes are the frontend contract:\n%s", lineDiff(string(want), got))
	}
}

func describeBoundSurface(app reflect.Type) string {
	d := &shapeDescriber{defs: map[string]string{}, memo: map[reflect.Type]string{}}
	var methods []string
	// Same rule as headlessserver.collectExportedMethods and Wails' binder.
	for i := 0; i < app.NumMethod(); i++ {
		m := app.Method(i)
		if !m.IsExported() {
			continue
		}
		var in, out []string
		for j := 1; j < m.Type.NumIn(); j++ {
			in = append(in, d.shape(m.Type.In(j), nil))
		}
		for j := 0; j < m.Type.NumOut(); j++ {
			out = append(out, d.shape(m.Type.Out(j), nil))
		}
		methods = append(methods, fmt.Sprintf("%s(%s) (%s)", m.Name, strings.Join(in, ", "), strings.Join(out, ", ")))
	}
	sort.Strings(methods)
	ids := make([]string, 0, len(d.defs))
	for id := range d.defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	fmt.Fprintf(&b, "# %d bound methods\n", len(methods))
	for _, m := range methods {
		b.WriteString(m + "\n")
	}
	b.WriteString("\n# struct shapes\n")
	for _, id := range ids {
		b.WriteString(id + " = " + d.defs[id] + "\n")
	}
	return b.String()
}

type shapeDescriber struct {
	defs map[string]string
	memo map[reflect.Type]string
}

var (
	errorType         = reflect.TypeOf((*error)(nil)).Elem()
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
)

func (d *shapeDescriber) shape(t reflect.Type, stack []reflect.Type) string {
	if t == errorType {
		return "error"
	}
	if t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) {
		return "marshaler(" + t.String() + ")"
	}
	if describe, ok := compositeShapes[t.Kind()]; ok {
		return describe(d, t, stack)
	}
	return t.Kind().String()
}

type shapeFunc func(d *shapeDescriber, t reflect.Type, stack []reflect.Type) string

// compositeShapes is filled in init because its funcs recurse through shape.
var compositeShapes map[reflect.Kind]shapeFunc

func init() {
	compositeShapes = map[reflect.Kind]shapeFunc{
		reflect.Pointer: func(d *shapeDescriber, t reflect.Type, stack []reflect.Type) string {
			return "*" + d.shape(t.Elem(), stack)
		},
		reflect.Slice: func(d *shapeDescriber, t reflect.Type, stack []reflect.Type) string {
			if t.Elem().Kind() == reflect.Uint8 {
				return "bytes"
			}
			return "[]" + d.shape(t.Elem(), stack)
		},
		reflect.Array: func(d *shapeDescriber, t reflect.Type, stack []reflect.Type) string {
			return fmt.Sprintf("[%d]%s", t.Len(), d.shape(t.Elem(), stack))
		},
		reflect.Map: func(d *shapeDescriber, t reflect.Type, stack []reflect.Type) string {
			return "map[" + d.shape(t.Key(), stack) + "]" + d.shape(t.Elem(), stack)
		},
		reflect.Interface: func(_ *shapeDescriber, t reflect.Type, _ []reflect.Type) string {
			if t.NumMethod() == 0 {
				return "any"
			}
			return "interface"
		},
		reflect.Func: (*shapeDescriber).funcShape,
		reflect.Chan: func(d *shapeDescriber, t reflect.Type, stack []reflect.Type) string {
			return "chan " + d.shape(t.Elem(), stack)
		},
		reflect.Struct: (*shapeDescriber).structShape,
	}
}

func (d *shapeDescriber) funcShape(t reflect.Type, stack []reflect.Type) string {
	var in, out []string
	for i := 0; i < t.NumIn(); i++ {
		s := d.shape(t.In(i), stack)
		if t.IsVariadic() && i == t.NumIn()-1 {
			s = "..." + strings.TrimPrefix(s, "[]")
		}
		in = append(in, s)
	}
	for i := 0; i < t.NumOut(); i++ {
		out = append(out, d.shape(t.Out(i), stack))
	}
	return "func(" + strings.Join(in, ", ") + ") (" + strings.Join(out, ", ") + ")"
}

func (d *shapeDescriber) structShape(t reflect.Type, stack []reflect.Type) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == t {
			return fmt.Sprintf("^%d", len(stack)-i)
		}
	}
	if id, ok := d.memo[t]; ok {
		return id
	}
	body := "{" + strings.Join(d.fields(t, append(stack, t)), ", ") + "}"
	sum := sha256.Sum256([]byte(body))
	id := "S" + hex.EncodeToString(sum[:])[:10]
	d.defs[id] = body
	d.memo[t] = id
	return id
}

// fields lists a struct's fields as encoding/json sees them, inlining untagged
// embedded structs.
func (d *shapeDescriber) fields(t reflect.Type, stack []reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := f.Type
		if f.Anonymous && name == "" {
			et := ft
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				out = append(out, d.fields(et, stack)...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if opts != "" {
			name += "," + opts
		}
		out = append(out, name+": "+d.shape(ft, stack))
	}
	return out
}

func lineDiff(want, got string) string {
	wantSet := map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		wantSet[l] = true
	}
	gotSet := map[string]bool{}
	for _, l := range strings.Split(got, "\n") {
		gotSet[l] = true
	}
	var b strings.Builder
	for _, l := range strings.Split(want, "\n") {
		if !gotSet[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !wantSet[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	return b.String()
}
