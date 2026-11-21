package app

import (
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"planeduel/internal/game"
)

const (
	inviteTTL    = 20 * time.Second
	offlineGrace = 120 * time.Second // 掉线后 2 分钟内回来都算继续

	defaultTurnSec = 300 // 默认每回合 5 分钟
	minTurnSec     = 60
	maxTurnSec     = 900
	idleLimit   = 150 * time.Second
	roomCodeLen = 6
)

// Client 是一条 WebSocket 连接（一个已登录昵称）。
// Token 用来判断"同一个玩家换了连接"——重连时据此接管原房间。
type Client struct {
	Nick     string
	Token    string
	send     func([]byte) error
	kill     func()
	room     *Room
	specRoom *Room // 正在观战的房间
	lastSeen time.Time
}

type invite struct {
	ID        string
	From      string
	To        string
	TurnSec   int
	ContinueOnHit bool // true=打中连击；false=每发换人
	Expires   time.Time
}

// Room 是一间双人对局房。玩家按 token 绑定，断线重连后自动接管自己的座位。
type Room struct {
	ID    string
	Code  string
	Rules game.Rules

	mu       sync.Mutex
	clients  [2]*Client
	tokens   [2]string
	specs    []*Client
	names    [2]string
	goneAt   [2]time.Time
	m         *game.Match
	turnLimit time.Duration // 每回合时限（3 或 5 分钟），每次换手重新计时
	state     string        // waiting / playing / done
	winner    int
	endReason string // win / timeout / offline / leave
	deadline  time.Time
}

type Hub struct {
	mu      sync.Mutex
	clients map[string]*Client
	rooms   map[string]*Room
	invites map[string]*invite
	store   *SessionStore
	rng     *rand.Rand
}

func NewHub(store *SessionStore, seed int64) *Hub {
	return &Hub{
		clients: map[string]*Client{},
		rooms:   map[string]*Room{},
		invites: map[string]*invite{},
		store:   store,
		rng:     rand.New(rand.NewSource(seed)),
	}
}

// ---------------------------------------------------------------- 连接

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	tok := sessionTokenFrom(r)
	if tok == "" {
		http.Error(w, "请先设置昵称再对战", http.StatusUnauthorized)
		return
	}
	sess, ok := h.store.Get(tok)
	if !ok {
		http.Error(w, "登录已过期，请刷新页面", http.StatusUnauthorized)
		return
	}

	ws, err := upgradeWS(w, r)
	if err != nil {
		http.Error(w, "WebSocket 升级失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	cl := &Client{
		Nick: sess.Nick, Token: tok, lastSeen: time.Now(),
		send: ws.WriteText, kill: func() { _ = ws.Close() },
	}

	h.mu.Lock()
	// 同一个身份（同一 token）已经有一个活连接：直接拒绝新连接，
	// 免得两个窗口互相踢、界面看着像"点了没反应"。
	if old, exists := h.clients[cl.Nick]; exists && old.Token == cl.Token {
		h.mu.Unlock()
		_ = ws.WriteText([]byte(`{"t":"dup","msg":"这个身份已经在另一个窗口打开了，请先把其中一个窗口改个昵称（比如后面加个数字）"}`))
		_ = ws.WriteClose()
		_ = ws.Close()
		return
	}
	if old, exists := h.clients[cl.Nick]; exists && old.Token != cl.Token {
		// 同昵称但不同身份：把旧连接关掉，避免两个窗口抢同一个昵称
		old.kill()
	}
	h.clients[cl.Nick] = cl

	// 断线重连：把原先属于这个 token 的座位接管回来
	var resumed *Room
	for _, rm := range h.rooms {
		if rm.bind(cl) {
			resumed = rm
			break
		}
	}
	h.mu.Unlock()

	h.broadcastPlayers()
	h.send(cl, map[string]any{"t": "welcome", "nick": cl.Nick})
	if resumed != nil {
		h.sendRoom(resumed, cl)
	}

	go func() {
		defer func() {
			h.mu.Lock()
			if cur, ok := h.clients[cl.Nick]; ok && cur == cl {
				delete(h.clients, cl.Nick)
			}
			h.markGone(cl)
			h.mu.Unlock()
			h.broadcastPlayers()
			ws.Close()
		}()

		for {
			_ = ws.SetReadDeadline(time.Now().Add(idleLimit))
			op, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			switch op {
			case 0x8: // close
				return
			case 0x9: // ping
				_ = ws.write(0xA, data)
			case 0xA:
				// pong，忽略
			case 0x1:
				cl.lastSeen = time.Now()
				h.handle(cl, data)
			}
		}
	}()
}

// bind 让新连接接管同一 token 的旧座位（断线重连用）。
// 只在「正在对局」时生效：已结束/等待中的房间不应该把玩家重新拉回去，
// 否则关掉浏览器再打开，会莫名其妙回到上一局的残局界面。
func (r *Room) bind(c *Client) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != "playing" {
		return false
	}
	for i := 0; i < 2; i++ {
		if r.tokens[i] != "" && r.tokens[i] == c.Token {
			r.clients[i] = c
			r.names[i] = c.Nick
			r.goneAt[i] = time.Time{}
			c.room = r
			return true
		}
	}
	return false
}

// markGone 标记连接断开：只记时间，不立刻判负 —— 给重连留宽限。
// TokenOnline 表示该身份当前是否有活跃的 WebSocket 连接。
func (h *Hub) TokenOnline(token string) bool {
	if token == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, cl := range h.clients {
		if cl.Token == token {
			return true
		}
	}
	return false
}

func (h *Hub) markGone(c *Client) {
	if sr := c.specRoom; sr != nil {
		sr.mu.Lock()
		for i, sp := range sr.specs {
			if sp == c {
				sr.specs = append(sr.specs[:i], sr.specs[i+1:]...)
				break
			}
		}
		sr.mu.Unlock()
		c.specRoom = nil
	}
	r := c.room
	if r == nil {
		return
	}
	r.mu.Lock()
	for i := 0; i < 2; i++ {
		if r.clients[i] == c {
			r.clients[i] = nil
			r.goneAt[i] = time.Now()
		}
	}
	empty := r.clients[0] == nil && r.clients[1] == nil
	if r.state == "waiting" || empty {
		r.state = "done"
		delete(h.rooms, r.ID)
	}
	r.mu.Unlock()
	c.room = nil
}

// ---------------------------------------------------------------- 消息

func (h *Hub) handle(c *Client, data []byte) {
	var in struct {
		T      string `json:"t"`
		To     string `json:"to"`
		ID     string `json:"id"`
		Code   string `json:"code"`
		Cell   string `json:"cell"`
		Accept bool   `json:"accept"`
		TurnSec int   `json:"turn_sec"`
		Continue *bool `json:"continue_on_hit"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		h.sendErr(c, "消息格式不对")
		return
	}
	switch in.T {
	case "ping":
		h.send(c, map[string]any{"t": "pong"})
	case "invite":
		h.doInvite(c, in.To, in.TurnSec, in.Continue)
	case "invite.reply":
		h.doInviteReply(c, in.ID, in.Accept)
	case "room.create":
		h.doCreate(c, in.TurnSec, in.Continue)
	case "room.join":
		h.doJoin(c, in.Code)
	case "room.spectate":
		h.doSpectate(c, in.Code)
	case "room.leave":
		h.doLeave(c)
	case "shot":
		h.doShot(c, in.Cell)
	}
}

// freeIfDoneLocked 释放「上一局已结束但还没点离开」的占位。
// 不这么做的话，玩家会被永久卡在旧房间里，再次邀请永远提示「有人已经在别的房间里了」。
// 调用方必须持有 h.mu。
func (h *Hub) freeIfDoneLocked(c *Client) {
	if sr := c.specRoom; sr != nil {
		sr.mu.Lock()
		for i, sp := range sr.specs {
			if sp == c {
				sr.specs = append(sr.specs[:i], sr.specs[i+1:]...)
				break
			}
		}
		sr.mu.Unlock()
		c.specRoom = nil
	}
	r := c.room
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.state == "playing" {
		r.mu.Unlock()
		return // 正在打，绝不释放
	}
	for i := 0; i < 2; i++ {
		if r.clients[i] == c {
			r.clients[i] = nil
			r.tokens[i] = ""
		}
	}
	if r.clients[0] == nil && r.clients[1] == nil {
		delete(h.rooms, r.ID)
	}
	r.mu.Unlock()
	c.room = nil
}

func (h *Hub) doInvite(c *Client, to string, turnSec int, cont *bool) {
	if to == "" || to == c.Nick {
		h.sendErr(c, "选一个对手吧")
		return
	}
	h.mu.Lock()
	h.freeIfDoneLocked(c) // 上一局已结束的话先释放，否则永远提示"正在对局中"
	target, ok := h.clients[to]
	if !ok {
		h.mu.Unlock()
		h.sendErr(c, "对方不在线")
		return
	}
	h.freeIfDoneLocked(target)
	if c.room != nil || target.room != nil {
		h.mu.Unlock()
		h.sendErr(c, "你或对方正在对局中")
		return
	}
	id := RandomToken(6)
	h.invites[id] = &invite{
		ID: id, From: c.Nick, To: to, TurnSec: clampTurn(turnSec),
		ContinueOnHit: cont != nil && *cont,
		Expires:       time.Now().Add(inviteTTL),
	}
	h.mu.Unlock()

	h.send(target, map[string]any{"t": "invite", "id": id, "from": c.Nick})
	h.send(c, map[string]any{"t": "invite.sent", "to": to})
}

func (h *Hub) doInviteReply(c *Client, id string, accept bool) {
	h.mu.Lock()
	h.freeIfDoneLocked(c)
	inv, ok := h.invites[id]
	if !ok || inv.To != c.Nick {
		h.mu.Unlock()
		h.sendErr(c, "邀请已失效")
		return
	}
	delete(h.invites, id)
	from, fromOK := h.clients[inv.From]
	if fromOK {
		h.freeIfDoneLocked(from)
	}

	if !accept {
		h.mu.Unlock()
		if fromOK {
			h.send(from, map[string]any{"t": "invite.declined", "by": c.Nick})
		}
		return
	}
	if !fromOK {
		h.mu.Unlock()
		h.sendErr(c, "对方已经离线了")
		return
	}
	if from.room != nil || c.room != nil {
		h.mu.Unlock()
		h.sendErr(c, "有人已经在别的房间里了")
		return
	}
	r := h.newRoomLocked(from, c)
	r.turnLimit = time.Duration(clampTurn(inv.TurnSec)) * time.Second
	r.Rules.ContinueOnHit = inv.ContinueOnHit
	h.mu.Unlock()

	h.startMatch(r)
}

func (h *Hub) doCreate(c *Client, turnSec int, cont *bool) {
	h.mu.Lock()
	h.freeIfDoneLocked(c)
	if c.room != nil {
		h.mu.Unlock()
		h.sendErr(c, "你已经在房间里了")
		return
	}
	r := h.newRoomLocked(c, nil)
	r.turnLimit = time.Duration(clampTurn(turnSec)) * time.Second
	r.Rules.ContinueOnHit = cont != nil && *cont
	h.mu.Unlock()
	h.sendRoom(r, c)
}

func (h *Hub) doJoin(c *Client, code string) {
	h.mu.Lock()
	h.freeIfDoneLocked(c)
	if c.room != nil {
		h.mu.Unlock()
		h.sendErr(c, "你已经在房间里了")
		return
	}
	want := strings.ToUpper(strings.TrimSpace(code))
	var room *Room
	for _, r := range h.rooms {
		if strings.EqualFold(r.Code, want) {
			room = r
			break
		}
	}
	if room == nil {
		h.mu.Unlock()
		h.sendErr(c, "房间不存在，检查一下房间码")
		return
	}
	room.mu.Lock()
	waiting := room.state == "waiting"
	if waiting {
		room.clients[1] = c
		room.tokens[1] = c.Token
		room.names[1] = c.Nick
	}
	room.mu.Unlock()
	h.mu.Unlock()

	if !waiting {
		// 房间满 2 人（或已结束）：直接转为旁观，不再让用户手动点按钮
		h.send(c, map[string]any{"t": "info", "msg": "这局已经开局（2 人满），已自动进入旁观"})
		h.doSpectate(c, want)
		return
	}
	c.room = room
	h.startMatch(room)
}

func (h *Hub) doLeave(c *Client) {
	// 观战者退出：只把自己从观战列表里摘掉
	if sr := c.specRoom; sr != nil {
		sr.mu.Lock()
		for i, sp := range sr.specs {
			if sp == c {
				sr.specs = append(sr.specs[:i], sr.specs[i+1:]...)
				break
			}
		}
		sr.mu.Unlock()
		c.specRoom = nil
		h.send(c, map[string]any{"t": "room.left"})
		h.broadcastSpectate(sr)
		return
	}
	h.mu.Lock()
	r := c.room
	var other *Client
	if r != nil {
		r.mu.Lock()
		idx := -1
		for i := 0; i < 2; i++ {
			if r.clients[i] == c {
				idx = i
			}
		}
		if idx >= 0 {
			if r.state == "playing" {
				r.state = "done"
				r.winner = 1 - idx // 主动退出算认输
				r.endReason = "leave"
			}
			other = r.clients[1-idx]
			r.clients[idx] = nil
			r.tokens[idx] = ""
		}
		r.mu.Unlock()
		delete(h.rooms, r.ID)
		c.room = nil
	}
	h.mu.Unlock()

	if other != nil {
		h.send(other, map[string]any{"t": "error", "msg": "对手离开了房间"})
		h.sendRoom(r, other)
	}
	h.broadcastPlayers()
	h.send(c, map[string]any{"t": "room.left"})
}

func (h *Hub) doShot(c *Client, cellName string) {
	room := c.room
	if room == nil {
		if c.specRoom != nil {
			h.sendErr(c, "观战者不能出手，等这局结束再上桌")
		} else {
			h.sendErr(c, "你还没有进入房间")
		}
		return
	}
	cell, err := game.ParseCell(cellName)
	if err != nil {
		h.sendErr(c, err.Error())
		return
	}
	room.mu.Lock()
	if room.state != "playing" || room.m == nil {
		room.mu.Unlock()
		h.sendErr(c, "对局还没开始或已结束")
		return
	}
	me := -1
	for i := 0; i < 2; i++ {
		if room.clients[i] == c {
			me = i
		}
	}
	if me < 0 {
		room.mu.Unlock()
		h.sendErr(c, "你不在这个房间里")
		return
	}
	if room.m.Turn() != me {
		room.mu.Unlock()
		h.sendErr(c, "还没轮到你")
		return
	}
	res, err := room.m.Fire(me, cell)
	if err != nil {
		room.mu.Unlock()
		h.sendErr(c, err.Error())
		return
	}
	// 每一发（含连击）都重新计时：换手后对方也有完整时限
	room.deadline = time.Now().Add(room.turnLimit)
	if res.Finished {
		room.state = "done"
		room.winner = res.Winner
		room.endReason = "win"
	}
	other := room.clients[1-me]
	room.mu.Unlock()

	h.sendRoom(room, c)
	if other != nil {
		h.sendRoom(room, other)
	}
	h.broadcastSpectate(room)
}

// ---------------------------------------------------------------- 房间与推送

func (h *Hub) newRoomLocked(a, b *Client) *Room {
	id := RandomToken(8)
	code := randomRoomCode(roomCodeLen)
	for h.codeTaken(code) {
		code = randomRoomCode(roomCodeLen)
	}
	rules := game.DefaultRules()
	rules.ContinueOnHit = false // 默认每发换人；想要连击的开局时选
	r := &Room{
		ID: id, Code: code, Rules: rules, state: "waiting", winner: -1,
		turnLimit: defaultTurnSec * time.Second,
	}
	r.clients[0] = a
	r.tokens[0] = a.Token
	r.names[0] = a.Nick
	if b != nil {
		r.clients[1] = b
		r.tokens[1] = b.Token
		r.names[1] = b.Nick
	}
	a.room = r
	if b != nil {
		b.room = r
	}
	h.rooms[id] = r
	return r
}

func (h *Hub) startMatch(r *Room) {
	r.mu.Lock()
	if r.clients[0] == nil || r.clients[1] == nil {
		r.mu.Unlock()
		return
	}
	b0, b1 := game.NewBoard(r.Rules), game.NewBoard(r.Rules)
	err0 := b0.Randomize(h.rng)
	err1 := b1.Randomize(h.rng)

	var err error
	if err0 == nil && err1 == nil {
		var m *game.Match
		m, err = game.NewMatch(r.Rules, b0, b1, 0)
		if err == nil {
			r.m = m
			r.state = "playing"
			r.deadline = time.Now().Add(r.turnLimit) // 开局计时
		}
	}
	c0, c1 := r.clients[0], r.clients[1]
	r.mu.Unlock()

	if err0 != nil || err1 != nil || err != nil {
		if c0 != nil {
			h.sendErr(c0, "开局失败，请重试")
		}
		if c1 != nil {
			h.sendErr(c1, "开局失败，请重试")
		}
		return
	}
	h.sendRoom(r, c0)
	h.sendRoom(r, c1)
	h.broadcastSpectate(r)
	h.broadcastPlayers()
}

type roomView struct {
	ID         string      `json:"id"`
	Code       string      `json:"code"`
	State      string      `json:"state"`
	Me         int         `json:"me"`
	Names      [2]string   `json:"names"`
	Turn       int         `json:"turn"`
	Finished   bool        `json:"finished"`
	Winner     int         `json:"winner"`
	Alive      [2]int      `json:"alive"`
	MyCells    []string    `json:"my_cells"`
	MyMarks    []mark      `json:"my_marks"`
	EnemyMarks []mark      `json:"enemy_marks"`
	LastShot   *mark       `json:"last_shot,omitempty"`
	DeadlineMS int64       `json:"deadline_ms"`
	Reason     string      `json:"reason,omitempty"`
	TurnSec    int         `json:"turn_sec"`
	ContinueOnHit bool     `json:"continue_on_hit"`
}

func (h *Hub) sendRoom(r *Room, c *Client) {
	if c == nil {
		return
	}
	r.mu.Lock()
	me := -1
	for i := 0; i < 2; i++ {
		if r.clients[i] == c {
			me = i
		}
	}
	if me < 0 {
		r.mu.Unlock()
		return
	}
	v := roomView{
		ID: r.ID, Code: r.Code, State: r.state, Me: me, Names: r.names,
		Turn: -1, Winner: r.winner, Reason: r.endReason, TurnSec: int(r.turnLimit / time.Second),
		ContinueOnHit: r.Rules.ContinueOnHit,
		MyCells: []string{}, MyMarks: []mark{}, EnemyMarks: []mark{},
	}
	if !r.deadline.IsZero() {
		v.DeadlineMS = r.deadline.UnixMilli()
	}
	if r.m != nil {
		v.Turn = r.m.Turn()
		v.Finished = r.m.Finished()
		v.Alive = [2]int{r.m.Alive(0), r.m.Alive(1)}
		mine := r.m.Board(me)
		for i := 0; i < mine.Count(); i++ {
			for _, cell := range mine.Plane(i).Cells {
				v.MyCells = append(v.MyCells, cell.Name())
			}
		}
		moves := r.m.Moves()
		for _, mv := range moves {
			mk := mark{Cell: mv.Cell.Name(), FB: mv.FB.String(), By: mv.By}
			if mv.By == me {
				v.EnemyMarks = append(v.EnemyMarks, mk)
			} else {
				v.MyMarks = append(v.MyMarks, mk)
			}
		}
		if n := len(moves); n > 0 {
			mk := mark{Cell: moves[n-1].Cell.Name(), FB: moves[n-1].FB.String(), By: moves[n-1].By}
			v.LastShot = &mk
		}
	}
	r.mu.Unlock()

	h.send(c, map[string]any{"t": "room", "room": v})
}

func (h *Hub) send(c *Client, payload map[string]any) {
	if c == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = c.send(b)
}

func (h *Hub) sendErr(c *Client, msg string) {
	h.send(c, map[string]any{"t": "error", "msg": msg})
}

type playerInfo struct {
	Nick   string `json:"nick"`
	Online bool   `json:"online"`
	Me     bool   `json:"me"`
	Busy   bool   `json:"busy"`
}

func (h *Hub) broadcastPlayers() {
	h.mu.Lock()
	list := make([]playerInfo, 0, len(h.clients))
	for _, c := range h.clients {
		list = append(list, playerInfo{Nick: c.Nick, Online: true, Busy: c.room != nil})
	}
	targets := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.Unlock()

	// 在线列表按昵称排序，稳定一些
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].Nick < list[i].Nick {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	for _, c := range targets {
		cp := make([]playerInfo, len(list))
		copy(cp, list)
		for i := range cp {
			cp[i].Me = cp[i].Nick == c.Nick
		}
		h.send(c, map[string]any{"t": "players", "list": cp})
	}
}

// Tick 处理邀请过期、掉线宽限期与回合超时，由 main 每秒调用一次。
func (h *Hub) Tick() {
	now := time.Now()

	h.mu.Lock()
	for id, inv := range h.invites {
		if now.After(inv.Expires) {
			delete(h.invites, id)
			if from, ok := h.clients[inv.From]; ok {
				h.send(from, map[string]any{"t": "invite.expired", "to": inv.To})
			}
		}
	}
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	for _, r := range rooms {
		r.mu.Lock()
		var c0, c1 *Client
		changed := false
		reason := ""

		if r.state == "playing" {
			// 1) 掉线超过宽限期 → 判负
			for i := 0; i < 2; i++ {
				if r.clients[i] == nil && !r.goneAt[i].IsZero() && now.Sub(r.goneAt[i]) > offlineGrace {
					r.state = "done"
					r.winner = 1 - i
					r.endReason = "offline"
					changed = true
					reason = "offline"
				}
			}
			// 2) 回合超时 → 当前出手方判负
			if !changed && !r.deadline.IsZero() && now.After(r.deadline) {
				turn := 0
				if r.m != nil {
					turn = r.m.Turn()
				}
				r.state = "done"
				r.winner = 1 - turn
				r.endReason = "timeout"
				r.deadline = time.Time{}
				changed = true
				reason = "timeout"
			}
		}
		c0, c1 = r.clients[0], r.clients[1]
		empty := c0 == nil && c1 == nil
		dead := r.state == "done" && empty
		winner := r.winner
		r.mu.Unlock()

		if changed {
			msg := map[string]any{"t": "timeout", "reason": reason, "winner": winner}
			if c0 != nil {
				h.send(c0, msg)
				h.sendRoom(r, c0)
			}
			if c1 != nil {
				h.send(c1, msg)
				h.sendRoom(r, c1)
			}
		}
		if dead {
			h.mu.Lock()
			delete(h.rooms, r.ID)
			h.mu.Unlock()
		}
	}
}

var errNoRoom = errors.New("房间不存在")

// clampTurn 把回合时限夹到 1-15 分钟，默认 5 分钟。
func clampTurn(sec int) int {
	if sec <= 0 {
		return defaultTurnSec
	}
	if sec < minTurnSec {
		return minTurnSec
	}
	if sec > maxTurnSec {
		return maxTurnSec
	}
	return sec
}
// 房间码字母表：去掉 I L O 0 1，避免看错。
const roomCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func randomRoomCode(n int) string {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = roomCodeAlphabet[int(v)%len(roomCodeAlphabet)]
	}
	return string(out)
}

// codeTaken 检查房间码是否已被占用（调用方持有 h.mu）。
func (h *Hub) codeTaken(code string) bool {
	for _, r := range h.rooms {
		if r.Code == code {
			return true
		}
	}
	return false
}
// ---------------------------------------------------------------- 观战

type spectateView struct {
	Code     string     `json:"code"`
	Names    [2]string  `json:"names"`
	State    string     `json:"state"`
	Turn     int        `json:"turn"`
	Finished bool       `json:"finished"`
	Winner   int        `json:"winner"`
	Alive    [2]int     `json:"alive"`
	Shots    []mark     `json:"shots"`            // 公共信息：谁打了哪格、结果
	Reveal   [2][]string `json:"reveal"`          // 结束后才给出双方布阵
	Viewers  int        `json:"viewers"`
}

func (h *Hub) doSpectate(c *Client, code string) {
	want := strings.ToUpper(strings.TrimSpace(code))
	if want == "" {
		h.sendErr(c, "请输入房间码")
		return
	}
	h.mu.Lock()
	var room *Room
	for _, r := range h.rooms {
		if strings.EqualFold(r.Code, want) {
			room = r
			break
		}
	}
	if room == nil {
		h.mu.Unlock()
		h.sendErr(c, "找不到这个房间（可能已经结束）")
		return
	}
	room.mu.Lock()
	if len(room.specs) >= 20 {
		room.mu.Unlock()
		h.mu.Unlock()
		h.sendErr(c, "观战人数已满")
		return
	}
	room.specs = append(room.specs, c)
	room.mu.Unlock()
	h.mu.Unlock()

	c.specRoom = room
	h.broadcastSpectate(room) // 新观战者也在列表里，一次广播即可
}

func (h *Hub) broadcastSpectate(r *Room) {
	r.mu.Lock()
	specs := append([]*Client(nil), r.specs...)
	r.mu.Unlock()
	for _, sp := range specs {
		h.sendSpectate(r, sp)
	}
}

func (h *Hub) sendSpectate(r *Room, c *Client) {
	r.mu.Lock()
	v := spectateView{
		Code: r.Code, Names: r.names, State: r.state, Turn: -1, Winner: r.winner,
		Alive: [2]int{}, Shots: []mark{}, Reveal: [2][]string{{}, {}}, Viewers: len(r.specs),
	}
	if r.m != nil {
		v.Turn = r.m.Turn()
		v.Finished = r.m.Finished()
		v.Alive = [2]int{r.m.Alive(0), r.m.Alive(1)}
		for _, mv := range r.m.Moves() {
			v.Shots = append(v.Shots, mark{Cell: mv.Cell.Name(), FB: mv.FB.String(), By: mv.By})
		}
		if r.m.Finished() {
			for p := 0; p < 2; p++ {
				b := r.m.Board(p)
				for i := 0; i < b.Count(); i++ {
					for _, cell := range b.Plane(i).Cells {
						v.Reveal[p] = append(v.Reveal[p], cell.Name())
					}
				}
			}
		}
	} else {
		v.Alive = [2]int{3, 3}
	}
	r.mu.Unlock()

	h.send(c, map[string]any{"t": "spectate", "spectate": v})
}