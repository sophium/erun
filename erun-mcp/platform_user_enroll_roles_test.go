package erunmcp

import (
	"reflect"
	"testing"
)

// TestPlatformUserEnrollInputCarriesRoleIDs locks the wire name of the one
// field that makes a cross-tenant enrollment usable. Without roleIds the
// enrolled user lands with the platform's default role, and in a tenant whose
// only grant-capable identity cannot authenticate nobody there can elevate
// them -- the state erun#1830 describes. The CLI half of this wiring is covered
// end to end by erun-integration's platform/user_enroll_with_roles_dry_run
// golden; this tool's own call path reaches a live platform, so what is
// assertable here is that the input still carries the field under the name the
// API reads.
func TestPlatformUserEnrollInputCarriesRoleIDs(t *testing.T) {
	field, ok := reflect.TypeOf(PlatformUserEnrollInput{}).FieldByName("RoleIDs")
	if !ok {
		t.Fatal("PlatformUserEnrollInput has no RoleIDs field")
	}
	if got := field.Tag.Get("json"); got != "roleIds,omitempty" {
		t.Fatalf("RoleIDs json tag = %q, want \"roleIds,omitempty\"", got)
	}
	if field.Type.Kind() != reflect.Slice || field.Type.Elem().Kind() != reflect.String {
		t.Fatalf("RoleIDs type = %s, want []string", field.Type)
	}
	if field.Tag.Get("jsonschema") == "" {
		t.Fatal("RoleIDs has no jsonschema description, so the tool's schema would not explain it")
	}
}
