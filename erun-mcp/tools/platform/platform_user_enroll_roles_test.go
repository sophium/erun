package platform

import (
	"reflect"
	"testing"
)

// Without roleIds an enrolled user gets the default role; the live call path is
// untestable here, so only the wire name is pinned.
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
