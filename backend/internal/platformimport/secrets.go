//go:build platform

package platformimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/localcrypto"
)

type secretCleaner struct {
	source string
	target string
	known  []string
}

func scrubBusinessData(ctx context.Context, db *gorm.DB, source, target string) error {
	cleaner := &secretCleaner{source: source, target: target}
	if err := collectLocalConfigSecrets(source, cleaner); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, field := range []string{"api_key", "secret_key"} {
			if err := transformColumn(tx, "model_channels", field, func(value string) (string, error) {
				if err := cleaner.credential(value); err != nil {
					return "", err
				}
				return "", nil
			}); err != nil {
				return err
			}
		}
		if err := transformColumn(tx, "model_channels", "headers_json", func(value string) (string, error) {
			if strings.TrimSpace(value) != "" {
				headers, err := strictJSON(value)
				if err != nil {
					return "", errors.New("渠道请求头 JSON 无效")
				}
				if err := cleaner.headerValues(headers); err != nil {
					return "", err
				}
			}
			return "", nil
		}); err != nil {
			return err
		}
		if err := transformColumn(tx, "tasks", "input_json", cleaner.cleanJSON); err != nil {
			return err
		}
		for _, item := range []struct{ table, column string }{{"tasks", "workflow_source_cipher"}, {"canvas_workflows", "recipe_cipher"}, {"canvas_workflow_revisions", "recipe_cipher"}, {"canvas_workflow_runs", "recipe_cipher"}} {
			if err := transformColumn(tx, item.table, item.column, cleaner.cleanEnvelope); err != nil {
				return err
			}
		}
		for _, field := range []string{"request_cipher", "query_cipher"} {
			if err := transformColumn(tx, "image_submissions", field, func(value string) (string, error) {
				if value != "" {
					plain, err := localcrypto.Decrypt(source, value)
					if err != nil || !json.Valid([]byte(plain)) {
						return "", errors.New("旧上游冻结请求无法解密或格式无效")
					}
					if _, err := cleaner.cleanJSON(plain); err != nil {
						return "", err
					}
				}
				return "", nil
			}); err != nil {
				return err
			}
		}
		if err := cleaner.settings(tx); err != nil {
			return err
		}
		// 历史请求和诊断正文可能包含未加密凭据；保留计数、身份和状态，移除执行材料。
		for table, fields := range map[string]map[string]any{
			"model_channels": {"enabled": false, "base_url": "", "headers_json": "", "api_format": ""},
			"channel_models": {"enabled": false}, "channel_model_variants": {"enabled": false}, "logical_models": {"enabled": false}, "logical_model_routes": {"enabled": false},
			"tasks":          {"lease_owner": "", "lease_expires_at": nil, "next_poll_at": nil, "poll_stage": "", "provider_cancel_next_check_at": nil, "provider_cancel_error": "", "failure_diagnostics": nil, "error": ""},
			"task_logs":      {"message": "已导入的本地历史日志", "payload": ""},
			"api_call_logs":  {"request_body": "", "response_body": "", "upstream_url": "", "path": "", "error": ""},
			"route_attempts": {"failure_message": ""},
		} {
			if err := tx.Table(table).Where("1 = 1").Updates(fields).Error; err != nil {
				return err
			}
		}
		return validateCipherInventory(ctx, tx, target, cleaner.known)
	})
}

func (s *secretCleaner) credential(value string) error {
	if value == "" || value == "system" {
		return nil
	}
	if strings.HasPrefix(value, localcrypto.Prefix) {
		plain, err := localcrypto.Decrypt(s.source, value)
		if err != nil {
			return errors.New("本地凭据不能由源密钥解密")
		}
		value = plain
	}
	if value != "" && value != "system" {
		s.known = append(s.known, value)
		parts := strings.Fields(value)
		if len(parts) == 2 && (strings.EqualFold(parts[0], "Bearer") || strings.EqualFold(parts[0], "Basic")) {
			s.known = append(s.known, parts[1])
		}
	}
	return nil
}

func (s *secretCleaner) cleanEnvelope(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	plain, err := localcrypto.Decrypt(s.source, value)
	if err != nil {
		return "", errors.New("业务配方无法由源密钥解密")
	}
	clean, err := s.cleanJSON(plain)
	if err != nil {
		return "", err
	}
	return localcrypto.Encrypt(s.target, clean)
}

func (s *secretCleaner) cleanJSON(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return value, nil
	}
	document, err := strictJSON(value)
	if err != nil {
		return "", fmt.Errorf("业务密文包含无法审计的 JSON：%w", err)
	}
	if err := s.walk(document); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(document)
	return string(encoded), err
}

func (s *secretCleaner) walk(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(key)
			switch normalized {
			case "apikey", "secretkey", "accesskeysecret", "secretaccesskey", "authorization", "cookie":
				text, ok := child.(string)
				if !ok && child != nil {
					return errors.New("本地凭据字段类型无效")
				}
				if err := s.credential(text); err != nil {
					return err
				}
				delete(typed, key)
			case "imagedeliverycipher":
				text, ok := child.(string)
				if !ok {
					return errors.New("图片交付密文类型无效")
				}
				if text != "" {
					plain, err := localcrypto.Decrypt(s.source, text)
					if err != nil {
						return errors.New("图片交付密文无法解密")
					}
					if _, err := s.cleanJSON(plain); err != nil {
						return err
					}
				}
				delete(typed, key)
			case "headers", "header":
				if err := s.headerValues(child); err != nil {
					return err
				}
				delete(typed, key)
			case "baseurl", "credentialref", "channelid", "channelmodelkey", "channelmodelid", "variantid", "interfacetype", "apiformat", "referenceassetorigin":
				if err := rejectCipher(child); err != nil {
					return err
				}
				delete(typed, key)
			default:
				if err := s.walk(child); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, child := range typed {
			if err := s.walk(child); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(typed, localcrypto.Prefix) {
			return errors.New("发现未分类业务密文，拒绝迁移")
		}
	}
	return nil
}

func rejectCipher(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if bytes.Contains(encoded, []byte(localcrypto.Prefix)) {
		return errors.New("执行配置包含未分类密文")
	}
	return nil
}

func (s *secretCleaner) headerValues(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if header, ok := typed["value"]; ok {
			return s.headerValues(header)
		}
		for _, child := range typed {
			if err := s.headerValues(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := s.headerValues(child); err != nil {
				return err
			}
		}
	case string:
		if !strings.HasPrefix(typed, localcrypto.Prefix) && strings.Contains(typed, localcrypto.Prefix) {
			return errors.New("请求头包含不支持密文")
		}
		return s.credential(typed)
	}
	return nil
}

func (s *secretCleaner) settings(tx *gorm.DB) error {
	var settings []struct{ Key, ValueJSON string }
	if err := tx.Table("system_settings").Find(&settings).Error; err != nil {
		return err
	}
	for _, setting := range settings {
		switch setting.Key {
		case "ark_private_assets":
			if _, err := s.cleanJSON(setting.ValueJSON); err != nil {
				return err
			}
			if err := tx.Table("system_settings").Where("key = ?", setting.Key).Delete(nil).Error; err != nil {
				return err
			}
		case "appearance", "canvas_document_limit_mb", "runtime_policy", "feature_availability":
			if err := rejectCipher(setting.ValueJSON); err != nil {
				return err
			}
		default:
			return fmt.Errorf("本阶段不支持系统配置 %s，源配置未改变", setting.Key)
		}
	}
	return nil
}

func transformColumn(db *gorm.DB, table, column string, transform func(string) (string, error)) error {
	var rows []struct {
		RowID int64
		Value string
	}
	if err := db.Raw("SELECT rowid AS row_id, " + column + " AS value FROM " + table + " WHERE " + column + " IS NOT NULL AND " + column + " <> ''").Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		value, err := transform(row.Value)
		if err != nil {
			return fmt.Errorf("%s.%s：%w", table, column, err)
		}
		if err := db.Table(table).Where("rowid = ?", row.RowID).Update(column, value).Error; err != nil {
			return err
		}
	}
	return nil
}

func validateCipherInventory(ctx context.Context, db *gorm.DB, directory string, secrets []string) error {
	schemas, err := modelSchemas()
	if err != nil {
		return err
	}
	for table, s := range schemas {
		for column, field := range s.FieldsByDBName {
			if field.DataType != "string" || !db.Migrator().HasColumn(table, column) {
				continue
			}
			var values []string
			if err := db.WithContext(ctx).Table(table).Where(column+" IS NOT NULL AND "+column+" <> ''").Pluck(column, &values).Error; err != nil {
				return err
			}
			for _, value := range values {
				if strings.Contains(value, localcrypto.Prefix) {
					known := table == "tasks" && column == "workflow_source_cipher" || (table == "canvas_workflows" || table == "canvas_workflow_revisions" || table == "canvas_workflow_runs") && column == "recipe_cipher"
					if !known {
						return fmt.Errorf("%s.%s 包含未知密文", table, column)
					}
					plain, err := localcrypto.Decrypt(directory, value)
					if err != nil {
						return errors.New("保留业务密文不能由目标密钥解密")
					}
					value = plain
					if strings.Contains(value, localcrypto.Prefix) {
						return errors.New("目标业务密文仍嵌套源密文")
					}
				}
				for _, secret := range secrets {
					if value == secret || len(secret) >= 4 && strings.Contains(value, secret) {
						return errors.New("仍有已识别的本地凭据残留在业务数据，拒绝导入")
					}
				}
			}
		}
	}
	return nil
}
