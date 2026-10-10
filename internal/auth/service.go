package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"golang.org/x/oauth2"
)

const sessionCookie = "ohyeah_session"
const bindingCookie = "ohyeah_login"

type Service struct {
	cfg      config.AuthConfig
	store    *store.Store
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	http     *http.Client
}

type Identity struct {
	config.PrincipalConfig
	Credential store.Credential `json:"credential"`
}

func (s *Service) Enabled() bool { return s != nil && s.cfg.Mode == "oidc" }

func New(ctx context.Context, cfg config.Config, state *store.Store) (*Service, error) {
	if err := cfg.ValidateAuth(); err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg.Auth, store: state, http: &http.Client{Timeout: 10 * time.Second}}
	if !s.Enabled() {
		return s, nil
	}
	ctx = oidc.ClientContext(ctx, s.http)
	provider, err := oidc.NewProvider(ctx, s.cfg.OIDC.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	endpoint := provider.Endpoint()
	var discovery struct {
		JWKS string `json:"jwks_uri"`
	}
	if err := provider.Claims(&discovery); err != nil {
		return nil, err
	}
	if !config.SecureURL(endpoint.AuthURL) || !config.SecureURL(endpoint.TokenURL) || !config.SecureURL(discovery.JWKS) {
		return nil, errors.New("OIDC endpoints must use HTTPS or loopback HTTP")
	}
	s.oauth = oauth2.Config{ClientID: s.cfg.OIDC.ClientID, ClientSecret: s.cfg.OIDC.ClientSecret, RedirectURL: s.origin() + "/auth/callback", Endpoint: endpoint, Scopes: []string{oidc.ScopeOpenID, "profile"}}
	s.verifier = provider.Verifier(&oidc.Config{ClientID: s.cfg.OIDC.ClientID})
	return s, nil
}

func random() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func equal(a, b string) bool      { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s *Service) origin() string { return strings.TrimRight(s.cfg.PublicURL, "/") }

func (s *Service) principal(subject string) (config.PrincipalConfig, bool) {
	for _, p := range s.cfg.Principals {
		if p.Subject == subject {
			return p, true
		}
	}
	return config.PrincipalConfig{}, false
}

func (s *Service) cookie(c *fiber.Ctx, name, value string, ttl time.Duration) {
	c.Cookie(&fiber.Cookie{Name: name, Value: value, Path: "/", HTTPOnly: true, Secure: strings.HasPrefix(s.origin(), "https://"), SameSite: "Lax", Expires: time.Now().Add(ttl), MaxAge: int(ttl.Seconds())})
}

func (s *Service) issue(ctx context.Context, subject, kind, name string, ttl time.Duration) (string, store.Credential, error) {
	if _, ok := s.principal(subject); !ok {
		return "", store.Credential{}, fiber.ErrForbidden
	}
	token, err := random()
	if err != nil {
		return "", store.Credential{}, err
	}
	id, err := random()
	if err != nil {
		return "", store.Credential{}, err
	}
	c := store.Credential{ID: id, Subject: subject, Issuer: s.cfg.OIDC.Issuer, Kind: kind, Name: name, ExpiresAt: time.Now().Add(ttl).Unix(), CreatedAt: time.Now().Unix()}
	err = s.store.CreateCredential(ctx, c, hash(token))
	if err == nil {
		slog.Info("credential issued", "subject", subject, "credential", id, "kind", kind)
	}
	return token, c, err
}

func Current(c *fiber.Ctx) *Identity { p, _ := c.Locals("identity").(*Identity); return p }
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func Can(c *fiber.Ctx, project, action string) bool {
	p := Current(c)
	if p == nil {
		return c.Locals("localAuth") == true
	}
	if p.Admin {
		return true
	}
	if action == "read" {
		return contains(p.Projects, project)
	}
	if action == "sync" {
		return contains(p.SyncProjects, project)
	}
	return false
}
func Require(c *fiber.Ctx, project, action string) error {
	if !Can(c, project, action) {
		return fiber.ErrForbidden
	}
	return nil
}

func (s *Service) Authenticate(c *fiber.Ctx) error {
	if !s.Enabled() {
		c.Locals("localAuth", true)
		return c.Next()
	}
	raw, kind := c.Cookies(sessionCookie), "session"
	if authorization := c.Get("Authorization"); authorization != "" {
		if !strings.HasPrefix(authorization, "Bearer ") {
			return fiber.ErrUnauthorized
		}
		raw, kind = strings.TrimPrefix(authorization, "Bearer "), "token"
	}
	if raw == "" {
		return fiber.ErrUnauthorized
	}
	credential, err := s.store.FindCredential(c.Context(), hash(raw), kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fiber.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if credential.Issuer != s.cfg.OIDC.Issuer {
		return fiber.ErrUnauthorized
	}
	p, ok := s.principal(credential.Subject)
	if !ok {
		return fiber.ErrForbidden
	}
	c.Locals("identity", &Identity{PrincipalConfig: p, Credential: credential})
	if kind == "session" && c.Method() != "GET" && c.Method() != "HEAD" {
		csrf := c.Get("X-CSRF-Token")
		if csrf == "" {
			csrf = c.FormValue("csrf")
		}
		if c.Get("Origin") != s.origin() || !equal(csrf, hash("csrf:"+raw)) {
			return fiber.NewError(403, "invalid CSRF token or origin")
		}
	}
	err = c.Next()
	if c.Method() != "GET" && c.Method() != "HEAD" {
		status := c.Response().StatusCode()
		if err != nil {
			status = 500
			var failure *fiber.Error
			if errors.As(err, &failure) {
				status = failure.Code
			}
		}
		slog.Info("authenticated action", "subject", p.Subject, "credential", credential.ID, "method", c.Method(), "path", c.Path(), "status", status)
	}
	return err
}

type loginFlow struct{ Binding, Nonce, Verifier, Pair, Issuer string }
type pairFlow struct {
	SecretHash string `json:"secretHash"`
	Issuer     string `json:"issuer"`
}

func (s *Service) Register(server *fiber.App) {
	server.Get("/auth/info", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"mode": s.cfg.Mode, "enabled": s.Enabled()}) })
	if !s.Enabled() {
		server.Get("/auth/me", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"subject": "local", "admin": true}) })
		return
	}
	server.Use("/auth", func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store")
		c.Set("Referrer-Policy", "strict-origin")
		c.Set("X-Frame-Options", "DENY")
		return c.Next()
	})
	limit := limiter.New(limiter.Config{Max: 20, Expiration: time.Minute})
	server.Get("/auth/login", limit, s.login)
	server.Get("/auth/callback", s.callback)
	server.Post("/auth/cli/start", limit, s.startPair)
	server.Post("/auth/cli/token", limiter.New(limiter.Config{Max: 120, Expiration: time.Minute}), s.pollPair)
	server.Get("/auth/cli", s.browserLogin, s.Authenticate, s.pairPage)
	server.Post("/auth/cli/approve", s.Authenticate, s.approvePair)
	server.Get("/auth/me", s.Authenticate, s.me)
	server.Post("/auth/logout", s.Authenticate, func(c *fiber.Ctx) error {
		p := Current(c)
		if err := s.store.RevokeCredential(c.Context(), p.Credential.ID, p.Subject, s.cfg.OIDC.Issuer, false); err != nil {
			return err
		}
		s.cookie(c, sessionCookie, "", -time.Hour)
		return c.JSON(fiber.Map{"loggedOut": true})
	})
	server.Get("/auth/tokens", s.Authenticate, func(c *fiber.Ctx) error {
		result, err := s.store.ListCredentials(c.Context(), Current(c).Subject, s.cfg.OIDC.Issuer)
		if err != nil {
			return err
		}
		return c.JSON(result)
	})
	server.Delete("/auth/tokens/:id", s.Authenticate, func(c *fiber.Ctx) error {
		p := Current(c)
		if err := s.store.RevokeCredential(c.Context(), c.Params("id"), p.Subject, s.cfg.OIDC.Issuer, p.Admin); err != nil {
			return err
		}
		slog.Info("credential revoked", "subject", p.Subject, "credential", c.Params("id"))
		return c.JSON(fiber.Map{"revoked": true})
	})
	server.Post("/auth/tokens", s.Authenticate, func(c *fiber.Ctx) error {
		if err := Require(c, "", "admin"); err != nil {
			return err
		}
		var request struct {
			Subject string `json:"subject"`
			Name    string `json:"name"`
		}
		if err := c.BodyParser(&request); err != nil || request.Subject == "" || len(request.Name) > 100 {
			return fiber.ErrBadRequest
		}
		token, credential, err := s.issue(c.Context(), request.Subject, "token", request.Name, s.cfg.TokenTTL)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"token": token, "credential": credential})
	})
	server.Use("/auth", func(c *fiber.Ctx) error { return fiber.ErrNotFound })
}

func (s *Service) me(c *fiber.Ctx) error {
	p := Current(c)
	result := fiber.Map{"subject": p.Subject, "name": p.Name, "admin": p.Admin, "projects": p.Projects, "syncProjects": p.SyncProjects, "credential": p.Credential}
	if p.Credential.Kind == "session" {
		result["csrf"] = hash("csrf:" + c.Cookies(sessionCookie))
	}
	return c.JSON(result)
}

func (s *Service) browserLogin(c *fiber.Ctx) error {
	credential, err := s.store.FindCredential(c.Context(), hash(c.Cookies(sessionCookie)), "session")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || credential.Issuer != s.cfg.OIDC.Issuer {
		s.cookie(c, sessionCookie, "", -time.Hour)
		return c.Redirect("/auth/login?pair="+url.QueryEscape(c.Query("code")), 303)
	}
	return c.Next()
}

func (s *Service) login(c *fiber.Ctx) error {
	state, err := random()
	if err != nil {
		return err
	}
	binding, err := random()
	if err != nil {
		return err
	}
	nonce, err := random()
	if err != nil {
		return err
	}
	pair := c.Query("pair")
	if pair != "" {
		if err := s.validPair(c.Context(), pair); err != nil {
			return fiber.ErrBadRequest
		}
	}
	flow := loginFlow{Binding: hash(binding), Nonce: nonce, Verifier: oauth2.GenerateVerifier(), Pair: pair, Issuer: s.cfg.OIDC.Issuer}
	payload, _ := json.Marshal(flow)
	if err := s.store.PutAuthFlow(c.Context(), hash(state), "oidc", string(payload)); err != nil {
		return err
	}
	s.cookie(c, bindingCookie, binding, 10*time.Minute)
	return c.Redirect(s.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(flow.Verifier)), 303)
}

func (s *Service) callback(c *fiber.Ctx) error {
	payload, _, err := s.store.AuthFlow(c.Context(), hash(c.Query("state")), "oidc", true)
	if err != nil {
		return fiber.NewError(400, "invalid or expired login state")
	}
	var flow loginFlow
	if json.Unmarshal([]byte(payload), &flow) != nil || flow.Issuer != s.cfg.OIDC.Issuer || !equal(hash(c.Cookies(bindingCookie)), flow.Binding) {
		return fiber.NewError(400, "login browser binding mismatch")
	}
	s.cookie(c, bindingCookie, "", -time.Hour)
	ctx, cancel := context.WithTimeout(oidc.ClientContext(c.Context(), s.http), 10*time.Second)
	defer cancel()
	token, err := s.oauth.Exchange(ctx, c.Query("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		slog.Warn("OIDC exchange failed")
		return fiber.NewError(401, "OIDC exchange failed")
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return fiber.NewError(401, "OIDC provider omitted ID token")
	}
	id, err := s.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" || !equal(id.Nonce, flow.Nonce) {
		return fiber.NewError(401, "invalid OIDC ID token")
	}
	session, _, err := s.issue(ctx, id.Subject, "session", "browser", s.cfg.SessionTTL)
	if err != nil {
		slog.Warn("OIDC login not authorized", "issuer", s.cfg.OIDC.Issuer, "subject", id.Subject)
		return err
	}
	// Rotate the old browser credential when logging in again.
	if old, err := s.store.FindCredential(ctx, hash(c.Cookies(sessionCookie)), "session"); err == nil {
		_ = s.store.RevokeCredential(ctx, old.ID, old.Subject, old.Issuer, false)
	}
	s.cookie(c, sessionCookie, session, s.cfg.SessionTTL)
	if flow.Pair != "" {
		return c.Redirect("/auth/cli?code="+url.QueryEscape(flow.Pair), 303)
	}
	return c.Redirect("/", 303)
}

func (s *Service) startPair(c *fiber.Ctx) error {
	secret, err := random()
	if err != nil {
		return err
	}
	code, err := random()
	if err != nil {
		return err
	}
	code = code[:16]
	payload, _ := json.Marshal(pairFlow{SecretHash: hash(secret), Issuer: s.cfg.OIDC.Issuer})
	if err := s.store.PutAuthFlow(c.Context(), hash(code), "cli", string(payload)); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"deviceCode": secret, "userCode": code, "verificationURI": s.origin() + "/auth/cli?code=" + code, "expiresIn": 600, "interval": 5})
}

var consent = template.Must(template.New("consent").Parse(`<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><title>Ohyeah CLI authorization</title></head><body><main><h1>Ohyeah CLI authorization</h1><p>Account: {{.Subject}}</p><p>Confirm that this code matches your CLI: <strong>{{.Code}}</strong></p><p>This grants the CLI your account's project permissions until the token expires. Only approve a login you started.</p><form method="post" action="/auth/cli/approve"><input type="hidden" name="code" value="{{.Code}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Authorize CLI</button></form></main></body></html>`))

func (s *Service) pairPage(c *fiber.Ctx) error {
	if err := s.validPair(c.Context(), c.Query("code")); err != nil {
		return fiber.ErrNotFound
	}
	c.Type("html")
	c.Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	return consent.Execute(c.Response().BodyWriter(), map[string]string{"Subject": Current(c).Subject, "Code": c.Query("code"), "CSRF": hash("csrf:" + c.Cookies(sessionCookie))})
}

func (s *Service) approvePair(c *fiber.Ctx) error {
	if Current(c).Credential.Kind != "session" {
		return fiber.ErrForbidden
	}
	if err := s.validPair(c.Context(), c.FormValue("code")); err != nil {
		return fiber.ErrNotFound
	}
	if err := s.store.ApproveAuthFlow(c.Context(), hash(c.FormValue("code")), Current(c).Subject); err != nil {
		return err
	}
	slog.Info("CLI authorization approved", "subject", Current(c).Subject)
	return c.Type("html").SendString("<!doctype html><title>Ohyeah</title><h1>CLI authorized</h1><p>You can close this window.</p>")
}

func (s *Service) validPair(ctx context.Context, code string) error {
	payload, _, err := s.store.AuthFlow(ctx, hash(code), "cli", false)
	if err != nil {
		return err
	}
	var flow pairFlow
	if json.Unmarshal([]byte(payload), &flow) != nil || flow.Issuer != s.cfg.OIDC.Issuer {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) pollPair(c *fiber.Ctx) error {
	var request struct {
		DeviceCode string `json:"deviceCode"`
		UserCode   string `json:"userCode"`
	}
	if c.BodyParser(&request) != nil {
		return fiber.ErrBadRequest
	}
	key := hash(request.UserCode)
	payload, subject, err := s.store.AuthFlow(c.Context(), key, "cli", false)
	if err != nil {
		return fiber.NewError(400, "expired or invalid authorization")
	}
	var flow pairFlow
	if json.Unmarshal([]byte(payload), &flow) != nil || flow.Issuer != s.cfg.OIDC.Issuer || !equal(flow.SecretHash, hash(request.DeviceCode)) {
		return fiber.ErrUnauthorized
	}
	if subject == "" {
		return c.Status(202).JSON(fiber.Map{"pending": true})
	}
	_, subject, err = s.store.AuthFlow(c.Context(), key, "cli", true)
	if err != nil {
		return fiber.NewError(400, "authorization already consumed")
	}
	token, credential, err := s.issue(c.Context(), subject, "token", "CLI", s.cfg.TokenTTL)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"token": token, "credential": credential})
}
