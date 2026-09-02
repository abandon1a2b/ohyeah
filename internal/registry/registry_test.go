package registry

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

func TestProjectTypeMountPlanAndRevision(t *testing.T) {
	root := t.TempDir()
	state, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	cfg := configured(root, 1, 1, "team", "/collector")
	reconciler := New(state, cfg)
	actions, err := reconciler.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAction(t, actions, EntityProject, "example-project", ActionAdd, false)
	assertAction(t, actions, EntityType, "example-project/markdown", ActionAdd, false)
	assertAction(t, actions, EntityMount, "example-project/markdown/team-docs", ActionAdd, false)
	if _, err := reconciler.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	mount, err := state.GetSource(ctx, "example-project/markdown/team-docs")
	if err != nil {
		t.Fatal(err)
	}
	if mount.ProjectID != "example-project" || mount.TypeID != "markdown" || mount.MountName != "team-docs" {
		t.Fatalf("mount = %#v", mount)
	}
	if err := state.UpdateSourceCursor(ctx, mount.ID, json.RawMessage(`{"offset":7}`)); err != nil {
		t.Fatal(err)
	}

	changedWithoutMountRevision := configured(root, 1, 1, "personal", "/collector")
	actions, err = New(state, changedWithoutMountRevision).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAction(t, actions, EntityMount, mount.ID, ActionConflict, false)

	changedWithMountRevision := configured(root, 1, 2, "personal", "/collector")
	actions, err = New(state, changedWithMountRevision).Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAction(t, actions, EntityMount, mount.ID, ActionUpdate, true)
	mount, err = state.GetSource(ctx, mount.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mount.Cursor) != 0 || mount.MountRevision != 2 {
		t.Fatalf("updated mount = %#v", mount)
	}
	if err := state.UpdateSourceCursor(ctx, mount.ID, json.RawMessage(`{"offset":8}`)); err != nil {
		t.Fatal(err)
	}

	forceMountRefresh := configured(root, 1, 3, "personal", "/collector")
	actions, err = New(state, forceMountRefresh).Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAction(t, actions, EntityMount, mount.ID, ActionUpdate, true)
	mount, err = state.GetSource(ctx, mount.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mount.Cursor) != 0 || mount.MountRevision != 3 {
		t.Fatalf("forced mount refresh = %#v", mount)
	}

	changedCollector := configured(root, 2, 3, "personal", "/collector-v2")
	actions, err = New(state, changedCollector).Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAction(t, actions, EntityType, "example-project/markdown", ActionUpdate, false)
	assertAction(t, actions, EntityMount, mount.ID, ActionUpdate, true)

	empty := config.Config{HasProjectConfig: true, Projects: map[string]config.ProjectConfig{}}
	actions, err = New(state, empty).Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAction(t, actions, EntityProject, "example-project", ActionDetach, false)
	assertAction(t, actions, EntityType, "example-project/markdown", ActionDetach, false)
	assertAction(t, actions, EntityMount, mount.ID, ActionDetach, false)
	mount, err = state.GetSource(ctx, mount.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mount.Enabled || mount.State != model.SourceStateDetached {
		t.Fatalf("detached mount = %#v", mount)
	}
}

func configured(root string, typeRevision, mountRevision int, profile, command string) config.Config {
	return config.Config{
		HasProjectConfig: true,
		Projects: map[string]config.ProjectConfig{
			"example-project": {
				Workspace: root,
				Types: map[string]config.DataTypeConfig{
					"markdown": {
						Collector: config.CollectorConfig{Command: command, Revision: typeRevision},
						Mounts: map[string]config.MountConfig{
							"team-docs": {Root: root, Revision: mountRevision, Options: map[string]any{"profile": profile}},
						},
					},
				},
			},
		},
	}
}

func assertAction(t *testing.T, actions []Action, entity, id, actionType string, reset bool) {
	t.Helper()
	for _, action := range actions {
		if action.Entity == entity && action.ID == id {
			if action.Type != actionType || action.ResetCursor != reset {
				t.Fatalf("action = %#v, want type=%s reset=%v", action, actionType, reset)
			}
			return
		}
	}
	t.Fatalf("missing action for %s %s: %#v", entity, id, actions)
}
