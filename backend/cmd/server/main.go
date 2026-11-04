package main

import (
	"compose-manager/backend/internal/auth"
	"compose-manager/backend/internal/command"
	"compose-manager/backend/internal/config"
	"compose-manager/backend/internal/database"
	"compose-manager/backend/internal/deploy"
	"compose-manager/backend/internal/docker"
	"compose-manager/backend/internal/httpapi"
	"compose-manager/backend/internal/notify"
	"compose-manager/backend/internal/proxy"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	configPath := flag.String("config", "", "YAML configuration file")
	initAdmin := flag.Bool("init-admin", false, "create first administrator with temporary password 123456")
	username := flag.String("username", "admin", "initial admin username")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return err
	}
	db, err := database.Open(filepath.Join(cfg.DataDir, "manager.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	// Hold a process lock so two schedulers cannot execute against one database.
	lock, err := os.OpenFile(filepath.Join(cfg.DataDir, "manager.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("数据目录正由另一个 Compose Manager 进程使用")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	authentication, err := auth.New(db, cfg.DataDir, cfg.SessionHours)
	if err != nil {
		return err
	}
	bootstrapName := "admin"
	if *initAdmin {
		bootstrapName = *username
	}
	created, err := authentication.InitDefault(bootstrapName)
	if err != nil {
		return err
	}
	if created {
		log.Printf("首次管理员 %s 已创建；使用初始密码登录后必须修改密码", bootstrapName)
	}
	if *initAdmin {
		if created {
			fmt.Println("管理员已创建，初始密码为 123456，首次登录必须修改密码。")
		} else {
			fmt.Println("管理员已存在，保留现有账户与密码。")
		}
		return nil
	}
	gin.SetMode(gin.ReleaseMode)
	d := &docker.Client{Runner: command.OSRunner{}, Roots: cfg.AllowedRoots}
	n := &notify.Notifier{DB: db, Auth: authentication}
	engine := deploy.New(db, d, cfg, n)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	if err = engine.Start(workerCtx); err != nil {
		return err
	}
	proxyManager := proxy.New(db, authentication, command.OSRunner{}, cfg.DataDir)
	if err = proxyManager.Start(); err != nil {
		return err
	}
	defer proxyManager.Close()
	api := &httpapi.Server{Proxy: proxyManager, DB: db, Auth: authentication, Docker: d, Engine: engine, Notify: n, Config: cfg}
	server := &http.Server{Addr: cfg.Listen, Handler: api.Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	failure := make(chan error, 1)
	go func() {
		log.Printf("Compose Manager listening on http://%s", cfg.Listen)
		failure <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err = <-failure:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancelWorkers()
	shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
		_ = server.Close()
	}
	engine.Wait()
	return err
}
