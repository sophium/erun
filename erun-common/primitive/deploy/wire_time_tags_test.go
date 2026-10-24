package deploy

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// wireTimeOmitzeroRoots are the JSON roots machine consumers read.
var wireTimeOmitzeroRoots = []any{
	model.EnvironmentIdleStatus{},
	model.EnvironmentActivitySnapshot{},
	model.EnvironmentIdleMarkerClient{},
	model.EnvironmentActivityLease{},
	model.EnvironmentStopPending{},
	model.EnvironmentStopHistoryEntry{},
	shared.EnvironmentJob{},
	model.AISessionStatus{},
	RuntimeUsageHistory{},
	model.HostCredentialsRefresh{},
	model.HostCredentialsStatus{},
}

// omitempty never fires on a value time.Time, so an unset one would publish
// as 0001-01-01T00:00:00Z.
func TestWireTimeFieldsOmitZeroNotOmitEmpty(t *testing.T) {
	for _, root := range wireTimeOmitzeroRoots {
		found := findOmitemptyTimeFields(reflect.TypeOf(root), map[reflect.Type]bool{})
		if len(found) == 0 {
			continue
		}
		t.Errorf("%s is rendered by --output json and must use omitzero, not omitempty:", reflect.TypeOf(root).Name())
		for _, field := range found {
			t.Errorf("  %s", field)
		}
	}
}

// Positive control for the walker; omitempty on a pointer is fine.
func TestWireTimeFieldGuardDetectsViolation(t *testing.T) {
	type planted struct {
		Never time.Time `json:"never,omitempty"`
	}
	found := findOmitemptyTimeFields(reflect.TypeOf(planted{}), map[reflect.Type]bool{})
	if len(found) != 1 || !strings.Contains(found[0], "planted.Never") {
		t.Fatalf("guard did not report a value time.Time tagged omitempty, got %v; it would pass a real violation", found)
	}
	type plantedPointer struct {
		Optional *time.Time `json:"optional,omitempty"`
	}
	if found := findOmitemptyTimeFields(reflect.TypeOf(plantedPointer{}), map[reflect.Type]bool{}); len(found) != 0 {
		t.Errorf("guard reported a pointer time.Time: %v", found)
	}
	type plantedNested struct {
		Inner []struct {
			Never time.Time `json:"never,omitempty"`
		} `json:"inner"`
	}
	if found := findOmitemptyTimeFields(reflect.TypeOf(plantedNested{}), map[reflect.Type]bool{}); len(found) != 1 || !strings.Contains(found[0], "Never") {
		t.Errorf("guard did not descend into a slice of structs, got %v", found)
	}
}

func wireStructElement(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
		typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	return typ
}

func findOmitemptyTimeFields(typ reflect.Type, seen map[reflect.Type]bool) []string {
	typ = wireStructElement(typ)
	if typ.Kind() != reflect.Struct || seen[typ] {
		return nil
	}
	seen[typ] = true
	timeType := reflect.TypeOf(time.Time{})
	var found []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Type == timeType {
			if tag := field.Tag.Get("json"); strings.Contains(tag, ",omitempty") {
				found = append(found, typ.Name()+"."+field.Name+` is tagged "`+tag+`"`)
			}
			continue
		}
		if field.PkgPath != "" {
			continue
		}
		found = append(found, findOmitemptyTimeFields(field.Type, seen)...)
	}
	return found
}
