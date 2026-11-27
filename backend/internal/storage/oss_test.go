package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

type fakeOSSHTTP struct {
	mu         sync.Mutex
	objects    map[string][]byte
	parts      map[int][]byte
	failStatus int
	failDelete bool
	failPart   bool
	badCRC     bool
	aborts     int
	requests   []string
}

func crcValue(data []byte) string {
	h := oss.NewCRC64(0)
	_, _ = h.Write(data)
	return strconv.FormatUint(h.Sum64(), 10)
}
func (f *fakeOSSHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method)
	w.Header().Set("Content-Type", "application/xml")
	if f.failStatus != 0 {
		w.WriteHeader(f.failStatus)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>sensitive-key internal-address</Message><RequestId>private</RequestId></Error>`)
		return
	}
	key := r.URL.Path
	query := r.URL.Query()
	switch {
	case r.Method == "POST" && query.Has("uploads"):
		f.parts = map[int][]byte{}
		fmt.Fprint(w, `<InitiateMultipartUploadResult><Bucket>test-private</Bucket><Key>large</Key><UploadId>multipart-one</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == "PUT" && query.Get("uploadId") != "":
		if f.failPart {
			w.WriteHeader(503)
			fmt.Fprint(w, `<Error><Code>ServiceUnavailable</Code></Error>`)
			return
		}
		part, _ := strconv.Atoi(query.Get("partNumber"))
		data, _ := io.ReadAll(r.Body)
		f.parts[part] = data
		w.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, part))
		w.Header().Set("x-oss-hash-crc64ecma", crcValue(data))
	case r.Method == "POST" && query.Get("uploadId") != "":
		keys := make([]int, 0, len(f.parts))
		for key := range f.parts {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		data := []byte{}
		for _, key := range keys {
			data = append(data, f.parts[key]...)
		}
		f.objects[key] = data
		checksum := crcValue(data)
		if f.badCRC {
			checksum = "1"
		}
		w.Header().Set("x-oss-hash-crc64ecma", checksum)
		fmt.Fprint(w, `<CompleteMultipartUploadResult><Bucket>test-private</Bucket><Key>large</Key><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == "DELETE" && query.Get("uploadId") != "":
		f.aborts++
		f.parts = nil
		w.WriteHeader(204)
	case r.Method == "PUT":
		if r.Header.Get("x-oss-object-acl") != "private" {
			w.WriteHeader(400)
			return
		}
		data, _ := io.ReadAll(r.Body)
		f.objects[key] = data
		checksum := crcValue(data)
		if f.badCRC {
			checksum = "1"
		}
		w.Header().Set("x-oss-hash-crc64ecma", checksum)
		w.Header().Set("ETag", `"uploaded"`)
	case r.Method == "DELETE":
		if f.failDelete {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>sensitive-key internal-address</Message><RequestId>private</RequestId></Error>`)
			return
		}
		delete(f.objects, key)
		w.WriteHeader(204)
	case r.Method == "GET" || r.Method == "HEAD":
		data, ok := f.objects[key]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("x-oss-hash-crc64ecma", crcValue(data))
		w.Header().Set("ETag", `"uploaded"`)
		status := 200
		if r.Header.Get("Range") != "" {
			var start, end int
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
			if start >= len(data) {
				w.WriteHeader(416)
				return
			}
			if end >= len(data) {
				end = len(data) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			data = data[start : end+1]
			status = 206
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(status)
		if r.Method != "HEAD" {
			_, _ = w.Write(data)
		}
	default:
		w.WriteHeader(400)
	}
}
func sdkFixture(t *testing.T) (*ossClient, *fakeOSSHTTP) {
	t.Helper()
	fake := &fakeOSSHTTP{objects: map[string][]byte{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	cfg := oss.LoadDefaultConfig().WithEndpoint(server.URL).WithRegion("cn-shanghai").WithUsePathStyle(true).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-id", "test-secret")).WithRetryMaxAttempts(1).
		WithHttpClient(server.Client()).WithDisableUploadCRC64Check(false).WithDisableDownloadCRC64Check(false)
	return &ossClient{client: oss.NewClient(cfg), bucket: "test-private"}, fake
}

func TestOSSOfficialSDKMultipartRangeAndCleanup(t *testing.T) {
	client, fake := sdkFixture(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("payload-"), (10<<20)/8)
	file := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(file, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	recorded := ""
	if _, err := client.PutFile(ctx, "large", file, "application/octet-stream", func(id string) error { recorded = id; return nil }); err != nil {
		t.Fatal(err)
	}
	if recorded == "" || len(fake.parts) != 2 {
		t.Fatal("大文件未分片或未登记分片身份")
	}
	stream, err := client.Get(ctx, "large", "bytes=2-8")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream.Body)
	stream.Body.Close()
	if err != nil || stream.StatusCode != 206 || string(data) != string(payload[2:9]) {
		t.Fatal("官方 SDK 范围读取失败", err)
	}
	if ok, err := client.Exists(ctx, "large"); err != nil || !ok {
		t.Fatal("HEAD 失败", err)
	}
	if err := client.Delete(ctx, "large"); err != nil {
		t.Fatal(err)
	}
	if ok, err := client.Exists(ctx, "large"); err != nil || ok {
		t.Fatalf("不存在状态错误: %v %v", ok, err)
	}
	fake.failPart = true
	if _, err := client.PutFile(ctx, "large", file, "application/octet-stream", func(string) error { return nil }); err == nil || fake.aborts == 0 {
		t.Fatal("失败分片没有中止")
	}
	fake.failPart, fake.badCRC = false, true
	if _, err := client.PutFile(ctx, "large", file, "application/octet-stream", nil); err == nil {
		t.Fatal("CRC 损坏被接受")
	}
}

func TestOSSErrorsAreSanitizedAndNotMissing(t *testing.T) {
	client, fake := sdkFixture(t)
	for _, status := range []int{403, 429, 503} {
		fake.failStatus = status
		_, err := client.Exists(context.Background(), "file")
		if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "private") {
			t.Fatalf("错误被吞或未洗敏: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	_, err := client.Get(ctx, "file", "")
	if err == nil {
		t.Fatal("超时请求成功")
	}
}
