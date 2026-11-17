package database

import (
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"infinite-canvas/backend/internal/desktopstorage"
	"infinite-canvas/backend/internal/identity"
)

// ValidateDatabaseDSN 在 SQLite 打开文件前核验归属，防止锁住目录 A 却写入目录 B。
func (d *RuntimeDirectory) ValidateDatabaseDSN(dsn string) error {
	path, err := runtimeDSNPath(dsn, d.path)
	if err != nil {
		return err
	}
	return d.validateDatabasePath(path)
}

func (d *RuntimeDirectory) validateDatabasePath(path string) error {
	if path == "" {
		if d.mode == identity.ModePlatform {
			return errors.New("平台必须使用数据目录内的持久数据库")
		}
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	canonical, err := desktopstorage.CanonicalDirectory(abs)
	if err != nil {
		return err
	}
	parent := filepath.Dir(canonical)
	same := parent == d.path
	if runtime.GOOS == "windows" {
		same = strings.EqualFold(parent, d.path)
	}
	if !same {
		return errors.New("数据库必须直接位于当前数据目录内，不能通过 DATABASE_URL 绕过工作区锁")
	}
	return ValidateDatabaseFile(canonical)
}

func runtimeDSNPath(dsn, dataDir string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return filepath.Join(dataDir, "open_ai_canvas.db"), nil
	}
	path, query, _ := strings.Cut(dsn, "?")
	options, err := url.ParseQuery(query)
	if err != nil || len(options["mode"]) > 1 {
		return "", errors.New("数据库地址参数无效")
	}
	if strings.HasPrefix(path, "file:") {
		parsed, err := url.Parse(dsn)
		if err != nil || parsed.User != nil || (parsed.Host != "" && parsed.Host != "localhost") || parsed.Fragment != "" {
			return "", errors.New("数据库文件地址无效")
		}
		path = parsed.Path
		if parsed.Opaque != "" {
			path, err = url.PathUnescape(parsed.Opaque)
			if err != nil {
				return "", errors.New("数据库文件地址无效")
			}
		}
		if options.Get("mode") == "memory" {
			return "", nil
		}
		if runtime.GOOS == "windows" && len(path) > 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
	}
	if path == ":memory:" {
		return "", nil
	}
	if path == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("数据库文件地址无效")
	}
	return path, nil
}
