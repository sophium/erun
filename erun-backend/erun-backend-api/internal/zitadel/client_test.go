package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newClient(server.URL, "auth.example.com", "test-pat"), server
}

func TestNewClientFromFileReturnsNilWhenUnconfigured(t *testing.T) {
	client, err := NewClientFromFile(Config{})
	if err != nil || client != nil {
		t.Fatalf("client=%v err=%v, want nil, nil for an unconfigured client", client, err)
	}
}

func TestNewClientFromFileFailsOnUnreadablePath(t *testing.T) {
	_, err := NewClientFromFile(Config{
		BaseURL:        "http://example.com",
		ExternalDomain: "auth.example.com",
		PATPath:        filepath.Join(t.TempDir(), "missing.pat"),
	})
	if err == nil {
		t.Fatal("want an error for an unreadable PAT path")
	}
}

func TestNewClientFromFileFailsOnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.pat")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewClientFromFile(Config{
		BaseURL:        "http://example.com",
		ExternalDomain: "auth.example.com",
		PATPath:        path,
	})
	if err == nil {
		t.Fatal("want an error for an empty PAT file")
	}
}

func TestNewClientFromFileLoadsThePAT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-sa.pat")
	if err := os.WriteFile(path, []byte(" secret-pat \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClientFromFile(Config{
		BaseURL:        server.URL,
		ExternalDomain: "auth.example.com",
		PATPath:        path,
	})
	if err != nil || client == nil {
		t.Fatalf("client=%v err=%v", client, err)
	}
	if err := client.DeactivateUser(context.Background(), "u1"); err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}
	if gotAuth != "Bearer secret-pat" {
		t.Fatalf("Authorization header = %q, want the trimmed PAT", gotAuth)
	}
}

func TestCallSetsHostHeaderAndNeverLeaksPATOnError(t *testing.T) {
	var gotHost string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"permission denied"}`))
	})
	err := client.DeactivateUser(context.Background(), "u1")
	if err == nil {
		t.Fatal("want an error for a 403 response")
	}
	if gotHost != "auth.example.com" {
		t.Fatalf("request Host = %q, want the configured external domain", gotHost)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if contains(err.Error(), "test-pat") {
		t.Fatalf("error text leaked the PAT: %q", err.Error())
	}
}

func contains(haystack string, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestListUsers(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/management/v1/users/_search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{
					"id":       "u1",
					"userName": "alice",
					"state":    "USER_STATE_ACTIVE",
					"human": map[string]any{
						"profile": map[string]any{"firstName": "Alice", "lastName": "Operator"},
						"email":   map[string]any{"email": "alice@example.com"},
					},
				},
				{"id": "svc1", "userName": "admin-sa", "state": "USER_STATE_ACTIVE"},
			},
		})
	})
	users, err := client.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0] != (User{ID: "u1", Username: "alice", State: "USER_STATE_ACTIVE", Email: "alice@example.com", FirstName: "Alice", LastName: "Operator"}) {
		t.Fatalf("users[0] = %+v", users[0])
	}
	if users[1].ID != "svc1" || users[1].Email != "" {
		t.Fatalf("users[1] = %+v, want a machine user with no email", users[1])
	}
}

func TestCreateHumanUserRequiresEmailAndUsername(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be sent for invalid input")
	})
	if _, err := client.CreateHumanUser(context.Background(), CreateHumanUserParams{Username: "alice"}); err == nil {
		t.Fatal("want an error when email is missing")
	}
	if _, err := client.CreateHumanUser(context.Background(), CreateHumanUserParams{Email: "a@example.com"}); err == nil {
		t.Fatal("want an error when username is missing")
	}
}

func TestCreateHumanUserSendsInviteWithNoPassword(t *testing.T) {
	var gotBody map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/management/v1/users/human" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"userId": "u2"})
	})
	user, err := client.CreateHumanUser(context.Background(), CreateHumanUserParams{
		Username: "bob", Email: "bob@example.com", FirstName: "Bob", LastName: "Operator",
	})
	if err != nil {
		t.Fatalf("CreateHumanUser: %v", err)
	}
	if user.ID != "u2" || user.Username != "bob" || user.Email != "bob@example.com" {
		t.Fatalf("user = %+v", user)
	}
	if _, hasPassword := gotBody["password"]; hasPassword {
		t.Fatal("request body must never carry a password for the invite flow")
	}
	email, _ := gotBody["email"].(map[string]any)
	if email["isEmailVerified"] != false {
		t.Fatalf("email.isEmailVerified = %v, want false so Zitadel sends a verification link", email["isEmailVerified"])
	}
}

// TestCreateHumanUserWithInitialPasswordSkipsTheEmailFlow locks the
// no-SMTP fallback (issue #1168): Zitadel only skips its initialization
// email when the email is marked verified AND a password is set (confirmed
// live -- either alone still leaves the account in USER_STATE_INITIAL
// waiting on a link nothing will ever send), so a caller-supplied
// InitialPassword must mark the email verified too, and the returned user
// must report USER_STATE_ACTIVE, not the invite-flow INITIAL state.
func TestCreateHumanUserWithInitialPasswordSkipsTheEmailFlow(t *testing.T) {
	var gotBody map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"userId": "u3"})
	})
	user, err := client.CreateHumanUser(context.Background(), CreateHumanUserParams{
		Username: "carol", Email: "carol@example.com", InitialPassword: "Er7hK2mQ9xL4nP6z!",
	})
	if err != nil {
		t.Fatalf("CreateHumanUser: %v", err)
	}
	if user.State != "USER_STATE_ACTIVE" {
		t.Fatalf("user.State = %q, want USER_STATE_ACTIVE", user.State)
	}
	if gotBody["initialPassword"] != "Er7hK2mQ9xL4nP6z!" {
		t.Fatalf("request body = %+v, want the initial password carried", gotBody)
	}
	email, _ := gotBody["email"].(map[string]any)
	if email["isEmailVerified"] != true {
		t.Fatalf("email.isEmailVerified = %v, want true -- otherwise Zitadel still emails an init link nothing can deliver", email["isEmailVerified"])
	}
}

func TestDeactivateAndReactivateUser(t *testing.T) {
	var gotPaths []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	if err := client.DeactivateUser(context.Background(), "u1"); err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}
	if err := client.ReactivateUser(context.Background(), "u1"); err != nil {
		t.Fatalf("ReactivateUser: %v", err)
	}
	want := []string{
		"POST /management/v1/users/u1/_deactivate",
		"POST /management/v1/users/u1/_reactivate",
	}
	if len(gotPaths) != 2 || gotPaths[0] != want[0] || gotPaths[1] != want[1] {
		t.Fatalf("requests = %v, want %v", gotPaths, want)
	}
}

func TestDeactivateUserPropagatesNotFound(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	err := client.DeactivateUser(context.Background(), "missing")
	apiErr, ok := err.(*APIError)
	if !ok || !apiErr.NotFound() {
		t.Fatalf("err = %v, want a not-found *APIError", err)
	}
}
