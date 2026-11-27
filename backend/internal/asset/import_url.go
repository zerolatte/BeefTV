package asset

import (
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/outbound"
)

// ImportURL 使用服务器出站保护流式下载，暂存后进入与普通上传相同的配额和保存入口。
func (s *Service) ImportURL(ctx context.Context, userID, rawURL, kind string, maxBytes int64, width, height int, duration int64, identity string) (*model.Resource, error) {
	if userID == "" || maxBytes <= 0 {
		return nil, kernel.BadAuthRequest("导入身份或文件限制无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	parsed, err := outbound.ValidateOutboundURLContext(ctx, rawURL)
	if err != nil {
		return nil, kernel.BadAuthRequest("导入地址无效或不允许访问")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, kernel.BadAuthRequest("导入地址无效")
	}
	response, err := outbound.OutboundHTTPClient(0).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, kernel.NewAppError(502, "无法下载远程文件，请检查地址后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, kernel.NewAppError(502, "远程文件服务拒绝下载，请检查地址和访问权限")
	}
	if response.ContentLength > maxBytes {
		return nil, kernel.BadAuthRequest("远程文件超过上传大小限制")
	}
	dir, err := s.makeDeriveTempDir()
	if err != nil {
		return nil, err
	}
	defer os.Remove(dir)
	file, err := os.CreateTemp(dir, "import-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	size, err := io.Copy(file, io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, kernel.NewAppError(502, "远程文件下载中断，请重试")
	}
	if size > maxBytes {
		return nil, kernel.BadAuthRequest("远程文件超过上传大小限制")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	name := path.Base(parsed.Path)
	if _, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		name = path.Base(params["filename"])
	}
	typeName := DetectUploadedMimeType(file, name, response.Header.Get("Content-Type"))
	return s.IngestUploadContext(ctx, userID, kind, name, typeName, size, width, height, duration, file, identity)
}
