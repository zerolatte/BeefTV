package asset

import (
	"context"
	"time"

	"infinite-canvas/backend/internal/model"
)

// RunObjectMaintenance 清理提交成功后因退出或磁盘错误遗留的 OSS 本地暂存，不触碰未就绪资源。
func (s *Service) RunObjectMaintenance(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.releaseCommittedStages(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) releaseCommittedStages(ctx context.Context) {
	if s.objects == nil || s.blobs == nil {
		return
	}
	after := ""
	for ctx.Err() == nil {
		rows, err := s.repo.ReadyRemoteResourcePage(ctx, after, 100)
		if err != nil || len(rows) == 0 {
			return
		}
		for i := range rows {
			row := &rows[i]
			unlock := s.lockWrite(row.UserID, row.UploadKey, row.ID)
			latest, err := s.ownedResource(row.UserID, row.ID)
			if err == nil && latest.Status == model.ResourceStatusReady && latest.Provider == "oss" && s.blobs.Exists(latest.ObjectKey) == nil {
				// 修复历史资源时，元数据提交可能失败并保留旧的就绪记录。
				// 必须确认远端仍有文件，才能清除这份可能是唯一可恢复内容的暂存。
				budget, cancel := context.WithTimeout(ctx, 25*time.Second)
				present, lookupErr := s.objects.Exists(budget, latest)
				cancel()
				if lookupErr == nil && present {
					s.releaseStage(latest)
				}
			}
			unlock()
		}
		after = rows[len(rows)-1].ID
	}
}
