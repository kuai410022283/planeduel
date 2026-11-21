package app

import (
	"testing"
	"time"

	"planeduel/internal/game"
)

// 断线不再立刻判负：房间保持 playing，等重连接管。
func TestReconnectKeepsGameAlive(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	room := a.room
	if room == nil {
		t.Fatal("没有房间")
	}

	// 甲掉线
	h.mu.Lock()
	h.markGone(a)
	h.mu.Unlock()

	room.mu.Lock()
	state := room.state
	room.mu.Unlock()
	if state != "playing" {
		t.Fatalf("掉线后应保持 playing 等待重连，实际 %q", state)
	}

	// 重连（同一 token 的新连接）应接管座位
	capA2 := &capture{}
	a2 := &Client{Nick: "甲", Token: "tok-a", send: capA2.send, kill: func() {}, lastSeen: time.Now()}
	if !room.bind(a2) {
		t.Fatal("同 token 的重连应能接管座位")
	}
	if a2.room != room {
		t.Fatal("重连后应绑定回房间")
	}

	// 重连后继续打：能正常开火
	h.sendRoom(room, a2)
	rm, ok := capA2.lastOf("room")
	if !ok {
		t.Fatal("重连后应收到房间状态")
	}
	if asRoom(t, rm).State != "playing" {
		t.Fatal("重连后状态应仍是 playing")
	}
}

// 同一昵称但不同身份（例如另一个浏览器）不能抢座位。
func TestDifferentTokenCannotStealSeat(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room

	other := &Client{Nick: "甲", Token: "someone-else", send: func([]byte) error { return nil }, kill: func() {}}
	if room.bind(other) {
		t.Fatal("不同 token 不应能接管座位")
	}
	_ = capA
}

// 掉线超过宽限期才判负。
func TestOfflineGraceThenForfeit(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room

	h.mu.Lock()
	h.markGone(a)
	h.mu.Unlock()

	// 宽限期内：仍是 playing
	h.Tick()
	if room.state != "playing" {
		t.Fatalf("宽限期内不应判负，实际 %q", room.state)
	}

	// 把掉线时间往前推过宽限期
	room.mu.Lock()
	room.goneAt[0] = time.Now().Add(-offlineGrace - time.Second)
	room.mu.Unlock()

	h.Tick()
	room.mu.Lock()
	state, winner := room.state, room.winner
	room.mu.Unlock()
	if state != "done" || winner != 1 {
		t.Fatalf("超过宽限应判掉线方负（winner=1），实际 state=%q winner=%d", state, winner)
	}
	_ = capA
}

// 回合超时要带上明确的结束原因，界面才能告诉玩家"为什么结束"。
func TestTimeoutCarriesReason(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room

	room.mu.Lock()
	turn := room.m.Turn()
	room.deadline = time.Now().Add(-time.Second) // 模拟超时
	room.mu.Unlock()

	h.Tick()

	room.mu.Lock()
	state, reason, winner := room.state, room.endReason, room.winner
	room.mu.Unlock()
	if state != "done" {
		t.Fatalf("超时应结束对局，实际 %q", state)
	}
	if reason != "timeout" {
		t.Fatalf("结束原因应为 timeout，实际 %q", reason)
	}
	if winner != 1-turn {
		t.Fatalf("应由超时方的对手获胜：turn=%d winner=%d", turn, winner)
	}

	// 视图里要能读到原因
	m, ok := capB.lastOf("room")
	if !ok {
		t.Fatal("没有房间视图")
	}
	if asRoom(t, m).Reason != "timeout" {
		t.Fatalf("视图未携带原因：%q", asRoom(t, m).Reason)
	}
}
// 每回合时限可配置（3/5 分钟），并且每一发之后都重新计时。
func TestTurnLimitConfigurableAndResets(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙", "turn_sec": 180})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	room := a.room
	room.mu.Lock()
	limit := room.turnLimit
	first := room.deadline
	room.mu.Unlock()

	if limit != 180*time.Second {
		t.Fatalf("应使用邀请里带的 180s 时限，实际 %v", limit)
	}
	if d := time.Until(first); d < 170*time.Second || d > 181*time.Second {
		t.Fatalf("开局计时应约 180s，实际 %v", d)
	}

	// 打一发（不论中空）之后必须重新计时
	time.Sleep(10 * time.Millisecond)
	turn := 0
	doSend(h, a, map[string]any{"t": "shot", "cell": "A1"})
	room.mu.Lock()
	next := room.deadline
	room.mu.Unlock()
	if !next.After(first) {
		t.Fatalf("开火后应重置计时：%v 未晚于 %v", next, first)
	}
	if d := time.Until(next); d < 175*time.Second {
		t.Fatalf("重置后应接近完整 180s，实际 %v", d)
	}
	_ = turn

	// 视图里也要带上限时
	m, _ := capB.lastOf("room")
	if asRoom(t, m).TurnSec != 180 {
		t.Fatalf("视图应带 turn_sec=180，实际 %d", asRoom(t, m).TurnSec)
	}
}

// 不传 turn_sec 时默认 5 分钟。
func TestDefaultTurnLimitIsFiveMinutes(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	room := a.room
	room.mu.Lock()
	limit := room.turnLimit
	room.mu.Unlock()
	if limit != 300*time.Second {
		t.Fatalf("默认应为 300s（5 分钟），实际 %v", limit)
	}
}
// 默认交替出手：打一发就换人（不会出现"只有一方一直打"）。
func TestDefaultRuleAlternatesEveryShot(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	room := a.room
	room.mu.Lock()
	cont := room.Rules.ContinueOnHit
	room.mu.Unlock()
	if cont {
		t.Fatal("新房间默认应为交替出手（ContinueOnHit=false）")
	}

	// 连打 6 发，不管中不中，轮次都必须交替
	for i := 0; i < 6; i++ {
		room.mu.Lock()
		want := room.m.Turn()
		room.mu.Unlock()
		var shooter *Client
		if want == 0 {
			shooter = a
		} else {
			shooter = b
		}
		cell := game.NewCell(i, 0).Name()
		doSend(h, shooter, map[string]any{"t": "shot", "cell": cell})

		room.mu.Lock()
		got := room.m.Turn()
		room.mu.Unlock()
		if got == want {
			t.Fatalf("第 %d 发后回合没有切换（仍是 %d）", i+1, got)
		}
	}
}

// 显式开启连击时，打中保持出手。
func TestContinueOnHitOption(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙", "continue_on_hit": true})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	room := a.room
	room.mu.Lock()
	cont := room.Rules.ContinueOnHit
	room.mu.Unlock()
	if !cont {
		t.Fatal("应开启连击规则")
	}
}
// 上一局打完之后（房间进入 done，但双方还没点「离开」），
// 再次邀请/接受邀请必须能正常开新局。
func TestInviteAgainAfterGameEnds(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	// 第一局
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room

	// 把这一局打完
	clients := [2]*Client{a, b}
	for guard := 0; guard < 300; guard++ {
		room.mu.Lock()
		if room.m == nil || room.m.Finished() {
			room.mu.Unlock()
			break
		}
		turn := room.m.Turn()
		target := ""
		b := room.m.Board(1 - turn)
		for i := 0; i < b.Count(); i++ {
			hc := b.Plane(i).Head
			if !room.m.Shot(turn, hc) { // 该玩家还没喊过这一架的机头
				target = hc.Name()
				break
			}
		}
		room.mu.Unlock()
		if target == "" {
			break
		}
		doSend(h, clients[turn], map[string]any{"t": "shot", "cell": target})
	}

	room.mu.Lock()
	state := room.state
	moves := 0
	if room.m != nil {
		moves = len(room.m.Moves())
	}
	room.mu.Unlock()
	if state != "done" {
		var lastErr string
		if e, ok := capA.lastOf("error"); ok {
			lastErr = e["msg"].(string)
		}
		t.Fatalf("第一局应已结束，实际 %q（走子 %d 手，最后错误：%s）", state, moves, lastErr)
	}

	// 关键：不点「离开房间」，直接再邀请
	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv2, ok := capB.lastOf("invite")
	if !ok {
		t.Fatal("上一局结束后应能再次邀请")
	}
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv2["id"].(string), "accept": true})

	// 应重新开局
	got, ok := capA.lastOf("room")
	if !ok {
		t.Fatal("接受后应收到房间状态")
	}
	v := asRoom(t, got)
	if v.State != "playing" {
		msg := ""
		if e, ok := capA.lastOf("error"); ok {
			msg = e["msg"].(string)
		}
		aRoom := "nil"
		if a.room != nil {
			aRoom = a.room.Code + "/" + a.room.state
		}
		t.Fatalf("应重新开局，实际 %q（错误：%q，邀请 id=%v，a.room=%s）", v.State, msg, inv2["id"], aRoom)
	}
}
// 对局已结束后，同一身份重连不应被拉回残局（关浏览器再打开应回到空白状态）。
func TestReconnectDoesNotResumeFinishedRoom(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	a.Token, b.Token = "tok-a", "tok-b"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room

	// 把这一局打完
	clients := [2]*Client{a, b}
	for guard := 0; guard < 40; guard++ {
		room.mu.Lock()
		if room.m == nil || room.m.Finished() {
			room.mu.Unlock()
			break
		}
		turn := room.m.Turn()
		target := ""
		bd := room.m.Board(1 - turn)
		for i := 0; i < bd.Count(); i++ {
			hc := bd.Plane(i).Head
			if !room.m.Shot(turn, hc) {
				target = hc.Name()
				break
			}
		}
		room.mu.Unlock()
		if target == "" {
			break
		}
		doSend(h, clients[turn], map[string]any{"t": "shot", "cell": target})
	}

	room.mu.Lock()
	state := room.state
	room.mu.Unlock()
	if state != "done" {
		t.Fatalf("对局应已结束，实际 %q", state)
	}

	// 甲关掉浏览器（掉线），再打开（同 token 的新连接）
	h.mu.Lock()
	h.markGone(a)
	h.mu.Unlock()

	capA2 := &capture{}
	a2 := &Client{Nick: "甲", Token: "tok-a", send: capA2.send, kill: func() {}, lastSeen: time.Now()}
	if room.bind(a2) {
		t.Fatal("已结束的房间不应再把人拉回去")
	}
	if a2.room != nil {
		t.Fatal("重连后不应绑定到已结束的房间")
	}
}