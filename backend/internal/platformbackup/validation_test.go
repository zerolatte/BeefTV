//go:build platform

package platformbackup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/model"
)

func TestBackupValidatesMasterKeyAgainstEveryCredentialVersion(t *testing.T) {
	source, db := fixture(t)
	ciphertext, err := localcrypto.Encrypt(source, `{"apiKey":"private-test-key"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int64{1, 2} {
		credential := model.PlatformCredentialVersion{ID: string(rune('a' + version)), ChannelID: "channel", Version: version, Ciphertext: ciphertext, UpstreamHost: "example.test", HeaderNamesJSON: "[]", CreatedBy: "admin"}
		if err := db.Create(&credential).Error; err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(t.TempDir(), "valid")
	manifest, err := Create(t.Context(), source, backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.PlatformCredentialVersion{}).Where("version = ?", 1).Update("ciphertext", "corrupt-historical-version").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), source, filepath.Join(t.TempDir(), "bad-history")); err == nil {
		t.Fatal("历史凭据损坏被最新有效版本掩盖")
	}
	if err := db.Model(&model.PlatformCredentialVersion{}).Where("version = ?", 1).Update("ciphertext", ciphertext).Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".settings-key"), bytes.Repeat([]byte{42}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), source, filepath.Join(t.TempDir(), "wrong-key")); err == nil || strings.Contains(err.Error(), "private-test-key") {
		t.Fatalf("错误主密钥被接受或错误暴露明文：%v", err)
	}
	// 即使文件摘要与错误密钥一起被重新生成，语义校验也必须拒绝这份不可解密的备份。
	data := filepath.Join(backup, "data")
	if err := os.WriteFile(filepath.Join(data, ".settings-key"), bytes.Repeat([]byte{43}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.Files, err = inventory(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, manifestName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(t.Context(), backup); err == nil {
		t.Fatal("备份摘要匹配掩盖了不可解密的主密钥")
	}
}

func TestBackupRejectsBrokenReadyResources(t *testing.T) {
	for _, damage := range []string{"missing", "traversal", "size", "external", "directory"} {
		t.Run(damage, func(t *testing.T) {
			source, db := fixture(t)
			resource := model.Resource{ID: "resource", UserID: "workspace", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "video.bin", Size: int64(len("original-media"))}
			switch damage {
			case "missing":
				if err := os.Remove(filepath.Join(source, "resources", "video.bin")); err != nil {
					t.Fatal(err)
				}
			case "traversal":
				resource.ObjectKey, resource.Size = "../.settings-key", 32
			case "size":
				resource.Size++
			case "external":
				resource.Provider = "external"
			case "directory":
				resource.ObjectKey = "nested"
				if err := os.Mkdir(filepath.Join(source, "resources", "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&resource).Error; err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "broken")
			if _, err := Create(t.Context(), source, destination); err == nil {
				t.Fatal("损坏资源被当作完整备份")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("验证失败交付了备份目录")
			}
		})
	}
}

func TestBackupPreservesResourcesAwaitingRecoveryOrDeletion(t *testing.T) {
	source, db := fixture(t)
	for _, status := range []model.ResourceStatus{model.ResourceStatusPending, model.ResourceStatusFailed, model.ResourceStatusDeleted} {
		resource := model.Resource{ID: string(status), UserID: "workspace", Status: status, Provider: "local", ObjectKey: "missing-" + string(status), Size: 7}
		if err := db.Create(&resource).Error; err != nil {
			t.Fatal(err)
		}
	}
	item := model.Asset{ID: "deleting", UserID: "workspace", Status: model.AssetVersionStatusDeleting, PayloadJSON: `{"data":{"storageKey":"resource:deleted"}}`}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	valid := model.Resource{ID: "ready", UserID: "workspace", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "video.bin", Size: int64(len("original-media"))}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(t.Context(), source, destination); err != nil {
		t.Fatal("待恢复状态阻止了完整备份", err)
	}
	if _, err := Verify(t.Context(), destination); err != nil {
		t.Fatal(err)
	}
}
