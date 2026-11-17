//go:build platform

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/outbound"
	"infinite-canvas/backend/internal/repository"
)

type PlatformCredentialInput struct {
	BaseURL   string                       `json:"baseUrl"`
	APIKey    string                       `json:"apiKey"`
	SecretKey string                       `json:"secretKey,omitempty"`
	Headers   []modelcatalog.ChannelHeader `json:"headers,omitempty"`
}

type PlatformChannelCreateInput struct {
	Name             string                  `json:"name"`
	PublicAlias      string                  `json:"publicAlias"`
	SortOrder        int                     `json:"sortOrder"`
	ConcurrencyLimit int                     `json:"concurrencyLimit"`
	Enabled          *bool                   `json:"enabled"`
	Credentials      PlatformCredentialInput `json:"credentials"`
}

type PlatformChannelUpdateInput struct {
	Name             *string `json:"name"`
	PublicAlias      *string `json:"publicAlias"`
	SortOrder        *int    `json:"sortOrder"`
	ConcurrencyLimit *int    `json:"concurrencyLimit"`
	Enabled          *bool   `json:"enabled"`
}

type PlatformChannelView struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	PublicAlias         string    `json:"publicAlias"`
	SortOrder           int       `json:"sortOrder"`
	ConcurrencyLimit    int       `json:"concurrencyLimit"`
	Enabled             bool      `json:"enabled"`
	CredentialVersionID string    `json:"credentialVersionId"`
	CredentialVersion   int64     `json:"credentialVersion"`
	UpstreamHost        string    `json:"upstreamHost"`
	HasAPIKey           bool      `json:"hasApiKey"`
	HasSecretKey        bool      `json:"hasSecretKey"`
	HeaderNames         []string  `json:"headerNames"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

func validatePlatformChannelMetadata(channel model.ModelChannel) error {
	if !utf8.ValidString(channel.Name) || utf8.RuneCountInString(channel.Name) < 1 || utf8.RuneCountInString(channel.Name) > 80 || utf8.RuneCountInString(channel.PublicAlias) > 80 {
		return kernel.BadAuthRequest("渠道名称须为 1—80 个字符，别名不能超过 80 个字符")
	}
	if channel.ConcurrencyLimit < 0 || channel.ConcurrencyLimit > 999 {
		return kernel.BadAuthRequest("渠道并发上限必须为 0—999，0 表示使用全局设置")
	}
	return modelcatalog.ValidateChannelSortOrder(channel.SortOrder)
}

func (s *PlatformCatalog) encodeCredential(input PlatformCredentialInput) (*model.PlatformCredentialVersion, error) {
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, kernel.BadAuthRequest("上游地址不能含用户信息、查询参数或片段")
	}
	if _, err := outbound.ValidateOutboundURL(input.BaseURL); err != nil {
		return nil, kernel.BadAuthRequest("上游地址不符合网络访问规则")
	}
	if strings.TrimSpace(input.APIKey) == "" || len(input.APIKey) > 8192 || len(input.SecretKey) > 8192 {
		return nil, kernel.BadAuthRequest("必须填写有效的上游凭据")
	}
	if len(input.Headers) > 32 {
		return nil, kernel.BadAuthRequest("上游请求头不能超过 32 项")
	}
	if _, err := modelcatalog.EncodeChannelHeadersJSON(input.Headers); err != nil {
		return nil, kernel.BadAuthRequest("上游请求头无效")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.deps.EncryptSecret(string(encoded))
	if err != nil {
		return nil, err
	}
	if ciphertext == "" || ciphertext == string(encoded) {
		return nil, errors.New("平台凭据加密未生效")
	}
	names := make([]string, 0, len(input.Headers))
	for _, header := range input.Headers {
		names = append(names, header.Name)
	}
	sort.Strings(names)
	headerNames, _ := json.Marshal(names)
	return &model.PlatformCredentialVersion{ID: uuid.NewString(), Ciphertext: ciphertext, UpstreamHost: parsed.Host, HasAPIKey: true, HasSecretKey: input.SecretKey != "", HeaderNamesJSON: string(headerNames), CreatedAt: time.Now().UTC()}, nil
}

func insertCredential(repo *repository.Repository, version *model.PlatformCredentialVersion, actorID, channelID string) error {
	updated := repo.DB().Model(&model.PlatformChannelState{}).Where("channel_id = ? AND credential_sequence < ?", channelID, int64(1<<53-1)).UpdateColumn("credential_sequence", gorm.Expr("credential_sequence + 1"))
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return errors.New("渠道凭据版本不可分配")
	}
	var state model.PlatformChannelState
	if err := repo.DB().Where("channel_id = ?", channelID).Take(&state).Error; err != nil {
		return err
	}
	version.ChannelID, version.Version, version.CreatedBy = channelID, state.CredentialSequence, actorID
	if err := repo.DB().Create(version).Error; err != nil {
		return err
	}
	return repo.DB().Model(&model.PlatformChannelState{}).Where("channel_id = ?", channelID).Update("current_credential_id", version.ID).Error
}

func platformChannelView(repo *repository.Repository, channel model.ModelChannel) (*PlatformChannelView, error) {
	var credential model.PlatformCredentialVersion
	if err := repo.DB().Model(&model.PlatformCredentialVersion{}).Joins("JOIN platform_channel_states ON platform_channel_states.current_credential_id = platform_credential_versions.id").Where("platform_channel_states.channel_id = ? AND platform_credential_versions.channel_id = ?", channel.ID, channel.ID).Take(&credential).Error; err != nil {
		return nil, err
	}
	names := []string{}
	if err := json.Unmarshal([]byte(credential.HeaderNamesJSON), &names); err != nil {
		return nil, err
	}
	return &PlatformChannelView{ID: channel.ID, Name: channel.Name, PublicAlias: channel.PublicAlias, SortOrder: channel.SortOrder, ConcurrencyLimit: channel.ConcurrencyLimit, Enabled: channel.Enabled, CredentialVersionID: credential.ID, CredentialVersion: credential.Version, UpstreamHost: credential.UpstreamHost, HasAPIKey: credential.HasAPIKey, HasSecretKey: credential.HasSecretKey, HeaderNames: names, CreatedAt: channel.CreatedAt, UpdatedAt: channel.UpdatedAt}, nil
}

func (s *PlatformCatalog) Channels(ctx context.Context, actorID string, page, size int) ([]PlatformChannelView, int64, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, 0, err
	}
	if err := validCatalogPage(page, size); err != nil {
		return nil, 0, err
	}
	repo := s.repo.WithContext(ctx)
	var total int64
	query := repo.DB().Model(&model.ModelChannel{}).Where("scope = ?", model.ChannelScopeSystem)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var channels []model.ModelChannel
	if err := query.Order("sort_order ASC, created_at ASC, id ASC").Offset((page - 1) * size).Limit(size).Find(&channels).Error; err != nil {
		return nil, 0, err
	}
	result := make([]PlatformChannelView, 0, len(channels))
	for _, channel := range channels {
		view, err := platformChannelView(repo, channel)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, *view)
	}
	return result, total, nil
}

func (s *PlatformCatalog) CreateChannel(ctx context.Context, actorID string, input PlatformChannelCreateInput) (*PlatformChannelView, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	channel := model.ModelChannel{ID: uuid.NewString(), Scope: model.ChannelScopeSystem, Enabled: true, Name: strings.TrimSpace(input.Name), PublicAlias: strings.TrimSpace(input.PublicAlias), SortOrder: input.SortOrder, ConcurrencyLimit: input.ConcurrencyLimit, APIFormat: "openai", ModelsJSON: "[]", HeadersJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if input.Enabled != nil {
		channel.Enabled = *input.Enabled
	}
	if err := validatePlatformChannelMetadata(channel); err != nil {
		return nil, err
	}
	credential, err := s.encodeCredential(input.Credentials)
	if err != nil {
		return nil, err
	}
	var result *PlatformChannelView
	err = s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		if err := repo.DB().Create(&channel).Error; err != nil {
			return err
		}
		if err := repo.DB().Create(&model.PlatformChannelState{ChannelID: channel.ID}).Error; err != nil {
			return err
		}
		if err := insertCredential(repo, credential, actorID, channel.ID); err != nil {
			return err
		}
		if err := catalogAudit(repo.DB(), actorID, "", "channel_created", map[string]any{"channelId": channel.ID, "name": channel.Name, "enabled": channel.Enabled, "credentialVersion": credential.Version}); err != nil {
			return err
		}
		result, err = platformChannelView(repo, channel)
		return err
	})
	return result, err
}

func (s *PlatformCatalog) UpdateChannel(ctx context.Context, actorID, channelID string, input PlatformChannelUpdateInput) (*PlatformChannelView, error) {
	if input.Name == nil && input.PublicAlias == nil && input.SortOrder == nil && input.ConcurrencyLimit == nil && input.Enabled == nil {
		return nil, kernel.BadAuthRequest("请提供要修改的渠道字段")
	}
	var result *PlatformChannelView
	err := s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		channel, err := repo.AdminSystemChannel(channelID)
		if err != nil {
			return catalogNotFound(err, "渠道")
		}
		if input.Name != nil {
			channel.Name = strings.TrimSpace(*input.Name)
		}
		if input.PublicAlias != nil {
			channel.PublicAlias = strings.TrimSpace(*input.PublicAlias)
		}
		if input.SortOrder != nil {
			channel.SortOrder = *input.SortOrder
		}
		if input.ConcurrencyLimit != nil {
			channel.ConcurrencyLimit = *input.ConcurrencyLimit
		}
		if input.Enabled != nil {
			channel.Enabled = *input.Enabled
		}
		if err := validatePlatformChannelMetadata(*channel); err != nil {
			return err
		}
		channel.UpdatedAt = time.Now().UTC()
		if err := repo.DB().Save(channel).Error; err != nil {
			return err
		}
		if err := catalogAudit(repo.DB(), actorID, "", "channel_updated", map[string]any{"channelId": channel.ID, "name": channel.Name, "enabled": channel.Enabled, "sortOrder": channel.SortOrder, "concurrencyLimit": channel.ConcurrencyLimit}); err != nil {
			return err
		}
		result, err = platformChannelView(repo, *channel)
		return err
	})
	return result, err
}

func (s *PlatformCatalog) RotateCredential(ctx context.Context, actorID, channelID string, input PlatformCredentialInput) (*PlatformChannelView, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	credential, err := s.encodeCredential(input)
	if err != nil {
		return nil, err
	}
	var result *PlatformChannelView
	err = s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		channel, err := repo.AdminSystemChannel(channelID)
		if err != nil {
			return catalogNotFound(err, "渠道")
		}
		if err := insertCredential(repo, credential, actorID, channelID); err != nil {
			return err
		}
		if err := catalogAudit(repo.DB(), actorID, "", "channel_credential_rotated", map[string]any{"channelId": channelID, "credentialVersionId": credential.ID, "version": credential.Version}); err != nil {
			return err
		}
		result, err = platformChannelView(repo, *channel)
		return err
	})
	return result, err
}

// ResolveCredentialVersion 只供服务端任务读取已冻结的凭据；轮换或渠道停用不影响旧版本取回。
func (s *PlatformCatalog) ResolveCredentialVersion(ctx context.Context, channelID, versionID string) (*PlatformCredentialInput, error) {
	if channelID == "" || versionID == "" {
		return nil, kernel.BadAuthRequest("缺少已冻结的渠道凭据版本")
	}
	var version model.PlatformCredentialVersion
	if err := s.repo.DB().WithContext(ctx).Where("id = ? AND channel_id = ?", versionID, channelID).Take(&version).Error; err != nil {
		return nil, catalogNotFound(err, "凭据版本")
	}
	plain, err := s.deps.DecryptSecret(version.Ciphertext)
	if err != nil {
		return nil, errors.New("平台凭据版本无法解密")
	}
	var credential PlatformCredentialInput
	if err := json.Unmarshal([]byte(plain), &credential); err != nil {
		return nil, errors.New("平台凭据版本格式错误")
	}
	return &credential, nil
}
