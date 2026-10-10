package config

import "testing"

func TestRemoteSourcesDoNotRequireLocalPathsOrExecutables(t *testing.T) {
	cfg := Config{Projects: map[string]ProjectConfig{"remote": {Workspace: "/nonexistent/remote/workspace", Types: map[string]DataTypeConfig{"documents": {Collector: CollectorConfig{Mode: "upload", Revision: 1}, Mounts: map[string]MountConfig{"node": {Writer: "service:node", Root: "/nonexistent/remote/documents", Revision: 1}}}}}}}
	if err := cfg.ValidateProjects(); err != nil {
		t.Fatal(err)
	}
	project := cfg.Projects["remote"]
	dataType := project.Types["documents"]
	mount := dataType.Mounts["node"]
	mount.Writer = ""
	dataType.Mounts["node"] = mount
	project.Types["documents"] = dataType
	if err := cfg.ValidateProjects(); err == nil {
		t.Fatal("upload source without owner accepted")
	}
}
