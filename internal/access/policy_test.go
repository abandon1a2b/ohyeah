package access

import (
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestPermissionsDoNotImplyOtherCapabilities(t *testing.T) {
	p := model.Principal{Projects: []string{"read"}, SyncProjects: []string{"sync"}, UploadSources: []string{"upload/a"}}
	if Can(p, "read", "sync") || Can(p, "sync", "read") || Can(p, "read", "admin") {
		t.Fatal("permissions crossed capability boundaries")
	}
	if !CanSource(p, model.Source{ID: "upload/a"}, "upload") || CanSource(p, model.Source{ID: "upload/b"}, "upload") {
		t.Fatal("upload is not scoped to exact source")
	}
	p.Admin = true
	p.Disabled = true
	if Can(p, "read", "read") || CanSource(p, model.Source{ID: "upload/a"}, "upload") {
		t.Fatal("disabled administrator retained permissions")
	}
}
