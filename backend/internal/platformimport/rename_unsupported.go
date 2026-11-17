//go:build platform && !windows && !linux && !darwin

package platformimport

import "errors"

func renameNewDirectory(source, target string) error {
	return errors.New("当前系统没有经过验证的目录排他发布实现")
}
