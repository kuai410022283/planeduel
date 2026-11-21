package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Config 是应用级设置，落盘在数据目录的 config.json。
type Config struct {
	PublicPortEnabled bool   `json:"public_port_enabled"` // 独立端口对外访问开关
	PublicPort        int    `json:"public_port"`

	NickTTLDays       int    `json:"nick_ttl_days"` // 游客昵称保留天数
}

func DefaultConfig() Config {
	return Config{PublicPortEnabled: false, PublicPort: 28082, NickTTLDays: 30}
}

type ConfigStore struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func LoadConfig(path string) (*ConfigStore, error) {
	s := &ConfigStore{path: path, cfg: DefaultConfig()}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, fmt.Errorf("配置文件 %s 解析失败: %w", path, err)
		}
	}
	changed := false
	// 0.9.6 起不再有管理口令：旧配置里残留的字段顺手清掉
	if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), "admin_token") {
		changed = true
	}
	if s.cfg.PublicPort == 0 {
		s.cfg.PublicPort = 28082
		changed = true
	}
	if s.cfg.NickTTLDays <= 0 {
		s.cfg.NickTTLDays = 30
		changed = true
	}
	// 环境变量覆盖（容器/桌面版部署用；优先级高于 config.json，每次启动生效）
	//   PD_PUBLIC_PORT           对外端口号
	//   PD_PUBLIC_PORT_ENABLED   是否开启对外端口（1/true/yes/on）
	//   PD_NICK_TTL_DAYS         游客昵称兜底 TTL（天）
	if v := strings.TrimSpace(os.Getenv("PD_PUBLIC_PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 && s.cfg.PublicPort != p {
			s.cfg.PublicPort = p
			changed = true
		}
	}
	if v := strings.TrimSpace(os.Getenv("PD_PUBLIC_PORT_ENABLED")); v != "" {
		on := v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
		if s.cfg.PublicPortEnabled != on {
			s.cfg.PublicPortEnabled = on
			changed = true
		}
	}
	if v := strings.TrimSpace(os.Getenv("PD_NICK_TTL_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && s.cfg.NickTTLDays != n {
			s.cfg.NickTTLDays = n
			changed = true
		}
	}

	if changed {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *ConfigStore) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Update 在锁内修改配置并原子落盘（先写临时文件再 rename）。
func (s *ConfigStore) Update(fn func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	if err := fn(&next); err != nil {
		return s.cfg, err
	}
	s.cfg = next
	if err := s.save(); err != nil {
		return s.cfg, err
	}
	return s.cfg, nil
}

func (s *ConfigStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// RandomToken 生成 32 字节随机串的 base64url 形式。
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
