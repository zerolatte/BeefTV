package runtimeinfo

import (
	"errors"
	"os"
	"sync"
)

// WorkspaceLock 统一持有工作区锁；Windows 同时持有用户发现锁和跨用户物理锁。
type WorkspaceLock struct {
	files    []*os.File
	once     sync.Once
	closeErr error
}

// Name 保留主要锁路径，便于现有诊断检查锁是否避开 AppData 影子目录。
func (l *WorkspaceLock) Name() string {
	if l == nil || len(l.files) == 0 {
		return ""
	}
	return l.files[0].Name()
}

// Close 在失败清理或正常关闭时释放全部句柄，重复关闭保持同一结果。
func (l *WorkspaceLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		for i := len(l.files) - 1; i >= 0; i-- {
			l.closeErr = errors.Join(l.closeErr, l.files[i].Close())
		}
	})
	return l.closeErr
}
