package repository

import (
	"database/sql"
	"testing"
)

func TestRolePermissionInputValidate(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		input   RolePermissionInput
		wantErr bool
	}{
		{
			name:  "valid exact",
			input: RolePermissionInput{APIMethod: "GET", APIPath: "/v1/reviews"},
		},
		{
			name:  "valid pattern",
			input: RolePermissionInput{APIMethodPattern: "^GET$", APIPathPattern: "^/v1/reviews$"},
		},
		{
			name:    "neither form set",
			input:   RolePermissionInput{},
			wantErr: true,
		},
		{
			name: "both forms set",
			input: RolePermissionInput{
				APIMethod: "GET", APIPath: "/v1/reviews",
				APIMethodPattern: "^GET$", APIPathPattern: "^/v1/reviews$",
			},
			wantErr: true,
		},
		{
			name:    "exact method without path",
			input:   RolePermissionInput{APIMethod: "GET"},
			wantErr: true,
		},
		{
			name:    "exact path without method",
			input:   RolePermissionInput{APIPath: "/v1/reviews"},
			wantErr: true,
		},
		{
			name:    "invalid exact method",
			input:   RolePermissionInput{APIMethod: "TRACE", APIPath: "/v1/reviews"},
			wantErr: true,
		},
		{
			name:    "pattern method without path pattern",
			input:   RolePermissionInput{APIMethodPattern: "^GET$"},
			wantErr: true,
		},
		{
			name:    "uncompilable method pattern",
			input:   RolePermissionInput{APIMethodPattern: "(", APIPathPattern: "^/v1/reviews$"},
			wantErr: true,
		},
		{
			name:    "uncompilable path pattern",
			input:   RolePermissionInput{APIMethodPattern: "^GET$", APIPathPattern: "("},
			wantErr: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.input.validate()
			if testCase.wantErr && err == nil {
				t.Fatalf("expected an error, got none")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestTenantHasGrantCapableUserReusesRuleMatcher locks in that the lockout
// guard shares rulesAllow with Authorize: a role permitting the grant route
// (exactly or via a pattern that covers it) makes the tenant grant-capable,
// and one that does not, does not.
func TestTenantHasGrantCapableUserReusesRuleMatcher(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		rules   []permissionRule
		capable bool
	}{
		{
			name:    "no rules at all",
			rules:   nil,
			capable: false,
		},
		{
			name: "exact grant route",
			rules: []permissionRule{
				{APIMethod: valid(grantRoleMethod), APIPath: valid(grantRolePath)},
			},
			capable: true,
		},
		{
			name: "WriteAll-shaped pattern covers it",
			rules: []permissionRule{
				{APIMethodPattern: valid("^(POST|PUT|PATCH|DELETE)$"), APIPathPattern: valid("^/.*$")},
			},
			capable: true,
		},
		{
			name: "ReadAll-shaped pattern does not cover a write",
			rules: []permissionRule{
				{APIMethodPattern: valid("^(GET|HEAD|OPTIONS)$"), APIPathPattern: valid("^/.*$")},
			},
			capable: false,
		},
		{
			name: "a narrow pattern for a different path",
			rules: []permissionRule{
				{APIMethodPattern: valid("^POST$"), APIPathPattern: valid("^/v1/reviews$")},
			},
			capable: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			allowed, err := rulesAllow(testCase.rules, grantRoleMethod, grantRolePath)
			if err != nil {
				t.Fatalf("rulesAllow: %v", err)
			}
			if allowed != testCase.capable {
				t.Fatalf("expected capable=%v, got %v", testCase.capable, allowed)
			}
		})
	}
}

func valid(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}
