package app

import (
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/resource"
	"io"
)

// 历史行为测试直接访问领域端口，不再将测试专用转发函数编入生产服务。
func normalizedResourceUploadKey(values []string) *string { return asset.NormalizedUploadKey(values) }
func (s *Service) storeResource(u, kind, name, mime string, sz int64, w, ht int, dur int64, body io.Reader, key *string, _ bool) (*model.Resource, bool, error) {
	return s.resourceDomain().Store(u, kind, name, mime, sz, w, ht, dur, body, key)
}
func (s *Service) storeResourceObject(item *model.Resource, name string, body io.Reader) (string, error) {
	return s.resourceDomain().WriteObject(item, name, body)
}
func (s *Service) retryStoredResource(u string, item *model.Resource, kind, mime string, sz int64, body io.Reader, _ ...bool) (*model.Resource, error) {
	return resource.RetryOwnedItem(s.resourceDomain(), u, item, kind, mime, sz, body)
}
func intValue(value any) int                       { return resource.IntValue(value) }
func imageDimensions(data []byte) (int, int)       { return resource.ImageDimensions(data) }
func normalizeSingleByteRange(value string) string { return asset.NormalizeSingleByteRange(value) }
func firstNonEmpty(values ...string) string        { return kernel.FirstNonEmpty(values...) }
