package game

import (
	"errors"
	"math/rand"
)

var (
	ErrOutOfBounds = errors.New("飞机超出棋盘")
	ErrOverlap     = errors.New("飞机重叠了")
	ErrTooClose    = errors.New("飞机贴在一起了（本局规则禁止相邻）")
	ErrTooMany     = errors.New("飞机数量已满")
	ErrNoRoom      = errors.New("随机布阵失败，请重试")
	ErrNotReady    = errors.New("双方都要先布好阵")
)

// Rules 是一局游戏的规则开关，对应纸上游戏里各地不同的「土规则」。
type Rules struct {
	Planes               int  `json:"planes"`                  // 飞机数量，一般 3
	NoTouch              bool `json:"no_touch"`                // 飞机之间不许贴边相邻
	ContinueOnHit        bool `json:"continue_on_hit"`         // 打中继续打（连击）
	DeceptionOnDeadPlane bool `json:"deception_on_dead_plane"` // 死机的残格继续答「中」
	KillByHeadOnly       bool `json:"kill_by_head_only"`       // 必须打中机头才算击落
}

func DefaultRules() Rules {
	return Rules{
		Planes:               3,
		NoTouch:              false,
		ContinueOnHit:        true,
		DeceptionOnDeadPlane: true,
		KillByHeadOnly:       true,
	}
}

func (r Rules) Validate() error {
	if r.Planes < 1 || r.Planes > 5 {
		return errors.New("飞机数量只能是 1-5 架")
	}
	return nil
}

// Placement 是一架飞机的摆放：包围盒左上角 + 朝向，可原样复算。
type Placement struct {
	Row    int         `json:"row"`
	Col    int         `json:"col"`
	Orient Orientation `json:"orient"`
}

type Plane struct {
	Cells     [10]Cell
	Head      Cell
	Placement Placement
	hits      int
	dead      bool
}

func (p *Plane) Dead() bool { return p.dead }
func (p *Plane) Hits() int  { return p.hits }

// Board 是一个玩家的棋盘（服务端权威，永不下发给对手）。
type Board struct {
	rules   Rules
	occ     [100]bool
	planeOf [100]int8
	isHead  [100]bool
	planes  []Plane
}

func NewBoard(r Rules) *Board {
	b := &Board{rules: r}
	for i := range b.planeOf {
		b.planeOf[i] = -1
	}
	return b
}

func (b *Board) Rules() Rules           { return b.rules }
func (b *Board) Count() int             { return len(b.planes) }
func (b *Board) Ready() bool            { return len(b.planes) == b.rules.Planes }
func (b *Board) Occupied(c Cell) bool   { return c.Valid() && b.occ[c] }
func (b *Board) IsHeadCell(c Cell) bool { return c.Valid() && b.isHead[c] }
func (b *Board) PlaneAt(c Cell) int     { return int(b.planeOf[c]) }

func (b *Board) Plane(i int) *Plane {
	if i < 0 || i >= len(b.planes) {
		return nil
	}
	return &b.planes[i]
}

// touching 判断格子的八邻域里是否已有飞机（NoTouch 规则用）。
func (b *Board) touching(c Cell) bool {
	r, col := c.Row(), c.Col()
	for dr := -1; dr <= 1; dr++ {
		for dc := -1; dc <= 1; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			rr, cc := r+dr, col+dc
			if rr < 0 || rr > 9 || cc < 0 || cc > 9 {
				continue
			}
			if b.occ[NewCell(rr, cc)] {
				return true
			}
		}
	}
	return false
}

// canPlace 只做校验，不改变棋盘。
func (b *Board) canPlace(row, col int, o Orientation) bool {
	if len(b.planes) >= b.rules.Planes {
		return false
	}
	cells, err := Cells(row, col, o)
	if err != nil {
		return false
	}
	for _, c := range cells {
		if b.occ[c] {
			return false
		}
	}
	if b.rules.NoTouch {
		for _, c := range cells {
			if b.touching(c) {
				return false
			}
		}
	}
	return true
}

// Place 校验并落下一架飞机；校验失败时棋盘不变。
func (b *Board) Place(row, col int, o Orientation) error {
	if len(b.planes) >= b.rules.Planes {
		return ErrTooMany
	}
	cells, err := Cells(row, col, o)
	if err != nil {
		return err
	}
	if !b.canPlace(row, col, o) {
		if b.rules.NoTouch && b.touchesNeighbor(cells) {
			return ErrTooClose
		}
		return ErrOverlap
	}
	idx := int8(len(b.planes))
	p := Plane{Head: cells[0], Placement: Placement{Row: row, Col: col, Orient: o}}
	for i, c := range cells {
		p.Cells[i] = c
		b.occ[c] = true
		b.planeOf[c] = idx
		if i == 0 {
			b.isHead[c] = true
		}
	}
	b.planes = append(b.planes, p)
	return nil
}

func (b *Board) touchesNeighbor(cells [10]Cell) bool {
	for _, c := range cells {
		if b.touching(c) {
			return true
		}
	}
	return false
}

// undoLast 撤回最后一架飞机（回溯随机布阵用）。
func (b *Board) undoLast() {
	if len(b.planes) == 0 {
		return
	}
	last := b.planes[len(b.planes)-1]
	for _, c := range last.Cells {
		b.occ[c] = false
		b.planeOf[c] = -1
		b.isHead[c] = false
	}
	b.planes = b.planes[:len(b.planes)-1]
}

func (b *Board) Reset() {
	*b = Board{rules: b.rules}
	for i := range b.planeOf {
		b.planeOf[i] = -1
	}
}

// Randomize 随机摆满所有飞机（服务端替玩家布阵用），失败会回溯重试。
func (b *Board) Randomize(rng *rand.Rand) error {
	b.Reset()
	return b.fillRandom(rng)
}

func (b *Board) fillRandom(rng *rand.Rand) error {
	if b.Ready() {
		return nil
	}
	type cand struct {
		row, col int
		o        Orientation
	}
	var cands []cand
	for o := Orientation(0); o < 4; o++ {
		for r := 0; r < 10; r++ {
			for c := 0; c < 10; c++ {
				if b.canPlace(r, c, o) {
					cands = append(cands, cand{r, c, o})
				}
			}
		}
	}
	rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	for _, cd := range cands {
		if !b.canPlace(cd.row, cd.col, cd.o) {
			continue
		}
		if err := b.Place(cd.row, cd.col, cd.o); err != nil {
			continue
		}
		if b.fillRandom(rng) == nil {
			return nil
		}
		b.undoLast()
	}
	return ErrNoRoom
}

// Placements 导出布阵，可用于存库、回放与复算校验。
func (b *Board) Placements() []Placement {
	out := make([]Placement, 0, len(b.planes))
	for _, p := range b.planes {
		out = append(out, p.Placement)
	}
	return out
}

// BuildFrom 按布阵清单重建棋盘。
func BuildFrom(r Rules, ps []Placement) (*Board, error) {
	b := NewBoard(r)
	for _, p := range ps {
		if err := b.Place(p.Row, p.Col, p.Orient); err != nil {
			return nil, err
		}
	}
	if !b.Ready() {
		return nil, ErrNotReady
	}
	return b, nil
}

// RandomPlacements 生成一份合法的随机布阵（服务端「随机布阵」按钮用）。
func RandomPlacements(r Rules, rng *rand.Rand) ([]Placement, error) {
	b := NewBoard(r)
	if err := b.Randomize(rng); err != nil {
		return nil, err
	}
	return b.Placements(), nil
}
// ValidatePlacements 校验一份「可能还没摆完」的布阵：
// 每一架都必须合法（不越界/不重叠/不违反相邻规则），但不要求摆满。
// 布阵阶段玩家每摆一架就同步一次，所以必须允许部分布阵。
func ValidatePlacements(r Rules, ps []Placement) error {
	if len(ps) > r.Planes {
		return ErrTooMany
	}
	b := NewBoard(r)
	for _, p := range ps {
		if err := b.Place(p.Row, p.Col, p.Orient); err != nil {
			return err
		}
	}
	return nil
}