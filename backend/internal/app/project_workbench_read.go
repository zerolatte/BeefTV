package app

import (
	localproject "infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/projectread"
)

type ProjectUnitWorkspace = projectread.UnitWorkspace
type ProjectShotAssetReference = localproject.ShotAssetReference
type ProjectShotAssetReferenceVersion = localproject.ShotAssetReferenceVersion
type ProjectAssetCandidatePage = localproject.AssetCandidatePage
type ProjectAssetPage = localproject.AssetPage

func (s *Service) ProjectUnitWorkspace(userID, projectID, unitID string) (ProjectUnitWorkspace, error) {
	return projectread.New(s.projectDomain(), s).ProjectUnitWorkspace(userID, projectID, unitID)
}
