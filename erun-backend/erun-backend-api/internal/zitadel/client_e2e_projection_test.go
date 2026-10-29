package zitadel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// laggingUserDirectory stands in for Zitadel v4's eventually consistent user
// projection: a user is listed only from the second read of its org after its
// creation, which is the shape a real core shows under load.
type laggingUserDirectory struct {
	mu    sync.Mutex
	orgs  int
	users []*laggingUser
}

type laggingUser struct {
	id    string
	name  string
	org   string // empty is the credential's own org
	reads int
}

func (d *laggingUserDirectory) handler(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	org := r.Header.Get("x-zitadel-orgid")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch {
	case r.URL.Path == "/management/v1/orgs":
		d.orgs++
		writeFakeJSON(w, map[string]any{"id": fmt.Sprintf("org-%d", d.orgs)})
	case r.URL.Path == "/management/v1/users/human":
		d.createUser(w, r, org)
	case r.URL.Path == "/management/v1/users/_search":
		d.searchUsers(w, org)
	case strings.HasSuffix(r.URL.Path, "/_deactivate"):
		d.deactivate(w, r, org)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (d *laggingUserDirectory) createUser(w http.ResponseWriter, r *http.Request, org string) {
	var body struct {
		UserName string `json:"userName"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	user := &laggingUser{id: fmt.Sprintf("user-%d", len(d.users)+1), name: body.UserName, org: org}
	d.users = append(d.users, user)
	writeFakeJSON(w, map[string]any{"userId": user.id})
}

func (d *laggingUserDirectory) searchUsers(w http.ResponseWriter, org string) {
	result := []map[string]any{}
	for _, user := range d.users {
		if user.org != org {
			continue
		}
		user.reads++
		if user.reads > 1 {
			result = append(result, map[string]any{"id": user.id, "userName": user.name, "state": "USER_STATE_INITIAL"})
		}
	}
	writeFakeJSON(w, map[string]any{"result": result, "details": map[string]any{"totalResult": fmt.Sprint(len(result))}})
}

func (d *laggingUserDirectory) deactivate(w http.ResponseWriter, r *http.Request, org string) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/management/v1/users/"), "/_deactivate")
	for _, user := range d.users {
		if user.id == id && user.org == org {
			writeFakeJSON(w, map[string]any{})
			return
		}
	}
	http.Error(w, `{"code":5,"message":"User could not be found"}`, http.StatusNotFound)
}

// The real-core test is opt-in, so this is what every go test runs: the e2e
// helpers must wait for a just-created user rather than trust the first read.
func TestE2EUserListingWaitsForALaggingProjection(t *testing.T) {
	directory := &laggingUserDirectory{}
	client, _ := newTestClient(t, directory.handler)
	ctx := context.Background()

	if created := e2eCreateAndListUser(t, ctx, client); created.ID == "" {
		t.Fatal("e2eCreateAndListUser returned no user")
	}
	e2eCrossOrgListAndDeactivate(t, ctx, client)
}
