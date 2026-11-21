package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// putSettings 通过网关通道改设置，返回解析后的响应。
func putSettings(t *testing.T, s *Server, body string) settingsResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Channeled("gateway").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("设置接口应返回 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var resp settingsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v（%s）", err, rec.Body.String())
	}
	return resp
}

// 端口被占用时，应自动顺延到下一个可用端口，并把新端口写回配置。
func TestPortFallbackWhenBusy(t *testing.T) {
	s := newTestServer(t)

	blocker, err := net.Listen("tcp", ":28082")
	if err != nil {
		t.Skipf("28082 无法占用，跳过：%v", err)
	}
	defer blocker.Close()

	resp := putSettings(t, s, `{"public_port_enabled":true,"public_port":28082}`)
	defer s.ports.Stop()

	if !resp.Enabled {
		t.Fatalf("应保持开启，实际 %+v", resp)
	}
	if resp.Port == 28082 {
		t.Fatalf("端口应被顺延，实际仍是 %d", resp.Port)
	}
	if resp.SwitchedFrom != 28082 {
		t.Fatalf("应标记原端口 28082，实际 %d", resp.SwitchedFrom)
	}
	if !resp.Status.Listening {
		t.Fatalf("顺延后应已监听，实际 %+v", resp.Status)
	}
	if resp.Notice == "" {
		t.Fatal("应给出提示文案")
	}
	if got := s.cfg.Get().PublicPort; got != resp.Port {
		t.Fatalf("配置里的端口应为 %d，实际 %d", resp.Port, got)
	}
}

// 不允许顺延时（attempts=0）应直接失败，并识别为「端口占用」。
func TestPortNoFallbackWhenAttemptsZero(t *testing.T) {
	s := newTestServer(t)

	blocker, err := net.Listen("tcp", ":28082")
	if err != nil {
		t.Skipf("28082 无法占用，跳过：%v", err)
	}
	defer blocker.Close()

	actual, switched, err := s.ports.ApplyWithFallback(true, 28082, s.PortHandler(), 0)
	if err == nil {
		t.Fatal("attempts=0 时应直接失败")
	}
	if actual != 28082 || switched != 0 {
		t.Fatalf("失败时应返回原端口，实际 actual=%d switched=%d", actual, switched)
	}
	if !IsAddrInUse(err) {
		t.Fatalf("应识别为端口占用：%v", err)
	}
}

// 非法端口不应触发顺延。
func TestPortInvalidNoFallback(t *testing.T) {
	p := &PortManager{}
	_, switched, err := p.ApplyWithFallback(true, 70000, nil, 20)
	if err == nil {
		t.Fatal("非法端口应报错")
	}
	if switched != 0 {
		t.Fatal("非法端口不应触发顺延")
	}
}

// 关掉开关后应真的停止监听。
func TestPortDisableStopsListener(t *testing.T) {
	s := newTestServer(t)

	resp := putSettings(t, s, `{"public_port_enabled":true,"public_port":28091}`)
	if !resp.Status.Listening {
		t.Skipf("28091 不可用，跳过：%+v", resp.Status)
	}
	resp2 := putSettings(t, s, `{"public_port_enabled":false}`)
	if resp2.Enabled {
		t.Fatal("应已关闭")
	}
	if resp2.Status.Listening {
		t.Fatal("关闭后不应再监听")
	}
}
