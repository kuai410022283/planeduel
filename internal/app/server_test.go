package app

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	sessions := NewSessionStore(24 * time.Hour)
	return NewServer(cfg, sessions, NewSoloStore(1), NewHub(sessions, 1), &PortManager{}, "")
}

// 端口通道（对外）必须彻底拿不到管理能力，即使知道管理口令也不行。
func TestAdminBlockedOnPortChannel(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("port")

	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"public_port":1234}`))
	// 管理权限只看入口，不需要口令
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("端口通道应被拒绝，实际 %d：%s", rec.Code, rec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req2.Header.Set("X-Trim-Isadmin", "true") // 伪造管理员头也不行
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("端口通道伪造管理员头应被拒绝，实际 %d", rec2.Code)
	}
}

// 网关通道 + 飞牛管理员身份：可以读写设置。
func TestAdminAllowedOnGateway(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("网关管理员应可读设置，实际 %d：%s", rec.Code, rec.Body.String())
	}

	// 网关入口本身就是管理员：不带任何头也应放行
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("网关通道应默认具备管理权限，实际 %d", rec2.Code)
	}
}

// 回归测试：通过面板热开端口后，新监听器必须仍然带着「端口通道」标记。
// （曾经的 bug：handlePutSettings 用裸 Handler 重建监听器，导致外网端口获得管理权限）
func TestHotToggleKeepsPortChannel(t *testing.T) {
	s := newTestServer(t)

	// 找一个空闲端口
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	admin := s.Channeled("admin")
	body := fmt.Sprintf(`{"public_port_enabled":true,"public_port":%d}`, port)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
	// 管理权限只看入口，不需要口令
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("开启端口失败 %d：%s", rec.Code, rec.Body.String())
	}
	defer s.ports.Stop()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	resp, err := http.Get(base + "/api/session")
	if err != nil {
		t.Fatalf("访问端口失败: %v", err)
	}
	sessBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(sessBody), `"channel":"port"`) {
		t.Fatalf("端口通道标记丢失：%s", sessBody)
	}

	// 端口通道上的管理接口必须 403（即使带上正确的口令）
	req2, _ := http.NewRequest(http.MethodPut, base+"/api/admin/settings", strings.NewReader(`{"public_port":1}`))
	// 管理权限只看入口，不需要口令
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("热开端口后管理接口未拦截，状态 %d", resp2.StatusCode)
	}
}
// 从飞牛网关进来：昵称默认取 NAS 用户名，且自动登录、自动下发 Cookie。
func TestGatewayAutoSession(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `"nick":"`+AdminNick+`"`) || !strings.Contains(body, `"authenticated":true`) {
		t.Fatalf("网关昵称应固定为「管理员」：%s", body)
	}
	if !strings.Contains(body, `"can_admin":true`) {
		t.Fatalf("管理员进入应拿到管理权限：%s", body)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("应下发身份 Cookie")
	}

	// 同一 NAS 用户再次访问（没带 Cookie）应复用同一身份，而不是变成 laok-2
	// 带上 Cookie 再访问：复用同一身份
	req2 := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, ck := range rec.Result().Cookies() {
		req2.AddCookie(ck)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), `"nick":"`+AdminNick+`"`) {
		t.Fatalf("同一浏览器应复用「管理员」：%s", rec2.Body.String())
	}
	if n := s.sessions.Count(); n != 1 {
		t.Fatalf("同一 NAS 用户应只占一个会话，实际 %d", n)
	}
}

// 端口通道（外网）绝不能因为伪造 X-Trim-Username 就自动登录成管理员。
func TestPortChannelIgnoresFnosHeaders(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("port")

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.Header.Set("X-Trim-Username", "laok")
	req.Header.Set("X-Trim-Isadmin", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `"authenticated":true`) {
		t.Fatalf("端口通道不应自动登录：%s", body)
	}
	if n := s.sessions.Count(); n != 0 {
		t.Fatalf("端口通道不应创建会话，实际 %d", n)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("端口通道不应下发身份 Cookie")
	}
}



func TestSessionReportsChannel(t *testing.T) {
	s := newTestServer(t)
	for _, ch := range []string{"gateway", "port", "admin"} {
		h := s.Channeled(ch)
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, `"channel":"`+ch+`"`) {
			t.Fatalf("%s 通道未体现：%s", ch, body)
		}
	}

	h := s.Channeled("port")
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"can_admin":false`) {
		t.Fatalf("端口通道 can_admin 必须为 false：%s", rec.Body.String())
	}
}
