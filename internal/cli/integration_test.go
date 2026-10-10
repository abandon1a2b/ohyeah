package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/managed"
	"github.com/abandon1a2b/ohyeah/internal/model"
)

// Opt in with an already downloaded, pinned community Meilisearch binary.
func TestRealServiceLifecycle(t *testing.T) {
	engineBinary := os.Getenv("OHYEAH_TEST_MEILISEARCH")
	serviceBinary := os.Getenv("OHYEAH_TEST_BINARY")
	if engineBinary == "" || serviceBinary == "" {
		t.Skip("set OHYEAH_TEST_MEILISEARCH and OHYEAH_TEST_BINARY for real service integration")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("cached binary fixture targets macOS arm64")
	}
	root := t.TempDir()
	engineDir := filepath.Join(root, "engine")
	if err := os.MkdirAll(engineDir, 0700); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(engineBinary)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(filepath.Join(engineDir, managed.Version+"-meilisearch-macos-apple-silicon"), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		t.Fatal(err)
	}
	output.Close()
	port := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().String()
	}
	webAddress, engineAddress := port(), port()
	collector := filepath.Join(root, "collector.py")
	fixture := `#!/usr/bin/env python3
import json, sys
request = json.load(sys.stdin)
with open('records.json') as source:
    documents = json.load(source)
for document in documents:
    print(json.dumps({'type': 'document', 'document': document}))
print(json.dumps({'type': 'complete', 'mode': 'snapshot', 'cursor': {'revision': 1}}))
`
	if err := os.WriteFile(collector, []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	writeRecords := func(title, content string, empty bool) {
		t.Helper()
		documents := make([]map[string]any, 0)
		if !empty {
			documents = []map[string]any{{"externalId": "ticket/1", "sourceType": "requirement", "reference": map[string]string{"url": "https://example.invalid/ticket/1"}, "units": []map[string]string{{"key": "final", "kind": "conclusion", "title": title, "content": content, "status": "confirmed"}}}}
		}
		data, _ := json.Marshal(documents)
		if err := os.WriteFile(filepath.Join(root, "records.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRecords("缓存修复结论", "cachemarker 缓存失效问题已验证修复", false)
	configPath := filepath.Join(root, "config.yaml")
	raw := fmt.Sprintf(`state_path: %q
server:
  address: %s
meilisearch:
  mode: managed
  url: http://%s
  directory: %q
projects:
  integration:
    workspace: %q
    types:
      documents:
        collector:
          command: %q
          revision: 1
        mounts:
          fixture:
            root: %q
            revision: 1
`, filepath.Join(root, "state.db"), webAddress, engineAddress, engineDir, root, collector, root)
	if err := os.WriteFile(configPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + webAddress
	request := func(method, path string, body []byte, status int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %s: status=%d body=%s err=%v", method, path, response.StatusCode, data, err)
		}
		return data
	}
	type running struct {
		cmd  *exec.Cmd
		done chan error
		log  *os.File
	}
	start := func() *running {
		t.Helper()
		log, err := os.CreateTemp(root, "serve-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(serviceBinary, "--config", configPath, "serve")
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		run := &running{cmd: cmd, done: make(chan error, 1), log: log}
		go func() { run.done <- cmd.Wait() }()
		t.Cleanup(func() {
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-run.done:
			case <-time.After(10 * time.Second):
				cmd.Process.Kill()
			}
			log.Close()
		})
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case err := <-run.done:
				data, _ := os.ReadFile(log.Name())
				t.Fatalf("serve failed: %v: %s", err, data)
			default:
			}
			response, err := client.Get(base + "/api/v1/health")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return run
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("service did not become ready")
		return nil
	}
	stop := func(run *running, success bool) {
		t.Helper()
		if success {
			run.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case err := <-run.done:
			if (err == nil) != success {
				data, _ := os.ReadFile(run.log.Name())
				t.Fatalf("serve exit: %v: %s", err, data)
			}
			run.done <- err
		case <-time.After(15 * time.Second):
			data, _ := os.ReadFile(run.log.Name())
			t.Logf("shutdown log: %s", data)
			t.Fatal("service failed to stop")
		}
		for _, address := range []string{webAddress, engineAddress} {
			l, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("port leaked %s: %v", address, err)
			}
			l.Close()
		}
	}
	run := start()
	request("POST", "/api/v1/sync", nil, 200)
	search := func(project, query string) []model.SearchHit {
		t.Helper()
		data := request("GET", "/api/v1/search?project="+project+"&q="+query, nil, 200)
		var hits []model.SearchHit
		if err := json.Unmarshal(data, &hits); err != nil {
			t.Fatal(err)
		}
		return hits
	}
	hits := search("integration", "cachemarker")
	if len(hits) != 1 {
		t.Fatalf("search results: %#v", hits)
	}
	id := hits[0].Unit.ID
	if hits[0].Unit.ProjectID != "integration" {
		t.Fatal("project provenance lost")
	}
	var record model.MemoryRecord
	json.Unmarshal(request("GET", "/api/v1/memories/"+id, nil, 200), &record)
	if record.Reference["url"] != "https://example.invalid/ticket/1" {
		t.Fatalf("provenance: %#v", record)
	}
	if len(search("other-project", "cachemarker")) != 0 {
		t.Fatal("cross-project leak")
	}
	request("GET", "/api/v1/memories/missing", nil, 404)
	for _, implementation := range []struct{ language, file, runtime string }{{"python", "ohyeah.py", "python3"}, {"node", "ohyeah.mjs", "node"}} {
		path := filepath.Join(root, implementation.file)
		if err := os.WriteFile(path, request("GET", "/downloads/"+implementation.language+"/"+implementation.file, nil, 200), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"search", "缓存", "--project", "integration", "--json"}, {"get", id}, {"status"}, {"projects"}} {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, implementation.runtime, append([]string{path}, args...)...)
			data, err := cmd.CombinedOutput()
			cancel()
			if err != nil || !json.Valid(data) {
				t.Fatalf("%s %v: %v %s", implementation.language, args, err, data)
			}
		}
	}
	writeRecords("更新结论", "updatedmarker 更新后的缓存修复证据", false)
	request("POST", "/api/v1/sync", nil, 200)
	if len(search("integration", "updatedmarker")) != 1 || len(search("integration", "cachemarker")) != 0 {
		t.Fatal("updated record not reflected in search")
	}
	stop(run, true)
	// Removing only the derived index simulates index loss without touching SQLite.
	if err := os.RemoveAll(filepath.Join(engineDir, "data-"+managed.Version)); err != nil {
		t.Fatal(err)
	}
	run = start()
	if len(search("integration", "updatedmarker")) != 1 {
		t.Fatal("SQLite index recovery failed")
	}
	if err := os.WriteFile(filepath.Join(root, "records.json"), []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/v1/sync", nil, 207)
	if len(search("integration", "updatedmarker")) != 1 {
		t.Fatal("failed collector destroyed existing data")
	}
	writeRecords("", "", true)
	request("POST", "/api/v1/sync", nil, 200)
	if len(search("integration", "updatedmarker")) != 0 {
		t.Fatal("snapshot deletion failed")
	}
	request("GET", "/api/v1/memories/"+id, nil, 404)
	// Kill only the child; the parent must exit with failure and release HTTP.
	outputBytes, err := exec.Command("pgrep", "-P", fmt.Sprint(run.cmd.Process.Pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(outputBytes)), &pid); err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	stop(run, false)
}
