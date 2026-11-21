package app

import (
	"errors"
	"fmt"
	"net"
	"net/http"
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

func (p *PortManager) Status() PortStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := PortStatus{Listening: p.ln != nil, Addr: p.addr}
	if p.lastErr != nil {
		st.Error = p.lastErr.Error()
	}
	return st
}

// Apply 按配置开启/关闭端口监听；重复调用是幂等的。
func (p *PortManager) Apply(enabled bool, port int, h http.Handler) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !enabled {
		p.stopLocked()
		return nil
	}
	if port < 1 || port > 65535 {
		p.lastErr = fmt.Errorf("端口号 %d 不合法", port)
		return p.lastErr
	}
	addr := fmt.Sprintf(":%d", port)
	if p.ln != nil && p.addr == addr {
		return nil // 已经在监听了
	}
	p.stopLocked()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		p.lastErr = fmt.Errorf("监听 %s 失败: %w", addr, err)
		return p.lastErr
	}
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
