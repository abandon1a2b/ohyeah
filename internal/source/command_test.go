package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestCommandCollectorProtocol(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "collector.sh")
	content := `#!/bin/sh
read request
case "$request" in
  *'"offset":7'*) ;;
  *) echo 'cursor missing' >&2; exit 4 ;;
esac
printf '%s\n' '{"type":"document","document":{"externalId":"item-1","sourceType":"fixture","reference":{"url":"local://one"},"units":[{"key":"answer","kind":"conclusion","title":"Final answer","content":"Use the verified source.","status":"confirmed"}]}}'
printf '%s\n' '{"type":"complete","mode":"delta","cursor":{"offset":8}}'
`
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	registered := model.Source{
		ID: "custom", WorkspaceID: "test", Driver: model.DriverCommand, Path: root,
		Options: map[string]any{"command": script}, Cursor: json.RawMessage(`{"offset":7}`), Enabled: true,
	}
	scanner, err := New(registered)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scanner.Scan(context.Background(), registered, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if result.PruneMissing || string(result.Cursor) != `{"offset":8}` {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Documents) != 1 || len(result.Documents[0].Units) != 1 {
		t.Fatalf("documents = %#v", result.Documents)
	}
	unit := result.Documents[0].Units[0]
	if unit.SourceType != "fixture" || unit.Status != model.StatusConfirmed {
		t.Fatalf("unit = %#v", unit)
	}
}

func TestCommandCollectorProtocolErrorTerminatesProcess(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "invalid.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho not-json\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	registered := model.Source{ID: "custom", WorkspaceID: "test", Driver: model.DriverCommand, Path: root, Options: map[string]any{"command": script}, Enabled: true}
	scanner, err := New(registered)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := scanner.Scan(context.Background(), registered, 1<<20); err == nil {
		t.Fatal("expected invalid protocol to fail")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("collector process was not terminated promptly: %s", elapsed)
	}
}

func TestCommandCollectorHonorsContextDeadline(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread request\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	registered := model.Source{ID: "custom", WorkspaceID: "test", Driver: model.DriverCommand, Path: root, Options: map[string]any{"command": script}, Enabled: true}
	scanner, err := New(registered)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := scanner.Scan(ctx, registered, 1<<20); err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("collector ignored deadline: %s", elapsed)
	}
}

func TestCommandCollectorRequiresCompleteEnvelope(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "incomplete.sh")
	content := "#!/bin/sh\nread request\nprintf '%s\\n' '{\"type\":\"document\",\"document\":{\"externalId\":\"partial\",\"units\":[]}}'\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	registered := model.Source{ID: "custom", WorkspaceID: "test", Driver: model.DriverCommand, Path: root, Options: map[string]any{"command": script}, Enabled: true}
	scanner, err := New(registered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(context.Background(), registered, 1<<20); err == nil {
		t.Fatal("expected incomplete collector to fail")
	}
}
