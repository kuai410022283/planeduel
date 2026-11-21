package app

import (
	"errors"
	"math/rand"
	"sync"
	"time"

	"planeduel/internal/game"
)

var (
	errFinished       = errors.New("对局已经结束了")
	errNoBotRoom      = errors.New("机器人没格子可打了")
	errMatchNotFound  = errors.New("对局不存在或已过期")
)

type mark struct {
	Cell string `json:"cell"`
	FB   string `json:"fb"`
	By   int    `json:"by"` // 0=我打出的，1=对手打出的
}

// Solo 是「单人视角」的一局：玩家 0 是我，玩家 1 是陪练机器人。
// 视角规则与纸面对战纸一致：我能看到自己的布阵图，敌情图只记录我打出的结果。
type Solo struct {
	ID    string
	Me    string
	Rules game.Rules

	mu      sync.Mutex
	m       *game.Match
	rng     *rand.Rand
	botQ    []game.Cell
	smart   bool // 是否开启「命中后试探相邻」的智能陪练（默认关闭，避免比人还强）
	last    []mark
	myShots int
	myHits  int
	botShot int
	botHit  int

	created time.Time
}

type soloView struct {
	ID         string      `json:"id"`
	Me         string      `json:"me"`
	Rules      game.Rules  `json:"rules"`
	Finished   bool        `json:"finished"`
	Winner     int         `json:"winner"` // 0=我赢，1=机器人赢
	Alive      int         `json:"alive"`
	EnemyAlive int         `json:"enemy_alive"`
	MyPlanes   []string    `json:"my_planes"`   // 布阵图上的飞机格（打平，兼容旧前端）
	MyPlaneGroups []planeView `json:"my_plane_groups"` // 按机分组：每架的格子/机头/是否存活
	MyHeads    []string    `json:"my_heads"`    // 我的机头
	MyMarks    []mark      `json:"my_marks"`    // 对手打在我棋盘上的结果
	EnemyMarks []mark      `json:"enemy_marks"` // 我打在敌情图上的结果
	Last       []mark      `json:"last"`        // 刚才这一回合的经过
	Shots      int         `json:"shots"`
	Hits       int         `json:"hits"`
	BotShots   int         `json:"bot_shots"`
	BotHits    int         `json:"bot_hits"`
	CreatedAt  time.Time   `json:"created_at"`
}

type soloStore struct {
	mu  sync.Mutex
	m   map[string]*Solo
	rng *rand.Rand
}

func NewSoloStore(seed int64) *soloStore {
	return &soloStore{m: map[string]*Solo{}, rng: rand.New(rand.NewSource(seed))}
}

func (s *soloStore) New(rules game.Rules, me string) (*Solo, error) {
	if err := rules.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	mine, theirs := game.NewBoard(rules), game.NewBoard(rules)
	if err := mine.Randomize(s.rng); err != nil {
		return nil, err
	}
	if err := theirs.Randomize(s.rng); err != nil {
		return nil, err
	}
	m, err := game.NewMatch(rules, mine, theirs, 0) // 我先手
	if err != nil {
		return nil, err
	}
	solo := &Solo{
		ID: RandomToken(6), Me: me, Rules: rules, m: m,
		rng:     rand.New(rand.NewSource(s.rng.Int63())),
		created: time.Now(),
	}
	s.m[solo.ID] = solo
	return solo, nil
}

func (s *soloStore) Get(id string) (*Solo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	return v, ok
}

func (s *soloStore) GC(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-maxAge)
	n := 0
	for id, v := range s.m {
		if v.created.Before(cut) {
			delete(s.m, id)
			n++
		}
	}
	return n
}

// Fire 处理我的一发，然后让机器人回合自动打完。
func (s *Solo) Fire(c game.Cell) (soloView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.m.Finished() {
		return s.view(), errFinished
	}
	res, err := s.m.Fire(0, c)
	if err != nil {
		return s.view(), err
	}
	s.myShots++
	if res.FB != game.FBEmpty {
		s.myHits++
	}
	s.last = []mark{{Cell: c.Name(), FB: res.FB.String(), By: 0}}

	// 机器人回合：连击规则下可能连打好几发
	for guard := 0; guard < 200 && !s.m.Finished() && s.m.Turn() == 1; guard++ {
		bc, err := s.botPick()
		if err != nil {
			break
		}
		r2, err := s.m.Fire(1, bc)
		if err != nil {
			break
		}
		s.botShot++
		if r2.FB != game.FBEmpty {
			s.botHit++
			if s.smart {
				s.pushNeighbors(bc)
			}
		}
		if r2.FB == game.FBKill {
			s.botQ = nil // 打死了就换个区域重新找
		}
		s.last = append(s.last, mark{Cell: bc.Name(), FB: r2.FB.String(), By: 1})
		if !s.Rules.ContinueOnHit || r2.FB == game.FBEmpty {
			break
		}
	}
	return s.view(), nil
}

// botPick 优先打已知命中点周围，否则随机找没打过的格子。
func (s *Solo) botPick() (game.Cell, error) {
	for len(s.botQ) > 0 {
		c := s.botQ[0]
		s.botQ = s.botQ[1:]
		if !s.m.Shot(1, c) {
			return c, nil
		}
	}
	order := s.rng.Perm(100)
	for _, v := range order {
		c := game.Cell(v)
		if !s.m.Shot(1, c) {
			return c, nil
		}
	}
	return 0, errNoBotRoom
}

func (s *Solo) pushNeighbors(c game.Cell) {
	r, col := c.Row(), c.Col()
	for _, d := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		rr, cc := r+d[0], col+d[1]
		if rr < 0 || rr > 9 || cc < 0 || cc > 9 {
			continue
		}
		n := game.NewCell(rr, cc)
		if !s.m.Shot(1, n) {
			s.botQ = append(s.botQ, n)
		}
	}
}

func (s *Solo) view() soloView {
	v := soloView{
		ID: s.ID, Me: s.Me, Rules: s.Rules,
		Finished: s.m.Finished(), Winner: s.m.Winner(),
		Alive: s.m.Alive(0), EnemyAlive: s.m.Alive(1),
		Shots: s.myShots, Hits: s.myHits, BotShots: s.botShot, BotHits: s.botHit,
		Last: s.last, CreatedAt: s.created,
		MyPlanes: []string{}, MyHeads: []string{}, MyMarks: []mark{}, EnemyMarks: []mark{},
	}
	mine := s.m.Board(0)
	groups := make([]planeView, 0, mine.Count())
	for i := 0; i < mine.Count(); i++ {
		p := mine.Plane(i)
		pv := planeView{Alive: !p.Dead(), Head: p.Head.Name(), Cells: make([]string, 0, len(p.Cells))}
		for _, c := range p.Cells {
			pv.Cells = append(pv.Cells, c.Name())
			v.MyPlanes = append(v.MyPlanes, c.Name())
		}
		v.MyHeads = append(v.MyHeads, p.Head.Name())
		groups = append(groups, pv)
	}
	v.MyPlaneGroups = groups
	for _, mv := range s.m.Moves() {
		mk := mark{Cell: mv.Cell.Name(), FB: mv.FB.String(), By: mv.By}
		if mv.By == 1 {
			v.MyMarks = append(v.MyMarks, mk)
		} else {
			v.EnemyMarks = append(v.EnemyMarks, mk)
		}
	}
	return v
}
