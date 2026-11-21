package app

import (
	"testing"
	"time"
)

// 新建会话时优先复用「编号最小且离线」的管理员会话，而不是一路往后加。
func TestAdminReusesLowestOfflineSlot(t *testing.T) {
	s := newTestServer(t)
	store := s.sessions

	first, err := store.CreateAdmin(nil) // 管理员
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateAdmin(func(tok string) bool { return tok == first.Token })
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.CreateAdmin(func(tok string) bool { return tok == first.Token || tok == second.Token })
	if err != nil {
		t.Fatal(err)
	}
	if third.Nick != AdminNick+"3" {
		t.Fatalf("第三个应为管理员3，实际 %q", third.Nick)
	}

	// 全部离线后再来一个：回到「管理员」
	got, err := store.CreateAdmin(func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if got.Nick != AdminNick {
		t.Fatalf("全部离线时应复用管理员，实际 %q", got.Nick)
	}

	// 管理员在线时顺延到下一个最小空位
	busy, err := store.CreateAdmin(func(tok string) bool { return tok == first.Token })
	if err != nil {
		t.Fatal(err)
	}
	if busy.Nick != AdminNick+"2" {
		t.Fatalf("管理员在线时应拿到管理员2，实际 %q", busy.Nick)
	}
}

// 离线超过 10 分钟的会话会被释放：管理员和游客一视同仁。
func TestReapIdleSessions(t *testing.T) {
	s := newTestServer(t)
	store := s.sessions

	admin, err := store.CreateAdmin(nil)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := store.Create("打飞机aa")
	if err != nil {
		t.Fatal(err)
	}
	recent, err := store.Create("打飞机bb")
	if err != nil {
		t.Fatal(err)
	}

	// admin 与 guest 都离线 15 分钟；recent 才刚离开
	store.mu.Lock()
	store.byTok[admin.Token].Seen = time.Now().Add(-15 * time.Minute)
	store.byTok[guest.Token].Seen = time.Now().Add(-15 * time.Minute)
	store.mu.Unlock()

	n := store.ReapIdleSessions(10*time.Minute, func(string) bool { return false }, func(string) bool { return false })
	if n != 2 {
		t.Fatalf("应回收 2 个会话（管理员 + 游客），实际 %d", n)
	}
	for _, tok := range []string{admin.Token, guest.Token} {
		if _, ok := store.byTok[tok]; ok {
			t.Fatalf("离线超过 10 分钟的会话应被释放: %s", tok)
		}
	}
	if _, ok := store.byTok[recent.Token]; !ok {
		t.Fatal("刚离线不久的会话不应被回收")
	}

	// 释放后的昵称可以被重新使用
	if _, err := store.Create("打飞机aa"); err != nil {
		t.Fatalf("释放后昵称应可复用: %v", err)
	}
	again, err := store.CreateAdmin(func(string) bool { return false })
	if err != nil || again.Nick != AdminNick {
		t.Fatalf("释放后应回到管理员，实际 %v %q", err, again.Nick)
	}
}

// 正在对局中的人：即使离线也不能删；在线的人更不能删。
func TestReapSkipsOnlineAndInGame(t *testing.T) {
	s := newTestServer(t)
	store := s.sessions

	online, err := store.Create("打飞机cc")
	if err != nil {
		t.Fatal(err)
	}
	inGame, err := store.Create("打飞机dd")
	if err != nil {
		t.Fatal(err)
	}
	idle, err := store.Create("打飞机ee")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	for _, tok := range []string{online.Token, inGame.Token, idle.Token} {
		store.byTok[tok].Seen = time.Now().Add(-30 * time.Minute)
	}
	store.mu.Unlock()

	onlineFn := func(tok string) bool { return tok == online.Token }
	busyFn := func(tok string) bool { return tok == inGame.Token }

	if n := store.ReapIdleSessions(10*time.Minute, onlineFn, busyFn); n != 1 {
		t.Fatalf("只应回收 1 个（idle），实际 %d", n)
	}
	if _, ok := store.byTok[online.Token]; !ok {
		t.Fatal("在线的会话不能被删")
	}
	if _, ok := store.byTok[inGame.Token]; !ok {
		t.Fatal("对局中的会话不能被删")
	}
	if _, ok := store.byTok[idle.Token]; ok {
		t.Fatal("空闲会话应被回收")
	}
}

// 对局中的身份会被 hub 标记为 busy（TokenBusy 依据房间占用）。
func TestHubTokenBusy(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	if h.TokenBusy("tok-a") {
		t.Fatal("还没进房间时不应算 busy")
	}

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	if !h.TokenBusy("tok-a") || !h.TokenBusy("tok-b") {
		t.Fatal("开局后双方都应算 busy")
	}
}
