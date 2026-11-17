//go:build platform && darwin

package platformimport

import "golang.org/x/sys/unix"

func renameNewDirectory(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
