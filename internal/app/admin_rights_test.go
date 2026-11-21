package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 管理权限只由入口决定：独立端口永远不是管理员，带什么口令都没用。
func TestPortChannelNeverAdmin(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("port")

	cases := []struct {
		name  string
		mut   func(*http.Request)
	}{
		{"不带任何凭证", func(r *http.Request) {}},
		{"伪造 X-Admin-Token", func(r *http.Request) { r.Header.Set("X-Admin-Token", "anything") }},
		{"伪造查询参数 token", func(r *http.Request) { r.URL.RawQuery = "token=anything" }},
		{"伪造飞牛管理员头", func(r *http.Request) { r.Header.Set("X-Trim-Isadmin", "true") }},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
		c.mut(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("[%s] 端口通道应 403，实际 %d", c.name, rec.Code)
		}
	}
}

// 飞牛网关入口：不带口令直接是管理员。
func TestGatewayChannelIsAdminWithoutToken(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("网关通道应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "admin_token") {
		t.Fatalf("设置接口不应再返回管理口令：%s", rec.Body.String())
	}
}

// 旧配置里残留的 admin_token 会在加载时被清掉（避免明文口令留在磁盘上）。
func TestLegacyAdminTokenRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{"public_port_enabled":true,"public_port":28082,"admin_token":"0Oq6_legacy_secret","nick_ttl_days":30}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Get().PublicPortEnabled {
		t.Fatal("原有配置应保留")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "admin_token") || strings.Contains(string(raw), "0Oq6_legacy_secret") {
		t.Fatalf("旧口令应从配置中移除，实际：%s", raw)
	}
	if !strings.Contains(string(raw), "28082") {
		t.Fatalf("其它字段不应丢失：%s", raw)
	}
}

// 会话接口在端口通道上不能自称管理员。
func TestSessionCanAdminByChannel(t *testing.T) {
	s := newTestServer(t)

	gw := s.Channeled("gateway")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if !strings.Contains(rec.Body.String(), `"can_admin":true`) {
		t.Fatalf("网关应为管理员：%s", rec.Body.String())
	}

	pt := s.Channeled("port")
	rec2 := httptest.NewRecorder()
	pt.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if !strings.Contains(rec2.Body.String(), `"can_admin":false`) {
		t.Fatalf("端口不应为管理员：%s", rec2.Body.String())
	}
}
