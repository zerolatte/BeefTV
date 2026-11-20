package platformadmin

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/repository"
)

type Channels interface {
	AdminSystemChannelPage(*model.User, admin.ListQuery) (*modelcatalog.AdminChannelPage, error)
	CreateSystemChannel(*model.User, modelcatalog.ChannelRequest) (*modelcatalog.PublicModelChannel, error)
	UpdateSystemChannel(*model.User, string, modelcatalog.ChannelRequest) (*modelcatalog.PublicModelChannel, error)
	SaveAdminChannelModel(*model.User, string, string, modelcatalog.ChannelModelRequest) (*model.ChannelModel, error)
	ImportAdminChannelModels(context.Context, *model.User, string, []string) (*modelcatalog.AdminChannelModelFetchResult, error)
}

type Service struct {
	auth     *identity.Service
	repo     *repository.Repository
	channels Channels
}

func New(auth *identity.Service, repo *repository.Repository, channels Channels) *Service {
	return &Service{auth: auth, repo: repo, channels: channels}
}
func (s *Service) Channels(actor string, q admin.ListQuery) (*modelcatalog.AdminChannelPage, error) {
	u, err := s.auth.RequireAdmin(actor)
	if err != nil {
		return nil, err
	}
	return s.channels.AdminSystemChannelPage(&u.User, q)
}
func (s *Service) SaveChannel(actor, id string, req modelcatalog.ChannelRequest) (*modelcatalog.PublicModelChannel, error) {
	u, err := s.auth.RequireAdmin(actor)
	if err != nil {
		return nil, err
	}
	if id == "" {
		row, err := s.channels.CreateSystemChannel(&u.User, req)
		if err != nil {
			return nil, err
		}
		return row, s.audit(actor, row.ID, "channel.create")
	}
	row, err := s.channels.UpdateSystemChannel(&u.User, id, req)
	if err != nil {
		return nil, err
	}
	return row, s.audit(actor, id, "channel.update")
}
func (s *Service) Models(actor, channelID string) ([]model.ChannelModel, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := s.repo.ChannelModels(channelID, true)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if err := json.Unmarshal([]byte(rows[i].CapabilityConfigJSON), &rows[i].CapabilityConfig); err != nil {
			return nil, err
		}
	}
	return rows, nil
}
func (s *Service) SaveModel(actor, channelID, id string, req modelcatalog.ChannelModelRequest) (*model.ChannelModel, error) {
	u, err := s.auth.RequireAdmin(actor)
	if err != nil {
		return nil, err
	}
	row, err := s.channels.SaveAdminChannelModel(&u.User, channelID, id, req)
	if err != nil {
		return nil, err
	}
	return row, s.audit(actor, row.ID, "model.save")
}
func (s *Service) ImportModels(ctx context.Context, actor, channelID string, models []string) (*modelcatalog.AdminChannelModelFetchResult, error) {
	u, err := s.auth.RequireAdmin(actor)
	if err != nil {
		return nil, err
	}
	row, err := s.channels.ImportAdminChannelModels(ctx, &u.User, channelID, models)
	if err != nil {
		return nil, err
	}
	return row, s.audit(actor, channelID, "model.import")
}

func (s *Service) audit(actor, subject, action string) error {
	return s.repo.RecordPlatformAudit(model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: subject, Action: action, Reason: "管理员更新平台模型配置"})
}
