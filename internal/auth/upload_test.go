package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestUploadAuthorizationAndDownloadedClients(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	registered := model.Source{ID: "allowed/remote/node", ProjectID: "allowed", WorkspaceID: "allowed", TypeID: "remote", Driver: model.DriverUpload, Path: "/remote", Enabled: true, State: model.SourceStateActive, Options: map[string]any{"__ohyeah_writer": "robot"}, TypeRevision: 1, MountRevision: 1}
	if err := f.state.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	f.login(t, 200)
	f.request(t, f.browser, "GET", "/api/v1/uploads/state?source="+registered.ID, "", nil, nil, 403)
	f.mu.Lock()
	f.subject = "admin"
	f.mu.Unlock()
	f.login(t, 200)
	me := f.request(t, f.browser, "GET", "/auth/me", "", nil, nil, 200)
	f.request(t, f.browser, "GET", "/api/v1/uploads/state?source="+registered.ID, "", nil, nil, 403)
	issued := f.request(t, f.browser, "POST", "/auth/tokens", "", map[string]string{"subject": "robot", "name": "remote-node"}, map[string]string{"Origin": f.server.URL, "X-CSRF-Token": me["csrf"].(string)}, 200)
	token := issued["token"].(string)
	expectedVersion := float64(0)
	f.request(t, http.DefaultClient, "GET", "/api/v1/uploads/state?source=allowed/docs/main", token, nil, nil, 403)
	for _, client := range []struct{ language, file, runtime string }{{"python", "ohyeah.py", "python3"}, {"node", "ohyeah.mjs", "node"}} {
		t.Run(client.language, func(t *testing.T) {
			if _, err := exec.LookPath(client.runtime); err != nil {
				t.Skip(err)
			}
			root := t.TempDir()
			path := filepath.Join(root, client.file)
			response, err := http.Get(f.server.URL + "/downloads/" + client.language + "/" + client.file)
			if err != nil {
				t.Fatal(err)
			}
			var script strings.Builder
			_, err = io.Copy(&script, response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(script.String()), 0600); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(root, "collector.ndjson")
			if err := os.WriteFile(input, []byte(`{"type":"document","document":{"externalId":"one","units":[{"key":"final","title":"Remote conclusion","content":"remote evidence"}]}}
{"type":"complete","mode":"snapshot","cursor":{"offset":1}}
`), 0600); err != nil {
				t.Fatal(err)
			}
			invoke := func(args ...string) []byte {
				t.Helper()
				cmd := exec.Command(client.runtime, append([]string{path}, args...)...)
				cmd.Env = append(os.Environ(), "OHYEAH_SERVER="+f.server.URL, "OHYEAH_TOKEN="+token, "OHYEAH_CREDENTIALS_DIR="+root)
				data, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %v %s", args, err, data)
				}
				return data
			}
			prepared := invoke("prepare-upload", registered.ID, "--file", input)
			var batch map[string]any
			if json.Unmarshal(prepared, &batch) != nil || batch["batchId"] == nil {
				t.Fatalf("invalid prepared batch: %s", prepared)
			}
			batchFile := filepath.Join(root, "batch.json")
			if err := os.WriteFile(batchFile, prepared, 0600); err != nil {
				t.Fatal(err)
			}
			first := invoke("upload", registered.ID, "--file", batchFile)
			expectedVersion++
			var receipt map[string]any
			json.Unmarshal(first, &receipt)
			if receipt["version"] == nil {
				t.Fatalf("no commit receipt: %s", first)
			}
			json.Unmarshal(invoke("upload", registered.ID, "--file", batchFile), &receipt)
			if receipt["replayed"] != true {
				t.Fatal("retry was not replayed")
			}
			if python, err := exec.LookPath("python3"); err == nil {
				collectorPath := filepath.Join(root, "local.py")
				program := `import json, os, sys
request = json.load(sys.stdin)
assert request['protocolVersion'] == 1
assert request['cursor']['offset'] == 1
assert os.path.realpath(request['mount']['root']) == os.getcwd()
assert os.environ['OHYEAH_COLLECTOR_PROTOCOL'] == '1'
print(json.dumps({'type':'document','document':{'externalId':'local','units':[{'key':'final','content':'locally collected remote evidence'}]}}))
print(json.dumps({'type':'complete','mode':'snapshot','cursor':{'offset':1}}))
`
				if err := os.WriteFile(collectorPath, []byte(program), 0600); err != nil {
					t.Fatal(err)
				}
				args, _ := json.Marshal([]string{collectorPath})
				prepared = invoke("prepare-upload", registered.ID, "--collector", python, "--collector-args", string(args), "--root", root)
				if err := os.WriteFile(batchFile, prepared, 0600); err != nil {
					t.Fatal(err)
				}
				invoke("upload", registered.ID, "--file", batchFile)
				expectedVersion++
				if err := os.WriteFile(collectorPath, []byte("import os,sys\nsys.stderr.write(os.environ['OHYEAH_TOKEN'])\nsys.exit(1)\n"), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(client.runtime, path, "prepare-upload", registered.ID, "--collector", python, "--collector-args", string(args), "--root", root)
				cmd.Env = append(os.Environ(), "OHYEAH_SERVER="+f.server.URL, "OHYEAH_TOKEN="+token, "OHYEAH_CREDENTIALS_DIR="+root)
				data, err := cmd.CombinedOutput()
				if err == nil || strings.Contains(string(data), token) || !json.Valid(data) {
					t.Fatalf("collector failure leaked output or prepared a batch: %v %s", err, data)
				}
			}
		})
	}
	f.request(t, f.browser, "GET", "/api/v1/memories/secret-memory", "", nil, nil, 200)
	state := f.request(t, http.DefaultClient, "GET", "/api/v1/uploads/state?source="+registered.ID, token, nil, nil, 200)
	if state["version"] != expectedVersion {
		t.Fatalf("unexpected upload state: %#v", state)
	}
}
