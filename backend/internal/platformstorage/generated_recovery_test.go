//go:build platform

package platformstorage_test

import (
	"bytes"
	"fmt"
	"testing"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"
)

func TestUnknownGeneratedReservationRestoresAfterRestart(t *testing.T) {
	for _, alreadyReserved := range []int64{0, 4} {
		for _, declaredSize := range []int64{0, 7} {
			t.Run(fmt.Sprintf("预留%d声明%d", alreadyReserved, declaredSize), func(t *testing.T) {
				f := newFixture(t, 7)
				key := asset.NormalizedUploadKey([]string{"task:unknown"})
				if _, err := f.storage.Quota().ReserveGenerated("workspace-alice", 0, *key); err != nil {
					t.Fatal(err)
				}
				resource := model.Resource{ID: "unknown-result", UserID: "workspace-alice", Kind: "file", MimeType: "application/octet-stream", Provider: "local", ObjectKey: "unknown-result.bin", Status: model.ResourceStatusPending, UploadKey: key}
				if err := f.repo.CreateResource(&resource); err != nil {
					t.Fatal(err)
				}
				if alreadyReserved > 0 {
					if err := f.storage.Extend(resource.UserID, *key, alreadyReserved); err != nil {
						t.Fatal(err)
					}
				}
				restarted := f.newResources()
				witness, err := f.repo.UploadReservation(resource.UserID, *key)
				if err != nil || witness == nil || witness.Unattributed() {
					t.Fatalf("持久生成预留被当作无归属：%+v %v", witness, err)
				}
				result, err := restarted.RecoverOwned(resource.UserID, "task:unknown", func() (asset.RecoveredArtifact, error) {
					return asset.RecoveredArtifact{Kind: "file", FileName: "unknown.bin", MimeType: "application/octet-stream", Size: declaredSize, Body: bytes.NewBufferString("payload")}, nil
				})
				if err != nil || result == nil || result.Size != 7 || result.Status != model.ResourceStatusReady {
					t.Fatalf("未知大小恢复失败：%+v %v", result, err)
				}
				usage(t, f, 7, 0)
				if _, err := restarted.RecoverOwned(resource.UserID, "task:unknown", func() (asset.RecoveredArtifact, error) {
					t.Fatal("重复恢复重新请求上游")
					return asset.RecoveredArtifact{}, nil
				}); err != nil {
					t.Fatal(err)
				}
				usage(t, f, 7, 0)
			})
		}
	}
}

func TestUnknownReservationDoesNotWeakenLocalAttribution(t *testing.T) {
	for _, value := range []model.UserUploadReservation{
		{UserID: "owner", Identity: "old", Day: "2026-01-01", Size: 0},
		{UserID: "owner", Identity: "old", Day: model.UploadReservationUnattributedDay, Size: 0, DurableOwnership: true},
	} {
		if !value.Unattributed() {
			t.Fatal("历史无归属预留被放行")
		}
	}
}

func TestUnknownGeneratedFilePublishedBeforeReadyCommitRecoversActualSize(t *testing.T) {
	f := newFixture(t, 7)
	key := asset.NormalizedUploadKey([]string{"task:published"})
	if _, err := f.storage.Quota().ReserveGenerated("workspace-alice", 0, *key); err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{ID: "published-result", UserID: "workspace-alice", Kind: "file", MimeType: "application/octet-stream", Provider: "local", ObjectKey: "published-result.bin", Status: model.ResourceStatusPending, UploadKey: key}
	if err := f.repo.CreateResource(&resource); err != nil {
		t.Fatal(err)
	}
	if _, err := f.resources.WriteObject(&resource, "", bytes.NewBufferString("payload")); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 0, 7)
	result, err := f.newResources().RecoverOwned(resource.UserID, "task:published", func() (asset.RecoveredArtifact, error) {
		t.Fatal("已发布文件不应重新读取上游")
		return asset.RecoveredArtifact{}, nil
	})
	if err != nil || result == nil || result.Size != 7 || result.Status != model.ResourceStatusReady {
		t.Fatalf("已发布文件恢复大小错误：%+v %v", result, err)
	}
	usage(t, f, 7, 0)
}
