package auth_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLivePermissionUpdatesAndBootstrapProtection(t *testing.T) {
	f := newFixture(t)
	f.mu.Lock()
	f.subject = "admin"
	f.mu.Unlock()
	f.login(t, 200)
	me := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	headers := map[string]string{"Origin": f.server.URL, "X-CSRF-Token": me["csrf"].(string)}
	issued := f.request(t, f.browser, "POST", "/auth/tokens", "", map[string]string{"subject": "alice"}, headers, 200)
	token := issued["token"].(string)
	policy := map[string]any{"subject": "alice", "projects": []string{"secret"}, "syncSources": []string{"secret/docs/main"}}
	f.request(t, f.browser, "PUT", "/auth/principals/alice", "", map[string]any{"expectedVersion": 0, "principal": policy}, headers, 200)
	f.request(t, http.DefaultClient, "GET", "/api/v1/memories/allowed-memory", token, nil, nil, 404)
	f.request(t, http.DefaultClient, "GET", "/api/v1/memories/secret-memory", token, nil, nil, 200)
	f.request(t, http.DefaultClient, "GET", "/auth/principals", token, nil, nil, 403)
	f.request(t, f.browser, "PUT", "/auth/principals/alice", "", map[string]any{"expectedVersion": 0, "principal": policy}, headers, 409)
	f.request(t, f.browser, "PUT", "/auth/principals/admin", "", map[string]any{"expectedVersion": 0, "principal": map[string]any{"subject": "admin", "admin": false}}, headers, 403)
	policy["disabled"] = true
	f.request(t, f.browser, "PUT", "/auth/principals/alice", "", map[string]any{"expectedVersion": 1, "principal": policy}, headers, 200)
	f.request(t, http.DefaultClient, "GET", "/auth/me", token, nil, nil, 403)
	f.request(t, http.DefaultClient, "POST", "/auth/logout", token, map[string]string{}, nil, 200)
	f.request(t, f.browser, "DELETE", "/auth/principals/alice?version=2", "", nil, headers, 200)
	issued = f.request(t, f.browser, "POST", "/auth/tokens", "", map[string]string{"subject": "alice"}, headers, 200)
	f.request(t, http.DefaultClient, "GET", "/api/v1/memories/allowed-memory", issued["token"].(string), nil, nil, 200)
	// Reset keeps a version tombstone so an old create/update cannot overwrite newer policy.
	f.request(t, f.browser, "PUT", "/auth/principals/alice", "", map[string]any{"expectedVersion": 0, "principal": policy}, headers, 409)
	bob := map[string]any{"subject": "bob", "name": "Bob", "projects": []string{"allowed"}}
	opaque := "team/user:% identity"
	f.request(t, f.browser, "PUT", "/auth/principals/"+url.PathEscape(opaque), "", map[string]any{"expectedVersion": 0, "principal": map[string]any{"subject": opaque, "projects": []string{"allowed"}}}, headers, 200)
	f.request(t, f.browser, "PUT", "/auth/principals/bob", "", map[string]any{"expectedVersion": 0, "principal": bob}, headers, 200)
	f.mu.Lock()
	f.subject = "bob"
	f.mu.Unlock()
	f.login(t, 200)
	identity := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	if identity["subject"] != "bob" || identity["admin"] != false {
		t.Fatal(identity)
	}
}

func TestTokenRotationAndPersistentAudit(t *testing.T) {
	f := newFixture(t)
	f.mu.Lock()
	f.subject = "admin"
	f.mu.Unlock()
	f.login(t, 200)
	me := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	headers := map[string]string{"Origin": f.server.URL, "X-CSRF-Token": me["csrf"].(string)}
	issued := f.request(t, f.browser, "POST", "/auth/tokens", "", map[string]any{"subject": "robot", "expiresIn": 60}, headers, 200)
	token := issued["token"].(string)
	old := issued["credential"].(map[string]any)
	id := old["id"].(string)
	rotated := f.request(t, http.DefaultClient, "POST", "/auth/tokens/"+id+"/rotate", token, map[string]string{}, nil, 200)
	if rotated["credential"].(map[string]any)["expiresAt"] != old["expiresAt"] {
		t.Fatal("rotation extended absolute lifetime")
	}
	f.request(t, http.DefaultClient, "GET", "/auth/me", token, nil, nil, 401)
	newToken := rotated["token"].(string)
	f.request(t, http.DefaultClient, "GET", "/auth/me", newToken, nil, nil, 200)
	f.request(t, http.DefaultClient, "GET", "/auth/tokens?all=true", newToken, nil, nil, 403)
	f.request(t, http.DefaultClient, "GET", "/auth/audit?all=true", newToken, nil, nil, 403)
	response, err := f.browser.Get(f.server.URL + "/auth/audit?all=true&limit=200")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var events []map[string]any
	if json.NewDecoder(response.Body).Decode(&events) != nil || len(events) < 2 {
		t.Fatal("missing durable audit")
	}
	data, _ := json.Marshal(events)
	if strings.Contains(string(data), token) || strings.Contains(string(data), newToken) {
		t.Fatal("audit leaked raw token")
	}
	for _, event := range events {
		if event["status"] == float64(0) {
			t.Fatal("audit remained pending after successful operation")
		}
	}
	f.request(t, f.browser, "GET", "/auth/audit?limit=1000", "", nil, nil, 400)
}
