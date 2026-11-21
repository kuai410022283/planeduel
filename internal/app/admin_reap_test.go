package app

import (
	"testing"
	"time"
)

// 新的浏览器进来时，应复用「编号最小且离线」的管理员会话，而不是一路往后加。
func TestAdminReusesLowestOfflineSlot(t *testing.T) {
	s := newTestServer(t)
	store := s.sessions

	first, err := store.CreateAdmin(nil) // 管理员
	if err != nil {
		t.Fatal(err)
	}
	// 造出 管理员2、管理员3（模拟历史上多开的浏览器，现已全部离线）
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

	// 全部离线后再来一个：应该回到「管理员」
	got, err := store.CreateAdmin(func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if got.Nick != AdminNick {
		t.Fatalf("全部离线时应复用管理员，实际 %q", got.Nick)
	}

	// 管理员在线时，才顺延到下一个最小空位（这里是管理员2）
	busy, err := store.CreateAdmin(func(tok string) bool { return tok == first.Token })
	if err != nil {
		t.Fatal(err)
	}
	if busy.Nick != AdminNick+"2" {
		t.Fatalf("管理员在线时应拿到管理员2，实际 %q", busy.Nick)
	}
}

// 离线超过 10 分钟的管理员会话应被释放；在线的不动；游客昵称不受影响。
func TestReapIdleAdminSessions(t *testing.T) {
	s := newTestServer(t)
	store := s.sessions

	a, err := store.CreateAdmin(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateAdmin(func(tok string) bool { return tok == a.Token })
	if err != nil {
		t.Fatal(err)
	}
	guest, err := store.Create(GuestPrefixTest(t))
	if err != nil {
		t.Fatal(err)
	}

	// 把 a 的最近活跃时间推到 15 分钟前（离线很久）
	store.mu.Lock()
	store.byTok[a.Token].Seen = time.Now().Add(-15 * time.Minute)
	store.mu.Unlock()

	// b 仍在线 → 不该被回收
	online := func(tok string) bool { return tok == b.Token }
	if n := store.ReapIdleAdmin(10*time.Minute, online); n != 1 {
		t.Fatalf("应回收 1 个空闲管理员会话，实际 %d", n)
	}
	if _, ok := store.byKey[lower(AdminNick)]; ok {
		t.Fatal("管理员会话应已被释放")
	}
	if _, ok := store.byTok[b.Token]; !ok {
		t.Fatal("在线会话不应被回收")
	}
	if _, ok := store.byTok[guest.Token]; !ok {
		t.Fatal("游客会话不应被回收")
	}

	// 释放后再来一个新浏览器：又回到「管理员」
	again, err := store.CreateAdmin(online)
	if err != nil {
		t.Fatal(err)
	}
	if again.Nick != AdminNick {
		t.Fatalf("释放后应回到管理员，实际 %q", again.Nick)
	}
}

func lower(s string) string { return s }

func GuestPrefixTest(t *testing.T) string {
	t.Helper()
	return "打飞机test"
}
