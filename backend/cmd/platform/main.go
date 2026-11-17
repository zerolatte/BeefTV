//go:build platform

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformbackup"
	"infinite-canvas/backend/internal/platformboot"
	"infinite-canvas/backend/internal/platformimport"

	"golang.org/x/term"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("platform", flag.ContinueOnError)
	dataDir := flags.String("data-dir", os.Getenv("CANVAS_PLATFORM_DATA_DIR"), "独立的平台数据目录（必填）")
	origin := flags.String("origin", os.Getenv("CANVAS_PLATFORM_ORIGIN"), "外部 HTTPS Origin（必填）")
	addr := flags.String("listen", "127.0.0.1:8080", "API 监听地址")
	insecure := flags.Bool("insecure-loopback", false, "允许本机 HTTP 开发来源")
	bootstrap := flags.Bool("bootstrap-admin", false, "一次性初始化管理员，完成后退出")
	username := flags.String("username", "", "初始化管理员用户名")
	displayName := flags.String("display-name", "", "初始化管理员显示名")
	points := flags.String("initial-points", "", "必填初始整数点数，允许 0")
	storage := flags.String("storage-limit-bytes", "", "必填存储上限字节数")
	backupTo := flags.String("backup-to", "", "停写后备份到全新目录，完成后退出")
	restoreFrom := flags.String("restore-from", "", "校验并恢复备份到全新 data-dir，完成后退出")
	verifyBackup := flags.String("verify-backup", "", "校验完整备份，完成后退出")
	importLocal := flags.String("import-local-from", "", "将受支持的本地工作区隔离导入全新 data-dir，完成后退出")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("存在未知命令参数")
	}
	maintenance := 0
	for _, value := range []bool{*bootstrap, *backupTo != "", *restoreFrom != "", *verifyBackup != "", *importLocal != ""} {
		if value {
			maintenance++
		}
	}
	if maintenance > 1 {
		return errors.New("初始化、备份、校验、恢复和导入命令不能组合使用")
	}
	if *backupTo != "" {
		if _, err := platformbackup.Create(ctx, *dataDir, *backupTo); err != nil {
			return err
		}
		fmt.Println("平台完整备份已创建并校验。")
		return nil
	}
	if *restoreFrom != "" {
		if err := platformbackup.Restore(ctx, *restoreFrom, *dataDir); err != nil {
			return err
		}
		fmt.Println("平台备份已恢复到全新目录。请使用与备份匹配的程序版本启动。")
		return nil
	}
	if *verifyBackup != "" {
		if _, err := platformbackup.Verify(ctx, *verifyBackup); err != nil {
			return err
		}
		fmt.Println("平台备份完整性校验通过。")
		return nil
	}
	var input platformauth.CreateAccountInput
	if *bootstrap || *importLocal != "" {
		initial, err := strconv.ParseInt(*points, 10, 64)
		if err != nil {
			return errors.New("初始化必须填写有效 initial-points")
		}
		limit, err := strconv.ParseInt(*storage, 10, 64)
		if err != nil {
			return errors.New("初始化必须填写有效 storage-limit-bytes")
		}
		input = platformauth.CreateAccountInput{Username: *username, DisplayName: *displayName, InitialPoints: &initial, StorageLimitBytes: &limit}
	}
	if *importLocal != "" {
		password, err := readPassword()
		if err != nil {
			return err
		}
		input.Password = password
		result, err := platformimport.Import(ctx, platformimport.Input{Source: *importLocal, Destination: *dataDir, Account: input})
		if err != nil {
			return err
		}
		if result.Repeated {
			fmt.Println("该来源已完成导入，返回既有工作区；未重复开户或发放额度。")
		} else {
			fmt.Println("本地工作区已隔离导入。历史任务不会恢复执行；请配置平台渠道与模型授权后使用。")
		}
		return nil
	}
	runtime, err := platformboot.Open(ctx, platformboot.Config{DataDir: *dataDir, Origin: *origin, InsecureLoopback: *insecure})
	if err != nil {
		return err
	}
	defer runtime.Close()
	if runtime.MigrationBackup != "" {
		log.Printf("迁移前完整备份已保存：%s", runtime.MigrationBackup)
	}
	if *bootstrap {
		password, err := readPassword()
		if err != nil {
			return err
		}
		input.Password = password
		if _, err := runtime.Auth.Bootstrap(ctx, input); err != nil {
			return err
		}
		fmt.Println("管理员初始化完成，可以登录平台。")
		return nil
	}
	server := &http.Server{Addr: *addr, Handler: runtime.Handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.ListenAndServe() }()
	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("初始化密码必须在交互终端中隐藏输入，不接受管道或命令行明文")
	}
	fmt.Fprint(os.Stderr, "管理员密码：")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	defer clear(first)
	fmt.Fprint(os.Stderr, "再次输入密码：")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	defer clear(second)
	if string(first) != string(second) {
		return "", errors.New("两次密码不一致")
	}
	return string(first), nil
}
