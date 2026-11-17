// Package projectread 组合项目和任务的读取视图，供本地与平台共同使用。
package projectread

import (
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/task"
)

type Tasks interface {
	TasksWithOptions(string, task.ListOptions) ([]task.Summary, error)
}

type Service struct {
	projects *project.Service
	tasks    Tasks
}

func New(projects *project.Service, tasks Tasks) *Service {
	return &Service{projects: projects, tasks: tasks}
}

type Detail struct {
	Project         model.Project                 `json:"project"`
	Units           []model.ProjectUnit           `json:"units"`
	Canvases        []model.CanvasProject         `json:"canvases"`
	CanvasUnitLinks []model.CanvasUnitLink        `json:"canvasUnitLinks"`
	Assets          []project.AssetSummary        `json:"assets"`
	AssetFolders    []model.ProjectAssetFolder    `json:"assetFolders"`
	Workflows       []project.WorkflowDetail      `json:"workflows"`
	Shots           []model.Shot                  `json:"shots"`
	ShotRevisions   []model.ShotRevision          `json:"shotRevisions"`
	ShotArtifacts   []model.ShotArtifact          `json:"shotArtifacts"`
	ShotReferences  []model.ShotAssetReference    `json:"shotReferences"`
	AssetCandidates []model.ProjectAssetCandidate `json:"assetCandidates"`
	Tasks           []task.Summary                `json:"tasks"`
}

func (s *Service) ProjectDetail(workspaceID, id string) (Detail, error) {
	core, err := s.projects.Inspect(workspaceID, id)
	if err != nil {
		return Detail{}, err
	}
	assets, err := s.projects.ProjectAssets(workspaceID, id)
	if err != nil {
		return Detail{}, err
	}
	workflows, err := s.projects.ProjectWorkflows(workspaceID, id)
	if err != nil {
		return Detail{}, err
	}
	tasks, err := s.tasks.TasksWithOptions(workspaceID, task.ListOptions{Limit: 100, ProjectID: id})
	if err != nil {
		return Detail{}, err
	}
	return Detail{
		Project: core.Project, Units: core.Units, Canvases: core.Canvases, CanvasUnitLinks: core.CanvasUnitLinks,
		Assets: assets, AssetFolders: core.AssetFolders, Workflows: workflows, Shots: core.Shots,
		ShotRevisions: core.ShotRevisions, ShotArtifacts: core.ShotArtifacts, ShotReferences: core.ShotReferences,
		AssetCandidates: core.AssetCandidates, Tasks: tasks,
	}, nil
}

type UnitWorkspace struct {
	Unit            model.ProjectUnit             `json:"unit"`
	ProjectRevision int64                         `json:"projectRevision"`
	Workflows       []project.WorkflowDetail      `json:"workflows"`
	Shots           []model.Shot                  `json:"shots"`
	ShotRevisions   []model.ShotRevision          `json:"shotRevisions"`
	ShotArtifacts   []model.ShotArtifact          `json:"shotArtifacts"`
	ShotReferences  []project.ShotAssetReference  `json:"shotReferences"`
	AssetCandidates []model.ProjectAssetCandidate `json:"assetCandidates"`
	Assets          []project.AssetSummary        `json:"assets"`
	Tasks           []task.Summary                `json:"tasks"`
}

func (s *Service) ProjectUnitWorkspace(workspaceID, projectID, unitID string) (UnitWorkspace, error) {
	snapshot, err := s.projects.GetProjectUnitSnapshot(workspaceID, projectID, unitID)
	if err != nil {
		return UnitWorkspace{}, err
	}
	view, err := s.projects.ProjectUnitWorkspace(workspaceID, projectID, unitID)
	if err != nil {
		return UnitWorkspace{}, err
	}
	recent, err := s.tasks.TasksWithOptions(workspaceID, task.ListOptions{Limit: 100, ProjectID: projectID})
	if err != nil {
		return UnitWorkspace{}, err
	}
	active, err := s.tasks.TasksWithOptions(workspaceID, task.ListOptions{Limit: 100, ProjectID: projectID, ActiveOnly: true})
	if err != nil {
		return UnitWorkspace{}, err
	}
	shotIDs := make(map[string]struct{}, len(view.Shots))
	for _, shot := range view.Shots {
		shotIDs[shot.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(recent)+len(active))
	tasks := make([]task.Summary, 0, len(recent)+len(active))
	for _, item := range append(active, recent...) {
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		if item.ClientContext == nil {
			continue
		}
		_, belongsToShot := shotIDs[item.ClientContext.ShotID]
		if item.ClientContext.ChapterID == unitID || belongsToShot {
			tasks = append(tasks, item)
		}
	}
	return UnitWorkspace{
		Unit: snapshot.Unit, ProjectRevision: snapshot.ProjectRevision,
		Workflows: view.Workflows, Shots: view.Shots, ShotRevisions: view.ShotRevisions,
		ShotArtifacts: view.ShotArtifacts, ShotReferences: view.ShotReferences,
		AssetCandidates: view.AssetCandidates, Assets: view.Assets, Tasks: tasks,
	}, nil
}
