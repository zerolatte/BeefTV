package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/outbound"
)

type ossClient struct {
	client *oss.Client
	bucket string
}

func newOSSClient(d OSSDraft) ObjectClient {
	client := outbound.OutboundHTTPClient(0)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("存储请求不允许重定向") }
	cfg := oss.LoadDefaultConfig().WithRegion(d.Region).WithEndpoint(d.Endpoint).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(d.AccessKeyID, d.AccessKeySecret)).
		WithHttpClient(client).WithRetryMaxAttempts(3).WithEnabledRedirect(false).
		WithConnectTimeout(8 * time.Second).WithReadWriteTimeout(30 * time.Second).
		WithDisableUploadCRC64Check(false).WithDisableDownloadCRC64Check(false)
	return &ossClient{client: oss.NewClient(cfg), bucket: d.Bucket}
}

func (c *ossClient) PutFile(ctx context.Context, key, path, mime string, record func(string) error) (string, error) {
	uploader := oss.NewUploader(trackedUpload{Client: c.client, record: record}, func(o *oss.UploaderOptions) { o.PartSize = 8 << 20; o.ParallelNum = 3; o.LeavePartsOnError = false })
	result, err := uploader.UploadFile(ctx, &oss.PutObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key), ContentType: oss.Ptr(mime), Acl: oss.ObjectACLPrivate}, path)
	if err != nil {
		return "", err
	}
	if result.HashCRC64 == nil {
		return "", storageError("integrity", "OSS 未返回文件完整性校验，文件未确认为保存成功")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	checksum := oss.NewCRC64(0)
	if _, err := io.Copy(checksum, file); err != nil {
		return "", err
	}
	if strconv.FormatUint(checksum.Sum64(), 10) != *result.HashCRC64 {
		return "", storageError("integrity", "OSS 文件完整性校验失败，请重试")
	}
	return oss.ToString(result.ETag), nil
}

type trackedUpload struct {
	*oss.Client
	record func(string) error
}

func (c trackedUpload) InitiateMultipartUpload(ctx context.Context, req *oss.InitiateMultipartUploadRequest, options ...func(*oss.Options)) (*oss.InitiateMultipartUploadResult, error) {
	result, err := c.Client.InitiateMultipartUpload(ctx, req, options...)
	if err != nil {
		return nil, err
	}
	if c.record != nil {
		if err := c.record(oss.ToString(result.UploadId)); err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.Client.AbortMultipartUpload(cleanup, &oss.AbortMultipartUploadRequest{Bucket: req.Bucket, Key: req.Key, UploadId: result.UploadId})
			return nil, storageError("cleanup", "无法持久记录分片上传身份，上传已中止")
		}
	}
	return result, nil
}
func (c *ossClient) Put(ctx context.Context, key string, body io.Reader) error {
	_, err := c.client.PutObject(ctx, &oss.PutObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key), Body: body, Acl: oss.ObjectACLPrivate})
	return err
}
func (c *ossClient) Get(ctx context.Context, key, span string) (*assets.ResourceStream, error) {
	req := &oss.GetObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key)}
	if span != "" {
		req.Range = oss.Ptr(span)
		req.RangeBehavior = oss.Ptr("standard")
	}
	result, err := c.client.GetObject(ctx, req)
	if err != nil {
		var service *oss.ServiceError
		if errors.As(err, &service) && service.StatusCode == 416 {
			return &assets.ResourceStream{StatusCode: 416, ContentLength: 0, AcceptRanges: "bytes", Body: http.NoBody}, nil
		}
		return nil, safeError(err)
	}
	return &assets.ResourceStream{Body: safeBody{result.Body}, StatusCode: result.StatusCode, ContentLength: result.ContentLength, ContentRange: oss.ToString(result.ContentRange), AcceptRanges: "bytes"}, nil
}

type safeBody struct{ io.ReadCloser }

func (b safeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, safeError(err)
	}
	return n, err
}
func (c *ossClient) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.HeadObject(ctx, &oss.HeadObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key)})
	var service *oss.ServiceError
	// HEAD 不返回错误正文，404 可能是桶不存在；用最小范围读取确认 NoSuchKey。
	if errors.As(err, &service) && service.StatusCode == 404 {
		result, lookupErr := c.client.GetObject(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key), Range: oss.Ptr("bytes=0-0"), RangeBehavior: oss.Ptr("standard")})
		if lookupErr == nil {
			_ = result.Body.Close()
			return true, nil
		}
		err = lookupErr
		if errors.As(err, &service) && service.StatusCode == 416 {
			return true, nil
		}
	}
	if errors.As(err, &service) && service.Code == "NoSuchKey" {
		return false, nil
	}
	if err != nil {
		return false, safeError(err)
	}
	return true, nil
}
func (c *ossClient) Delete(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key)})
	var service *oss.ServiceError
	if errors.As(err, &service) && service.StatusCode == http.StatusForbidden && service.Code == "AccessDenied" {
		return storageError("delete_permission", "OSS 拒绝删除文件。请为当前密钥所属的 RAM 身份授权 oss:DeleteObject，并检查 Bucket Policy 或其他策略中的显式拒绝")
	}
	return safeError(err)
}
func (c *ossClient) Abort(ctx context.Context, key, uploadID string) error {
	_, err := c.client.AbortMultipartUpload(ctx, &oss.AbortMultipartUploadRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key), UploadId: oss.Ptr(uploadID)})
	var service *oss.ServiceError
	if errors.As(err, &service) && service.Code == "NoSuchUpload" {
		return nil
	}
	return safeError(err)
}
func (c *ossClient) Sign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	result, err := c.client.Presign(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(c.bucket), Key: oss.Ptr(key)}, oss.PresignExpires(ttl))
	if err != nil {
		return "", safeError(err)
	}
	return result.URL, nil
}

func storageError(reason, message string) *kernel.AppError {
	return &kernel.AppError{Status: 503, Code: 503, Reason: kernel.ErrorReason("storage_" + reason), Message: message, Retryable: true}
}

// 只映射状态与类型，禁止把 SDK 错误正文（含地址、签名和请求号）输出。
func safeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return storageError("timeout", "OSS 请求超时，请重试")
	}
	var public *kernel.AppError
	if errors.As(err, &public) {
		return public
	}
	var service *oss.ServiceError
	if errors.As(err, &service) {
		switch service.StatusCode {
		case 401, 403:
			return storageError("permission", "OSS 鉴权或权限校验失败，请检查密钥与读写删除授权")
		case 404:
			if service.Code == "NoSuchKey" {
				return &kernel.AppError{Status: 404, Code: 404, Reason: "storage_object_missing", Message: "OSS 文件不存在，请检查原存储位置"}
			}
			return storageError("configuration", "OSS 存储桶不存在或区域配置有误")
		case 429:
			return storageError("rate_limited", "OSS 请求受限，请稍后重试")
		default:
			return storageError("upstream", "OSS 服务暂时不可用，请稍后重试")
		}
	}
	// SDK v2 的 CRC 校验使用未导出的普通错误，只匹配最内层固定前缀，不回显正文。
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.HasPrefix(cause.Error(), "crc is inconsistent, client ") {
			return storageError("integrity", "OSS 文件完整性校验失败，请重试")
		}
	}
	return storageError("network", "无法完成 OSS 请求，请检查网络和存储配置后重试")
}
