package game

import (
	"errors"
	"math/rand"
	"testing"
)

// 标准测试布阵：三架飞机互不重叠，占据 A-D 行左右两侧 + F-I 行左侧。
var stdLayout = []Placement{
	{Row: 0, Col: 0, Orient: Up},
	{Row: 0, Col: 5, Orient: Up},
	{Row: 5, Col: 0, Orient: Up},
}

func mustBoard(t *testing.T, r Rules, ps ...Placement) *Board {
	t.Helper()
	b := NewBoard(r)
	for _, p := range ps {
		if err := b.Place(p.Row, p.Col, p.Orient); err != nil {
			t.Fatalf("放置 %+v 失败: %v", p, err)
		}
	}
	if !b.Ready() {
		t.Fatalf("棋盘未摆满：%d/%d", b.Count(), r.Planes)
	}
	return b
}

func mustMatch(t *testing.T, r Rules, first int) *Match {
	t.Helper()
	m, err := NewMatch(r, mustBoard(t, r, stdLayout...), mustBoard(t, r, stdLayout...), first)
	if err != nil {
		t.Fatalf("建局失败: %v", err)
	}
	return m
}

func cell(t *testing.T, name string) Cell {
	t.Helper()
	c, err := ParseCell(name)
	if err != nil {
		t.Fatalf("坐标 %q 解析失败: %v", name, err)
	}
	return c
}

func TestCellNameRoundTrip(t *testing.T) {
	cases := []struct {
		row, col int
		name     string
	}{
		{0, 0, "A1"}, {0, 2, "A3"}, {3, 4, "D5"}, {9, 9, "J10"}, {5, 2, "F3"},
	}
	for _, tc := range cases {
		c := NewCell(tc.row, tc.col)
		if got := c.Name(); got != tc.name {
			t.Errorf("NewCell(%d,%d).Name() = %q, 期望 %q", tc.row, tc.col, got, tc.name)
		}
		parsed, err := ParseCell(tc.name)
		if err != nil || parsed != c {
			t.Errorf("ParseCell(%q) = %v, %v; 期望 %v", tc.name, parsed, err, c)
		}
	}
	for _, bad := range []string{"", "K1", "A0", "A11", "5", "AA"} {
		if _, err := ParseCell(bad); err == nil {
			t.Errorf("ParseCell(%q) 应当报错", bad)
		}
	}
}

func TestOrientationsAreDistinctAndValid(t *testing.T) {
	seen := map[[10][2]int]bool{}
	for o := Orientation(0); o < 4; o++ {
		off := Outline(o)
		if seen[off] {
			t.Fatalf("朝向 %d 与前面的朝向重复", o)
		}
		seen[off] = true
		if off[0] != [2]int{0, 2} && o == Up {
			t.Fatalf("机头偏移不对: %v", off[0])
		}
		// 10 格互不相同，且都能放进 10x10
		uniq := map[[2]int]bool{}
		for _, p := range off {
			if uniq[p] {
				t.Fatalf("朝向 %d 出现重复格子 %v", o, p)
			}
			uniq[p] = true
			if p[0] < 0 || p[0] > 9 || p[1] < 0 || p[1] > 9 {
				t.Fatalf("朝向 %d 越界: %v", o, p)
			}
		}
	}
	// 四个朝向的机头位置应各不相同（顺时针旋转的感觉）
	heads := map[[2]int]bool{}
	for o := 0; o < 4; o++ {
		heads[Outline(Orientation(o))[0]] = true
	}
	if len(heads) != 4 {
		t.Fatalf("四个朝向的机头应位于不同位置，实际 %v", heads)
	}
	// 朝向 1（机头朝右）与朝向 3（机头朝左）必须是镜像
	if Outline(Right)[0] != [2]int{2, 3} {
		t.Errorf("机头朝右应在 (2,3)，实际 %v", Outline(Right)[0])
	}
	if Outline(Left)[0] != [2]int{2, 0} {
		t.Errorf("机头朝左应在 (2,0)，实际 %v", Outline(Left)[0])
	}
}

func TestPlaceRejects(t *testing.T) {
	r := DefaultRules()

	if err := NewBoard(r).Place(9, 9, Up); !errors.Is(err, ErrOutOfBounds) {
		t.Errorf("右下角放 4x5 飞机应越界，得到 %v", err)
	}

	b := NewBoard(r)
	if err := b.Place(0, 0, Up); err != nil {
		t.Fatalf("首架应可放置: %v", err)
	}
	if err := b.Place(1, 1, Up); !errors.Is(err, ErrOverlap) {
		t.Errorf("重叠应被拒绝，得到 %v", err)
	}
	// 失败后棋盘不应被污染
	if b.Count() != 1 {
		t.Errorf("失败的放置改变了棋盘：%d 架", b.Count())
	}

	full := NewBoard(Rules{Planes: 1})
	if err := full.Place(0, 0, Up); err != nil {
		t.Fatal(err)
	}
	if err := full.Place(5, 0, Up); !errors.Is(err, ErrTooMany) {
		t.Errorf("超量应被拒绝，得到 %v", err)
	}
}

func TestNoTouchRule(t *testing.T) {
	r := Rules{Planes: 3, NoTouch: true}
	b := NewBoard(r)
	if err := b.Place(0, 0, Up); err != nil {
		t.Fatal(err)
	}
	// 紧贴第一架的包围盒边缘（列 5 与列 4 相邻）
	if err := b.Place(0, 5, Up); !errors.Is(err, ErrTooClose) {
		t.Errorf("相邻放置应被拒绝，得到 %v", err)
	}
	if err := b.Place(5, 0, Up); err != nil {
		t.Errorf("隔着 4 行空白应可放置，得到 %v", err)
	}
}

func TestRandomizeAndBuildFrom(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		r := DefaultRules()
		r.NoTouch = i%2 == 0
		b := NewBoard(r)
		if err := b.Randomize(rng); err != nil {
			t.Fatalf("随机布阵失败: %v", err)
		}
		ps := b.Placements()
		if len(ps) != r.Planes {
			t.Fatalf("布阵导出数量 %d，期望 %d", len(ps), r.Planes)
		}
		b2, err := BuildFrom(r, ps)
		if err != nil {
			t.Fatalf("按导出的布阵重建失败: %v", err)
		}
		// 重建后的占用格必须完全一致
		for c := Cell(0); c < 100; c++ {
			if b.Occupied(c) != b2.Occupied(c) {
				t.Fatalf("重建后格子 %s 占用不一致", c.Name())
			}
			if b.IsHeadCell(c) != b2.IsHeadCell(c) {
				t.Fatalf("重建后格子 %s 机头标记不一致", c.Name())
			}
		}
	}
}

func TestContinueOnHitAndTurnSwitch(t *testing.T) {
	m := mustMatch(t, DefaultRules(), 0)

	res, err := m.Fire(0, cell(t, "A3")) // 机头
	if err != nil || res.FB != FBKill {
		t.Fatalf("打机头应报死：%v %v", res.FB, err)
	}
	if m.Turn() != 0 {
		t.Errorf("连击规则下打中应继续由 0 号出手，实际轮到 %d", m.Turn())
	}
	if _, err := m.Fire(1, cell(t, "E1")); !errors.Is(err, ErrNotYourTurn) {
		t.Errorf("不是自己的回合应被拒绝，得到 %v", err)
	}
	if _, err := m.Fire(0, cell(t, "A3")); !errors.Is(err, ErrRepeated) {
		t.Errorf("重复喊同一格应被拒绝，得到 %v", err)
	}

	res, err = m.Fire(0, cell(t, "E1")) // 空
	if err != nil || res.FB != FBEmpty {
		t.Fatalf("空格应报空：%v %v", res.FB, err)
	}
	if m.Turn() != 1 {
		t.Errorf("打空应换人，实际轮到 %d", m.Turn())
	}
}

func TestDeceptionOnDeadPlane(t *testing.T) {
	// 开：死机残格继续答「中」
	m := mustMatch(t, DefaultRules(), 0)
	if _, err := m.Fire(0, cell(t, "A3")); err != nil { // 打死 0 号棋盘的第一架
		t.Fatal(err)
	}
	res, err := m.Fire(0, cell(t, "B1")) // 该机残格
	if err != nil {
		t.Fatal(err)
	}
	if res.FB != FBHit {
		t.Errorf("迷惑规则下残格应报「中」，实际 %v", res.FB)
	}

	// 关：残格报「空」
	r := DefaultRules()
	r.DeceptionOnDeadPlane = false
	m2 := mustMatch(t, r, 0)
	if _, err := m2.Fire(0, cell(t, "A3")); err != nil {
		t.Fatal(err)
	}
	res2, err := m2.Fire(0, cell(t, "B1"))
	if err != nil {
		t.Fatal(err)
	}
	if res2.FB != FBEmpty {
		t.Errorf("关闭迷惑后残格应报「空」，实际 %v", res2.FB)
	}
}

func TestKillByBodyWhenRuleDisabled(t *testing.T) {
	r := DefaultRules()
	r.KillByHeadOnly = false
	m := mustMatch(t, r, 0)

	body := []string{"B1", "B2", "B3", "B4", "B5", "C3", "D2", "D3", "D4"}
	for i, name := range body {
		res, err := m.Fire(0, cell(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.FB != FBHit {
			t.Fatalf("%s 应报「中」，实际 %v", name, res.FB)
		}
		if i < len(body)-1 && m.Alive(1) != 3 {
			t.Fatalf("第 %d 格就打死了飞机", i+1)
		}
	}
	if got := m.Alive(1); got != 2 {
		t.Errorf("机身 9 格打光后应剩 2 架，实际 %d", got)
	}
}

func TestWinAndReplay(t *testing.T) {
	m := mustMatch(t, DefaultRules(), 0)
	for _, name := range []string{"A3", "A8", "F3"} { // 三架飞机的机头
		res, err := m.Fire(0, cell(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.FB != FBKill {
			t.Fatalf("%s 应报死，实际 %v", name, res.FB)
		}
	}
	if !m.Finished() || m.Winner() != 0 {
		t.Fatalf("应判 0 号获胜，实际 finished=%v winner=%d", m.Finished(), m.Winner())
	}
	if _, err := m.Fire(0, cell(t, "J10")); !errors.Is(err, ErrFinished) {
		t.Errorf("结束后不应还能开火，得到 %v", err)
	}

	// 回放复算：结果必须一致
	rm, err := Replay(DefaultRules(), stdLayout, stdLayout, m.Moves(), 0)
	if err != nil {
		t.Fatalf("回放失败: %v", err)
	}
	if !rm.Finished() || rm.Winner() != 0 {
		t.Errorf("回放结果不一致：finished=%v winner=%d", rm.Finished(), rm.Winner())
	}

	// 篡改日志应被回放抓出来
	bad := append([]Move(nil), m.Moves()...)
	bad[0].FB = FBHit
	if _, err := Replay(DefaultRules(), stdLayout, stdLayout, bad, 0); err == nil {
		t.Error("篡改反馈后回放应当报错")
	}
}

func TestRandomPlayoutsTerminate(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for round := 0; round < 30; round++ {
		r := DefaultRules()
		b1, b2 := NewBoard(r), NewBoard(r)
		if err := b1.Randomize(rng); err != nil {
			t.Fatal(err)
		}
		if err := b2.Randomize(rng); err != nil {
			t.Fatal(err)
		}
		m, err := NewMatch(r, b1, b2, round%2)
		if err != nil {
			t.Fatal(err)
		}
		// 每个玩家各自按随机顺序扫格子，保证一定收敛
		orders := [2][100]Cell{}
		for p := 0; p < 2; p++ {
			perm := rng.Perm(100)
			for i, v := range perm {
				orders[p][i] = Cell(v)
			}
		}
		cursor := [2]int{}
		guard := 0
		for !m.Finished() {
			guard++
			if guard > 400 {
				t.Fatalf("第 %d 局没有在合理步数内结束", round)
			}
			by := m.Turn()
			var c Cell
			for {
				if cursor[by] >= 100 {
					t.Fatalf("第 %d 局：0 号或 1 号已无格可打却未分出胜负", round)
				}
				c = orders[by][cursor[by]]
				cursor[by]++
				if !m.Shot(by, c) {
					break
				}
			}
			if _, err := m.Fire(by, c); err != nil {
				t.Fatalf("随机开火 %s 失败: %v", c.Name(), err)
			}
		}
		if m.Winner() != 0 && m.Winner() != 1 {
			t.Fatalf("第 %d 局胜者异常: %d", round, m.Winner())
		}
	}
}

// 默认规则下：把三架飞机的机身/机翅/机尾全部打光，但没碰机头 —— 不应该结束，也不应该击落任何一架。
func TestNoWinWithoutHeadShot(t *testing.T) {
	r := DefaultRules()
	m := mustMatch(t, r, 0)
	target := m.Board(1) // 我打的是对方棋盘

	shots := 0
	for i := 0; i < target.Count(); i++ {
		p := target.Plane(i)
		for _, c := range p.Cells {
			if c == p.Head {
				continue // 不动机头
			}
			res, err := m.Fire(0, c)
			if err != nil {
				t.Fatalf("打 %s 失败: %v", c.Name(), err)
			}
			shots++
			if res.FB == FBKill {
				t.Fatalf("打到 %s 判定了击落，但它不是机头", c.Name())
			}
		}
	}
	if shots != 27 {
		t.Fatalf("应打 27 格非机头，实际 %d", shots)
	}
	if m.Finished() {
		t.Fatal("只打机身就结束了对局 —— 规则错误")
	}
	if got := m.Alive(1); got != 3 {
		t.Fatalf("机头没中却击落了飞机：剩 %d 架（应 3 架）", got)
	}
	if m.Board(1).Plane(0).Hits() != 9 {
		t.Fatalf("第一架应被打中 9 格，实际 %d", m.Board(1).Plane(0).Hits())
	}
}

// 「机身打光也算击落」的变体规则下，9 格才死亡（第 9 格触发），且仍然不影响其它飞机。
func TestKillByBodyVariantIsolated(t *testing.T) {
	r := DefaultRules()
	r.KillByHeadOnly = false
	m := mustMatch(t, r, 0)
	p := m.Board(1).Plane(0)

	n := 0
	for _, c := range p.Cells {
		if c == p.Head {
			continue
		}
		if _, err := m.Fire(0, c); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 9 {
		t.Fatalf("非机头格应为 9，实际 %d", n)
	}
	if m.Alive(1) != 2 {
		t.Fatalf("变体规则下机身打光应击落 1 架，实际剩 %d", m.Alive(1))
	}
	if m.Finished() {
		t.Fatal("只击落 1 架不应结束对局")
	}
}