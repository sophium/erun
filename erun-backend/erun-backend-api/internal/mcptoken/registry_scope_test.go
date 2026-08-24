package mcptoken

import (
	"reflect"
	"testing"
)

func TestParseRegistryTokenScopes(t *testing.T) {
	tests := []struct {
		name string
		raw  []string
		want []RegistryAccessScope
	}{
		{
			name: "single repository scope",
			raw:  []string{"repository:frs/hello:pull,push"},
			want: []RegistryAccessScope{{Type: "repository", Name: "frs/hello", Actions: []string{"pull", "push"}}},
		},
		{
			name: "space-separated scopes in one query value",
			raw:  []string{"repository:frs/a:pull repository:frs/b:push"},
			want: []RegistryAccessScope{
				{Type: "repository", Name: "frs/a", Actions: []string{"pull"}},
				{Type: "repository", Name: "frs/b", Actions: []string{"push"}},
			},
		},
		{
			name: "repeated query params",
			raw:  []string{"repository:frs/a:pull", "repository:frs/b:push"},
			want: []RegistryAccessScope{
				{Type: "repository", Name: "frs/a", Actions: []string{"pull"}},
				{Type: "repository", Name: "frs/b", Actions: []string{"push"}},
			},
		},
		{name: "malformed entry dropped", raw: []string{"not-a-scope"}, want: nil},
		{name: "empty action list dropped", raw: []string{"repository:frs/hello:"}, want: nil},
		{name: "empty input", raw: nil, want: nil},
		{
			name: "malformed entry alongside a valid one keeps only the valid one",
			raw:  []string{"repository:frs/a:pull garbage"},
			want: []RegistryAccessScope{{Type: "repository", Name: "frs/a", Actions: []string{"pull"}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRegistryTokenScopes(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseRegistryTokenScopes(%v) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestClampRegistryScopesToTenant(t *testing.T) {
	requested := []RegistryAccessScope{
		{Type: "repository", Name: "frs", Actions: []string{"pull", "push"}},
		{Type: "repository", Name: "frs/hello", Actions: []string{"pull"}},
		{Type: "repository", Name: "other-tenant", Actions: []string{"pull"}},
		{Type: "repository", Name: "other-tenant/hello", Actions: []string{"pull"}},
		// "frsking" merely starts with "frs" but is not the tenant's namespace —
		// a naive strings.HasPrefix(name, tenant) would wrongly grant this.
		{Type: "repository", Name: "frsking", Actions: []string{"pull"}},
		{Type: "registry", Name: "catalog", Actions: []string{"*"}},
	}
	got := ClampRegistryScopesToTenant("frs", requested)
	want := []RegistryAccessScope{
		{Type: "repository", Name: "frs", Actions: []string{"pull", "push"}},
		{Type: "repository", Name: "frs/hello", Actions: []string{"pull"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClampRegistryScopesToTenant() = %#v, want %#v", got, want)
	}
}

func TestClampRegistryScopesToTenantEmptyTenantGrantsNothing(t *testing.T) {
	requested := []RegistryAccessScope{{Type: "repository", Name: "frs", Actions: []string{"pull"}}}
	if got := ClampRegistryScopesToTenant("", requested); got != nil {
		t.Fatalf("ClampRegistryScopesToTenant(\"\", ...) = %#v, want nil", got)
	}
}

func TestClampRegistryScopesToTenantCrossTenantScopeGrantsNothing(t *testing.T) {
	// The exact attack the issue calls out: a valid tenant-A token requesting
	// tenant-B's repository scope must be granted nothing, not a narrowed scope.
	requested := []RegistryAccessScope{{Type: "repository", Name: "tenant-b/secret", Actions: []string{"pull", "push"}}}
	if got := ClampRegistryScopesToTenant("tenant-a", requested); len(got) != 0 {
		t.Fatalf("ClampRegistryScopesToTenant(tenant-a, tenant-b scope) = %#v, want empty", got)
	}
}
