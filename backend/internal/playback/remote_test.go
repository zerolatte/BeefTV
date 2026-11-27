package playback

import (
	"context"
	"errors"
	"infinite-canvas/backend/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestRemotePlaybackMaterializesAndReleases(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "成功", true: "下载失败"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			store := &memStore{}
			store.put(model.Resource{ID: "remote-video", UserID: "owner", Provider: "oss", Kind: "video", Status: model.ResourceStatusReady})
			released := false
			transcodes := 0
			svc := New(Deps{DataDir: dir, Store: store, Runner: syncRunner{}, Materialize: func(ctx context.Context, owner, id string) (string, func(), error) {
				if owner != "owner" || id != "remote-video" {
					t.Fatal("媒体归属未传递")
				}
				if fail {
					return "", nil, errors.New("OSS 访问暂时失败")
				}
				path := filepath.Join(dir, "temporary.mp4")
				writeCodecMP4(t, path, "hvc1")
				return path, func() { released = true; _ = os.Remove(path) }, nil
			}, Transcode: func(ctx context.Context, src, dst string) error {
				transcodes++
				writeCodecMP4(t, dst, "avc1")
				return nil
			}})
			svc.MaybeStart(store.get("remote-video"))
			row := store.get("remote-video")
			if fail {
				if row.PlaybackStatus != model.PlaybackStatusFailed || transcodes != 0 {
					t.Fatal("下载失败被伪装成功")
				}
				return
			}
			if row.PlaybackStatus != model.PlaybackStatusReady || !released || transcodes != 1 {
				t.Fatalf("远端播放缓存错误: %+v released=%v", row, released)
			}
			stream, err := svc.OpenRange("owner", row.ID)
			if err != nil {
				t.Fatal(err)
			}
			stream.Body.Close()
		})
	}
}
