package app

import (
	"infinite-canvas/backend/internal/model"
	localproject "infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/projectread"
)

type CreateProjectRequest = localproject.CreateProjectRequest
type UpdateProjectRequest = localproject.UpdateProjectRequest
type CreateProjectUnitRequest = localproject.CreateProjectUnitRequest
type UpdateProjectUnitRequest = localproject.UpdateProjectUnitRequest
type ImportProjectUnitsRequest = localproject.ImportProjectUnitsRequest
type ReorderProjectUnitsRequest = localproject.ReorderProjectUnitsRequest
type LinkCanvasUnitRequest = localproject.LinkCanvasUnitRequest
type CreateProjectFolderRequest = localproject.CreateProjectFolderRequest
type ProjectSummary = localproject.Summary
type ProjectListPage = localproject.ListPage
type ProjectCore = localproject.Core
type ProjectUnitSummaries = localproject.UnitSummaries
type ProjectOverview = localproject.Overview
type ProjectOverviewMetrics = localproject.OverviewMetrics
type ProjectOverviewUnit = localproject.OverviewUnit
type ProjectCanvasPage = localproject.CanvasPage
type ProjectAssetSummary = localproject.AssetSummary
type ProjectWorkflowDetail = localproject.WorkflowDetail

type ProjectDetail = projectread.Detail

func (s *Service) ListProjects(userID string) ([]ProjectSummary, error) {
	return s.projectDomain().ListProjects(userID)
}

func (s *Service) ProjectDetail(userID string, id string) (ProjectDetail, error) {
	return projectread.New(s.projectDomain(), s).ProjectDetail(userID, id)
}

func (s *Service) activeProjectForUser(userID string, projectID string) (*model.Project, error) {
	return s.projectDomain().Active(userID, projectID)
}

func IsProjectNotFound(err error) bool {
	return localproject.IsNotFound(err)
}

func (s *Service) ensureTaskProjectActive(userID string, canvasOrProjectID string) error {
	return s.projectDomain().EnsureTaskScopeActive(userID, canvasOrProjectID)
}

func normalizeProjectPage(page int, pageSize int, maximum int) (int, int) {
	return localproject.NormalizePage(page, pageSize, maximum)
}
