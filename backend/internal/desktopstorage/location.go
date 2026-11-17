package desktopstorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const locationFile = "storage-location.json"

type Location struct {
	DataDir       string            `json:"dataDir"`
	PreviousDir   string            `json:"previousDir,omitempty"`
	PreviousFiles map[string]string `json:"previousFiles,omitempty"`
}

func DefaultRoot() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "BeefTV"), nil
}

// The pointer stays in the system profile, independent of the selected data volume.
// A missing selected volume must never silently create an empty workspace.
func Resolve(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, locationFile))
	if errors.Is(err, os.ErrNotExist) {
		return root, nil
	}
	if err != nil {
		return "", fmt.Errorf("读取存储目录设置失败: %w", err)
	}
	var location Location
	if err := json.Unmarshal(body, &location); err != nil {
		return "", fmt.Errorf("存储目录设置损坏: %w", err)
	}
	if !filepath.IsAbs(location.DataDir) {
		return "", errors.New("存储目录设置无效")
	}
	if info, err := os.Stat(location.DataDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("存储目录无法访问，请连接对应磁盘后重试：%s", location.DataDir)
	}
	return location.DataDir, nil
}

func Save(root, dataDir, previousDir string) error {
	return saveLocation(root, Location{DataDir: dataDir, PreviousDir: previousDir})
}

func SaveMigration(root, dataDir, previousDir string, files map[string]string) error {
	return saveLocation(root, Location{DataDir: dataDir, PreviousDir: previousDir, PreviousFiles: files})
}

func saveLocation(root string, location Location) error {
	body, err := json.Marshal(location)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".storage-location-*")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(path, filepath.Join(root, locationFile)); err != nil {
		return err
	}
	return nil
}

// CanonicalDirectory 统一目录别名、Windows 短路径和尚未创建的子目录身份。
func CanonicalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) || filepath.Dir(filepath.Clean(path)) == filepath.Clean(path) {
		return "", errors.New("请选择磁盘中的文件夹，例如 D:\\BeefTV")
	}
	path = filepath.Clean(path)
	missing := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// 只有路径项本身不存在才允许补齐；已有悬空链接不能当作普通的新目录。
		if _, statErr := os.Lstat(path); statErr == nil {
			return "", fmt.Errorf("已有路径无法解析，请检查悬空链接：%w", err)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}

func contains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
