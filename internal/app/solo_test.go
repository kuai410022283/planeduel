package app

import (
	"testing"

	"planeduel/internal/game"
)

func TestSoloPlaysToEnd(t *testing.T) {
	store := NewSoloStore(7)
	solo, err := store.New(game.DefaultRules(), "测试玩家")
	if err != nil {
		t.Fatalf("建局失败: %v", err)
	}

	v := solo.view()
	if len(v.MyPlanes) != 30 {
		t.Fatalf("布阵图应有 30 格，实际 %d", len(v.MyPlanes))
	}
	if len(v.EnemyMarks) != 0 {
		t.Fatalf("开局敌情图应为空，实际 %d 个标记", len(v.EnemyMarks))
	}

	// 按固定顺序扫格子，直到分出胜负（自己赢或机器人赢）
	shots := 0
	for r := 0; r < 10 && !v.Finished; r++ {
		for c := 0; c < 10 && !v.Finished; c++ {
			cell := game.NewCell(r, c)
			v, err = solo.Fire(cell)
			if err != nil {
				t.Fatalf("开火 %s 失败: %v", cell.Name(), err)
			}
			shots++
			if shots > 100 {
				t.Fatalf("打了 %d 发仍未结束", shots)
			}
			// 每一发都必须落在敌情图上，且不能重复
			seen := map[string]bool{}
			for _, m := range v.EnemyMarks {
				if seen[m.Cell] {
					t.Fatalf("敌情图出现重复标记 %s", m.Cell)
				}
				seen[m.Cell] = true
			}
		}
	}

	if !v.Finished {
		t.Fatal("对局没有结束")
	}
	if v.Winner != 0 && v.Winner != 1 {
		t.Fatalf("胜者异常: %d", v.Winner)
	}
	// 机器人只要动过手，我的棋盘上就应有被击中的记录
	if v.BotShots > 0 && len(v.MyMarks) == 0 {
		t.Fatal("机器人开过火，但我的布阵图上没有任何标记")
	}
	t.Logf("结束：winner=%d 我打了 %d 发（中 %d），机器人 %d 发（中 %d），剩我 %d 架 / 对方 %d 架",
		v.Winner, v.Shots, v.Hits, v.BotShots, v.BotHits, v.Alive, v.EnemyAlive)
}

func TestSoloRejectsRepeatedShot(t *testing.T) {
	store := NewSoloStore(11)
	solo, _ := store.New(game.DefaultRules(), "重复测试")
	if _, err := solo.Fire(game.NewCell(0, 0)); err != nil {
		t.Fatalf("第一发失败: %v", err)
	}
	if _, err := solo.Fire(game.NewCell(0, 0)); err == nil {
		t.Fatal("重复打同一格应当报错")
	}
}
