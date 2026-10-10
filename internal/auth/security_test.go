package auth

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/gofiber/fiber/v2"
)

func TestIssuerSwitchInvalidatesCredentialsAndPendingPairing(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	s := &Service{store: state, cfg: config.AuthConfig{Mode: "oidc", TokenTTL: time.Hour, OIDC: config.OIDCConfig{Issuer: "https://old.example"}, Principals: []config.PrincipalConfig{{Subject: "alice", Projects: []string{"project"}}}}}
	token, _, err := s.issue(context.Background(), "alice", "token", "fixture", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(pairFlow{SecretHash: hash("secret"), Issuer: s.cfg.OIDC.Issuer})
	if err := state.PutAuthFlow(context.Background(), hash("code"), "cli", string(payload)); err != nil {
		t.Fatal(err)
	}
	if err := state.ApproveAuthFlow(context.Background(), hash("code"), "alice"); err != nil {
		t.Fatal(err)
	}
	s.cfg.OIDC.Issuer = "https://new.example"
	server := fiber.New()
	server.Get("/protected", s.Authenticate, func(c *fiber.Ctx) error { return c.SendStatus(200) })
	server.Post("/poll", s.pollPair)
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("old issuer token accepted: %d", response.StatusCode)
	}
	req = httptest.NewRequest("POST", "/poll", strings.NewReader(`{"userCode":"code","deviceCode":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	response, err = server.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("old issuer pairing accepted: %d", response.StatusCode)
	}
	if err := s.validPair(context.Background(), "code"); err == nil {
		t.Fatal("old issuer browser pairing accepted")
	}
}

func TestMissingIdentityDeniesPermissions(t *testing.T) {
	server := fiber.New()
	server.Get("/", func(c *fiber.Ctx) error {
		if Can(c, "project", "read") || Can(c, "", "admin") {
			t.Error("missing identity granted permissions")
		}
		return c.SendStatus(200)
	})
	response, err := server.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestHTTPSCookiesUseHostPrefix(t *testing.T) {
	s := &Service{cfg: config.AuthConfig{PublicURL: "https://memory.example.com"}}
	server := fiber.New()
	server.Get("/", func(c *fiber.Ctx) error { s.cookie(c, sessionCookie, "test", time.Hour); return c.SendStatus(200) })
	response, err := server.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-ohyeah_session" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" || cookies[0].Path != "/" {
		t.Fatalf("unsafe session cookie: %#v", cookies)
	}
}
