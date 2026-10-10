package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
)

func TestResolveCredentials(t *testing.T) {
	cfg := config.MeilisearchConfig{Mode: "managed", Directory: t.TempDir(), URL: "http://127.0.0.1:7700"}
	if err := Resolve(&cfg); err != nil {
		t.Fatal(err)
	}
	key := cfg.APIKey
	if len(key) != 64 {
		t.Fatalf("key length: %d", len(key))
	}
	cfg.APIKey = ""
	if err := Resolve(&cfg); err != nil || cfg.APIKey != key {
		t.Fatalf("credentials did not persist: %v", err)
	}
	cfg.APIKey = strings.Repeat("x", 32)
	if err := Resolve(&cfg); err == nil {
		t.Fatal("accepted conflicting key")
	}
	for _, raw := range []string{"http://example.com:7700", "http://0.0.0.0:7700", "https://127.0.0.1:7700", "http://127.0.0.1", "http://127.0.0.1:7700/path", "http://127.0.0.1:0", "http://127.0.0.1:65536"} {
		if _, err := (config.MeilisearchConfig{URL: raw}).ManagedAddress(); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestConcurrentCredentialResolution(t *testing.T) {
	directory := t.TempDir()
	var group sync.WaitGroup
	keys := make(chan string, 12)
	for i := 0; i < cap(keys); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			cfg := config.MeilisearchConfig{Mode: "managed", Directory: directory, URL: "http://127.0.0.1:7700"}
			if err := Resolve(&cfg); err != nil {
				t.Error(err)
				return
			}
			keys <- cfg.APIKey
		}()
	}
	group.Wait()
	close(keys)
	var first string
	for key := range keys {
		if first == "" {
			first = key
		}
		if key != first {
			t.Fatal("concurrent commands generated different credentials")
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "master-key"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("key permissions: %v %v", info, err)
		}
	}
}

func TestDownloadFailureAndIgnoredRange(t *testing.T) {
	for _, failure := range []string{"status", "cancel", "bad-range", "ignored-range"} {
		t.Run(failure, func(t *testing.T) {
			body := "complete download"
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := requests.Add(1)
				switch failure {
				case "status":
					w.WriteHeader(503)
				case "cancel":
					<-r.Context().Done()
				default:
					if attempt == 1 {
						w.Header().Set("Content-Length", fmt.Sprint(len(body)))
						io.WriteString(w, body[:4])
						return
					}
					if failure == "bad-range" {
						w.Header().Set("Content-Range", "bytes 0-2/3")
						w.WriteHeader(206)
						io.WriteString(w, "bad")
						return
					}
					io.WriteString(w, body)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			hash := sha256.Sum256([]byte(body))
			path := filepath.Join(t.TempDir(), "binary")
			err := download(ctx, server.Client(), server.URL, path, hex.EncodeToString(hash[:]))
			if (err == nil) != (failure == "ignored-range") {
				t.Fatalf("download result: %v", err)
			}
			if failure != "ignored-range" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("failed download was installed")
				}
			}
			files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".meilisearch-download-*"))
			if len(files) != 0 {
				t.Fatal("temporary file leaked")
			}
		})
	}
}

func TestDownloadVerification(t *testing.T) {
	body := "binary fixture"
	hash := sha256.Sum256([]byte(body))
	checksum := hex.EncodeToString(hash[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "meilisearch")
	if err := download(context.Background(), server.Client(), server.URL, path, "wrong"); err == nil {
		t.Fatal("accepted corrupt download")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed download installed a file")
	}
	if err := download(context.Background(), server.Client(), server.URL, path, checksum); err != nil {
		t.Fatal(err)
	}
	if err := verify(path, checksum); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), server.Client(), server.URL, path, "wrong"); err == nil {
		t.Fatal("accepted corrupt replacement")
	}
	if err := verify(path, checksum); err != nil {
		t.Fatalf("failed download damaged installed binary: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".meilisearch-download-*"))
	if len(files) != 0 {
		t.Fatal("temporary downloads leaked")
	}
}

func TestDownloadResumesAfterDisconnect(t *testing.T) {
	body := "complete binary fixture"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			io.WriteString(w, body[:8])
			return
		}
		if r.Header.Get("Range") != "bytes=8-" {
			t.Errorf("range = %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 8-%d/%d", len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		io.WriteString(w, body[8:])
	}))
	defer server.Close()
	hash := sha256.Sum256([]byte(body))
	path := filepath.Join(t.TempDir(), "binary")
	if err := download(context.Background(), server.Client(), server.URL, path, hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestManagedLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a Unix shebang")
	}
	_, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	cfg := config.MeilisearchConfig{Mode: "managed", Directory: t.TempDir(), URL: "http://" + address}
	if err := Resolve(&cfg); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env python3
import http.server, sys
address = sys.argv[sys.argv.index('--http-addr') + 1]
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"status":"available"}')
http.server.HTTPServer(('127.0.0.1', int(address.split(':')[-1])), Handler).serve_forever()
`
	platform := runtime.GOOS + "/" + runtime.GOARCH
	original := assets[platform]
	defer func() { assets[platform] = original }()
	hash := sha256.Sum256([]byte(script))
	assets[platform] = asset{name: "fixture", checksum: hex.EncodeToString(hash[:])}
	if err := os.WriteFile(filepath.Join(cfg.Directory, Version+"-fixture"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	process, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if _, err := Start(context.Background(), cfg); err == nil {
		t.Fatal("duplicate owner accepted")
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("child still listening after shutdown: %v", err)
	}
	probe.Close()
	process, err = Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(context.Background()); err == nil {
		t.Fatal("unexpected exit was not reported")
	}
	process.Close()
	occupied, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "occupied") {
		t.Fatalf("port conflict: %v", err)
	}
	occupied.Close()
	failing := []byte("#!/bin/sh\nexit 9\n")
	hash = sha256.Sum256(failing)
	assets[platform] = asset{name: "fixture", checksum: hex.EncodeToString(hash[:])}
	if err := os.WriteFile(filepath.Join(cfg.Directory, Version+"-fixture"), failing, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "startup") {
			t.Fatalf("startup failure did not release lock: %v", err)
		}
	}
}
