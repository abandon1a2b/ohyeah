// Package access defines permissions independently of HTTP and configuration loading.
package access

import (
	"slices"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func Can(principal model.Principal, project, action string) bool {
	if principal.Disabled {
		return false
	}
	if principal.Admin {
		return true
	}
	switch action {
	case "read":
		return slices.Contains(principal.Projects, project)
	case "sync":
		return slices.Contains(principal.SyncProjects, project)
	default:
		return false
	}
}

func CanSource(principal model.Principal, source model.Source, action string) bool {
	if principal.Disabled {
		return false
	}
	if Can(principal, source.ProjectID, action) {
		return true
	}
	switch action {
	case "sync":
		return slices.Contains(principal.SyncSources, source.ID)
	case "upload":
		return slices.Contains(principal.UploadSources, source.ID)
	default:
		return false
	}
}
