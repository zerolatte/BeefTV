//go:build !windows && !linux && !darwin

package database

import (
	"errors"
	"os"
)

func databaseFileLinkCount(_ *os.File) (uint64, error) {
	return 0, errors.New("当前系统不支持数据库文件链接校验")
}
