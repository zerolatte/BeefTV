//go:build platform && windows

package platformimport

import "golang.org/x/sys/windows"

func renameNewDirectory(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
