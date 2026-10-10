package auth

import (
	"database/sql"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/gofiber/fiber/v2"
)

func (s *Service) protectedBootstrap(p config.PrincipalConfig) bool {
	for _, configured := range s.cfg.Principals {
		if configured.Subject == p.Subject && configured.Admin && !configured.Disabled {
			return p.Disabled || !p.Admin
		}
	}
	return false
}

func (s *Service) registerPrincipals(server *fiber.App) {
	subjectOf := func(c *fiber.Ctx) string {
		value, err := url.PathUnescape(c.Params("subject"))
		if err != nil {
			return c.Params("subject")
		}
		return value
	}
	admin := func(c *fiber.Ctx) error {
		if err := Require(c, "", "admin"); err != nil {
			return err
		}
		return c.Next()
	}
	server.Get("/auth/principals", s.Authenticate, admin, func(c *fiber.Ctx) error {
		policies, err := s.store.ListPrincipalPolicies(c.Context(), s.cfg.OIDC.Issuer)
		if err != nil {
			return err
		}
		bySubject := map[string]store.PrincipalPolicy{}
		for _, p := range s.cfg.Principals {
			p := p
			bySubject[p.Subject] = store.PrincipalPolicy{Subject: p.Subject, Principal: &p, Configured: true}
		}
		for _, policy := range policies {
			baseline, configured := bySubject[policy.Subject]
			policy.Configured = configured
			if policy.Principal == nil {
				if configured {
					policy.Principal = baseline.Principal
				} else {
					policy.Principal = &config.PrincipalConfig{Subject: policy.Subject, Disabled: true}
				}
			}
			bySubject[policy.Subject] = policy
		}
		result := []store.PrincipalPolicy{}
		for _, p := range bySubject {
			result = append(result, p)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Subject < result[j].Subject })
		return c.JSON(result)
	})
	server.Get("/auth/principals/:subject", s.Authenticate, admin, func(c *fiber.Ctx) error {
		subject := subjectOf(c)
		policy, err := s.store.PrincipalPolicy(c.Context(), s.cfg.OIDC.Issuer, subject)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if policy.Principal == nil {
			for _, p := range s.cfg.Principals {
				if p.Subject == subject {
					p := p
					policy.Principal = &p
					policy.Configured = true
					break
				}
			}
		}
		return c.JSON(policy)
	})
	server.Put("/auth/principals/:subject", s.Authenticate, admin, func(c *fiber.Ctx) error {
		var request struct {
			ExpectedVersion int64                  `json:"expectedVersion"`
			Principal       config.PrincipalConfig `json:"principal"`
		}
		if c.BodyParser(&request) != nil || request.ExpectedVersion < 0 || request.Principal.Subject != subjectOf(c) || len(request.Principal.Subject) > 256 || len(request.Principal.Name) > 100 {
			return fiber.ErrBadRequest
		}
		candidate := config.Config{Auth: s.cfg}
		candidate.Auth.Principals = []config.PrincipalConfig{request.Principal}
		if err := candidate.ValidateAuth(); err != nil {
			return fiber.NewError(400, err.Error())
		}
		if s.protectedBootstrap(request.Principal) || (request.Principal.Subject == Current(c).Subject && (request.Principal.Disabled || !request.Principal.Admin)) {
			return fiber.NewError(403, "cannot disable or demote the active or configured bootstrap administrator")
		}
		policy, err := s.store.SavePrincipalPolicy(c.Context(), s.cfg.OIDC.Issuer, request.Principal.Subject, request.ExpectedVersion, &request.Principal)
		if errors.Is(err, store.ErrPolicyConflict) {
			return fiber.NewError(409, err.Error())
		}
		if err != nil {
			return err
		}
		return c.JSON(policy)
	})
	server.Delete("/auth/principals/:subject", s.Authenticate, admin, func(c *fiber.Ctx) error {
		version, err := strconv.ParseInt(c.Query("version"), 10, 64)
		if err != nil || version < 0 {
			return fiber.ErrBadRequest
		}
		subject := subjectOf(c)
		if strings.TrimSpace(subject) == "" || len(subject) > 256 {
			return fiber.ErrBadRequest
		}
		if subject == Current(c).Subject {
			found := false
			for _, p := range s.cfg.Principals {
				if p.Subject == subject && p.Admin && !p.Disabled {
					found = true
				}
			}
			if !found {
				return fiber.ErrForbidden
			}
		}
		policy, err := s.store.SavePrincipalPolicy(c.Context(), s.cfg.OIDC.Issuer, subject, version, nil)
		if errors.Is(err, store.ErrPolicyConflict) {
			return fiber.NewError(409, err.Error())
		}
		if err != nil {
			return err
		}
		return c.JSON(policy)
	})
}
