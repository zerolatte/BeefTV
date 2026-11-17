//go:build platform

package platformbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"infinite-canvas/backend/internal/runtimeinfo"
)

func existingDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("目录不能为空")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("目标不是目录")
	}
	return path, nil
}

func contained(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func newDestination(source, destination string) (string, error) {
	destination, err := newDestinationPath(source, destination)
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return "", fmt.Errorf("目标目录必须尚不存在：%w", err)
	}
	return destination, nil
}

// 先规范化尚不存在的目标路径，恢复流程才能在创建目录之前取得同一个工作区锁。
func newDestinationPath(source, destination string) (string, error) {
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("必须指定全新的目标目录")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	parent, err := existingDirectory(filepath.Dir(destination))
	if err != nil {
		return "", err
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if contained(source, destination) || contained(destination, source) {
		return "", errors.New("源目录与目标目录不能相同或互相包含")
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", errors.New("目标目录必须尚不存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return destination, nil
}

func ignored(name string) bool {
	return name == runtimeinfo.FileName || name == strings.TrimSuffix(runtimeinfo.FileName, ".json")+".lock"
}

func copyTree(ctx context.Context, source, destination string, skipDatabase bool) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if ignored(rel) || skipDatabase && (rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" || rel == databaseName+"-journal") {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("备份不允许符号链接或目录联接")
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("备份只接受普通文件和目录")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, contextReader{ctx, file})
		syncErr := out.Sync()
		return errors.Join(copyErr, syncErr, out.Close())
	})
}

func inventory(ctx context.Context, root string) ([]File, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("备份数据目录缺失或不是普通目录")
	}
	var files []File
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || ignored(rel) {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("备份数据含符号链接或目录联接")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("备份数据含非普通文件")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		hash := sha256.New()
		size, err := io.Copy(hash, contextReader{ctx, file})
		if err != nil {
			return err
		}
		files = append(files, File{Path: filepath.ToSlash(rel), Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, err
}

func sameFiles(a, b []File) bool { return slices.Equal(a, b) }

func writeSynced(path string, value []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(value)
	syncErr := file.Sync()
	return errors.Join(writeErr, syncErr, file.Close())
}

func readJSON(path string, output any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("备份清单不是有效的普通文件")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(value, output)
}

type contextReader struct {
	ctx context.Context
	in  io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(data)
}
