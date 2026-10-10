package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
)

func TestDiscoveryRedirectFailsClosed(t *testing.T) {
	var contacted atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Store(true); w.Write([]byte(`{}`)) }))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer provider.Close()
	cfg := config.Config{Server: config.ServerConfig{Address: "127.0.0.1:8787"}, Auth: config.AuthConfig{Mode: "oidc", PublicURL: "http://127.0.0.1:8787", SessionTTL: time.Hour, TokenTTL: time.Hour, OIDC: config.OIDCConfig{Issuer: provider.URL, ClientID: "client"}}}
	if _, err := New(context.Background(), cfg, nil); err == nil {
		t.Fatal("redirected discovery accepted")
	}
	if contacted.Load() {
		t.Fatal("provider redirect target contacted")
	}
}
