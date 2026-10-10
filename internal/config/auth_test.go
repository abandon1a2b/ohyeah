package config

import (
	"testing"
	"time"
)

func TestAuthenticationConfiguration(t *testing.T) {
	valid := Config{Server: ServerConfig{Address: "0.0.0.0:8787"}, Auth: AuthConfig{Mode: "oidc", PublicURL: "https://memory.example.com", SessionTTL: 8 * time.Hour, TokenTTL: 24 * time.Hour, OIDC: OIDCConfig{Issuer: "https://identity.example.com", ClientID: "ohyeah"}, Principals: []PrincipalConfig{{Subject: "alice", Projects: []string{"example"}}}}}
	if err := valid.ValidateAuth(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		modify func(*Config)
	}{
		{"local wildcard", func(c *Config) { c.Auth.Mode = "local" }},
		{"invalid mode", func(c *Config) { c.Auth.Mode = "none" }},
		{"insecure public URL", func(c *Config) { c.Auth.PublicURL = "http://memory.example.com" }},
		{"insecure issuer", func(c *Config) { c.Auth.OIDC.Issuer = "http://identity.example.com" }},
		{"public path", func(c *Config) { c.Auth.PublicURL += "/path" }},
		{"missing client ID", func(c *Config) { c.Auth.OIDC.ClientID = "" }},
		{"zero TTL", func(c *Config) { c.Auth.TokenTTL = 0 }},
		{"excess TTL", func(c *Config) { c.Auth.TokenTTL = 31 * 24 * time.Hour }},
		{"duplicate subject", func(c *Config) { c.Auth.Principals = append(c.Auth.Principals, c.Auth.Principals[0]) }},
		{"wildcard grant", func(c *Config) { c.Auth.Principals = []PrincipalConfig{{Subject: "alice", Projects: []string{"*"}}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			tc.modify(&candidate)
			if err := candidate.ValidateAuth(); err == nil {
				t.Fatal("invalid auth configuration accepted")
			}
		})
	}
	local := Config{Server: ServerConfig{Address: "127.0.0.1:8787"}}
	if err := local.ValidateAuth(); err != nil {
		t.Fatal(err)
	}
}
