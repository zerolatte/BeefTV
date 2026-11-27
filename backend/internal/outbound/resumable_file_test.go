package outbound

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResumableFileResultDoesNotMaterializeBytes(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	payload := bytes.Repeat([]byte("file-content"), 1<<18)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"file-result"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "result.part")
	result, err := DownloadResumable(context.Background(), server.URL+"/result.bin", ResumableOptions{ReturnFile: true, PartialPath: path, MaxBytes: int64(len(payload)) + 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 0 || result.FilePath != path || result.Size != int64(len(payload)) {
		t.Fatalf("未按文件合同返回: size=%d memory=%d path=%s", result.Size, len(result.Data), result.FilePath)
	}
	file, err := os.Open(result.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	count, err := io.Copy(io.Discard, file)
	if err != nil || count != result.Size {
		t.Fatal("文件未完整落盘")
	}
}
