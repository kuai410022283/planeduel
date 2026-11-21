package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const roomCodeAlphabetForTest = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// 房间码必须是 6 位、只含安全字符，并且大小写不敏感地可加入。
func TestRoomCodeLengthAndJoinCaseInsensitive(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "房主")
	b, capB := join(h, "客人")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "room.create"})
	rm, ok := capA.lastOf("room")
	if !ok {
		t.Fatal("建房后应收到房间状态")
	}
	v := asRoom(t, rm)
	if len(v.Code) != 6 {
		t.Fatalf("房间码应为 6 位，实际 %q（%d 位）", v.Code, len(v.Code))
	}
	for _, ch := range v.Code {
		if !strings.ContainsRune(roomCodeAlphabetForTest, ch) {
			t.Fatalf("房间码含易混字符 %q：%s", ch, v.Code)
		}
	}

	// 用小写加入必须成功（前端会 toUpperCase，服务端也要兼容）
	doSend(h, b, map[string]any{"t": "room.join", "code": strings.ToLower(v.Code)})
	rmB, ok := capB.lastOf("room")
	if !ok {
		t.Fatal("加入后应收到房间状态")
	}
	if asRoom(t, rmB).State != "placing" {
		t.Fatalf("加入后应进入布阵阶段，实际 %q", asRoom(t, rmB).State)
	}
	beginPlaying(t, h, a, b)
	rmB2, _ := capB.lastOf("room")
	if asRoom(t, rmB2).State != "playing" {
		t.Fatalf("双方就绪后应开局，实际 %q", asRoom(t, rmB2).State)
	}
}

// 飞牛网关进来：昵称固定为「管理员」。
func TestGatewayNicknameIsAdmin(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	// 关键：一个飞牛相关的头都不带，昵称也必须是「管理员」
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"nick":"`+AdminNick+`"`) {
		t.Fatalf("网关昵称应为 %s（不依赖任何请求头），实际 %s", AdminNick, body)
	}
	if !strings.Contains(body, `"authenticated":true`) {
		t.Fatalf("应自动登录：%s", body)
	}
}

// 外网访客：自动分配「打飞机 + 0~3 位字母数字」，且互不相同。
func TestGuestNicknameFormat(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("port")

	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"nick":""}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("自动分配应成功，实际 %d：%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		idx := strings.Index(body, `"nick":"`)
		if idx < 0 {
			t.Fatalf("响应缺少 nick：%s", body)
		}
		rest := body[idx+len(`"nick":"`):]
		nick := rest[:strings.Index(rest, `"`)]

		if !strings.HasPrefix(nick, "打飞机") {
			t.Fatalf("昵称应以「打飞机」开头，实际 %q", nick)
		}
		suffix := strings.TrimPrefix(nick, "打飞机")
		if len(suffix) > 3 {
			t.Fatalf("后缀应 0-3 位，实际 %q", suffix)
		}
		for _, ch := range suffix {
			if !(ch >= 'a' && ch <= 'z' || ch >= '2' && ch <= '9') {
				t.Fatalf("后缀含非法字符 %q：%s", ch, nick)
			}
		}
		if seen[nick] {
			t.Fatalf("昵称重复：%s", nick)
		}
		seen[nick] = true
	}
}

// 自动分配的昵称仍然可以自己改。
func TestGuestCanRename(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("port")

	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"nick":""}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	idx := strings.Index(body, `"token":"`)
	token := body[idx+len(`"token":"`):]
	token = token[:strings.Index(token, `"`)]

	req2 := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"nick":"小明同学"}`))
	req2.Header.Set("X-Session", token)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("改名应成功，实际 %d：%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"nick":"小明同学"`) {
		t.Fatalf("改名未生效：%s", rec2.Body.String())
	}
}

// 网关用户也可以改名（改成之后不会被固定昵称覆盖）。
func TestGatewayCanRename(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieNameFor(chGateway) {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("应下发身份 Cookie")
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"nick":"老K"}`))
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("网关用户改名应成功，实际 %d：%s", rec2.Code, rec2.Body.String())
	}

	// 再请求一次 /api/session：应保持改名后的昵称，而不是被重置为「管理员」
	req3 := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req3.AddCookie(cookie)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if !strings.Contains(rec3.Body.String(), `"nick":"老K"`) {
		t.Fatalf("改名被固定昵称覆盖了：%s", rec3.Body.String())
	}
	_ = time.Now
}

// 即使客户端伪造了飞牛的用户名头，也仍然是「管理员」，不会被当成别人。
func TestGatewayIgnoresFnosUsernameHeader(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.Header.Set("X-Trim-Username", "somebody-else")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), `"nick":"`+AdminNick+`"`) {
		t.Fatalf("不应识别 NAS 用户名：%s", rec.Body.String())
	}
}


// 新浏览器（没有 Cookie）打开网关：上一个「管理员」已离线，应复用同一身份而不是变成管理员2。
func TestAdminSessionReusedWhenOffline(t *testing.T) {
	s := newTestServer(t)
	h := s.Channeled("gateway")

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if !strings.Contains(first.Body.String(), `"nick":"`+AdminNick+`"`) {
		t.Fatalf("首次应为管理员：%s", first.Body.String())
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if !strings.Contains(second.Body.String(), `"nick":"`+AdminNick+`"`) {
		t.Fatalf("旧会话离线时应复用管理员：%s", second.Body.String())
	}
}

// 上一个管理员在线时（另一个窗口正在用），新浏览器才加后缀。
func TestAdminSuffixOnlyWhenOnline(t *testing.T) {
	s := newTestServer(t)
	store := s.sessions

	first, err := store.CreateAdmin(nil) // 离线场景
	if err != nil || first.Nick != AdminNick {
		t.Fatalf("首次应为管理员：%v %q", err, first.Nick)
	}

	// 第一个仍在线 → 第二个换后缀
	second, err := store.CreateAdmin(func(tok string) bool { return tok == first.Token })
	if err != nil {
		t.Fatal(err)
	}
	if second.Nick != AdminNick+"2" {
		t.Fatalf("在线时应加后缀，实际 %q", second.Nick)
	}

	// 都离线后再来 → 又回到「管理员」
	third, err := store.CreateAdmin(func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if third.Nick != AdminNick {
		t.Fatalf("离线时应复用管理员，实际 %q", third.Nick)
	}
}
// Cookie 必须按通道隔离：同一个浏览器在网关是「管理员」，在端口不应沿用这个身份。
func TestCookieIsolatedPerChannel(t *testing.T) {
	s := newTestServer(t)

	// 1) 网关取身份 → pd_sid_gw
	gw := s.Channeled("gateway")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	var gwCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieNameFor(chGateway) {
			gwCookie = c
		}
	}
	if gwCookie == nil {
		t.Fatalf("网关应下发 %s：%v", cookieNameFor(chGateway), rec.Result().Cookies())
	}

	// 2) 把这个网关 Cookie 发到端口通道：不应被识别
	pt := s.Channeled("port")
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: cookieNameFor(chGateway), Value: gwCookie.Value})
	rec2 := httptest.NewRecorder()
	pt.ServeHTTP(rec2, req)

	if strings.Contains(rec2.Body.String(), `"nick":"`+AdminNick+`"`) {
		t.Fatalf("端口不应沿用网关身份：%s", rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"authenticated":false`) {
		t.Fatalf("端口应视为未登录：%s", rec2.Body.String())
	}
}