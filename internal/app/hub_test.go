package app

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"planeduel/internal/game"
)

// ---------------------------------------------------------------- 测试辅助

type capture struct {
	mu   sync.Mutex
	msgs []map[string]any
}

func (c *capture) send(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
	return nil
}

func (c *capture) lastOf(t string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i]["t"] == t {
			return c.msgs[i], true
		}
	}
	return nil, false
}

func (c *capture) countOf(t string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if m["t"] == t {
			n++
		}
	}
	return n
}

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	return NewHub(NewSessionStore(time.Hour), 1)
}

// join 模拟一个已登录的连接加入 hub。
func join(h *Hub, nick string) (*Client, *capture) {
	cap := &capture{}
	c := &Client{Nick: nick, send: cap.send, kill: func() {}, lastSeen: time.Now()}
	h.mu.Lock()
	h.clients[nick] = c
	h.mu.Unlock()
	return c, cap
}

func doSend(h *Hub, c *Client, v map[string]any) {
	b, _ := json.Marshal(v)
	h.handle(c, b)
}

// ---------------------------------------------------------------- 用例

// 邀请 → 接受 → 双方进入对局
func TestInviteAcceptStartsRoom(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	if _, ok := capB.lastOf("invite"); !ok {
		t.Fatal("乙没有收到邀请")
	}
	inv, _ := capB.lastOf("invite")
	id, _ := inv["id"].(string)

	doSend(h, b, map[string]any{"t": "invite.reply", "id": id, "accept": true})

	rmA, okA := capA.lastOf("room")
	rmB, okB := capB.lastOf("room")
	if !okA || !okB {
		t.Fatalf("双方都应收到房间状态：A=%v B=%v", okA, okB)
	}
	ra := asRoom(t, rmA)
	rb := asRoom(t, rmB)
	if ra.State != "playing" || rb.State != "playing" {
		t.Fatalf("应直接开局：%s / %s", ra.State, rb.State)
	}
	if ra.Me == rb.Me {
		t.Fatal("双方视角编号不应相同")
	}
	if len(ra.MyCells) != 30 || len(rb.MyCells) != 30 {
		t.Fatalf("每人应有 30 格布阵：%d / %d", len(ra.MyCells), len(rb.MyCells))
	}
	if len(ra.EnemyMarks) != 0 {
		t.Fatal("开局时敌情图应为空")
	}
}

// 邀请自己 / 邀请不在线的人都要被挡住
func TestInviteErrors(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")

	doSend(h, a, map[string]any{"t": "invite", "to": "甲"})
	if _, ok := capA.lastOf("error"); !ok {
		t.Fatal("邀请自己应报错")
	}
	doSend(h, a, map[string]any{"t": "invite", "to": "不存在的人"})
	if capA.countOf("error") < 2 {
		t.Fatal("邀请离线玩家应报错")
	}
}

// 完整打完一局：轮次推进、敌情图累积、最后判出胜负
func TestPlayFullMatch(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	seq := func() []string {
		out := make([]string, 0, 100)
		for r := 0; r < 10; r++ {
			for c := 0; c < 10; c++ {
				out = append(out, game.NewCell(r, c).Name())
			}
		}
		return out
	}
	order := [2][]string{seq(), seq()}
	cursor := [2]int{}
	clients := [2]*Client{a, b}
	caps := [2]*capture{capA, capB}

	for guard := 0; guard < 260; guard++ {
		rm, ok := caps[0].lastOf("room")
		if !ok {
			t.Fatal("没有房间消息")
		}
		v := asRoom(t, rm)
		if v.Finished {
			if v.Winner != 0 && v.Winner != 1 {
				t.Fatalf("胜者异常: %d", v.Winner)
			}
			// 双方都应该看到结束状态
			rmB, _ := caps[1].lastOf("room")
			if !asRoom(t, rmB).Finished {
				t.Fatal("对手没有收到结束状态")
			}
			return
		}
		turn := v.Turn
		if turn != 0 && turn != 1 {
			t.Fatalf("轮次异常: %d", turn)
		}
		if cursor[turn] >= 100 {
			t.Fatalf("玩家 %d 已无格可打但没结束", turn)
		}
		cell := order[turn][cursor[turn]]
		cursor[turn]++
		doSend(h, clients[turn], map[string]any{"t": "shot", "cell": cell})
	}
	t.Fatal("对局没有在合理步数内结束")
}

// 不是自己的回合不能开火
func TestOutOfTurnRejected(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")

	doSend(h, a, map[string]any{"t": "invite", "to": "乙"})
	inv, _ := capB.lastOf("invite")
	doSend(h, b, map[string]any{"t": "invite.reply", "id": inv["id"].(string), "accept": true})

	// 先确认当前是谁的回合（先手是甲）
	doSend(h, b, map[string]any{"t": "shot", "cell": "A1"})
	msg, ok := capB.lastOf("error")
	if !ok {
		t.Fatal("抢跑应被拒绝")
	}
	if s, _ := msg["msg"].(string); s == "" {
		t.Fatal("错误消息为空")
	}
	_ = capA
}

// 建房 + 房间码加入
func TestCreateAndJoinByCode(t *testing.T) {
	h := newTestHub(t)
	a, capA := join(h, "甲")
	b, capB := join(h, "乙")

	doSend(h, a, map[string]any{"t": "room.create"})
	rm, ok := capA.lastOf("room")
	if !ok {
		t.Fatal("建房后应收到房间状态")
	}
	v := asRoom(t, rm)
	if v.State != "waiting" || v.Code == "" {
		t.Fatalf("应为等待状态且有房间码：%s %q", v.State, v.Code)
	}

	doSend(h, b, map[string]any{"t": "room.join", "code": v.Code})
	rmB, ok := capB.lastOf("room")
	if !ok {
		t.Fatal("加入后应收到房间状态")
	}
	if asRoom(t, rmB).State != "playing" {
		t.Fatal("加入后应直接开局")
	}
}

func asRoom(t *testing.T, m map[string]any) roomView {
	t.Helper()
	raw, err := json.Marshal(m["room"])
	if err != nil {
		t.Fatalf("房间消息序列化失败: %v", err)
	}
	var v roomView
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("房间消息解析失败: %v", err)
	}
	return v
}