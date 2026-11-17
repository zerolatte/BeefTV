package database

import (
	"errors"
	"fmt"
	"os"
)

// ValidateDatabaseFile 在 SQLite 打开前拒绝硬链接主文件，防止不同目录锁保护同一物理数据库。
// 未创建的路径交给初始化；现存文件通过只读句柄检查，不修改数据库或日志。
func ValidateDatabaseFile(path string) error {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查数据库文件失败：%w", err)
	}
	if !before.Mode().IsRegular() {
		return errors.New("数据库必须是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开数据库文件进行校验失败：%w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("读取数据库文件属性失败：%w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return errors.New("数据库文件在校验期间发生变化")
	}
	links, err := databaseFileLinkCount(file)
	if err != nil {
		return fmt.Errorf("读取数据库文件链接数失败：%w", err)
	}
	if links > 1 {
		return errors.New("数据库主文件存在硬链接，请停止相关实例并使用独立数据库副本")
	}
	if links != 1 {
		return errors.New("数据库文件链接状态无效")
	}
	return nil
}
