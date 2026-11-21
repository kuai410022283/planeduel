package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 环境变量覆盖：容器/桌面版靠它把对外端口打开（配置文件默认是关的）。
func TestConfigEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	t.Setenv("PD_PUBLIC_PORT", "29090")
	t.Setenv("PD_PUBLIC_PORT_ENABLED", "1")
	t.Setenv("PD_NICK_TTL_DAYS", "7")

	store, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	if !cfg.PublicPortEnabled {
		t.Fatal("PD_PUBLIC_PORT_ENABLED=1 应开启对外端口")
	}
	if cfg.PublicPort != 29090 {
		t.Fatalf("端口应为 29090，实际 %d", cfg.PublicPort)
	}
	if cfg.NickTTLDays != 7 {
		t.Fatalf("昵称 TTL 应为 7 天，实际 %d", cfg.NickTTLDays)
	}

	// 覆盖结果要落盘，重启后依然是这个值
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "29090") || !strings.Contains(string(raw), "public_port_enabled\":true") &&
		!strings.Contains(string(raw), "public_port_enabled\": true") {
		t.Fatalf("配置文件应写入覆盖后的值：%s", raw)
	}
}

// 非法/未设置的环境变量不能破坏配置。
func TestConfigEnvIgnoresBadValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	t.Setenv("PD_PUBLIC_PORT", "not-a-port")
	t.Setenv("PD_PUBLIC_PORT_ENABLED", "")
	t.Setenv("PD_NICK_TTL_DAYS", "-3")

	store, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	if cfg.PublicPort != 28082 {
		t.Fatalf("非法端口应回落到默认 28082，实际 %d", cfg.PublicPort)
	}
	if cfg.PublicPortEnabled {
		t.Fatal("默认应关闭对外端口（飞牛上的安全默认）")
	}
	if cfg.NickTTLDays != 30 {
		t.Fatalf("非法 TTL 应回落 30，实际 %d", cfg.NickTTLDays)
	}
}
