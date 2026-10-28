package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

type AuthConfig struct {
	Mode       string            `mapstructure:"mode"`
	PublicURL  string            `mapstructure:"public_url"`
	SessionTTL time.Duration     `mapstructure:"session_ttl"`
	TokenTTL   time.Duration     `mapstructure:"token_ttl"`
	OIDC       OIDCConfig        `mapstructure:"oidc"`
	Principals []PrincipalConfig `mapstructure:"principals"`
}

type OIDCConfig struct {
	Issuer       string `mapstructure:"issuer"`
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
}

type PrincipalConfig = model.Principal

func SecureURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func (c Config) ValidateAuth() error {
	if c.Auth.Mode == "" || c.Auth.Mode == "local" {
		host, _, err := net.SplitHostPort(c.Server.Address)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			return errors.New("auth.mode local requires server.address to bind a loopback IP; use oidc for central deployment")
		}
		return nil
	}
	if c.Auth.Mode != "oidc" {
		return errors.New("auth.mode must be local or oidc")
	}
	a := c.Auth
	if !SecureURL(a.PublicURL) || !SecureURL(a.OIDC.Issuer) || strings.TrimSpace(a.OIDC.ClientID) == "" {
		return errors.New("OIDC requires auth.public_url, auth.oidc.issuer and client_id; URLs must use HTTPS (HTTP allowed only for loopback IPs)")
	}
	u, _ := url.Parse(a.PublicURL)
	if u.Path != "" && u.Path != "/" {
		return errors.New("auth.public_url must be an origin without a path")
	}
	if a.SessionTTL <= 0 || a.SessionTTL > 24*time.Hour || a.TokenTTL <= 0 || a.TokenTTL > 30*24*time.Hour {
		return errors.New("auth session_ttl must be within 24h and token_ttl within 30 days, both positive")
	}
	seen := map[string]bool{}
	for _, p := range a.Principals {
		if strings.TrimSpace(p.Subject) == "" || seen[p.Subject] {
			return errors.New("auth.principals require unique nonempty subjects")
		}
		seen[p.Subject] = true
		for _, project := range append(append([]string{}, p.Projects...), p.SyncProjects...) {
			if !configIDPattern.MatchString(project) {
				return fmt.Errorf("invalid auth project %q", project)
			}
		}
		for _, source := range append(append([]string{}, p.SyncSources...), p.UploadSources...) {
			parts := strings.Split(source, "/")
			if len(parts) != 3 {
				return fmt.Errorf("invalid source grant %q", source)
			}
			for _, part := range parts {
				if !configIDPattern.MatchString(part) {
					return fmt.Errorf("invalid source grant %q", source)
				}
			}
		}
	}
	return nil
}
