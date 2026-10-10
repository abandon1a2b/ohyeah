package distribution

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestDownloads(t *testing.T) {
	server := fiber.New()
	Register(server, "127.0.0.1:9876")
	server.Use(func(c *fiber.Ctx) error { return c.SendString("SPA") })
	for language, client := range clients {
		response, err := server.Test(httptest.NewRequest("GET", "/downloads/"+language+".zip", nil))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/zip" {
			t.Fatalf("download: %v, %d", err, response.StatusCode)
		}
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		if len(archive.File) != 3 {
			t.Fatalf("files: %d", len(archive.File))
		}
		repeated, err := server.Test(httptest.NewRequest("GET", "/downloads/"+language+".zip", nil))
		if err != nil {
			t.Fatal(err)
		}
		repeatedData, _ := io.ReadAll(repeated.Body)
		repeated.Body.Close()
		if !bytes.Equal(data, repeatedData) {
			t.Fatal("download archive is not deterministic")
		}
		for _, name := range []string{"ohyeah/SKILL.md", "ohyeah/README.md", "ohyeah/scripts/" + client.file} {
			file, err := archive.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			content, _ := io.ReadAll(file)
			file.Close()
			if !bytes.Contains(content, []byte("http://127.0.0.1:9876")) || bytes.Contains(content, []byte("__SERVER_JSON__")) {
				t.Fatalf("unrendered file: %s", name)
			}
			if name == "ohyeah/scripts/"+client.file {
				response, err := server.Test(httptest.NewRequest("GET", "/downloads/"+language+"/"+client.file, nil))
				if err != nil {
					t.Fatal(err)
				}
				direct, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if !bytes.Equal(content, direct) {
					t.Fatal("single-file download differs from archive")
				}
			}
		}
	}
	for _, path := range []string{"/downloads/go.zip", "/downloads/python/nope", "/downloads/unknown/path/deep"} {
		response, err := server.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
}

func TestWildcardDownloadAddressAndHostHeader(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8787", "[::]:8787", ":8787", "[::1]:8787"} {
		server := fiber.New()
		Register(server, address)
		request := httptest.NewRequest("GET", "http://attacker.invalid/downloads", nil)
		response, err := server.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if bytes.Contains(data, []byte("attacker.invalid")) || bytes.Contains(data, []byte("0.0.0.0")) {
			t.Fatalf("unsafe default: %s", data)
		}
	}
}

// Run the same contract against both generated clients, including actual HTTP I/O.
func TestClientContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/search":
			_ = json.NewEncoder(w).Encode(r.URL.Query())
		case "/api/v1/projects":
			io.WriteString(w, `[{"id":"example"}]`)
		case "/api/v1/status":
			io.WriteString(w, `{"backend":"available"}`)
		case "/api/v1/memories/missing":
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"memory missing"}`)
		case "/api/v1/memories/slow":
			<-r.Context().Done()
		case "/api/v1/memories/error-stall":
			w.WriteHeader(503)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/api/v1/memories/truncated":
			w.Header().Set("Content-Length", "100")
			io.WriteString(w, `{"ok":`)
		case "/api/v1/memories/error-html":
			w.WriteHeader(502)
			io.WriteString(w, "<html>bad gateway</html>")
		case "/api/v1/memories/dribble":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			for _, char := range `{"ok":true}` {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(30 * time.Millisecond):
				}
				fmt.Fprintf(w, "%c", char)
				w.(http.Flusher).Flush()
			}
		case "/api/v1/memories/bad-json":
			io.WriteString(w, `<html>not JSON</html>`)
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
		}
	}))
	defer server.Close()
	for language, client := range clients {
		t.Run(language, func(t *testing.T) {
			if _, err := exec.LookPath(client.run); err != nil {
				t.Skip(client.run + " unavailable")
			}
			files, err := Files(language, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), client.file)
			if err := os.WriteFile(path, files["scripts/"+client.file], 0600); err != nil {
				t.Fatal(err)
			}
			invoke := func(expectedCode int, args ...string) map[string]any {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, client.run, append([]string{path}, args...)...)
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(entry, "OHYEAH_SERVER=") {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				code := 0
				if err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						code = exit.ExitCode()
					} else {
						t.Fatal(err)
					}
				}
				if code != expectedCode {
					t.Fatalf("%v: exit=%d stdout=%s stderr=%s", args, code, &stdout, &stderr)
				}
				output := stdout.Bytes()
				if code != 0 {
					if stdout.Len() != 0 {
						t.Fatal("error leaked to stdout")
					}
					output = stderr.Bytes()
				} else if stderr.Len() != 0 {
					t.Fatalf("unexpected stderr: %s", &stderr)
				}
				var result map[string]any
				if err := json.Unmarshal(output, &result); err != nil {
					t.Fatalf("invalid JSON: %s: %v", output, err)
				}
				return result
			}
			query := "缓存 失效 & 修复"
			result := invoke(0, "--server", server.URL, "search", query, "--project", "测试", "--mount", "p/t/m", "--kind", "conclusion,verification", "--mode", "hybrid", "--limit", "3", "--json")
			for key, expected := range map[string]string{"q": query, "project": "测试", "mount": "p/t/m", "kind": "conclusion,verification", "mode": "hybrid", "limit": "3"} {
				values, ok := result[key].([]any)
				if !ok || len(values) != 1 || values[0] != expected {
					t.Fatalf("%s: %#v", key, result[key])
				}
			}
			if result := invoke(0, "status"); result["backend"] != "available" {
				t.Fatal(result)
			}
			invoke(0, "search", "--project", "p", "--json", "query")
			invoke(0, "search", "--project", "p", "--", "-query")
			invoke(2, "search", "query", "--project", "p", "--limit", "2147483648")
			if result := invoke(0, "get", "中文/?#"); result["path"] != "/api/v1/memories/中文/?#" {
				t.Fatal(result)
			}
			if result := invoke(1, "get", "missing"); result["status"] != float64(404) || result["error"] != "memory missing" {
				t.Fatal(result)
			}
			invoke(1, "get", "bad-json")
			invoke(1, "get", "slow", "--timeout", "0.05")
			invoke(1, "get", "error-stall", "--timeout", "0.05")
			invoke(1, "get", "dribble", "--timeout", "0.05")
			invoke(1, "get", "truncated")
			if result := invoke(1, "get", "error-html"); result["status"] != float64(502) {
				t.Fatal(result)
			}
			for _, args := range [][]string{{"search", "query"}, {"search", "query", "--project", "p", "--limit", "0"}, {"search", "query", "--project", "p", "--mode", "bad"}, {"get"}, {"status", "extra"}, {"status", "--server", "file:///tmp"}, {"status", "--timeout", "nan"}, {"status", "--unknown"}} {
				invoke(2, args...)
			}
		})
	}
}
