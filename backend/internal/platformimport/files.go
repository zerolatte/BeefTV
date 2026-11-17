//go:build platform

package platformimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/runtimeinfo"
)

type fileRecord struct {
	Path   string
	Size   int64
	Digest string
}

func importPaths(source, destination string) (string, string, error) {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(destination) == "" {
		return "", "", errors.New("必须填写源目录和全新目标目录")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(source); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", errors.New("源根目录不得为符号链接或目录联接")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", "", err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return "", "", err
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if contains(source, destination) || contains(destination, source) {
		return "", "", errors.New("源目录与目标目录不能相同或互相包含")
	}
	if info, err := os.Lstat(source); err != nil || !info.IsDir() {
		return "", "", errors.New("源不是有效目录")
	}
	return source, destination, nil
}

func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func openReadOnly(path string) (*gorm.DB, func(), error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("导入数据库缺失或不是普通文件")
	}
	if err := database.ValidateDatabaseFile(path); err != nil {
		return nil, nil, err
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if sidecar, err := os.Lstat(path + suffix); err == nil {
			if !sidecar.Mode().IsRegular() || sidecar.Size() != 0 {
				return nil, nil, errors.New("数据库仍有未合并日志，请先用原程序恢复并正常关闭")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path}
	// 不创建或更新源 WAL/SHM；非空日志已在上方拒绝，避免 immutable 忽略未合并提交。
	uri.RawQuery = url.Values{"mode": {"ro"}, "immutable": {"1"}, "_busy_timeout": {"5000"}}.Encode()
	db, err := database.Open(database.Config{DSN: uri.String()})
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() { _ = sqlDB.Close() }
	if err := database.ConfigurePool(db); err != nil {
		closeDB()
		return nil, nil, err
	}
	return db, closeDB, nil
}

func ignoredFile(name string) bool {
	return name == runtimeinfo.FileName || name == strings.TrimSuffix(runtimeinfo.FileName, ".json")+".lock" || name == databaseName+"-shm"
}

func skippedDirectory(name string) bool {
	switch name {
	case "cache", "tmp", "backups", "diagnostics", "support-traces", "models", "runtimes", "downloads", "skill-market":
		return true
	default:
		return false
	}
}

func acceptedRootFile(name string) bool {
	switch name {
	case databaseName, databaseName + "-wal", databaseName + "-journal", database.RuntimeModeFileName, ".settings-key", ".storage-v2-migrated", "local-model-config.json", "local-model-config.json.bak", "reference-storage.json", "beeftv-connection.json", "agent_owner_token", "agent_host_token", "agent_clients.json", "agent_config.json":
		return true
	}
	return false
}

func sourceInventory(ctx context.Context, root string) ([]fileRecord, string, error) {
	var files []fileRecord
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || ignoredFile(rel) {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("导入不接受符号链接、目录联接或特殊文件")
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if entry.IsDir() {
			if len(parts) == 1 && skippedDirectory(rel) {
				return filepath.SkipDir
			}
			if parts[0] != "resources" && parts[0] != "playback" {
				entries, readErr := os.ReadDir(path)
				if readErr != nil {
					return readErr
				}
				if len(entries) != 0 {
					return fmt.Errorf("本阶段不支持非空目录 %s，未复制或删除源数据", parts[0])
				}
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("导入只接受普通文件和目录")
		}
		if len(parts) == 1 && !acceptedRootFile(rel) {
			return fmt.Errorf("本阶段不识别源文件 %s，未导入", rel)
		}
		digest, err := fileDigest(ctx, path)
		if err != nil {
			return err
		}
		files = append(files, fileRecord{Path: filepath.ToSlash(rel), Size: info.Size(), Digest: digest})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	slices.SortFunc(files, func(a, b fileRecord) int { return strings.Compare(a.Path, b.Path) })
	raw, err := json.Marshal(files)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	return files, hex.EncodeToString(digest[:]), nil
}

func copyBusinessFiles(ctx context.Context, source, stage string, files []fileRecord) error {
	for _, item := range files {
		if !strings.HasPrefix(item.Path, "resources/") && !strings.HasPrefix(item.Path, "playback/") {
			continue
		}
		destination := filepath.Join(stage, filepath.FromSlash(item.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err := copyFile(ctx, filepath.Join(source, filepath.FromSlash(item.Path)), destination); err != nil {
			return err
		}
		digest, err := fileDigest(ctx, destination)
		if err != nil || digest != item.Digest {
			return errors.New("业务文件复制期间发生变化")
		}
	}
	return nil
}

func copyFile(ctx context.Context, source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, contextReader{ctx, in})
	return errors.Join(copyErr, out.Sync(), out.Close())
}

func fileDigest(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, contextReader{ctx, file})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func writeFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	return errors.Join(err, file.Sync(), file.Close())
}

func readJSON(path string, output any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("导入配置缺失或不是有效普通文件")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := strictJSON(string(raw)); err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}

func validateLocalConfigFiles(source string) error {
	return collectLocalConfigSecrets(source, &secretCleaner{source: source})
}

func collectLocalConfigSecrets(source string, cleaner *secretCleaner) error {
	for _, entry := range []struct{ name, field string }{{"local-model-config.json", "encryptedConfig"}, {"local-model-config.json.bak", "encryptedConfig"}, {"reference-storage.json", "encryptedConfig"}, {"beeftv-connection.json", "encryptedApiKey"}} {
		path := filepath.Join(source, entry.name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		var document map[string]json.RawMessage
		if err := readJSON(path, &document); err != nil {
			return err
		}
		var cipher string
		if raw := document[entry.field]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &cipher); err != nil {
				return fmt.Errorf("%s 凭据格式无效", entry.name)
			}
		}
		if cipher != "" {
			plain, err := localcrypto.Decrypt(source, cipher)
			if err != nil {
				return fmt.Errorf("%s 无法由源密钥解密", entry.name)
			}
			if entry.field == "encryptedApiKey" {
				if err := cleaner.credential(plain); err != nil {
					return err
				}
			} else if _, err := cleaner.cleanJSON(plain); err != nil {
				return fmt.Errorf("%s 不能完成凭据审计：%w", entry.name, err)
			}
		} else {
			encoded, err := json.Marshal(document)
			if err != nil {
				return err
			}
			if _, err := cleaner.cleanJSON(string(encoded)); err != nil {
				return fmt.Errorf("%s 不能完成旧配置凭据审计：%w", entry.name, err)
			}
		}
	}
	return nil
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
