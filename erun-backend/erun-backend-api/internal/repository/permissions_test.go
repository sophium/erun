package repository

import (
	"database/sql"
	"testing"
)

func TestPermissionRuleMatchesExactMethodAndPath(t *testing.T) {
	rule := permissionRule{
		APIMethod: sql.NullString{String: "GET", Valid: true},
		APIPath:   sql.NullString{String: "/v1/reviews", Valid: true},
	}

	matches, err := rule.matches("GET", "/v1/reviews")
	if err != nil {
		t.Fatalf("matches failed: %v", err)
	}
	if !matches {
		t.Fatal("expected exact permission to match")
	}

	matches, err = rule.matches("POST", "/v1/reviews")
	if err != nil {
		t.Fatalf("matches failed: %v", err)
	}
	if matches {
		t.Fatal("did not expect different method to match")
	}
}

func TestPermissionRuleMatchesRegexMethodAndPath(t *testing.T) {
	rule := permissionRule{
		APIMethodPattern: sql.NullString{String: "^(GET|HEAD|OPTIONS)$", Valid: true},
		APIPathPattern:   sql.NullString{String: "^/.*$", Valid: true},
	}

	matches, err := rule.matches("GET", "/v1/reviews/019abc")
	if err != nil {
		t.Fatalf("matches failed: %v", err)
	}
	if !matches {
		t.Fatal("expected regex permission to match")
	}

	matches, err = rule.matches("POST", "/v1/reviews/019abc")
	if err != nil {
		t.Fatalf("matches failed: %v", err)
	}
	if matches {
		t.Fatal("did not expect write method to match read permission")
	}
}

func TestPermissionRuleReturnsRegexErrors(t *testing.T) {
	rule := permissionRule{
		APIMethodPattern: sql.NullString{String: "(", Valid: true},
		APIPathPattern:   sql.NullString{String: "^/.*$", Valid: true},
	}

	if _, err := rule.matches("GET", "/v1/reviews"); err == nil {
		t.Fatal("expected invalid regex error")
	}
}
