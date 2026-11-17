//go:build platform

package platformstorage_test

import (
	"bytes"
	"testing"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/platformstorage"
)

func TestUploadRequestSurvivesServiceRestart(t *testing.T) {
	f := newFixture(t, 100)
	if err := f.db.AutoMigrate(&platformstorage.UploadRequest{}); err != nil {
		t.Fatal(err)
	}
	digest, _ := asset.UploadContentDigest(bytes.NewBufferString("payload"))
	req := asset.ChunkedUploadStart{FileName: "payload.bin", Kind: "file", Size: 7, IdempotencyKey: "same-key", ContentDigest: digest}
	if err := f.storage.BindUploadRequest("workspace-alice", req); err != nil {
		t.Fatal(err)
	}
	restarted := platformstorage.New(f.db)
	if err := restarted.BindUploadRequest("workspace-alice", req); err != nil {
		t.Fatal(err)
	}
	changedMetadata := req
	changedMetadata.Width = 100
	reason(t, restarted.BindUploadRequest("workspace-alice", changedMetadata), "idempotency_conflict")
	req.ContentDigest, _ = asset.UploadContentDigest(bytes.NewBufferString("changed"))
	reason(t, restarted.BindUploadRequest("workspace-alice", req), "idempotency_conflict")
	if err := restarted.BindUploadRequest("workspace-bob", req); err != nil {
		t.Fatal(err)
	}
}

func TestUploadReplayAcceptsServerProbedMetadata(t *testing.T) {
	f := newFixture(t, 100)
	req := asset.ChunkedUploadStart{FileName: "payload.bin", Kind: "file", Size: 7, IdempotencyKey: "probe-replay"}
	session, err := f.resources.StartChunkedUpload("workspace-alice", req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.resources.PutChunkedUpload("workspace-alice", session.UploadID, 0, bytes.NewBufferString("payload")); err != nil {
		t.Fatal(err)
	}
	resource, err := f.resources.CompleteChunkedUpload("workspace-alice", session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Table("resources").Where("id = ?", resource.ID).Updates(map[string]any{"width": 1280, "height": 720, "duration_ms": 1000}).Error; err != nil {
		t.Fatal(err)
	}
	restarted := f.newResources()
	replay, err := restarted.StartChunkedUpload("workspace-alice", req)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := restarted.CompleteChunkedUpload("workspace-alice", replay.UploadID)
	if err != nil || actual.ID != resource.ID || actual.Width != 1280 {
		t.Fatalf("重放结果错误 %+v %v", actual, err)
	}
	usage(t, f, 7, 0)
}

func TestResumedChunkUploadRetainsContentDigest(t *testing.T) {
	f := newFixture(t, 100)
	digest, _ := asset.UploadContentDigest(bytes.NewBufferString("payload"))
	req := asset.ChunkedUploadStart{FileName: "payload.bin", Kind: "file", Size: 7, IdempotencyKey: "resume-digest", ContentDigest: digest}
	session, err := f.resources.StartChunkedUpload("workspace-alice", req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.resources.PutChunkedUpload("workspace-alice", session.UploadID, 0, bytes.NewBufferString("changed")); err != nil {
		t.Fatal(err)
	}
	restarted := f.newResources()
	_, err = restarted.CompleteChunkedUpload("workspace-alice", session.UploadID)
	reason(t, err, "upload_content_mismatch")
	usage(t, f, 0, 7)
	if err := restarted.PutChunkedUpload("workspace-alice", session.UploadID, 0, bytes.NewBufferString("payload")); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CompleteChunkedUpload("workspace-alice", session.UploadID); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 7, 0)
}
