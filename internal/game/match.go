package game

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrFinished    = errors.New("对局已经结束了")
	ErrNotYourTurn = errors.New("还没轮到你喊号")
	ErrRepeated    = errors.New("这一格已经打过了")
	ErrNotPlayer   = errors.New("没有这个玩家")
)

type Feedback int

const (
	FBEmpty Feedback = iota // 空
	FBHit                   // 中（含死机残格的「中」，若开了迷惑规则）
	FBKill                  // 死（机头命中，该架当场报废）
)

func (f Feedback) String() string {
	switch f {
	case FBEmpty:
		return "空"
	case FBHit:
		return "中"
	default:
		return "死"
	}
}

func (f Feedback) MarshalJSON() ([]byte, error) { return json.Marshal(f.String()) }

// Move 是一步喊号记录，用于回放与复算校验。
type Move struct {
	By   int      `json:"by"`
	Cell Cell     `json:"cell"`
	FB   Feedback `json:"fb"`
}

type FireResult struct {
	By          int
	Cell        Cell
	FB          Feedback
	DeadPlane   int // -1 表示这一发没有打死飞机
	Turn        int
	Finished    bool
	Winner      int
	AliveTarget int // 对手还剩几架活飞机
}

type Match struct {
	rules    Rules
	boards   [2]*Board
	shot     [2][100]bool
	turn     int
	moves    []Move
	finished bool
	winner   int
}

func NewMatch(r Rules, p1, p2 *Board, first int) (*Match, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if p1 == nil || p2 == nil || !p1.Ready() || !p2.Ready() {
		return nil, ErrNotReady
	}
	if first != 0 && first != 1 {
		return nil, ErrNotPlayer
	}
	return &Match{rules: r, boards: [2]*Board{p1, p2}, turn: first, winner: -1}, nil
}

func (m *Match) Rules() Rules       { return m.rules }
func (m *Match) Turn() int          { return m.turn }
func (m *Match) Finished() bool     { return m.finished }
func (m *Match) Winner() int        { return m.winner }
func (m *Match) Moves() []Move      { return m.moves }
func (m *Match) Board(p int) *Board { return m.boards[p] }
func (m *Match) Shot(p int, c Cell) bool {
	return c.Valid() && m.shot[p][c]
}

// Alive 返回该玩家还剩几架活飞机。
func (m *Match) Alive(p int) int {
	n := 0
	for i := 0; i < m.boards[p].Count(); i++ {
		if !m.boards[p].Plane(i).Dead() {
			n++
		}
	}
	return n
}

func (m *Match) allDead(p int) bool {
	for i := 0; i < m.boards[p].Count(); i++ {
		if !m.boards[p].Plane(i).Dead() {
			return false
		}
	}
	return m.boards[p].Count() > 0
}

// Fire 是裁判核心：校验轮次 → 判定 → 处理连击 → 判胜负。
func (m *Match) Fire(by int, c Cell) (FireResult, error) {
	if m.finished {
		return FireResult{}, ErrFinished
	}
	if by < 0 || by > 1 {
		return FireResult{}, ErrNotPlayer
	}
	if !c.Valid() {
		return FireResult{}, ErrOutOfBounds
	}
	if by != m.turn {
		return FireResult{}, ErrNotYourTurn
	}
	if m.shot[by][c] {
		return FireResult{}, ErrRepeated
	}
	m.shot[by][c] = true

	target := m.boards[1-by]
	res := FireResult{By: by, Cell: c, DeadPlane: -1, Turn: m.turn}
	fb := FBEmpty

	if target.Occupied(c) {
		idx := target.PlaneAt(c)
		p := target.Plane(idx)
		p.hits++
		switch {
		case p.dead:
			// 已经报废的飞机：老实版答「空」，迷惑版继续答「中」
			if m.rules.DeceptionOnDeadPlane {
				fb = FBHit
			}
		case target.IsHeadCell(c):
			p.dead = true
			fb = FBKill
			res.DeadPlane = idx
		default:
			fb = FBHit
			// 非「只打机头」规则：机身 9 格全被打中也算击落（机头未被击中的情形）
			if !m.rules.KillByHeadOnly && p.hits == 9 {
				p.dead = true
			}
		}
	}
	res.FB = fb
	m.moves = append(m.moves, Move{By: by, Cell: c, FB: fb})

	// 连击：打空才换人（关闭连击时每发都换人）
	if !(m.rules.ContinueOnHit && fb != FBEmpty) {
		m.turn = 1 - m.turn
	}
	if m.allDead(1 - by) {
		m.finished = true
		m.winner = by
	}
	res.Turn = m.turn
	res.Finished = m.finished
	res.Winner = m.winner
	res.AliveTarget = m.Alive(1 - by)
	return res, nil
}

// Replay 用「布阵 + 喊号序列」复算整局；与记录的反馈不一致即说明数据被改过。
func Replay(r Rules, p1, p2 []Placement, moves []Move, first int) (*Match, error) {
	b1, err := BuildFrom(r, p1)
	if err != nil {
		return nil, fmt.Errorf("玩家1布阵无效: %w", err)
	}
	b2, err := BuildFrom(r, p2)
	if err != nil {
		return nil, fmt.Errorf("玩家2布阵无效: %w", err)
	}
	m, err := NewMatch(r, b1, b2, first)
	if err != nil {
		return nil, err
	}
	for i, mv := range moves {
		res, err := m.Fire(mv.By, mv.Cell)
		if err != nil {
			return nil, fmt.Errorf("第 %d 步无效: %w", i+1, err)
		}
		if res.FB != mv.FB {
			return nil, fmt.Errorf("第 %d 步 %s 记录为 %s，复算为 %s", i+1, mv.Cell.Name(), mv.FB, res.FB)
		}
	}
	return m, nil
}
