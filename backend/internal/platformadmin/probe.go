package platformadmin

import (
	"context"
	"strings"
	"time"

	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/outbound"
	"infinite-canvas/backend/internal/repository"
)

func (s *Service) Probe(ctx context.Context, actor string, req ProbeRequest) (*ProbeResult, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	existingModelIDs := []string{}
	if req.ChannelID != "" {
		channel, err := s.channel(req.ChannelID)
		if err != nil {
			return nil, err
		}
		existingModelIDs, err = s.repo.PlatformChannelModelKeys(channel.ID)
		if err != nil {
			return nil, err
		}
		if s.options.Secrets == nil {
			return nil, kernel.NewAppError(500, "渠道密钥存储尚未就绪")
		}
		if err := admin.DecryptSystemChannelSecrets(s.options.Secrets.Decrypt, channel); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.BaseURL) == "" {
			req.BaseURL = channel.BaseURL
		}
		if req.APIKey == "" {
			req.APIKey = channel.APIKey
		}
		if req.Headers == nil {
			req.Headers, err = modelcatalog.ParseChannelHeadersJSON(channel.HeadersJSON)
			if err != nil {
				return nil, err
			}
		}
	}
	format, err := modelcatalog.NormalizeCatalogAPIFormat(req.CatalogFormat)
	if err != nil {
		return nil, err
	}
	headers, err := outbound.NormalizeOutboundHeaders(req.Headers)
	if err != nil {
		return nil, kernel.BadAuthRequest(err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	started := time.Now()
	items, err := modelcatalog.LoadChannelModelCatalog(ctx, generation.FetchChannelModelCatalogBytes, req.BaseURL, format, req.APIKey, headers, nil)
	if err != nil {
		return nil, generation.MapChannelModelCatalogError(err)
	}
	return &ProbeResult{Models: items, ExistingModelIDs: existingModelIDs, DurationMs: time.Since(started).Milliseconds()}, nil
}

func (s *Service) ImportModels(ctx context.Context, actor, channelID string, selected []string, format string) (*modelcatalog.AdminChannelModelFetchResult, error) {
	probe, err := s.Probe(ctx, actor, ProbeRequest{ChannelID: channelID, CatalogFormat: format})
	if err != nil {
		return nil, err
	}
	chosen, err := modelcatalog.SelectCatalogModels(selected, modelcatalog.CatalogModelIDs(probe.Models))
	if err != nil {
		return nil, err
	}
	var added int64
	err = s.repo.PlatformCatalogTransaction(func(repo *repository.Repository) error {
		channel, err := repo.AdminSystemChannel(channelID)
		if err != nil {
			return catalogError(err)
		}
		existing, err := repo.ChannelModels(channelID, true)
		if err != nil {
			return err
		}
		missing, err := modelcatalog.MissingImportedChannelModels(channelID, chosen, existing, modelcatalog.RetiredChannelModelKeys(channel.RetiredModelsJSON), func(string) (string, error) { return kernel.NewID(), nil })
		if err != nil {
			return err
		}
		added, err = repo.CreateMissingChannelModels(missing)
		if err != nil {
			return err
		}
		return repo.RecordPlatformAudit(model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: channelID, Action: "model.import", Reason: "导入管理员选择的模型草稿"})
	})
	if err != nil {
		return nil, err
	}
	return &modelcatalog.AdminChannelModelFetchResult{Models: chosen, Added: added}, nil
}
