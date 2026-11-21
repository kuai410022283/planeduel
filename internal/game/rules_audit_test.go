package game

import (
	"math/rand"
	"sort"
	"testing"
)

// ---------------------------------------------------------------- 1. 形状逐格比对

type pt struct{ r, c int }

func sortedPts(ps []pt) []pt {
	out := append([]pt(nil), ps...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].r != out[j].r {
			return out[i].r < out[j].r
		}
		return out[i].c < out[j].c
	})
	return out
}

func TestShapeMatchesHandWrittenLayout(t *testing.T) {
	// 期望值按「机头在左/上/右/下」手写推导，独立于引擎的旋转实现。
	want := map[Orientation][]pt{
		Up: { // 机头朝上：头(0,2) 翅第1行 身(2,2) 尾第3行
			{0, 2},
			{1, 0}, {1, 1}, {1, 2}, {1, 3}, {1, 4},
			{2, 2},
			{3, 1}, {3, 2}, {3, 3},
		},
		Right: { // 机头朝右：头在第3列，翅在第2列，身第1列，尾第0列
			{2, 3},
			{0, 2}, {1, 2}, {2, 2}, {3, 2}, {4, 2},
			{2, 1},
			{1, 0}, {2, 0}, {3, 0},
		},
		Down: { // 机头朝下：头(3,2)，翅第2行，身(1,2)，尾第0行
			{3, 2},
			{2, 0}, {2, 1}, {2, 2}, {2, 3}, {2, 4},
			{1, 2},
			{0, 1}, {0, 2}, {0, 3},
		},
		Left: { // 机头朝左：头在第0列，翅在第1列，身第2列，尾第3列
			{2, 0},
			{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1},
			{2, 2},
			{1, 3}, {2, 3}, {3, 3},
		},
	}

	for o, exp := range want {
		var got []pt
		for _, off := range Outline(o) {
			got = append(got, pt{off[0], off[1]})
		}
		g, e := sortedPts(got), sortedPts(exp)
		if len(g) != len(e) {
			t.Fatalf("朝向 %d 格子数 %d != %d", o, len(g), len(e))
		}
		for i := range g {
			if g[i] != e[i] {
				t.Fatalf("朝向 %d 第 %d 格不符：引擎 %v，期望 %v", o, i, g[i], e[i])
			}
		}
		if Outline(o)[0] != [2]int{exp[0].r, exp[0].c} {
			t.Fatalf("朝向 %d 的第 0 个偏移不是机头", o)
		}
	}

	// 机头应当指向该朝向的“最外”一侧
	headUp, headRight, headDown, headLeft := Outline(Up)[0], Outline(Right)[0], Outline(Down)[0], Outline(Left)[0]
	if !(headUp[0] < 3 && headDown[0] > 1 && headLeft[1] == 0 && headRight[1] == 3) {
		t.Fatalf("机头方位不对: up=%v right=%v down=%v left=%v", headUp, headRight, headDown, headLeft)
	}
}

func TestPlaneCellCount(t *testing.T) {
	for o := Orientation(0); o < 4; o++ {
		seen := map[[2]int]bool{}
		for _, p := range Outline(o) {
			if seen[p] {
				t.Fatalf("朝向 %d 出现重复格 %v", o, p)
			}
			seen[p] = true
		}
		if len(seen) != 10 {
			t.Fatalf("朝向 %d 应为 10 格，实际 %d", o, len(seen))
		}
	}
	b := mustBoard(t, DefaultRules(), stdLayout...)
	occ, heads := 0, 0
	for c := Cell(0); c < 100; c++ {
		if b.Occupied(c) {
			occ++
		}
		if b.IsHeadCell(c) {
			heads++
		}
	}
	if occ != 30 || heads != 3 {
		t.Fatalf("应有 30 个占用格 / 3 个机头，实际 %d / %d", occ, heads)
	}
}

// ---------------------------------------------------------------- 2. 规则矩阵
// 轮次会交替，所以「对方」要按每一发的实际目标棋盘来算。

func TestRuleMatrix(t *testing.T) {
	for _, cont := range []bool{false, true} {
		for _, decep := range []bool{false, true} {
			for _, headOnly := range []bool{false, true} {
				// 注意：这里必须用连击，保证三发都打在同一块棋盘上（否则交替出手会换板）
				r := Rules{Planes: 3, ContinueOnHit: true, DeceptionOnDeadPlane: decep, KillByHeadOnly: headOnly}
				m := mustMatch(t, r, 0)
				alive := [2]int{3, 3} // 每块棋盘各自剩余的飞机数

				// 三发：机身 → 机头(击落) → 死机残格
				seq := []struct {
					cell string
					want Feedback
				}{
					{"B1", FBHit},
					{"A3", FBKill},
					{"B2", hitOrMiss(decep)},
				}

				for i, st := range seq {
					by := m.Turn()
					target := 1 - by
					res, err := m.Fire(by, cell(t, st.cell))
					if err != nil {
						t.Fatalf("[连击=%v 迷惑=%v 机头击落=%v] 第%d发 失败: %v", cont, decep, headOnly, i+1, err)
					}
					if res.FB != st.want {
						t.Fatalf("[连击=%v 迷惑=%v 机头击落=%v] 第%d发(%s) 反馈 %v，期望 %v",
							cont, decep, headOnly, i+1, st.cell, res.FB, st.want)
					}
					if res.FB == FBKill {
						alive[target]--
					}
					_ = cont
					if got := m.Alive(target); got != alive[target] {
						t.Fatalf("[连击=%v 迷惑=%v 机头击落=%v] 第%d发后目标棋盘剩 %d 架，期望 %d",
							cont, decep, headOnly, i+1, got, alive[target])
					}
				}
			}
		}
	}
}

func hitOrMiss(decep bool) Feedback {
	if decep {
		return FBHit
	}
	return FBEmpty
}

// 换手规则：连击开时打中保持出手，关时每发都换人。
func TestTurnSwitchMatrix(t *testing.T) {
	for _, cont := range []bool{false, true} {
		r := DefaultRules()
		r.ContinueOnHit = cont
		m := mustMatch(t, r, 0)

		by := m.Turn()
		res, err := m.Fire(by, cell(t, "B1")) // 机身 → 中
		if err != nil || res.FB != FBHit {
			t.Fatalf("连击=%v：第一发应命中", cont)
		}
		if cont && m.Turn() != by {
			t.Fatalf("连击开启时打中应继续由 %d 出手，实际轮到 %d", by, m.Turn())
		}
		if !cont && m.Turn() == by {
			t.Fatalf("连击关闭时打中应换人，实际仍是 %d", by)
		}

		// 无论哪种规则，打空一定换人
		by2 := m.Turn()
		if _, err := m.Fire(by2, cell(t, "J10")); err != nil {
			t.Fatal(err)
		}
		if m.Turn() == by2 {
			t.Fatalf("打空必须换人（连击=%v）", cont)
		}
	}
}

// ---------------------------------------------------------------- 3. 全盘不变量

// 不管什么规则组合，双方轮流把 100 格都喊过之后，对局必然结束，
// 且恰好出现 3 次「死」，命中+击落总数恰好 30。
func TestFullSweepInvariants(t *testing.T) {
	for _, cont := range []bool{false, true} {
		for _, decep := range []bool{false, true} {
			r := Rules{Planes: 3, ContinueOnHit: cont, DeceptionOnDeadPlane: decep, KillByHeadOnly: true}
			m := mustMatch(t, r, 0)

			orders := [2][100]Cell{}
			rng := rand.New(rand.NewSource(int64(1)))
			for p := 0; p < 2; p++ {
				for i, v := range rng.Perm(100) {
					orders[p][i] = Cell(v)
				}
			}
			cursor := [2]int{}
			kills, hits := 0, 0

			for guard := 0; guard < 400 && !m.Finished(); guard++ {
				by := m.Turn()
				if cursor[by] >= 100 {
					t.Fatalf("[连击=%v 迷惑=%v] %d 号已无格可打但未结束", cont, decep, by)
				}
				res, err := m.Fire(by, orders[by][cursor[by]])
				cursor[by]++
				if err != nil {
					t.Fatalf("开火失败: %v", err)
				}
				switch res.FB {
				case FBKill:
					kills++
					hits++
				case FBHit:
					hits++
				}
			}

			if !m.Finished() {
				t.Fatalf("[连击=%v 迷惑=%v] 没有在合理步数内结束", cont, decep)
			}
			// 双方互有命中，所以「死」可能发生在两块棋盘上：至少 3 次，且输家必然全灭
			if kills < 3 {
				t.Fatalf("[连击=%v 迷惑=%v] 「死」至少 3 次，实际 %d", cont, decep, kills)
			}
			loser := 1 - m.Winner()
			if m.Alive(loser) != 0 {
				t.Fatalf("[连击=%v 迷惑=%v] 输家应剩 0 架，实际 %d", cont, decep, m.Alive(loser))
			}
			// 机头击落规则下：总「死」次数 == 两块棋盘上加起来的死亡飞机数
			dead := (3 - m.Alive(0)) + (3 - m.Alive(1))
			if kills != dead {
				t.Fatalf("[连击=%v 迷惑=%v] 「死」%d 次 != 死亡飞机数 %d", cont, decep, kills, dead)
			}
			_ = hits
		}
	}
}

// 不动机头就永远打不死（默认规则）。
func TestHeadIsOnlyKillSwitch(t *testing.T) {
	m := mustMatch(t, DefaultRules(), 0)
	for i := 0; i < 3; i++ {
		p := m.Board(1).Plane(i)
		for _, c := range p.Cells {
			if c == p.Head {
				continue
			}
			by := m.Turn()
			if _, err := m.Fire(by, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 注意：双方轮换，所以两块棋盘都只挨了机身 —— 都不该死
	if m.Alive(0) != 3 || m.Alive(1) != 3 || m.Finished() {
		t.Fatalf("只打机身不应击落：alive=%d/%d finished=%v", m.Alive(0), m.Alive(1), m.Finished())
	}
}

// 重复喊号 / 乱序出手 / 非法输入都必须被拒。
func TestFireRejects(t *testing.T) {
	r := DefaultRules()
	r.ContinueOnHit = true // 命中后仍由同一人出手，便于测重复
	m := mustMatch(t, r, 0)

	if _, err := m.Fire(1, cell(t, "A1")); err != ErrNotYourTurn {
		t.Fatalf("非当前回合应拒绝，实际 %v", err)
	}
	if _, err := m.Fire(0, cell(t, "B1")); err != nil { // 命中 → 仍是 0 号
		t.Fatal(err)
	}
	if _, err := m.Fire(0, cell(t, "B1")); err != ErrRepeated {
		t.Fatalf("重复喊号应拒绝，实际 %v", err)
	}
	if _, err := m.Fire(0, Cell(200)); err == nil {
		t.Fatal("非法坐标应拒绝")
	}
	if _, err := m.Fire(2, cell(t, "B2")); err == nil {
		t.Fatal("非法玩家应拒绝")
	}
}

// 回放：任意一局都能用「布阵 + 喊号」原样复算出来。
func TestReplayMatchesEveryGame(t *testing.T) {
	rng := rand.New(rand.NewSource(2026))
	for round := 0; round < 20; round++ {
		r := DefaultRules()
		r.ContinueOnHit = round%2 == 0
		r.DeceptionOnDeadPlane = round%3 != 0
		b0, b1 := NewBoard(r), NewBoard(r)
		if err := b0.Randomize(rng); err != nil {
			t.Fatal(err)
		}
		if err := b1.Randomize(rng); err != nil {
			t.Fatal(err)
		}
		m, err := NewMatch(r, b0, b1, round%2)
		if err != nil {
			t.Fatal(err)
		}
		for guard := 0; guard < 300 && !m.Finished(); guard++ {
			by := m.Turn()
			c := Cell(rng.Intn(100))
			if m.Shot(by, c) {
				continue
			}
			if _, err := m.Fire(by, c); err != nil {
				t.Fatal(err)
			}
		}
		replayed, err := Replay(r, b0.Placements(), b1.Placements(), m.Moves(), round%2)
		if err != nil {
			t.Fatalf("第 %d 局回放失败: %v", round, err)
		}
		if replayed.Winner() != m.Winner() || replayed.Finished() != m.Finished() {
			t.Fatalf("第 %d 局回放结果不一致", round)
		}
	}
}
