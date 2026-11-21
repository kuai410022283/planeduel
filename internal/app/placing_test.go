package app

import (
	"testing"
	"time"

	"planeduel/internal/game"
)

// ---------------------------------------------------------------- 辅助

// beginPlaying 把房间从「布阵」推进到「对局」：双方随机布阵并按下「开始」。
func beginPlaying(t *testing.T, h *Hub, a, b *Client) {
	t.Helper()
	for _, c := range []*Client{a, b} {
		doSend(h, c, map[string]any{"t": "placing.rand"})
		doSend(h, c, map[string]any{"t": "placing.ready", "ready": true})
	}
}

// 三架互不重叠的合法布阵（机头朝上）
func samplePlacements() []game.Placement {
	return []game.Placement{
		{Row: 0, Col: 0, Orient: game.Up},
		{Row: 0, Col: 5, Orient: game.Up},
		{Row: 5, Col: 0, Orient: game.Up},
	}
}

// 邀请接受后应进入布阵阶段，棋盘是空的（不再自动随机）。
func TestPlacingPhaseStartsEmpty(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	rm, ok := capA.lastOf("room")
	if !ok {
		t.Fatal("没有房间消息")
	}
	v := asRoom(t, rm)
	if v.State != "placing" {
		t.Fatalf("开局前应为布阵阶段，实际 %q", v.State)
	}
	if len(v.MyCells) != 0 {
		t.Fatalf("布阵阶段棋盘应为空，实际 %d 格", len(v.MyCells))
	}
	if v.PlaceDeadlineMS == 0 {
		t.Fatal("应带布阵倒计时")
	}
	if v.MyReady || v.OppReady {
		t.Fatal("初始都不应处于已就绪状态")
	}
}

// 玩家自己摆放：合法布阵被接受，非法布阵被拒绝并回显当前状态。
func TestPlacingSetValidAndInvalid(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	// 合法：三架互不重叠
	doSend(h, a, map[string]any{"t": "placing.set", "placements": samplePlacements()})
	rm, _ := capA.lastOf("room")
	v := asRoom(t, rm)
	if len(v.MyPlanes) != 3 {
		t.Fatalf("应下发 3 架飞机的格子，实际 %d", len(v.MyPlanes))
	}
	if len(v.MyCells) != 30 {
		t.Fatalf("三架共 30 格，实际 %d", len(v.MyCells))
	}
	if v.MyPlanes[0].Head == "" || len(v.MyPlanes[0].Cells) != 10 {
		t.Fatalf("每架应有 10 格且带机头：%+v", v.MyPlanes[0])
	}

	// 非法：两架重叠
	overlap := []game.Placement{
		{Row: 0, Col: 0, Orient: game.Up},
		{Row: 0, Col: 0, Orient: game.Up},
		{Row: 5, Col: 0, Orient: game.Up},
	}
	before := capA.countOf("error")
	doSend(h, a, map[string]any{"t": "placing.set", "placements": overlap})
	if capA.countOf("error") <= before {
		t.Fatal("重叠布阵应被拒绝")
	}
	// 拒绝后棋盘不应被改动
	rm2, _ := capA.lastOf("room")
	if len(asRoom(t, rm2).MyPlanes) != 3 {
		t.Fatal("非法布阵不应覆盖已确认的布阵")
	}
}

// 按下「开始」后布阵锁定：再提交修改会被拒绝。
func TestPlacingLocksAfterReady(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	doSend(h, a, map[string]any{"t": "placing.set", "placements": samplePlacements()})
	doSend(h, a, map[string]any{"t": "placing.ready", "ready": true})

	rm, _ := capA.lastOf("room")
	if v := asRoom(t, rm); !v.MyReady || v.State != "placing" {
		t.Fatalf("应按开始但还没开局：ready=%v state=%q", v.MyReady, v.State)
	}

	// 锁定后再改：必须被拒
	before := capA.countOf("error")
	doSend(h, a, map[string]any{"t": "placing.set", "placements": samplePlacements()})
	if capA.countOf("error") <= before {
		t.Fatal("锁定后不应允许再修改布阵")
	}
	// 随机按钮同样应被拒
	before2 := capA.countOf("error")
	doSend(h, a, map[string]any{"t": "placing.rand"})
	if capA.countOf("error") <= before2 {
		t.Fatal("锁定后不应允许再随机布阵")
	}

	// 对手还没开始时可以取消，取消后恢复可编辑
	doSend(h, a, map[string]any{"t": "placing.ready", "ready": false})
	doSend(h, a, map[string]any{"t": "placing.set", "placements": samplePlacements()})
	rm2, _ := capA.lastOf("room")
	if v := asRoom(t, rm2); v.MyReady {
		t.Fatal("取消后应恢复未就绪")
	}
}

// 双方都按开始 → 用各自的布阵开局，棋盘与提交的布阵一致。
func TestPlacingBothReadyStartsMatch(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	mine := samplePlacements()
	doSend(h, a, map[string]any{"t": "placing.set", "placements": mine})
	doSend(h, a, map[string]any{"t": "placing.ready", "ready": true})
	doSend(h, b, map[string]any{"t": "placing.rand"})
	doSend(h, b, map[string]any{"t": "placing.ready", "ready": true})

	rm, _ := capA.lastOf("room")
	v := asRoom(t, rm)
	if v.State != "playing" {
		t.Fatalf("双方就绪后应开局，实际 %q", v.State)
	}
	if len(v.MyCells) != 30 {
		t.Fatalf("开局后应看到 30 格布阵，实际 %d", len(v.MyCells))
	}

	// 服务端棋盘应与提交的布阵一致（用机头校验）
	room := a.room
	room.mu.Lock()
	board := room.m.Board(v.Me)
	heads := []string{}
	for i := 0; i < board.Count(); i++ {
		heads = append(heads, board.Plane(i).Head.Name())
	}
	room.mu.Unlock()
	want := []string{"A3", "A8", "F3"} // (0,0)/(0,5)/(5,0) 朝上时的机头
	for i := range want {
		if heads[i] != want[i] {
			t.Fatalf("第 %d 架机头应为 %s，实际 %s", i+1, want[i], heads[i])
		}
	}
}

// 没放满就按开始 → 拒绝。
func TestPlacingReadyRequiresFullBoard(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	before := capA.countOf("error")
	doSend(h, a, map[string]any{"t": "placing.ready", "ready": true})
	if capA.countOf("error") <= before {
		t.Fatal("没布阵就按开始应被拒绝")
	}
}

// 布阵超时：没按开始的一方自动随机布阵并直接开局。
func TestPlacingTimeoutAutoRandom(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	doSend(h, a, map[string]any{"t": "placing.set", "placements": samplePlacements()})
	doSend(h, a, map[string]any{"t": "placing.ready", "ready": true})

	// 把倒计时推到过去
	room := a.room
	room.mu.Lock()
	room.placeDeadline = time.Now().Add(-time.Second)
	room.mu.Unlock()

	h.Tick()

	room.mu.Lock()
	state := room.state
	readyB := room.ready[1]
	placedB := len(room.placed[1])
	room.mu.Unlock()
	if state != "playing" {
		t.Fatalf("超时应自动开局，实际 %q", state)
	}
	if !readyB || placedB != 3 {
		t.Fatalf("超时方应被自动随机布阵：ready=%v 飞机数=%d", readyB, placedB)
	}
	rm, _ := capA.lastOf("room")
	if asRoom(t, rm).State != "playing" {
		t.Fatal("视图应变为 playing")
	}
}

// 随机按钮：服务端生成 3 架合法布阵。
func TestPlacingRandomButton(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	doSend(h, a, map[string]any{"t": "placing.rand"})
	rm, _ := capA.lastOf("room")
	v := asRoom(t, rm)
	if len(v.Placed) != 3 {
		t.Fatalf("随机应给 3 架，实际 %d", len(v.Placed))
	}
	// 服务端复算应通过
	if _, err := game.BuildFrom(game.DefaultRules(), v.Placed); err != nil {
		t.Fatalf("随机布阵本身应合法：%v", err)
	}
	// 清除：提交空布阵
	doSend(h, a, map[string]any{"t": "placing.set", "placements": []game.Placement{}})
	rm2, _ := capA.lastOf("room")
	if n := len(asRoom(t, rm2).MyCells); n != 0 {
		t.Fatalf("清空后不应有格子，实际 %d", n)
	}
}
