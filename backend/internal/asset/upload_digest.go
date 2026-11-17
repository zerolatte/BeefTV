package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"infinite-canvas/backend/internal/kernel"
)

const UploadDigestPrefix = "sha256-chunks-v1:"

// UploadContentDigest 按固定 8 MiB 分块计算摘要；浏览器不必一次载入整个视频。
func UploadContentDigest(body io.Reader) (string, error) {
	root := sha256.New()
	for {
		chunk := sha256.New()
		n, err := io.CopyN(chunk, body, ChunkUploadSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if n > 0 {
			_, _ = root.Write(chunk.Sum(nil))
		}
		if errors.Is(err, io.EOF) {
			return UploadDigestPrefix + hex.EncodeToString(root.Sum(nil)), nil
		}
	}
}

func ValidateUploadDigest(digest string) error {
	if !strings.HasPrefix(digest, UploadDigestPrefix) || len(digest) != len(UploadDigestPrefix)+64 {
		return kernel.BadAuthRequest("必须提供有效的上传内容摘要")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(digest, UploadDigestPrefix)); err != nil || digest != strings.ToLower(digest) {
		return kernel.BadAuthRequest("上传内容摘要无效")
	}
	return nil
}

func VerifyUploadContent(body io.Reader, expected string) error {
	if err := ValidateUploadDigest(expected); err != nil {
		return err
	}
	actual, err := UploadContentDigest(body)
	if err != nil {
		return err
	}
	if actual != expected {
		return &kernel.AppError{Status: 409, Code: 409, Reason: "upload_content_mismatch", Message: "上传字节与声明摘要不一致，请重新选择原文件"}
	}
	return nil
}
