package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func (s *Service) readPolicy() (policy, string, error) {
	row, err := s.repo.SystemSetting(policyKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return policy{Provider: "local"}, "", nil
	}
	if err != nil {
		return policy{}, "", err
	}
	var value policy
	if json.Unmarshal([]byte(row.ValueJSON), &value) != nil || (value.Provider != "local" && value.Provider != "oss") {
		return value, "", storageError("configuration", "存储策略损坏，请联系管理员检查")
	}
	return value, row.ValueJSON, nil
}

func (s *Service) readLocation(id string) (*location, error) {
	row, err := s.repo.SystemSetting(locationPrefix + id)
	if err != nil {
		return nil, storageError("configuration", "原存储位置的配置不可用，请联系管理员检查")
	}
	var value location
	if json.Unmarshal([]byte(row.ValueJSON), &value) != nil || value.ID != id {
		return nil, storageError("configuration", "存储位置配置损坏")
	}
	return &value, nil
}

func publicLocation(value *location) *PublicOSS {
	if value == nil {
		return nil
	}
	return &PublicOSS{Endpoint: value.Endpoint, Region: value.Region, Bucket: value.Bucket,
		HasAccessKeyID: value.AccessKeyID != "", HasAccessKeySecret: value.AccessKeySecret != ""}
}

func (s *Service) Get(actor string) (*View, error) {
	if _, err := s.admin.RequireAdmin(actor); err != nil {
		return nil, err
	}
	p, _, err := s.readPolicy()
	if err != nil {
		return nil, err
	}
	view := &View{Provider: p.Provider, Version: p.Version}
	if p.LocationID != "" {
		loc, err := s.readLocation(p.LocationID)
		if err != nil {
			return nil, err
		}
		view.OSS = publicLocation(loc)
	}
	view.Statistics, err = s.repo.StorageStatistics()
	if err != nil {
		return nil, err
	}
	view.RecentFallbacks, err = s.repo.StorageFallbacks(10)
	return view, err
}

var regionPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)+$`)
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func validateDraft(d OSSDraft) error {
	if !regionPattern.MatchString(d.Region) || len(d.Region) > 48 {
		return kernel.BadAuthRequest("Region 必须填写标准区域，例如 cn-shanghai")
	}
	u, err := url.Parse(d.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "oss-"+d.Region+".aliyuncs.com" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return kernel.BadAuthRequest("Endpoint 必须是与 Region 一致的阿里云官方 HTTPS 地址，例如 https://oss-cn-shanghai.aliyuncs.com")
	}
	if !bucketPattern.MatchString(d.Bucket) {
		return kernel.BadAuthRequest("Bucket 名称格式无效")
	}
	if d.AccessKeyID == "" || d.AccessKeySecret == "" {
		return kernel.BadAuthRequest("请完整配置 AccessKey ID 和 AccessKey Secret")
	}
	if len(d.AccessKeyID) > 256 || len(d.AccessKeySecret) > 256 || strings.ContainsAny(d.AccessKeyID+d.AccessKeySecret, "\r\n\t ") {
		return kernel.BadAuthRequest("访问密钥格式无效")
	}
	return nil
}

func locationID(d OSSDraft) string {
	h := sha256.Sum256([]byte(d.Endpoint + "\x00" + d.Bucket))
	return hex.EncodeToString(h[:16])
}

func (s *Service) plain(loc *location) (OSSDraft, error) {
	d := loc.OSSDraft
	if !strings.HasPrefix(d.AccessKeyID, localcrypto.Prefix) || !strings.HasPrefix(d.AccessKeySecret, localcrypto.Prefix) {
		return OSSDraft{}, storageError("configuration", "存储凭据未加密，拒绝使用")
	}
	var err error
	d.AccessKeyID, err = s.options.Secrets.Decrypt(d.AccessKeyID)
	if err == nil {
		d.AccessKeySecret, err = s.options.Secrets.Decrypt(d.AccessKeySecret)
	}
	if err != nil {
		return OSSDraft{}, storageError("configuration", "无法解密存储凭据，请检查设置密钥文件")
	}
	return d, nil
}

func (s *Service) mergeDraft(p policy, draft *OSSDraft) (OSSDraft, error) {
	var old OSSDraft
	if p.LocationID != "" {
		loc, err := s.readLocation(p.LocationID)
		if err != nil {
			return old, err
		}
		old, err = s.plain(loc)
		if err != nil {
			return old, err
		}
	}
	if draft == nil {
		return old, validateDraft(old)
	}
	d := *draft
	d.Endpoint = strings.TrimRight(strings.TrimSpace(d.Endpoint), "/")
	d.Region, d.Bucket = strings.TrimSpace(d.Region), strings.TrimSpace(d.Bucket)
	d.AccessKeyID, d.AccessKeySecret = strings.TrimSpace(d.AccessKeyID), strings.TrimSpace(d.AccessKeySecret)
	if d.AccessKeyID != "" && d.AccessKeyID != old.AccessKeyID && d.AccessKeySecret == "" {
		return d, kernel.BadAuthRequest("更换 AccessKey ID 时必须同时填写匹配的 Secret")
	}
	if d.AccessKeyID == "" {
		d.AccessKeyID = old.AccessKeyID
	}
	if d.AccessKeySecret == "" {
		d.AccessKeySecret = old.AccessKeySecret
	}
	return d, validateDraft(d)
}

func (s *Service) Save(ctx context.Context, actor string, req SaveRequest) (*View, error) {
	if _, err := s.admin.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if req.Provider != "local" && req.Provider != "oss" {
		return nil, kernel.BadAuthRequest("请选择本地或阿里云 OSS 存储")
	}
	p, raw, err := s.readPolicy()
	if err != nil {
		return nil, err
	}
	if req.ExpectedVersion != p.Version {
		return nil, versionConflict()
	}
	var locationRow *model.SystemSetting
	if req.OSS != nil || req.Provider == "oss" {
		draft, err := s.mergeDraft(p, req.OSS)
		if err != nil {
			return nil, err
		}
		if _, err := s.probe(ctx, draft); err != nil {
			return nil, err
		}
		loc := location{ID: locationID(draft), OSSDraft: draft}
		loc.AccessKeyID, err = s.options.Secrets.Encrypt(draft.AccessKeyID)
		if err == nil {
			loc.AccessKeySecret, err = s.options.Secrets.Encrypt(draft.AccessKeySecret)
		}
		if err != nil {
			return nil, storageError("configuration", "加密存储凭据失败，设置未保存")
		}
		data, _ := json.Marshal(loc)
		locationRow = &model.SystemSetting{Key: locationPrefix + loc.ID, ValueJSON: string(data), UpdatedBy: actor}
		p.LocationID = loc.ID
	}
	p.Provider, p.Version = req.Provider, p.Version+1
	data, _ := json.Marshal(p)
	err = s.repo.ReplaceStorageSettings(ctx, raw, &model.SystemSetting{Key: policyKey, ValueJSON: string(data), UpdatedBy: actor}, locationRow,
		model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: policyKey, Action: "storage.configure", Reason: "管理员更新文件存储策略"})
	if errors.Is(err, repository.ErrStorageVersion) {
		return nil, versionConflict()
	}
	if err != nil {
		return nil, err
	}
	return s.Get(actor)
}

func versionConflict() error {
	return &kernel.AppError{Status: 409, Code: 409, Reason: "storage_version_conflict", Message: "存储设置已被其他管理员更新，请刷新配置后重新保存"}
}
