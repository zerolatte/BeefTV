package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"time"

	"infinite-canvas/backend/internal/kernel"
)

func (s *Service) Probe(ctx context.Context, actor string, req ProbeRequest) (*ProbeResult, error) {
	if _, err := s.admin.RequireAdmin(actor); err != nil {
		return nil, err
	}
	p, _, err := s.readPolicy()
	if err != nil {
		return nil, err
	}
	draft, err := s.mergeDraft(p, req.OSS)
	if err != nil {
		return nil, err
	}
	return s.probe(ctx, draft)
}

func (s *Service) probe(parent context.Context, draft OSSDraft) (*ProbeResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	client := s.options.Client(draft)
	key := ".beeftv-probe/" + kernel.NewID()
	unlock, err := lockObject(ctx, "", key)
	if err != nil {
		return nil, safeError(err)
	}
	defer unlock()
	payload := make([]byte, 256)
	if _, err := rand.Read(payload); err != nil {
		return nil, storageError("probe", "无法创建连接测试数据")
	}
	// 请求发出前持久登记清理任务，中断或删除失败也能继续清理。
	job, err := s.registerCleanup(draft, "", key, "")
	if err != nil {
		return nil, err
	}
	err = client.Put(ctx, key, bytes.NewReader(payload))
	if err == nil {
		var streamBody io.ReadCloser
		stream, readErr := client.Get(ctx, key, "")
		if readErr == nil {
			streamBody = stream.Body
			got, bodyErr := io.ReadAll(io.LimitReader(streamBody, 257))
			_ = streamBody.Close()
			if bodyErr != nil {
				readErr = safeError(bodyErr)
			} else if !bytes.Equal(got, payload) {
				readErr = storageError("integrity", "OSS 测试内容校验失败")
			}
		}
		err = readErr
	}
	// 探测取消立即返回，已登记任务由清理循环执行；正常探测在同一 25 秒预算内删除。
	if ctx.Err() != nil {
		return nil, safeError(ctx.Err())
	}
	cleanupErr := s.cleanJob(ctx, job, client)
	if err != nil {
		return nil, safeError(err)
	}
	if cleanupErr != nil {
		return nil, probeCleanupError(cleanupErr)
	}
	return &ProbeResult{ElapsedMS: time.Since(started).Milliseconds(), Message: "OSS 写入、读取校验与删除测试通过"}, nil
}

func probeCleanupError(err error) error {
	public := safeError(err)
	var detail *kernel.AppError
	if errors.As(public, &detail) {
		// 保留洗敏后的真实分类，远端拒绝、网络异常与本地记录失败不能合并为删除权限不足。
		result := *detail
		result.Message = "OSS 读写验证完成，但" + detail.Message
		return &result
	}
	return public
}
