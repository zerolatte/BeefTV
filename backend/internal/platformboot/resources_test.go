//go:build platform

package platformboot

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformstorage"
)

func rawPlatformRequest(t *testing.T, b *platformBrowser, method, path string, body io.Reader, headers map[string]string, expected int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, b.fixture.server.URL+"/api"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", b.fixture.server.URL)
	req.Header.Set("X-CSRF-Token", b.csrf)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != expected {
		t.Fatalf("%s %s status=%d body=%s err=%v", method, path, response.StatusCode, data, err)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("平台资源响应允许持久缓存")
	}
	return data
}

func TestPlatformResourceQuotaAndHTTPIsolation(t *testing.T) {
	f := newPlatformFixture(t)
	input := platformAccountInput("alice")
	limit := int64(10)
	input.StorageLimitBytes = &limit
	account, err := f.runtime.Auth.CreateAccount(t.Context(), f.admin.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	alice := f.login(t, "alice", true)
	admin := f.login(t, f.admin.Username, false)
	digest, err := asset.UploadContentDigest(bytes.NewBufferString("payload"))
	if err != nil {
		t.Fatal(err)
	}
	start := map[string]any{"fileName": "payload.bin", "kind": "file", "size": 7, "contentDigest": digest}
	alice.request(t, "POST", "/resources/uploads", start, 400, nil)
	session := decodePlatformData[struct{ UploadID string }](t, alice.request(t, "POST", "/resources/uploads", start, 200, map[string]string{"X-Idempotency-Key": "first"}))
	rawPlatformRequest(t, admin, "PUT", "/resources/uploads/"+session.UploadID+"/chunks/0", bytes.NewReader([]byte("payload")), nil, 404)
	admin.request(t, "POST", "/resources/uploads/"+session.UploadID+"/complete", nil, 404, nil)
	rawPlatformRequest(t, alice, "PUT", "/resources/uploads/"+session.UploadID+"/chunks/0", bytes.NewReader([]byte("payload")), map[string]string{"Content-Type": "application/octet-stream"}, 200)
	resource := decodePlatformData[struct{ Resource model.Resource }](t, alice.request(t, "POST", "/resources/uploads/"+session.UploadID+"/complete", nil, 200, nil)).Resource
	if resource.UserID != account.WorkspaceID {
		t.Fatal("资源误绑定账号 ID")
	}
	usage := decodePlatformData[struct{ Usage platformstorage.Usage }](t, alice.request(t, "GET", "/resources/storage-usage", nil, 200, nil)).Usage
	if usage.UsedBytes != 7 || usage.ReservedBytes != 0 || usage.LimitBytes != 10 {
		t.Fatalf("用量错误 %+v", usage)
	}
	for _, path := range []string{"/resources/" + resource.ID, "/resources/" + resource.ID + "/file"} {
		admin.request(t, "GET", path, nil, 404, map[string]string{"Range": "bytes=0-2"})
	}
	data := rawPlatformRequest(t, alice, "GET", "/resources/"+resource.ID+"/file", nil, map[string]string{"Range": "bytes=0-2"}, 206)
	if string(data) != "pay" {
		t.Fatalf("Range 内容错误 %q", data)
	}
	start["size"] = 4
	response := alice.request(t, "POST", "/resources/uploads", start, 403, map[string]string{"X-Idempotency-Key": "exceeds"})
	if response.Reason != "storage_quota_exceeded" {
		t.Fatal(response.Reason)
	}
}

func TestPlatformMultipartReservesBeforeBody(t *testing.T) {
	f := newPlatformFixture(t)
	admin := f.login(t, f.admin.Username, false)
	if _, err := f.runtime.Auth.UpdateStorageLimit(t.Context(), f.admin.ID, f.admin.ID, 5); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "payload.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("payload"))
	_ = form.WriteField("kind", "file")
	_ = form.Close()
	digest, err := asset.UploadContentDigest(bytes.NewBufferString("payload"))
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"X-Idempotency-Key": "multipart", "X-Upload-Size": "7", "X-Upload-Digest": digest, "Content-Type": form.FormDataContentType()}
	data := rawPlatformRequest(t, admin, "POST", "/resources", bytes.NewReader(body.Bytes()), headers, 403)
	var response platformResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Reason != "storage_quota_exceeded" {
		t.Fatalf("错误响应 %s", data)
	}
	var count int64
	if err := f.runtime.db.Model(&model.Resource{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("超限上传留下资源记录", count, err)
	}
	if _, err := f.runtime.Auth.UpdateStorageLimit(t.Context(), f.admin.ID, f.admin.ID, 7); err != nil {
		t.Fatal(err)
	}
	_ = rawPlatformRequest(t, admin, "POST", "/resources", bytes.NewReader(body.Bytes()), headers, 200)
	_ = rawPlatformRequest(t, admin, "POST", "/resources", bytes.NewReader(body.Bytes()), headers, 200)
	usage := decodePlatformData[struct{ Usage platformstorage.Usage }](t, admin.request(t, "GET", "/resources/storage-usage", nil, 200, nil)).Usage
	if usage.UsedBytes != 7 || usage.ReservedBytes != 0 {
		t.Fatal("重复上传重复计算空间", usage)
	}
}

func TestPlatformUploadDigestRejectsChangedBytesAndReplay(t *testing.T) {
	f := newPlatformFixture(t)
	admin := f.login(t, f.admin.Username, false)
	digest, _ := asset.UploadContentDigest(bytes.NewBufferString("payload"))
	start := map[string]any{"fileName": "payload.bin", "kind": "file", "size": 7, "contentDigest": digest}
	headers := map[string]string{"X-Idempotency-Key": "digest-upload"}
	session := decodePlatformData[struct{ UploadID string }](t, admin.request(t, "POST", "/resources/uploads", start, 200, headers))
	rawPlatformRequest(t, admin, "PUT", "/resources/uploads/"+session.UploadID+"/chunks/0", bytes.NewBufferString("changed"), nil, 200)
	response := admin.request(t, "POST", "/resources/uploads/"+session.UploadID+"/complete", nil, 409, nil)
	if response.Reason != "upload_content_mismatch" {
		t.Fatal(response.Reason)
	}
	rawPlatformRequest(t, admin, "PUT", "/resources/uploads/"+session.UploadID+"/chunks/0", bytes.NewBufferString("payload"), nil, 200)
	admin.request(t, "POST", "/resources/uploads/"+session.UploadID+"/complete", nil, 200, nil)
	otherDigest, _ := asset.UploadContentDigest(bytes.NewBufferString("changed"))
	start["contentDigest"] = otherDigest
	response = admin.request(t, "POST", "/resources/uploads", start, 409, headers)
	if response.Reason != "idempotency_conflict" {
		t.Fatal(response.Reason)
	}
}
