package canvas

import (
	"encoding/json"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/kernel"

	"infinite-canvas/backend/internal/model"
)

type MediaAssetReference = asset.CanvasMediaReference

// validateCanvasMediaAssets is the final server-side invariant for canvas sync:
// a persisted canvas may only point at an uploaded Resource through an Asset
// owned by the same user. The client writes Assets before canvases, so rejecting
// an incomplete pair prevents a durable Resource-only (ghost capacity) state.
func (s *Service) ValidateCanvasMediaAssets(userID string, raw json.RawMessage) error {
	return s.validateCanvasMediaAssetsWithBaseline(userID, raw, nil, nil)
}

func (s *Service) validateCanvasMediaAssetsWithCandidates(userID string, raw json.RawMessage, candidates []model.Asset) error {
	return s.validateCanvasMediaAssetsWithBaseline(userID, raw, nil, candidates)
}

func (s *Service) validateCanvasMediaAssetsWithBaseline(userID string, raw json.RawMessage, existing *model.CanvasProject, candidates []model.Asset) error {
	if err := s.validateCanvasResourceOwnership(userID, raw, existing); err != nil {
		return err
	}
	references, err := MediaAssetReferences(raw)
	if err != nil {
		return kernel.BadAuthRequest("画布媒体数据格式错误")
	}
	if len(references) == 0 {
		return nil
	}
	if existing != nil && len(candidates) == 0 && existing.PayloadJSON != "" {
		if existingRefs, existingErr := MediaAssetReferences([]byte(existing.PayloadJSON)); existingErr == nil && len(existingRefs) > 0 {
			baseline := make(map[MediaAssetReference]int, len(existingRefs))
			for _, ref := range existingRefs {
				if ref.AssetID != "" && ref.ResourceID != "" {
					baseline[ref]++
				}
			}
			filtered := make([]MediaAssetReference, 0, len(references))
			for _, ref := range references {
				if count := baseline[ref]; count > 0 {
					baseline[ref] = count - 1
					continue
				}
				filtered = append(filtered, ref)
			}
			references = filtered
			if len(references) == 0 {
				return nil
			}
		}
	}

	assetIDSet := make(map[string]struct{}, len(references))
	resourceIDSet := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if reference.AssetID == "" {
			return kernel.BadAuthRequest("画布媒体尚未进入素材库，请等待同步完成后重试")
		}
		assetIDSet[reference.AssetID] = struct{}{}
		resourceIDSet[reference.ResourceID] = struct{}{}
	}

	ownedAssets, err := s.repo.AssetsForUserIDs(userID, assets.SortedIDs(assetIDSet))
	if err != nil {
		return err
	}
	assetResources := make(map[string]map[string]struct{}, len(ownedAssets))
	for _, asset := range ownedAssets {
		assetResources[asset.ID] = assets.DocumentReferencedIDs(asset.PayloadJSON, resourceIDSet)
	}
	for _, asset := range candidates {
		if asset.UserID == userID {
			assetResources[asset.ID] = assets.DocumentReferencedIDs(asset.PayloadJSON, resourceIDSet)
		}
	}

	resources, err := s.repo.ResourcesForUserIDs(userID, assets.SortedIDs(resourceIDSet))
	if err != nil {
		return err
	}
	readyResources := make(map[string]struct{}, len(resources))
	ownedResourceIDs := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		ownedResourceIDs[resource.ID] = struct{}{}
		if resource.Status == model.ResourceStatusReady {
			readyResources[resource.ID] = struct{}{}
		}
	}

	for _, reference := range references {
		if _, exists := ownedResourceIDs[reference.ResourceID]; !exists {
			return kernel.NotFound("画布媒体对应的资源不存在")
		}
		if _, exists := readyResources[reference.ResourceID]; !exists {
			return kernel.BadAuthRequest("画布媒体对应的资源不存在或尚未就绪，请重新上传")
		}
		resourceIDs, assetExists := assetResources[reference.AssetID]
		if !assetExists {
			return kernel.NotFound("画布媒体对应的素材不存在")
		}
		if _, matches := resourceIDs[reference.ResourceID]; !matches {
			return kernel.BadAuthRequest("画布媒体与素材库记录不一致，请重新同步")
		}
	}
	return nil
}

// 除主媒体外，封面、预览和节点元数据里的新增资源引用也必须属于当前工作区。
// 已保存的历史失效引用允许保留，避免普通文字编辑被旧缺失资源阻断。
func (s *Service) validateCanvasResourceOwnership(userID string, raw json.RawMessage, existing *model.CanvasProject) error {
	ids := map[string]struct{}{}
	if err := assets.CollectOwnedDocumentReferences(string(raw), ids); err != nil {
		return kernel.BadAuthRequest("画布资源数据格式错误")
	}
	if existing != nil {
		baseline := map[string]struct{}{}
		if err := assets.CollectOwnedDocumentReferences(existing.PayloadJSON, baseline); err != nil {
			return err
		}
		for id := range baseline {
			delete(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	resources, err := s.repo.ResourcesForUserIDs(userID, assets.SortedIDs(ids))
	if err != nil {
		return err
	}
	if len(resources) != len(ids) {
		return kernel.NotFound("画布引用的资源不存在")
	}
	for _, resource := range resources {
		if resource.Status != model.ResourceStatusReady {
			return kernel.BadAuthRequest("画布引用的资源尚未就绪")
		}
	}
	return nil
}

// validateAssetCanvasReferences prevents an Asset update from changing the
// resource behind a canvas that already points at that Asset.
func (s *Service) ValidateAssetCanvasReferences(userID string, item model.Asset) error {
	return s.Library().GuardCanvasReferences(userID, item)
}

// validateAssetReplacementCanvasReferences applies the same invariant to the
// legacy full-replacement endpoint, which otherwise could silently remove an
// Asset that a canvas still needs.
func (s *Service) ValidateAssetReplacementCanvasReferences(userID string, replacement []model.Asset) error {
	return s.Library().GuardReplacementCanvasReferences(userID, replacement)
}

func MediaAssetReferences(raw json.RawMessage) ([]MediaAssetReference, error) {
	return asset.CanvasMediaReferences(raw)
}
