//go:build platform

package platformboot

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestPlatformAssetOwnershipAndAssociationIsolation(t *testing.T) {
	f := newPlatformFixture(t)
	account, err := f.runtime.Auth.CreateAccount(t.Context(), f.admin.ID, platformAccountInput("alice"))
	if err != nil {
		t.Fatal(err)
	}
	alice := f.login(t, "alice", true)
	admin := f.login(t, f.admin.Username, false)
	payload := map[string]any{"asset": map[string]any{"id": "alice-asset", "kind": "text", "title": "私人素材", "coverUrl": "", "tags": []string{}, "data": map[string]any{"content": "hello"}}}
	alice.request(t, "PUT", "/assets/alice-asset", payload, 200, nil)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		admin.request(t, method, "/assets/alice-asset", payload, 404, nil)
	}
	admin.request(t, "POST", "/assets/batch", map[string]any{"ids": []string{"alice-asset"}}, 404, nil)
	folder := decodePlatformData[struct{ Folder model.AssetFolder }](t, alice.request(t, "POST", "/asset-folders", map[string]any{"name": "私人分类"}, 200, nil)).Folder
	admin.request(t, "PATCH", "/asset-folders/"+folder.ID, map[string]any{"name": "修改"}, 404, nil)
	admin.request(t, "DELETE", "/asset-folders/"+folder.ID, nil, 404, nil)
	admin.request(t, "PATCH", "/assets/folder", map[string]any{"assetIds": []string{"alice-asset"}, "folderId": ""}, 404, nil)
	resource := model.Resource{ID: "alice-resource", UserID: account.WorkspaceID, Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/alice/private.bin"}
	if err := f.runtime.db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	admin.request(t, "PUT", "/assets/admin-asset", map[string]any{"asset": map[string]any{"id": "admin-asset", "kind": "image", "title": "引用", "coverUrl": "", "tags": []string{}, "data": map[string]any{"storageKey": "resource:" + resource.ID, "width": 1, "height": 1, "bytes": 1, "mimeType": "image/png"}}}, 404, nil)
	admin.request(t, "PUT", "/assets/admin-asset", map[string]any{"asset": map[string]any{"id": "admin-asset", "kind": "text", "title": "引用", "coverUrl": "", "tags": []string{}, "folderId": folder.ID, "data": map[string]any{"content": "x"}}}, 404, nil)
	alice.request(t, "DELETE", "/assets/alice-asset", nil, 200, nil)
	alice.request(t, "GET", "/assets/alice-asset", nil, 404, nil)
}
