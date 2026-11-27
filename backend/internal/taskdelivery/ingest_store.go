package taskdelivery

import (
	"bytes"
	"context"
	"io"
	"strings"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

type assetInlineStore struct {
	assets *asset.Service
}

func NewAssetInlineStore(assets *asset.Service) InlineStore {
	return assetInlineStore{assets: assets}
}

func (s assetInlineStore) PersistInline(userID string, artifact InlineArtifact) (*model.Resource, error) {
	ctx := artifact.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if s.assets == nil {
		return nil, asset.ResourceMissing()
	}
	data, err := readInlineBytes(artifact)
	if err != nil {
		return nil, err
	}
	if !artifact.EnforceQuota {
		resource, _, err := s.assets.StoreContext(ctx, userID, artifact.Kind, artifact.FileName, artifact.MimeType, artifact.Size, artifact.Width, artifact.Height, artifact.DurationMs, bytes.NewReader(data), nil)
		return resource, err
	}
	if strings.TrimSpace(artifact.Identity) == "" {
		artifact.Identity = kernel.NewID()
	}
	return s.assets.RecoverOwnedContext(ctx, userID, artifact.Identity, func() (asset.RecoveredArtifact, error) {
		return asset.RecoveredArtifact{
			Kind:       artifact.Kind,
			FileName:   artifact.FileName,
			MimeType:   artifact.MimeType,
			Size:       artifact.Size,
			Width:      artifact.Width,
			Height:     artifact.Height,
			DurationMs: artifact.DurationMs,
			Body:       bytes.NewReader(data),
		}, nil
	})
}

func readInlineBytes(artifact InlineArtifact) ([]byte, error) {
	if artifact.Body == nil {
		return nil, asset.MissingUpload()
	}
	if artifact.Size < 0 {
		return nil, asset.MissingUpload()
	}
	data, err := io.ReadAll(io.LimitReader(artifact.Body, artifact.Size+1))
	if err != nil {
		return nil, err
	}
	if artifact.Size > 0 && int64(len(data)) != artifact.Size {
		return nil, asset.MissingUpload()
	}
	return data, nil
}
