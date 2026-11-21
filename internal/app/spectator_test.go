package app

import (
	"encoding/json"
	"testing"

	"planeduel/internal/game"
)

// 观战：第 3 人能进、能看到已打出的格子、但看不到未曝光的布阵；结束后亮牌。
func TestSpectatorSeesOnlyPublicInfo(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")
	c, capC := join(h, "观众")
	a.Token, b.Token, c.Token = "tok-a", "tok-b", "tok-c"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room
	code := room.Code
	_ = capA

	// 第三人旁观
	doSend(h, c, map[string]any{"t": "room.spectate", "code": code})
	sp, ok := capC.lastOf("spectate")
	if !ok {
		t.Fatal("观战者应收到观战视图")
	}
	v := asSpectate(t, sp)
	if v.State != "playing" {
		t.Fatalf("应看到对局进行中，实际 %q", v.State)
	}
	if len(v.Reveal[0]) != 0 || len(v.Reveal[1]) != 0 {
		t.Fatal("对局进行中不应泄露布阵")
	}
	if len(v.Shots) != 0 {
		t.Fatalf("开局时应尚无喊号记录，实际 %d", len(v.Shots))
	}

	// 打一发：观战者应该收到更新，且只有那一发的公开信息
	doSend(h, a, map[string]any{"t": "shot", "cell": "A1"})
	sp2, _ := capC.lastOf("spectate")
	v2 := asSpectate(t, sp2)
	if len(v2.Shots) != 1 || v2.Shots[0].Cell != "A1" {
		t.Fatalf("观战者应看到 1 条喊号记录，实际 %+v", v2.Shots)
	}
	if len(v2.Reveal[0]) != 0 {
		t.Fatal("进行中仍然不应亮牌")
	}

	// 观战者不能出手
	doSend(h, c, map[string]any{"t": "shot", "cell": "B2"})
	if _, ok := capC.lastOf("error"); !ok {
		t.Fatal("观战者出手应被拒绝")
	}
}

// 打完一局后，观战者能看到双方 30 格布阵。
func TestSpectatorRevealAfterGame(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	c, capC := join(h, "观众")
	a.Token, b.Token, c.Token = "tok-a", "tok-b", "tok-c"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	room := a.room

	doSend(h, c, map[string]any{"t": "room.spectate", "code": room.Code})

	// 双方轮流把所有格子打一遍，直到结束
	orders := [2][]string{}
	for p := 0; p < 2; p++ {
		for r0 := 0; r0 < 10; r0++ {
			for c0 := 0; c0 < 10; c0++ {
				orders[p] = append(orders[p], cellNameOf(r0, c0))
			}
		}
	}
	cursor := [2]int{}
	clients := [2]*Client{a, b}

	for guard := 0; guard < 260; guard++ {
		room.mu.Lock()
		finished := room.m != nil && room.m.Finished()
		turn := 0
		if room.m != nil {
			turn = room.m.Turn()
		}
		room.mu.Unlock()
		if finished {
			break
		}
		if cursor[turn] >= 100 {
			t.Fatalf("%d 号无格可打但未结束", turn)
		}
		doSend(h, clients[turn], map[string]any{"t": "shot", "cell": orders[turn][cursor[turn]]})
		cursor[turn]++
	}

	sp, ok := capC.lastOf("spectate")
	if !ok {
		t.Fatal("观战者应收到最终视图")
	}
	v := asSpectate(t, sp)
	if !v.Finished {
		t.Fatalf("观战视图应标记结束，实际 state=%q", v.State)
	}
	if len(v.Reveal[0]) != 30 || len(v.Reveal[1]) != 30 {
		t.Fatalf("结束后应亮出双方各 30 格，实际 %d / %d", len(v.Reveal[0]), len(v.Reveal[1]))
	}
}

// 房间不存在时观战应给出提示。
func TestSpectateUnknownRoom(t *testing.T) {
	h := newTestHub(t)
	c, capC := join(h, "观众")
	doSend(h, c, map[string]any{"t": "room.spectate", "code": "ZZZZZZ"})
	if _, ok := capC.lastOf("error"); !ok {
		t.Fatal("不存在的房间应报错")
	}
}

func asSpectate(t *testing.T, m map[string]any) spectateView {
	t.Helper()
	raw, err := json.Marshal(m["spectate"])
	if err != nil {
		t.Fatalf("观战视图序列化失败: %v", err)
	}
	var v spectateView
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("观战视图解析失败: %v", err)
	}
	return v
}

func cellNameOf(r, c int) string { return game.NewCell(r, c).Name() }

// 房间已满 2 人时，点「加入」应自动转为旁观（不再需要单独的旁观按钮）。
func TestJoinFullRoomAutoSpectates(t *testing.T) {
	h := newTestHub(t)
	a, _ := join(h, "甲")
	b, capB := join(h, "乙")
	c, capC := join(h, "丙")
	a.Token, b.Token, c.Token = "tok-a", "tok-b", "tok-c"

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})
	code := a.room.Code

	// 第三人用「加入」而不是「旁观」
	doSend(h, c, map[string]any{"t": "room.join", "code": code})

	sp, ok := capC.lastOf("spectate")
	if !ok {
		t.Fatal("满员后加入应自动进入旁观")
	}
	if v := asSpectate(t, sp); v.State != "playing" {
		t.Fatalf("应看到进行中的对局，实际 %q", v.State)
	}
	if _, ok := capC.lastOf("info"); !ok {
		t.Fatal("应提示已自动进入旁观")
	}
	// 第三人不应被当成参战方
	if c.room != nil {
		t.Fatal("旁观者不应占用对局座位")
	}
}