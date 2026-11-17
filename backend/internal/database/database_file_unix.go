//go:build linux || darwin

package database

import (
	"os"

	"golang.org/x/sys/unix"
)

func databaseFileLinkCount(file *os.File) (uint64, error) {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return 0, err
	}
	return uint64(info.Nlink), nil
}
