#!/usr/bin/env python3
# 对比两种 32px 图标的画质：
#   A：专用绘制（小尺寸关掉格缝、格子放大）
#   B：直接由 180/512 缩放到 32
import sys
from PIL import Image
import importlib.util

spec = importlib.util.spec_from_file_location("gi", sys.argv[1] + "/gen_icons.py")
gi = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gi)


def stats(img, name):
    px = img.convert("RGBA").load()
    w, h = img.size
    white = blue = mid = 0
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a < 40:
                continue
            if min(r, g, b) > 225:
                white += 1
            elif b > r + 35 and b > 120:
                blue += 1
            else:
                mid += 1     # 灰/半白：抗锯齿或糊掉的格缝
    total = w * h
    print(f"  {name}")
    print(f"    纯白 {white:>4} ({white/total*100:4.1f}%)   纯蓝 {blue:>4} ({blue/total*100:4.1f}%)   中间色(糊) {mid:>4} ({mid/total*100:4.1f}%)")
    return mid


def show(img, title):
    print(f"\n  ---- {title} ----")
    px = img.convert("RGBA").load()
    w, h = img.size
    for y in range(h):
        row = ""
        for x in range(w):
            r, g, b, a = px[x, y]
            if a < 40:
                row += " "
            elif min(r, g, b) > 225:
                row += "#"
            elif r > 200 and g > 150 and b < 140:
                row += "?"
            elif b > r + 35:
                row += "."
            else:
                row += "+"      # 糊的地方
        print("  " + row)


# A：现有的专用 32px 绘制
a = gi.make(32)
a.save(sys.argv[2] + "/cmp-A-dedicated-32.png")

# B：从 512 缩放到 32
big = gi.make(512)
b = big.resize((32, 32), Image.LANCZOS)
b.save(sys.argv[2] + "/cmp-B-downscaled-32.png")

print("=== 画质统计 ===")
ma = stats(a, "A 专用绘制（格缝关闭，格子放大）")
mb = stats(b, "B 由 512 缩放（保留格缝与圆角）")
print(f"\n  → 中间色（糊）像素：A={ma}  B={mb}   （越少越干净）")

show(a, "A 专用绘制")
show(b, "B 由 512 缩放")
