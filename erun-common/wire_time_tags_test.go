package eruncommon

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// wireTimeOmitzeroRoots are the JSON roots a machine consumer actually reads:
// `--output json`, the MCP edge, and the desktop's own read models. Every
// timestamp reachable from one of them must render an unset value by
// disappearing, never as the Go zero instant.
var wireTimeOmitzeroRoots = []any{
	EnvironmentIdleStatus{},
	EnvironmentActivitySnapshot{},
	EnvironmentIdleMarkerClient{},
	EnvironmentActivityLease{},
	EnvironmentStopPending{},
	EnvironmentStopHistoryEntry{},
	EnvironmentJob{},
	AISessionStatus{},
	RuntimeUsageHistory{},
	HostCredentialsRefresh{},
	HostCredentialsStatus{},
}

// TestWireTimeFieldsOmitZeroNotOmitEmpty guards the whole class rather than the
// fields one report named: encoding/json has no notion of an "empty" struct, so
// omitempty on a value time.Time never fires and a never-set timestamp is
// published as 0001-01-01T00:00:00Z — a real-looking instant a consumer cannot
// distinguish from one that was actually recorded. omitzero is the tag that
// works, and it is already how the stop-history record renders its own
// optional ArmedAt.
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

// TestWireTimeFieldGuardDetectsViolation is the positive control: a walker that
// silently stopped walking would let the next field through, so it is required
// to find a violation that is really there — and to leave a correctly tagged
// pointer field alone, since omitempty does omit a nil pointer.
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

// wireStructElement follows the pointer, slice, array and map wrappers a JSON
// value can arrive behind, so the walk sees the struct they carry.
func wireStructElement(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
		typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	return typ
}

// findOmitemptyTimeFields describes every value time.Time reachable from typ
// that carries omitempty, which encoding/json never honours.
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
