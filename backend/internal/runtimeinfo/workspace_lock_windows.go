package runtimeinfo

import (
	"errors"
	"fmt"
	"infinite-canvas/backend/internal/desktopstorage"
	"os"
	"strings"
)

func lockWorkspaceFile(dataDir string) (*WorkspaceLock, error) {
	path, err := Path(dataDir)
	if err != nil {
		return nil, err
	}
	userLock, err := desktopstorage.LockFile(strings.TrimSuffix(path, ".json") + ".lock")
	if err != nil {
		return nil, err
	}
	// 用户锁覆盖 MSIX 影子路径；目录旁的物理锁让服务与维护命令跨系统账户互斥。
	physicalLock, err := desktopstorage.Lock(dataDir)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("取得工作区物理锁失败：%w", err), userLock.Close())
	}
	return &WorkspaceLock{files: []*os.File{userLock, physicalLock}}, nil
}
