package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"planeduel/internal/app"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := flag.String("data", envOr("PD_DATA", "./data"), "数据目录（存 config.json / 数据库）")
	prefix := flag.String("gateway-prefix", envOr("PD_GATEWAY_PREFIX", "/app/planeduel"), "飞牛统一网关路径前缀")
	adminAddr := flag.String("admin-listen", envOr("PD_ADMIN_LISTEN", "127.0.0.1:8081"), "本机管理入口（仅回环地址，留空则关闭）")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	cfgStore, err := app.LoadConfig(filepath.Join(*dataDir, "config.json"))
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	cfg := cfgStore.Get()

	sessions := app.NewSessionStore(time.Duration(cfg.NickTTLDays) * 24 * time.Hour)
	solo := app.NewSoloStore(time.Now().UnixNano())
	hub := app.NewHub(sessions, time.Now().UnixNano())
	ports := &app.PortManager{}
	srv := app.NewServer(cfgStore, sessions, solo, hub, ports, *prefix)
	// 每个入口绑定一个「通道」，服务端据此决定是否给管理能力：
	// 端口通道永远没有管理权限，网关通道需飞牛管理员，回环管理口用于恢复。
	adminHandler := srv.Channeled("admin")
	portHandler := srv.PortHandler()
	gatewayHandler := srv.Channeled("gateway")

	// 入口 0：本机管理入口（仅回环）。端口关闭时也能进设置面板，网关故障时还能救急。
	var closers []net.Listener
	if *adminAddr != "" {
		ln, err := net.Listen("tcp", *adminAddr)
		if err != nil {
			log.Printf("[管理] 监听 %s 失败: %v", *adminAddr, err)
		} else {
			closers = append(closers, ln)
			go func() {
				if err := http.Serve(ln, adminHandler); err != nil {
					log.Printf("[管理] 服务退出: %v", err)
				}
			}()
			log.Printf("[管理] 本机入口 http://%s（仅回环地址）", *adminAddr)
		}
	}

	// 入口 1：独立端口（受设置面板开关控制，默认关闭）
	if err := ports.Apply(cfg.PublicPortEnabled, cfg.PublicPort, portHandler); err != nil {
		log.Printf("[端口] 未开启: %v", err)
	} else if cfg.PublicPortEnabled {
		log.Printf("[端口] 已监听 :%d（对外可直连）", cfg.PublicPort)
	} else {
		log.Printf("[端口] 对外访问已关闭（仅飞牛网关可用）")
	}

	// 入口 2：飞牛统一网关（仅当部署在 fnOS 上时存在 TRIM_APPDEST）
	var unixLn net.Listener
	if dest := os.Getenv("TRIM_APPDEST"); dest != "" {
		sock := filepath.Join(dest, "app.sock")
		_ = os.Remove(sock)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			log.Printf("[网关] 监听 %s 失败: %v", sock, err)
		} else {
			if err := os.Chmod(sock, 0o660); err != nil {
				log.Printf("[网关] 设置 socket 权限失败: %v", err)
			}
			unixLn = ln
			go func() {
				if err := http.Serve(ln, gatewayHandler); err != nil {
					log.Printf("[网关] 服务退出: %v", err)
				}
			}()
			log.Printf("[网关] 已监听 %s（前缀 %s）", sock, *prefix)
		}
	} else {
		log.Printf("[网关] 未检测到 TRIM_APPDEST，跳过网关入口（本地开发模式）")
	}

	// 每秒检查：邀请过期、回合超时判负
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			hub.Tick()
		}
	}()

	// 定时清理
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			if n := solo.GC(2 * time.Hour); n > 0 {
				log.Printf("[GC] 清理了 %d 局过期对局", n)
			}
			sessions.Count() // 顺带触发昵称过期回收
			// 管理员昵称的空闲回收：关掉的浏览器不该一直占着「管理员 / 管理员2…」
			if n := srv.ReapIdleAdminSessions(10 * time.Minute); n > 0 {
				log.Printf("[GC] 释放了 %d 个空闲管理员昵称", n)
			}
		}
	}()

	log.Printf("管理权限：仅飞牛网关入口（无口令）")
	if cfg.PublicPortEnabled {
		log.Printf("直连地址: http://<NAS-IP>:%d", cfg.PublicPort)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Printf("收到退出信号，正在关闭…")
	ports.Stop()
	for _, ln := range closers {
		_ = ln.Close()
	}
	if unixLn != nil {
		_ = unixLn.Close()
	}
}
