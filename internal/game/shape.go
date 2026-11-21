package game

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Cell 是 0..99 的格子编号：row*10+col，row 0..9 对应 A..J，col 0..9 对应 1..10。
type Cell uint8

func NewCell(row, col int) Cell { return Cell(row*10 + col) }

func (c Cell) Row() int    { return int(c) / 10 }
func (c Cell) Col() int    { return int(c) % 10 }
func (c Cell) Valid() bool { return int(c) < 100 }

// Name 返回纸面对战纸上的叫号写法，例如 "C5"。
func (c Cell) Name() string {
	return string(rune('A'+c.Row())) + strconv.Itoa(c.Col()+1)
}

// JSON 里统一用 "C5" 这种可读坐标，方便前后端与回放共存。
func (c Cell) MarshalJSON() ([]byte, error) { return json.Marshal(c.Name()) }

func (c *Cell) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	cell, err := ParseCell(s)
	if err != nil {
		return err
	}
	*c = cell
	return nil
}

// ParseCell 解析 "C5" / "c5" 这样的叫号。
func ParseCell(s string) (Cell, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("坐标 %q 格式不对，应形如 C5", s)
	}
	r := s[0]
	if r >= 'a' && r <= 'z' {
		r -= 'a' - 'A'
	}
	if r < 'A' || r > 'J' {
		return 0, fmt.Errorf("行必须是 A-J，收到 %q", string(s[0]))
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 1 || n > 10 {
		return 0, fmt.Errorf("列必须是 1-10，收到 %q", s[1:])
	}
	return NewCell(int(r-'A'), n-1), nil
}

// Orientation 是飞机朝向：机头朝上/右/下/左。
type Orientation int

const (
	Up Orientation = iota
	Right
	Down
	Left
)

// base 是 1513 机型在「机头朝上」时的 10 个格子偏移（行, 列）。
// 第 0 个是机头：1 格机头 + 5 格机翅 + 1 格机身 + 3 格机尾 = 10 格。
var base = [10][2]int{
	{0, 2},                                 // 机头
	{1, 0}, {1, 1}, {1, 2}, {1, 3}, {1, 4}, // 机翅
	{2, 2},                 // 机身
	{3, 1}, {3, 2}, {3, 3}, // 机尾
}

// outline[o] 是朝向 o 的 10 个偏移；包围盒为 4 行 x 5 列（横向）或 5 行 x 4 列（纵向）。
var outline [4][10][2]int

func init() {
	// 包围盒随旋转在 4x5 与 5x4 之间互换，所以每次旋转都要更新高度
	for o := 0; o < 4; o++ {
		h := 4 // 机头朝上时包围盒高 4 行
		for i, p := range base {
			r, c := p[0], p[1]
			hh := h
			for k := 0; k < o; k++ {
				r, c = c, hh-1-r // 顺时针 90°
				hh = 9 - hh      // 4 <-> 5
			}
			outline[o][i] = [2]int{r, c}
		}
	}
}

// Outline 返回朝向 o 的格子偏移（第 0 个是机头），供布阵校验与随机布阵使用。
func Outline(o Orientation) [10][2]int { return outline[int(o)%4] }

// Cells 返回以 (row,col) 为包围盒左上角、朝向 o 的 10 个格子。
func Cells(row, col int, o Orientation) ([10]Cell, error) {
	var out [10]Cell
	for i, p := range Outline(o) {
		r, c := row+p[0], col+p[1]
		if r < 0 || r > 9 || c < 0 || c > 9 {
			return out, ErrOutOfBounds
		}
		out[i] = NewCell(r, c)
	}
	return out, nil
}
