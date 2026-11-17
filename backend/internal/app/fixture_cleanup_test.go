package app

import (
	"context"
	"testing"
	"time"
)

// 服务清理后注册，确保后台写入停止后才执行之前注册的数据库清理。
func registerTestServiceCleanup(t *testing.T, service *Service) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.StopWorker(ctx); err != nil {
			t.Errorf("停止测试后台任务失败: %v", err)
		}
		if err := service.Close(); err != nil {
			t.Errorf("关闭测试服务失败: %v", err)
		}
	})
}
