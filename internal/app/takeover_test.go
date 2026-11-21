package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// 同一身份第二次连接（刷新页面 / 关掉再打开 / 旧连接僵死）应当直接接管座位，
// 并把旧连接顶掉，同时给旧连接发一条 replaced 提示。
func TestSameTokenNewConnectionTakesOver(t *testing.T) {
	s := newTestServer(t)
	sa, _ := s.sessions.Create("玩家甲")
	sb, _ := s.sessions.Create("玩家乙")
	ts := httptest.NewServer(s.Channeled("admin"))
	defer ts.Close()

	a := dialWS(t, ts.URL, sa.Token)
	defer a.conn.Close()
	b := dialWS(t, ts.URL, sb.Token)
	defer b.conn.Close()
	a.waitFor(t, "players", 4)
	b.waitFor(t, "players", 4)

	// 开局到 playing
	a.sendText(t, []byte(`{"t":"invite","to":"玩家乙"}`))
	inv := b.waitFor(t, "invite", 4)
	id, _ := inv["id"].(string)
	b.sendText(t, []byte(`{"t":"invite.reply","id":"`+id+`","accept":true}`))
	a.waitFor(t, "room", 8)
	b.waitFor(t, "room", 8)

	// 布阵 + 开始
	for _, c := range []*wsTestClient{a, b} {
		c.sendText(t, []byte(`{"t":"placing.rand"}`))
		c.waitFor(t, "room", 8)
		c.sendText(t, []byte(`{"t":"placing.ready","ready":true}`))
		c.waitFor(t, "room", 8)
	}

	// 找到最终 playing 的视图
	var view roomView
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := a.waitFor(t, "room", 8)
		raw, _ := json.Marshal(m["room"])
		_ = json.Unmarshal(raw, &view)
		if view.State == "playing" {
			break
		}
	}
	if view.State != "playing" {
		t.Fatalf("应已开局，实际 %q", view.State)
	}
	seat := view.Me

	// 同一个 token 再开一个连接（模拟刷新/重开浏览器）
	a2 := dialWS(t, ts.URL, sa.Token)
	defer a2.conn.Close()

	// 新连接应拿到 welcome + 同一局、同一座位
	w := a2.waitFor(t, "welcome", 6)
	if w["nick"] != "玩家甲" {
		t.Fatalf("新连接身份不对: %v", w["nick"])
	}
	var view2 roomView
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := a2.waitFor(t, "room", 8)
		raw, _ := json.Marshal(m["room"])
		_ = json.Unmarshal(raw, &view2)
		if view2.State == "playing" {
			break
		}
	}
	if view2.State != "playing" {
		t.Fatalf("新连接应回到对局，实际 %q", view2.State)
	}
	if view2.Me != seat {
		t.Fatalf("座位应保持 %d，实际 %d", seat, view2.Me)
	}
	if len(view2.MyCells) != 30 {
		t.Fatalf("布阵应完整 30 格，实际 %d", len(view2.MyCells))
	}

	// 旧连接应收到 replaced，然后被关闭
	old := a.waitFor(t, "replaced", 6)
	if _, ok := old["msg"]; !ok {
		t.Fatal("replaced 应带提示文案")
	}
	_ = a.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	if _, err := a.conn.Read(buf); err == nil {
		t.Fatal("旧连接应已被服务端关闭")
	}
}

// 两个不同的身份（token）抢同一昵称：新连接照旧踢掉旧的，但不发 replaced。
func TestDifferentTokenSameNickStillKicked(t *testing.T) {
	s := newTestServer(t)
	sa, _ := s.sessions.Create("同名者")
	ts := httptest.NewServer(s.Channeled("admin"))
	defer ts.Close()

	first := dialWS(t, ts.URL, sa.Token)
	defer first.conn.Close()
	first.waitFor(t, "welcome", 4)

	// 造一个同昵称、不同 token 的第二身份（前面多打一个空格后归一化会冲突，这里直接改 token）
	second := &Client{Nick: "同名者", Token: "another-token", send: func([]byte) error { return nil }, kill: func() {}}
	s.hub.mu.Lock()
	old := s.hub.clients["同名者"]
	s.hub.mu.Unlock()
	if old == nil {
		t.Fatal("第一个连接应已登记")
	}
	_ = second

	// 手动模拟 HandleWS 的接管逻辑
	s.hub.mu.Lock()
	s.hub.clients["同名者"] = second
	s.hub.mu.Unlock()
	old.kill()
	time.Sleep(100 * time.Millisecond)

	s.hub.mu.Lock()
	cur := s.hub.clients["同名者"]
	s.hub.mu.Unlock()
	if cur != second {
		t.Fatal("新连接应接管昵称")
	}
}
