package task

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestGenerationFileDeliveryReusesOriginalIdentity(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "files.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := db.AutoMigrate(&model.Task{}, &model.Resource{}, &model.UserUploadReservation{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	resources := asset.NewService(asset.Dependencies{Repository: asset.NewRepository(repo), Blobs: asset.NewFileStore(dir)})
	requests := 0
	payload := strings.Repeat("generated-file", 1<<18)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	deps := DeliveryMediaDeps{Repo: repo, DataDir: dir, Resources: resources, MaxBytes: func() (int64, error) { return int64(len(payload)) + 1, nil },
		BeginDownload: func(string, string, time.Duration) (context.Context, func(error), error) {
			return context.Background(), func(error) {}, nil
		}}
	first, err := PersistRemoteGenerationArtifact(deps, "owner", "file", server.URL+"/generated.bin", "task:0", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PersistRemoteGenerationArtifact(deps, "owner", "file", server.URL+"/generated.bin", "task:0", time.Minute)
	if err != nil || first.ID != second.ID || requests != 1 || first.Size != int64(len(payload)) {
		t.Fatalf("文件交付身份或重复请求错误: %v requests=%d", err, requests)
	}
	_, body, err := resources.Open("owner", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	count, err := io.Copy(io.Discard, body)
	if err != nil || count != first.Size {
		t.Fatal("交付内容不完整")
	}
}
