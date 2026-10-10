package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/app"
	"github.com/abandon1a2b/ohyeah/internal/auth"
	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/search"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/abandon1a2b/ohyeah/internal/syncer"
	"github.com/abandon1a2b/ohyeah/internal/web"
	"github.com/go-jose/go-jose/v4"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

type fixture struct {
	state    *store.Store
	server   *httptest.Server
	provider *httptest.Server
	cfg      config.Config
	browser  *http.Client
	mu       sync.Mutex
	subject  string
	bad      string
}

type testBackend struct {
	search.Backend
	state *store.Store
}

func (b testBackend) Healthy(context.Context) error          { return nil }
func (b testBackend) Ensure(context.Context) error           { return nil }
func (b testBackend) Upsert(context.Context, [][]byte) error { return nil }
func (b testBackend) Delete(context.Context, []string) error { return nil }
func (b testBackend) Count(ctx context.Context) (int64, bool, error) {
	n, err := b.state.MemoryCount(ctx)
	return n, true, err
}
func (b testBackend) Search(ctx context.Context, q model.SearchQuery) ([]model.SearchHit, error) {
	record, err := b.state.GetMemory(ctx, q.ProjectID+"-memory")
	if err != nil {
		return []model.SearchHit{}, nil
	}
	return []model.SearchHit{{Unit: record.Unit, Reference: record.Reference}}, nil
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{subject: "alice"}
	var nonce, challenge string
	f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": f.provider.URL, "authorization_endpoint": f.provider.URL + "/authorize", "token_endpoint": f.provider.URL + "/token", "jwks_uri": f.provider.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
		case "/authorize":
			if r.URL.Query().Get("code_challenge_method") != "S256" {
				t.Error("PKCE missing")
			}
			nonce, challenge = r.URL.Query().Get("nonce"), r.URL.Query().Get("code_challenge")
			http.Redirect(w, r, r.URL.Query().Get("redirect_uri")+"?code=test&state="+url.QueryEscape(r.URL.Query().Get("state")), 303)
		case "/token":
			r.ParseForm()
			if oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			claims := jwt.MapClaims{"iss": f.provider.URL, "sub": f.subject, "aud": "ohyeah-test", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce}
			switch f.bad {
			case "nonce":
				claims["nonce"] = "wrong"
			case "audience":
				claims["aud"] = "wrong"
			case "issuer":
				claims["iss"] = "https://wrong.invalid"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test"
			signed, err := token.SignedString(key)
			if err != nil {
				t.Error(err)
			}
			if f.bad == "signature" {
				parts := strings.Split(signed, ".")
				parts[2] = "aW52YWxpZA"
				signed = strings.Join(parts, ".")
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "provider-access", "token_type": "Bearer", "id_token": signed, "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.provider.Close)
	f.state, err = store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.state.Close() })
	var handler http.Handler
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.server.Close)
	f.cfg = config.Config{Server: config.ServerConfig{Address: strings.TrimPrefix(f.server.URL, "http://")}, Auth: config.AuthConfig{Mode: "oidc", PublicURL: f.server.URL, SessionTTL: time.Hour, TokenTTL: time.Hour, OIDC: config.OIDCConfig{Issuer: f.provider.URL, ClientID: "ohyeah-test"}, Principals: []config.PrincipalConfig{
		{Subject: "alice", Name: "Alice", Projects: []string{"allowed"}},
		{Subject: "admin", Admin: true},
		{Subject: "robot", Projects: []string{"allowed"}, SyncProjects: []string{"allowed"}},
	}}}
	security, err := auth.New(context.Background(), f.cfg, f.state)
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Sync.CollectorTimeout = 5 * time.Second
	f.cfg.Sync.MaxFileSize = 1 << 20
	f.cfg.Meilisearch.BatchSize = 100
	backend := testBackend{state: f.state}
	application := &app.App{Config: f.cfg, Store: f.state, Auth: security, Backend: backend, Syncer: syncer.New(f.state, backend, f.cfg)}
	handler = adaptor.FiberApp(web.New(application))
	jar, _ := cookiejar.New(nil)
	f.browser = &http.Client{Jar: jar, Timeout: 5 * time.Second}
	for _, project := range []string{"allowed", "secret"} {
		ctx := context.Background()
		if err := f.state.UpsertProject(ctx, model.Project{ID: project, Workspace: "/fixture", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if err := f.state.UpsertSource(ctx, model.Source{ID: project + "/docs/main", ProjectID: project, WorkspaceID: "fixture", Driver: model.DriverCommand, Path: "/fixture", Enabled: true, Options: map[string]any{"command": "secret-executable"}}); err != nil {
			t.Fatal(err)
		}
		doc := model.Document{Object: model.SourceObject{ID: project + "-object", SourceID: project + "/docs/main", ExternalID: "a", ContentHash: "hash", ModifiedAt: time.Now(), ParserVersion: 1}, Units: []model.MemoryUnit{{ID: project + "-memory", ProjectID: project, SourceID: project + "/docs/main", WorkspaceID: "fixture", Kind: "document", Content: "private", Status: "observed", OccurredAt: time.Now()}}}
		if err := f.state.ReplaceDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) request(t *testing.T, client *http.Client, method, path, token string, data any, headers map[string]string, expected int) map[string]any {
	t.Helper()
	var body io.Reader
	if data != nil {
		bytes, _ := json.Marshal(data)
		body = strings.NewReader(string(bytes))
	}
	req, err := http.NewRequest(method, f.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("%s %s: %d expected %d: %s", method, path, response.StatusCode, expected, content)
	}
	var result map[string]any
	json.Unmarshal(content, &result)
	return result
}

func (f *fixture) login(t *testing.T, expected int) {
	t.Helper()
	response, err := f.browser.Get(f.server.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// The successful callback reaches the SPA; failed callbacks return JSON.
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("login status=%d expected=%d body=%s", response.StatusCode, expected, content)
	}
}

func TestOIDCAndAuthorization(t *testing.T) {
	f := newFixture(t)
	f.request(t, http.DefaultClient, "GET", "/api/v1/projects", "", nil, nil, 401)
	f.request(t, http.DefaultClient, "GET", "/api/v1/health", "", nil, nil, 200)
	f.login(t, 200)
	me := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	if me["subject"] != "alice" {
		t.Fatal(me)
	}
	response, err := f.browser.Get(f.server.URL + "/api/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	var projects []model.Project
	json.NewDecoder(response.Body).Decode(&projects)
	response.Body.Close()
	if len(projects) != 1 || projects[0].ID != "allowed" {
		t.Fatalf("project leak: %#v", projects)
	}
	response, err = f.browser.Get(f.server.URL + "/api/v1/sources")
	if err != nil {
		t.Fatal(err)
	}
	var sources []model.Source
	json.NewDecoder(response.Body).Decode(&sources)
	response.Body.Close()
	if len(sources) != 1 || sources[0].Options != nil {
		t.Fatalf("source leak: %#v", sources)
	}
	f.request(t, f.browser, "GET", "/api/v1/memories/allowed-memory", "", nil, nil, 200)
	f.request(t, f.browser, "GET", "/api/v1/memories/secret-memory", "", nil, nil, 404)
	f.request(t, f.browser, "GET", "/api/v1/search?q=x", "", nil, nil, 400)
	f.request(t, f.browser, "GET", "/api/v1/search?q=x&project=secret", "", nil, nil, 403)
	f.request(t, f.browser, "GET", "/api/v1/search?q=x&project=allowed", "", nil, nil, 200)
	f.request(t, f.browser, "GET", "/api/v1/sync/runs", "", nil, nil, 404)
	f.request(t, f.browser, "GET", "/api/v1/sync/runs?source=secret/docs/main", "", nil, nil, 404)
	response, err = f.browser.Get(f.server.URL + "/api/v1/sync/runs?source=allowed/docs/main")
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || strings.TrimSpace(string(runs)) != "[]" {
		t.Fatalf("empty sync runs: %d %s", response.StatusCode, runs)
	}
	for _, path := range []string{"/api/v1/config", "/api/v1/index/doctor"} {
		f.request(t, f.browser, "GET", path, "", nil, nil, 403)
	}
	headers := map[string]string{"Origin": f.server.URL, "X-CSRF-Token": me["csrf"].(string)}
	f.request(t, f.browser, "POST", "/api/v1/sync", "", map[string]string{"sourceId": "allowed/docs/main"}, headers, 403)
	f.request(t, f.browser, "POST", "/api/v1/sync", "", map[string]string{}, headers, 403)
	f.request(t, f.browser, "PUT", "/api/v1/config", "", map[string]string{"raw": "auth:\n  mode: local"}, headers, 403)
	f.request(t, f.browser, "POST", "/api/v1/index/rebuild", "", map[string]string{}, headers, 403)
	u, _ := url.Parse(f.server.URL)
	for _, cookie := range f.browser.Jar.Cookies(u) {
		if cookie.Name == "ohyeah_session" {
			f.request(t, http.DefaultClient, "GET", "/auth/me", cookie.Value, nil, nil, 401)
		}
	}
	f.request(t, f.browser, "POST", "/auth/logout", "", map[string]string{}, nil, 403)
	f.request(t, f.browser, "POST", "/auth/logout", "", map[string]string{}, map[string]string{"Origin": "https://evil.invalid", "X-CSRF-Token": me["csrf"].(string)}, 403)
	f.request(t, f.browser, "POST", "/auth/logout", "", map[string]string{}, headers, 200)
	f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 401)
}

func TestMissingOIDCServiceFailsClosed(t *testing.T) {
	server := web.New(&app.App{Config: config.Config{Auth: config.AuthConfig{Mode: "oidc"}}})
	response, err := server.Test(httptest.NewRequest("GET", "/api/v1/projects", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatalf("missing auth service fell back to local: %d", response.StatusCode)
	}
}

func TestOIDCRejectsInvalidTokensAndUnknownUsers(t *testing.T) {
	for _, bad := range []string{"nonce", "audience", "issuer", "expired", "unknown", "signature"} {
		t.Run(bad, func(t *testing.T) {
			f := newFixture(t)
			f.mu.Lock()
			f.bad = bad
			if bad == "unknown" {
				f.subject = "unknown"
			}
			f.mu.Unlock()
			status := 401
			if bad == "unknown" {
				status = 403
			}
			f.login(t, status)
			f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 401)
		})
	}
}

func TestStateBindingAndReplay(t *testing.T) {
	f := newFixture(t)
	client := &http.Client{Jar: f.browser.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(f.server.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	location := response.Header.Get("Location")
	response.Body.Close()
	response, err = client.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	callback := response.Header.Get("Location")
	response.Body.Close()
	// A different browser cannot use the captured callback, and state is single-use.
	response, err = http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
	response, err = client.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
}

func TestCLIPairingRevocationAndMachineTokens(t *testing.T) {
	f := newFixture(t)
	pair := f.request(t, http.DefaultClient, "POST", "/auth/cli/start", "", map[string]string{}, nil, 200)
	poll := map[string]any{"userCode": pair["userCode"], "deviceCode": pair["deviceCode"]}
	f.request(t, http.DefaultClient, "POST", "/auth/cli/token", "", poll, nil, 202)
	f.request(t, http.DefaultClient, "POST", "/auth/cli/token", "", map[string]any{"userCode": pair["userCode"], "deviceCode": "wrong"}, nil, 401)
	f.login(t, 200)
	me := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	form := url.Values{"code": {pair["userCode"].(string)}, "csrf": {me["csrf"].(string)}}
	req, _ := http.NewRequest("POST", f.server.URL+"/auth/cli/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", f.server.URL)
	response, err := f.browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	result := f.request(t, http.DefaultClient, "POST", "/auth/cli/token", "", poll, nil, 200)
	token := result["token"].(string)
	id := result["credential"].(map[string]any)["id"].(string)
	f.request(t, http.DefaultClient, "POST", "/auth/cli/token", "", poll, nil, 400)
	f.request(t, http.DefaultClient, "GET", "/auth/me", token, nil, nil, 200)
	f.request(t, http.DefaultClient, "POST", "/auth/tokens", token, map[string]string{"subject": "admin"}, nil, 403)
	f.request(t, http.DefaultClient, "DELETE", "/auth/tokens/"+id, token, nil, nil, 200)
	f.request(t, http.DefaultClient, "GET", "/api/v1/projects", token, nil, nil, 401)
	f.mu.Lock()
	f.subject = "admin"
	f.mu.Unlock()
	f.login(t, 200)
	me = f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	robot := f.request(t, f.browser, "POST", "/auth/tokens", "", map[string]string{"subject": "robot", "name": "collector"}, map[string]string{"Origin": f.server.URL, "X-CSRF-Token": me["csrf"].(string)}, 200)
	f.request(t, http.DefaultClient, "GET", "/auth/me", robot["token"].(string), nil, nil, 200)
	f.request(t, http.DefaultClient, "GET", "/api/v1/config", robot["token"].(string), nil, nil, 403)
	f.request(t, http.DefaultClient, "POST", "/api/v1/sync", robot["token"].(string), map[string]string{"sourceId": "secret/docs/main"}, nil, 403)
	f.request(t, http.DefaultClient, "POST", "/api/v1/sync", robot["token"].(string), map[string]string{}, nil, 403)
	if shell, err := exec.LookPath("sh"); err == nil {
		root := t.TempDir()
		script := filepath.Join(root, "collector.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nread request\nprintf '%s\\n' '{\"type\":\"complete\",\"mode\":\"snapshot\"}'\n"), 0600); err != nil {
			t.Fatal(err)
		}
		source, err := f.state.GetSource(context.Background(), "allowed/docs/main")
		if err != nil {
			t.Fatal(err)
		}
		source.Path = root
		source.Options = map[string]any{"command": shell, "args": []string{script}}
		if err := f.state.UpsertSource(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		f.request(t, http.DefaultClient, "POST", "/api/v1/sync", robot["token"].(string), map[string]string{"sourceId": source.ID}, nil, 200)
		f.request(t, f.browser, "GET", "/api/v1/memories/secret-memory", "", nil, nil, 200)
	}
	// Reopening the same SQLite state preserves token validity, expiry, and revocation.
	expired := store.Credential{ID: "expired", Subject: "alice", Issuer: f.provider.URL, Kind: "token", ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	sum := sha256.Sum256([]byte("expired-token"))
	if err := f.state.CreateCredential(context.Background(), expired, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	f.request(t, http.DefaultClient, "GET", "/auth/me", "expired-token", nil, nil, 401)
}

func TestDownloadedClientLogin(t *testing.T) {
	for _, implementation := range []struct{ language, file, runtime string }{{"python", "ohyeah.py", "python3"}, {"node", "ohyeah.mjs", "node"}} {
		t.Run(implementation.language, func(t *testing.T) {
			if _, err := exec.LookPath(implementation.runtime); err != nil {
				t.Skip(err)
			}
			f := newFixture(t)
			response, err := http.Get(f.server.URL + "/downloads/" + implementation.language + "/" + implementation.file)
			if err != nil {
				t.Fatal(err)
			}
			script, _ := io.ReadAll(response.Body)
			response.Body.Close()
			root := t.TempDir()
			path := filepath.Join(root, implementation.file)
			if err := os.WriteFile(path, script, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, implementation.runtime, path, "login", "--no-browser")
			cmd.Env = append(os.Environ(), "OHYEAH_CREDENTIALS_DIR="+filepath.Join(root, "credentials"), "OHYEAH_TOKEN=", "OHYEAH_SERVER="+f.server.URL)
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			buffer := make([]byte, 2048)
			n, err := stderr.Read(buffer)
			if err != nil {
				t.Fatal(err)
			}
			text := string(buffer[:n])
			start := strings.Index(text, f.server.URL+"/auth/cli?code=")
			if start < 0 {
				t.Fatalf("login URL missing: %s", text)
			}
			verification := strings.Fields(text[start:])[0]
			response, err = f.browser.Get(verification)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode)
			}
			me := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
			u, _ := url.Parse(verification)
			form := url.Values{"code": {u.Query().Get("code")}, "csrf": {me["csrf"].(string)}}
			req, _ := http.NewRequest("POST", f.server.URL+"/auth/cli/approve", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", f.server.URL)
			response, err = f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("login: %v %s", err, stdout.String())
			}
			var result map[string]any
			json.Unmarshal(stdout.Bytes(), &result)
			if result["loggedIn"] != true || result["token"] != nil {
				t.Fatal(result)
			}
			entries, err := os.ReadDir(filepath.Join(root, "credentials"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("saved credentials: %v %v", entries, err)
			}
			info, err := entries[0].Info()
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("credential permissions: %v", info.Mode())
			}
			otherRuntime, otherLanguage, otherFile := "node", "node", "ohyeah.mjs"
			if implementation.language == "node" {
				otherRuntime, otherLanguage, otherFile = "python3", "python", "ohyeah.py"
			}
			if _, err := exec.LookPath(otherRuntime); err == nil {
				response, err := http.Get(f.server.URL + "/downloads/" + otherLanguage + "/" + otherFile)
				if err != nil {
					t.Fatal(err)
				}
				otherScript, _ := io.ReadAll(response.Body)
				response.Body.Close()
				otherPath := filepath.Join(root, otherFile)
				if err := os.WriteFile(otherPath, otherScript, 0600); err != nil {
					t.Fatal(err)
				}
				check := exec.CommandContext(ctx, otherRuntime, otherPath, "whoami")
				check.Env = cmd.Env
				data, err := check.CombinedOutput()
				if err != nil {
					t.Fatalf("shared credentials: %v %s", err, data)
				}
				var identity map[string]any
				json.Unmarshal(data, &identity)
				if identity["subject"] != "alice" {
					t.Fatalf("shared identity: %s", data)
				}
			}
			for _, action := range []string{"whoami", "logout"} {
				check := exec.CommandContext(ctx, implementation.runtime, path, action)
				check.Env = cmd.Env
				data, err := check.CombinedOutput()
				if err != nil || !json.Valid(data) {
					t.Fatalf("%s: %v %s", action, err, data)
				}
			}
		})
	}
}

// Keeps a simulated OIDC deployment available briefly for browser QA.
func TestBrowserFixture(t *testing.T) {
	stop := os.Getenv("OHYEAH_AUTH_QA_STOP")
	if stop == "" {
		t.Skip("set OHYEAH_AUTH_QA_STOP for interactive browser QA")
	}
	f := newFixture(t)
	t.Logf("OIDC_BROWSER_QA_URL=%s", f.server.URL)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stop); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
