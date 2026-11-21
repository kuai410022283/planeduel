package app

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PortManager 管理「独立端口对外访问」这个可选监听器，支持运行时热开关。
type PortManager struct {
	mu      sync.Mutex
	srv     *http.Server
	ln      net.Listener
	addr    string
	lastErr error
}

type PortStatus struct {
	Listening bool   `json:"listening"`
	Addr      string `json:"addr"`
	Error     string `json:"error,omitempty"`
}

// 端口被占用时的自动顺延范围与尝试次数
const (
	fallbackAttempts = 20
	fallbackLo       = 28000
	fallbackHi       = 28099
)

func (p *PortManager) Status() PortStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := PortStatus{Listening: p.ln != nil, Addr: p.addr}
	if p.lastErr != nil {
		st.Error = p.lastErr.Error()
	}
	return st
}

// IsAddrInUse 判断绑定失败是否因为端口已被占用（各平台错误文案不同）。
func IsAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"address already in use",              // Linux / macOS
		"only one usage of each socket address", // Windows
		"in use",                              // 兜底
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Apply 按配置开启/关闭端口监听；重复调用是幂等的。
func (p *PortManager) Apply(enabled bool, port int, h http.Handler) error {
	_, _, err := p.ApplyWithFallback(enabled, port, h, 0)
	return err
}

// ApplyWithFallback 开启端口；被占用时在 [fallbackLo, fallbackHi] 内从 port+1 顺延，
// 最多尝试 attempts 次。返回值 actual 为实际监听端口，switchedFrom 为原端口（0 表示没切换）。
func (p *PortManager) ApplyWithFallback(enabled bool, port int, h http.Handler, attempts int) (actual, switchedFrom int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !enabled {
		p.stopLocked()
		return port, 0, nil
	}
	if port < 1 || port > 65535 {
		p.lastErr = fmt.Errorf("端口号 %d 不合法", port)
		return port, 0, p.lastErr
	}

	addr := fmt.Sprintf(":%d", port)
	if p.ln != nil && p.addr == addr {
		return port, 0, nil // 已经在监听了
	}

	// 1) 先试用户指定的端口
	firstErr := p.tryListenLocked(port, h)
	if firstErr == nil {
		return port, 0, nil
	}
	if !IsAddrInUse(firstErr) || attempts <= 0 {
		p.lastErr = firstErr
		return port, 0, firstErr
	}

	// 2) 被占用：向后找第一个能绑上的端口
	for i := 1; i <= attempts; i++ {
		cand := port + i
		if cand < fallbackLo || cand > fallbackHi {
			continue
		}
		if err := p.tryListenLocked(cand, h); err == nil {
			return cand, port, nil
		}
	}

	p.lastErr = fmt.Errorf("端口 %d 已被占用，%d-%d 范围内也没找到可用端口", port, fallbackLo, fallbackHi)
	return port, 0, p.lastErr
}

// tryListenLocked 假定已持有锁，尝试监听指定端口并接管服务。
func (p *PortManager) tryListenLocked(port int, h http.Handler) error {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	p.stopLocked()
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	p.ln, p.srv, p.addr, p.lastErr = ln, srv, addr, nil
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.mu.Lock()
			p.lastErr = err
			p.mu.Unlock()
		}
	}()
	return nil
}

// Stop 关闭端口监听（已经在进行的连接会被断开；网关入口不受影响）。
func (p *PortManager) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *PortManager) stopLocked() {
	if p.srv != nil {
		_ = p.srv.Close()
	}
	if p.ln != nil {
		_ = p.ln.Close()
	}
	p.srv, p.ln, p.addr = nil, nil, ""
}
