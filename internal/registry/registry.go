package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/identity"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

const (
	EntityProject = "project"
	EntityType    = "type"
	EntityMount   = "mount"

	ActionAdd       = "add"
	ActionUpdate    = "update"
	ActionDisable   = "disable"
	ActionDetach    = "detach"
	ActionUnchanged = "unchanged"
	ActionConflict  = "conflict"
)

type Action struct {
	Entity         string          `json:"entity"`
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	Reason         string          `json:"reason"`
	ResetCursor    bool            `json:"resetCursor"`
	DesiredProject *model.Project  `json:"desiredProject,omitempty"`
	DesiredType    *model.DataType `json:"desiredType,omitempty"`
	DesiredMount   *model.Source   `json:"desiredMount,omitempty"`
}

type desiredState struct {
	projects   []model.Project
	types      []model.DataType
	mounts     []model.Source
	projectIDs map[string]struct{}
	typeIDs    map[string]struct{}
	mountIDs   map[string]struct{}
}

type Reconciler struct {
	store  *store.Store
	config config.Config
}

func New(state *store.Store, cfg config.Config) *Reconciler {
	return &Reconciler{store: state, config: cfg}
}

func (r *Reconciler) Plan(ctx context.Context) ([]Action, error) {
	if !r.config.HasProjectConfig {
		return nil, nil
	}
	desired := buildDesiredState(r.config)
	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	types, err := r.store.ListDataTypes(ctx)
	if err != nil {
		return nil, err
	}
	mounts, err := r.store.ListSources(ctx, false)
	if err != nil {
		return nil, err
	}
	projectByID := projectMap(projects)
	typeByID := typeMap(types)
	mountByID := mountMap(mounts)
	var actions []Action

	for _, candidate := range desired.projects {
		current, found := projectByID[candidate.ID]
		switch {
		case !found:
			actions = append(actions, Action{Entity: EntityProject, Type: ActionAdd, ID: candidate.ID, Reason: "configured project is not registered", DesiredProject: cloneProject(candidate)})
		case current.Workspace != candidate.Workspace || current.Enabled != candidate.Enabled || current.State != candidate.State:
			actionType := ActionUpdate
			if !candidate.Enabled {
				actionType = ActionDisable
			}
			actions = append(actions, Action{Entity: EntityProject, Type: actionType, ID: candidate.ID, Reason: "project configuration changed", DesiredProject: cloneProject(candidate)})
		default:
			actions = append(actions, Action{Entity: EntityProject, Type: ActionUnchanged, ID: candidate.ID, Reason: "configuration matches runtime state"})
		}
	}
	for _, current := range projects {
		if _, found := desired.projectIDs[current.ID]; !found && current.State != model.SourceStateDetached {
			actions = append(actions, Action{Entity: EntityProject, Type: ActionDetach, ID: current.ID, Reason: "project was removed from configuration"})
		}
	}

	for _, candidate := range desired.types {
		current, found := typeByID[candidate.ID]
		switch {
		case !found:
			actions = append(actions, Action{Entity: EntityType, Type: ActionAdd, ID: candidate.ID, Reason: "configured data type is not registered", DesiredType: cloneType(candidate)})
		case candidate.Revision < current.Revision:
			actions = append(actions, Action{Entity: EntityType, Type: ActionConflict, ID: candidate.ID, Reason: fmt.Sprintf("type revision cannot decrease below %d", current.Revision)})
		case current.ConfigHash != candidate.ConfigHash && candidate.Revision <= current.Revision:
			actions = append(actions, Action{Entity: EntityType, Type: ActionConflict, ID: candidate.ID, Reason: fmt.Sprintf("collector changed without increasing type revision above %d", current.Revision)})
		case current.ConfigHash != candidate.ConfigHash || candidate.Revision > current.Revision:
			actions = append(actions, Action{Entity: EntityType, Type: ActionUpdate, ID: candidate.ID, Reason: "collector configuration or revision changed", DesiredType: cloneType(candidate)})
		case current.Enabled != candidate.Enabled || current.State != candidate.State:
			actionType := ActionUpdate
			if !candidate.Enabled {
				actionType = ActionDisable
			}
			actions = append(actions, Action{Entity: EntityType, Type: actionType, ID: candidate.ID, Reason: "data type enabled state changed", DesiredType: cloneType(candidate)})
		default:
			actions = append(actions, Action{Entity: EntityType, Type: ActionUnchanged, ID: candidate.ID, Reason: "configuration matches runtime state"})
		}
	}
	for _, current := range types {
		if _, found := desired.typeIDs[current.ID]; !found && current.State != model.SourceStateDetached {
			actions = append(actions, Action{Entity: EntityType, Type: ActionDetach, ID: current.ID, Reason: "data type was removed from configuration"})
		}
	}

	for _, candidate := range desired.mounts {
		current, found := mountByID[candidate.ID]
		switch {
		case !found:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionAdd, ID: candidate.ID, Reason: "configured mount is not registered", DesiredMount: cloneMount(candidate)})
		case current.ManagedBy != model.ManagedByConfig:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionConflict, ID: candidate.ID, Reason: "configured mount id is already owned by CLI"})
		case candidate.TypeRevision < current.TypeRevision:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionConflict, ID: candidate.ID, Reason: fmt.Sprintf("type revision cannot decrease below %d", current.TypeRevision)})
		case candidate.MountRevision < current.MountRevision:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionConflict, ID: candidate.ID, Reason: fmt.Sprintf("mount revision cannot decrease below %d", current.MountRevision)})
		case current.TypeConfigHash != candidate.TypeConfigHash && candidate.TypeRevision <= current.TypeRevision:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionConflict, ID: candidate.ID, Reason: fmt.Sprintf("collector changed without increasing type revision above %d", current.TypeRevision)})
		case current.MountConfigHash != candidate.MountConfigHash && candidate.MountRevision <= current.MountRevision:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionConflict, ID: candidate.ID, Reason: fmt.Sprintf("mount scope changed without increasing mount revision above %d", current.MountRevision)})
		case current.TypeConfigHash != candidate.TypeConfigHash || current.MountConfigHash != candidate.MountConfigHash ||
			candidate.TypeRevision > current.TypeRevision || candidate.MountRevision > current.MountRevision:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionUpdate, ID: candidate.ID, Reason: "collector or mount configuration changed", ResetCursor: true, DesiredMount: cloneMount(candidate)})
		case current.Enabled != candidate.Enabled || current.State != candidate.State:
			actionType := ActionUpdate
			if !candidate.Enabled {
				actionType = ActionDisable
			}
			actions = append(actions, Action{Entity: EntityMount, Type: actionType, ID: candidate.ID, Reason: "mount enabled state changed", DesiredMount: cloneMount(candidate)})
		default:
			actions = append(actions, Action{Entity: EntityMount, Type: ActionUnchanged, ID: candidate.ID, Reason: "configuration matches runtime state"})
		}
	}
	for _, current := range mounts {
		if current.ManagedBy != model.ManagedByConfig {
			continue
		}
		if _, found := desired.mountIDs[current.ID]; !found && current.State != model.SourceStateDetached {
			actions = append(actions, Action{Entity: EntityMount, Type: ActionDetach, ID: current.ID, Reason: "mount was removed from configuration"})
		}
	}

	sortActions(actions)
	return actions, nil
}

func (r *Reconciler) Apply(ctx context.Context) ([]Action, error) {
	actions, err := r.Plan(ctx)
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		if action.Type == ActionConflict {
			return actions, errors.New("configuration has conflicts; inspect config plan before applying")
		}
	}
	change := store.ConfigStateChange{}
	for _, action := range actions {
		switch action.Entity {
		case EntityProject:
			switch action.Type {
			case ActionAdd, ActionUpdate, ActionDisable:
				change.Projects = append(change.Projects, *action.DesiredProject)
			case ActionDetach:
				change.DetachProjects = append(change.DetachProjects, action.ID)
			}
		case EntityType:
			switch action.Type {
			case ActionAdd, ActionUpdate, ActionDisable:
				change.DataTypes = append(change.DataTypes, *action.DesiredType)
			case ActionDetach:
				change.DetachTypes = append(change.DetachTypes, action.ID)
			}
		case EntityMount:
			switch action.Type {
			case ActionAdd, ActionUpdate, ActionDisable:
				change.Mounts = append(change.Mounts, *action.DesiredMount)
				if action.ResetCursor {
					change.ResetCursors = append(change.ResetCursors, action.ID)
				}
			case ActionDetach:
				change.DetachMounts = append(change.DetachMounts, action.ID)
			}
		}
	}
	if err := r.store.ApplyConfigState(ctx, change); err != nil {
		return actions, err
	}
	return actions, nil
}

func buildDesiredState(cfg config.Config) desiredState {
	state := desiredState{
		projectIDs: make(map[string]struct{}), typeIDs: make(map[string]struct{}), mountIDs: make(map[string]struct{}),
	}
	for projectID, projectCfg := range cfg.Projects {
		projectEnabled := config.Enabled(projectCfg.Enabled)
		projectState := enabledState(projectEnabled)
		project := model.Project{ID: projectID, Workspace: projectCfg.Workspace, Enabled: projectEnabled, State: projectState}
		state.projects = append(state.projects, project)
		state.projectIDs[projectID] = struct{}{}
		for typeID, typeCfg := range projectCfg.Types {
			typeEnabled := projectEnabled && config.Enabled(typeCfg.Enabled)
			command := resolveCommand(projectCfg.Workspace, typeCfg.Collector.Command)
			typeHash := hashJSON(struct {
				Command string        `json:"command"`
				Args    []string      `json:"args"`
				Timeout time.Duration `json:"timeout"`
			}{command, typeCfg.Collector.Args, typeCfg.Collector.Timeout})
			typeCanonicalID := projectID + "/" + typeID
			state.types = append(state.types, model.DataType{
				ID: typeCanonicalID, ProjectID: projectID, Name: typeID,
				Command: command, Args: append([]string(nil), typeCfg.Collector.Args...),
				Revision: typeCfg.Collector.Revision, ConfigHash: typeHash,
				Enabled: typeEnabled, State: enabledState(typeEnabled),
			})
			state.typeIDs[typeCanonicalID] = struct{}{}
		}
	}
	for _, configured := range cfg.ConfiguredMounts() {
		projectCfg := cfg.Projects[configured.ProjectID]
		projectEnabled := config.Enabled(projectCfg.Enabled)
		typeEnabled := projectEnabled && config.Enabled(configured.Type.Enabled)
		mountEnabled := typeEnabled && config.Enabled(configured.Mount.Enabled)
		command := resolveCommand(configured.Workspace, configured.Type.Collector.Command)
		typeHash := hashJSON(struct {
			Command string        `json:"command"`
			Args    []string      `json:"args"`
			Timeout time.Duration `json:"timeout"`
		}{command, configured.Type.Collector.Args, configured.Type.Collector.Timeout})
		typeCanonicalID := configured.ProjectID + "/" + configured.TypeID
		mountHash := hashJSON(struct {
			Workspace string         `json:"workspace"`
			Root      string         `json:"root"`
			Options   map[string]any `json:"options"`
		}{configured.Workspace, configured.Mount.Root, configured.Mount.Options})
		options := cloneOptions(configured.Mount.Options)
		options["command"] = command
		options["args"] = append([]string(nil), configured.Type.Collector.Args...)
		timeout := configured.Type.Collector.Timeout
		if timeout <= 0 {
			timeout = cfg.Sync.CollectorTimeout
		}
		options["__ohyeah_timeout"] = timeout.String()
		mountCanonicalID := typeCanonicalID + "/" + configured.MountID
		state.mounts = append(state.mounts, model.Source{
			ID: mountCanonicalID, ProjectID: configured.ProjectID, ProjectWorkspace: configured.Workspace,
			TypeID: configured.TypeID, MountName: configured.MountID,
			WorkspaceID: configured.ProjectID, Driver: model.DriverCommand, Path: configured.Mount.Root,
			Enabled: mountEnabled, ManagedBy: model.ManagedByConfig, State: enabledState(mountEnabled),
			TypeConfigHash: typeHash, MountConfigHash: mountHash,
			TypeRevision: configured.Type.Collector.Revision, MountRevision: configured.Mount.Revision,
			Options: options,
		})
		state.mountIDs[mountCanonicalID] = struct{}{}
	}
	sort.Slice(state.projects, func(i, j int) bool { return state.projects[i].ID < state.projects[j].ID })
	sort.Slice(state.types, func(i, j int) bool { return state.types[i].ID < state.types[j].ID })
	sort.Slice(state.mounts, func(i, j int) bool { return state.mounts[i].ID < state.mounts[j].ID })
	return state
}

func enabledState(enabled bool) string {
	if enabled {
		return model.SourceStateActive
	}
	return model.SourceStateDisabled
}

func resolveCommand(workspace, command string) string {
	if filepath.IsAbs(command) || !strings.ContainsRune(command, filepath.Separator) {
		return command
	}
	return filepath.Join(workspace, command)
}

func hashJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return identity.Hash(string(encoded))
}

func projectMap(values []model.Project) map[string]model.Project {
	result := make(map[string]model.Project, len(values))
	for _, value := range values {
		result[value.ID] = value
	}
	return result
}

func typeMap(values []model.DataType) map[string]model.DataType {
	result := make(map[string]model.DataType, len(values))
	for _, value := range values {
		result[value.ID] = value
	}
	return result
}

func mountMap(values []model.Source) map[string]model.Source {
	result := make(map[string]model.Source, len(values))
	for _, value := range values {
		result[value.ID] = value
	}
	return result
}

func cloneProject(value model.Project) *model.Project { copy := value; return &copy }
func cloneType(value model.DataType) *model.DataType {
	copy := value
	copy.Args = append([]string(nil), value.Args...)
	return &copy
}
func cloneMount(value model.Source) *model.Source {
	copy := value
	copy.Options = cloneOptions(value.Options)
	return &copy
}
func cloneOptions(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+2)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sortActions(actions []Action) {
	rank := map[string]int{EntityProject: 0, EntityType: 1, EntityMount: 2}
	sort.Slice(actions, func(i, j int) bool {
		if rank[actions[i].Entity] != rank[actions[j].Entity] {
			return rank[actions[i].Entity] < rank[actions[j].Entity]
		}
		return actions[i].ID < actions[j].ID
	})
}
