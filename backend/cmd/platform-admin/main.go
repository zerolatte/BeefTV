package main

import (
	"flag"
	"fmt"
	"golang.org/x/term"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/runtimeinfo"
	"log"
	"os"
)

func main() {
	dir := flag.String("data-dir", "", "平台专用数据目录")
	username := flag.String("username", "", "管理员登录名")
	recoverAdmin := flag.Bool("recover", false, "恢复管理员密码（需先停止服务）")
	flag.Parse()
	if *dir == "" || *username == "" {
		log.Fatal("必须指定 --data-dir 和 --username")
	}
	if err := database.PreparePlatformDirectory(*dir); err != nil {
		log.Fatal(err)
	}
	lock, err := runtimeinfo.LockWorkspace(*dir)
	if err != nil {
		log.Fatal(err)
	}
	defer lock.Close()
	db, err := database.Open(database.Config{Driver: "sqlite", DataDir: *dir})
	if err != nil {
		log.Fatal(err)
	}
	if err := database.ConfigurePool(db); err != nil {
		log.Fatal(err)
	}
	if err := database.MigratePlatformSchema(db); err != nil {
		log.Fatal(err)
	}
	fmt.Print("输入管理员密码（至少 12 个字符，不回显）：")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatal("无法读取密码，请在交互终端执行")
	}
	fmt.Print("\n再次输入密码：")
	confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil || string(password) != string(confirm) {
		log.Fatal("两次密码不一致")
	}
	auth := identity.New(repository.New(db))
	if *recoverAdmin {
		err = auth.RecoverAdmin(*username, string(password))
	} else {
		_, err = auth.InitializeAdmin(*username, string(password))
	}
	clear(password)
	clear(confirm)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("管理员已保存。请启动平台服务后登录。")
}
