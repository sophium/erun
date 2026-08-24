package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// jsonRoute is one canned response in a routedTestServer, keyed by method and
// exact path. Keeping the routing table data (rather than a per-test
// if/switch chain) is what keeps each test's own cyclomatic complexity low.
type jsonRoute struct {
	method string
	path   string
	body   map[string]any
	// record, when set, is called with the decoded request body before the
	// canned response (if any) is written; used to capture and count writes.
	record func(body map[string]any)
}

func routedTestClient(t *testing.T, routes []jsonRoute) *Client {
	t.Helper()
	index := make(map[string]jsonRoute, len(routes))
	for _, r := range routes {
		index[r.method+" "+r.path] = r
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		route, ok := index[req.Method+" "+req.URL.Path]
		if !ok {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return
		}
		if route.record != nil {
			var decoded map[string]any
			_ = json.NewDecoder(req.Body).Decode(&decoded)
			route.record(decoded)
		}
		if route.body == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(route.body)
	}))
	t.Cleanup(server.Close)
	return newClient(server.URL, "auth.example.com", "test-pat")
}

func emptyDomainSearchRoute() jsonRoute {
	return jsonRoute{method: http.MethodPost, path: "/management/v1/orgs/me/domains/_search", body: map[string]any{"result": []map[string]any{}}}
}

func TestGetOrgSettings(t *testing.T) {
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodGet, path: "/management/v1/policies/login", body: map[string]any{
			"policy": map[string]any{"allowUsernamePassword": true, "forceMfa": true, "passwordlessType": "PASSWORDLESS_TYPE_NOT_ALLOWED"},
		}},
		{method: http.MethodGet, path: "/management/v1/policies/password/complexity", body: map[string]any{
			"policy": map[string]any{"minLength": "10", "hasUppercase": true, "hasLowercase": true, "hasNumber": false, "hasSymbol": false},
		}},
		{method: http.MethodPost, path: "/management/v1/orgs/me/domains/_search", body: map[string]any{
			"result": []map[string]any{
				{"domainName": "erun.example.com", "isVerified": true},
				{"domainName": "unverified.example.com", "isVerified": false},
			},
		}},
	})

	settings, err := client.GetOrgSettings(context.Background())
	if err != nil {
		t.Fatalf("GetOrgSettings: %v", err)
	}
	want := OrgSettings{
		ForceMFA: true, MinPasswordLength: 10,
		PasswordRequiresUppercase: true, PasswordRequiresLowercase: true,
		VerifiedDomains: []string{"erun.example.com"},
	}
	assertOrgSettingsEqual(t, settings, want)
}

func assertOrgSettingsEqual(t *testing.T, got OrgSettings, want OrgSettings) {
	t.Helper()
	if got.ForceMFA != want.ForceMFA || got.MinPasswordLength != want.MinPasswordLength ||
		got.PasswordRequiresUppercase != want.PasswordRequiresUppercase ||
		got.PasswordRequiresLowercase != want.PasswordRequiresLowercase ||
		got.PasswordRequiresNumber != want.PasswordRequiresNumber ||
		got.PasswordRequiresSymbol != want.PasswordRequiresSymbol {
		t.Fatalf("OrgSettings = %+v, want %+v", got, want)
	}
	if len(got.VerifiedDomains) != len(want.VerifiedDomains) {
		t.Fatalf("VerifiedDomains = %v, want %v", got.VerifiedDomains, want.VerifiedDomains)
	}
	for i, domain := range want.VerifiedDomains {
		if got.VerifiedDomains[i] != domain {
			t.Fatalf("VerifiedDomains = %v, want %v", got.VerifiedDomains, want.VerifiedDomains)
		}
	}
}

// TestUpdateOrgSettingsForceMFAPreservesOtherLoginFields locks the
// read-modify-write contract: changing forceMfa must not clobber the other
// login-policy fields Zitadel already has, must not touch the password
// policy at all, and must use PUT for an org that already overrode the
// instance default (isDefault absent/false) — see loginPolicyResponse.IsDefault
// for why POST would fail here.
func TestUpdateOrgSettingsForceMFAPreservesOtherLoginFields(t *testing.T) {
	var loginPutBody map[string]any
	loginPuts, passwordPuts := 0, 0
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodGet, path: "/management/v1/policies/login", body: map[string]any{
			"policy": map[string]any{"allowUsernamePassword": true, "allowRegister": false, "forceMfa": false, "passwordlessType": "PASSWORDLESS_TYPE_NOT_ALLOWED"},
		}},
		{method: http.MethodPut, path: "/management/v1/policies/login", record: func(body map[string]any) {
			loginPuts++
			loginPutBody = body
		}},
		{method: http.MethodGet, path: "/management/v1/policies/password/complexity", body: map[string]any{"policy": map[string]any{"minLength": "8"}}},
		{method: http.MethodPut, path: "/management/v1/policies/password/complexity", record: func(map[string]any) { passwordPuts++ }},
		emptyDomainSearchRoute(),
	})

	forceMFA := true
	if _, err := client.UpdateOrgSettings(context.Background(), UpdateOrgSettingsParams{ForceMFA: &forceMFA}); err != nil {
		t.Fatalf("UpdateOrgSettings(forceMfa): %v", err)
	}
	if loginPuts != 1 || passwordPuts != 0 {
		t.Fatalf("loginPuts=%d passwordPuts=%d, want only the login policy touched", loginPuts, passwordPuts)
	}
	if loginPutBody["forceMfa"] != true || loginPutBody["allowUsernamePassword"] != true || loginPutBody["passwordlessType"] != "PASSWORDLESS_TYPE_NOT_ALLOWED" {
		t.Fatalf("login PUT body = %+v, want forceMfa flipped and every other field preserved", loginPutBody)
	}
}

// TestUpdateOrgSettingsMinPasswordLengthPreservesOtherPasswordFields is the
// password-policy half of the same read-modify-write contract.
func TestUpdateOrgSettingsMinPasswordLengthPreservesOtherPasswordFields(t *testing.T) {
	var passwordPutBody map[string]any
	loginPuts, passwordPuts := 0, 0
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodGet, path: "/management/v1/policies/login", body: map[string]any{"policy": map[string]any{"forceMfa": false}}},
		{method: http.MethodPut, path: "/management/v1/policies/login", record: func(map[string]any) { loginPuts++ }},
		{method: http.MethodGet, path: "/management/v1/policies/password/complexity", body: map[string]any{
			"policy": map[string]any{"minLength": "8", "hasUppercase": true, "hasLowercase": true, "hasNumber": true, "hasSymbol": false},
		}},
		{method: http.MethodPut, path: "/management/v1/policies/password/complexity", record: func(body map[string]any) {
			passwordPuts++
			passwordPutBody = body
		}},
		emptyDomainSearchRoute(),
	})

	minLength := uint64(12)
	if _, err := client.UpdateOrgSettings(context.Background(), UpdateOrgSettingsParams{MinPasswordLength: &minLength}); err != nil {
		t.Fatalf("UpdateOrgSettings(minLength): %v", err)
	}
	if loginPuts != 0 || passwordPuts != 1 {
		t.Fatalf("loginPuts=%d passwordPuts=%d, want only the password policy touched", loginPuts, passwordPuts)
	}
	if passwordPutBody["minLength"] != "12" || passwordPutBody["hasUppercase"] != true || passwordPutBody["hasNumber"] != true {
		t.Fatalf("password PUT body = %+v, want minLength updated and every other field preserved", passwordPutBody)
	}
}

// TestUpdateOrgSettingsSkipsAnUnchangedValue locks the convergence
// discipline: Zitadel answers 400 "NotChanged" for a write that carries no
// real diff, so a caller re-asserting the current value must not issue one.
func TestUpdateOrgSettingsSkipsAnUnchangedValue(t *testing.T) {
	writes := 0
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodGet, path: "/management/v1/policies/login", body: map[string]any{"policy": map[string]any{"forceMfa": true}}},
		{method: http.MethodPut, path: "/management/v1/policies/login", record: func(map[string]any) { writes++ }},
		{method: http.MethodGet, path: "/management/v1/policies/password/complexity", body: map[string]any{"policy": map[string]any{"minLength": "8"}}},
		emptyDomainSearchRoute(),
	})

	forceMFA := true
	if _, err := client.UpdateOrgSettings(context.Background(), UpdateOrgSettingsParams{ForceMFA: &forceMFA}); err != nil {
		t.Fatalf("UpdateOrgSettings: %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want 0 for a value that already matches Zitadel's current policy", writes)
	}
}

// TestUpdateOrgSettingsPostsWhenStillOnTheInstanceDefault locks the other
// half of the POST/PUT split: an org that has never overridden the instance
// default (isDefault present and true) must be written with POST, since PUT
// against it fails with "not found".
func TestUpdateOrgSettingsPostsWhenStillOnTheInstanceDefault(t *testing.T) {
	posts, puts := 0, 0
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodGet, path: "/management/v1/policies/login", body: map[string]any{"policy": map[string]any{"forceMfa": false}, "isDefault": true}},
		{method: http.MethodPost, path: "/management/v1/policies/login", record: func(map[string]any) { posts++ }},
		{method: http.MethodPut, path: "/management/v1/policies/login", record: func(map[string]any) { puts++ }},
		{method: http.MethodGet, path: "/management/v1/policies/password/complexity", body: map[string]any{"policy": map[string]any{"minLength": "8"}}},
		emptyDomainSearchRoute(),
	})

	forceMFA := true
	if _, err := client.UpdateOrgSettings(context.Background(), UpdateOrgSettingsParams{ForceMFA: &forceMFA}); err != nil {
		t.Fatalf("UpdateOrgSettings: %v", err)
	}
	if posts != 1 || puts != 0 {
		t.Fatalf("posts=%d puts=%d, want POST for an org still on the instance default", posts, puts)
	}
}
