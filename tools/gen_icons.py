#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""按飞牛应用图标（dist/gen_icon.py）的同一套绘制，生成浏览器图标。

与 App Center 图标保持完全一致的视觉：
蓝色渐变圆角底 + 1513 白色格状飞机 + 琥珀色机头。
小尺寸（<=48px）不画格缝，否则 32px 下会糊成一团。
"""
import sys
from PIL import Image, ImageDraw

S = 1024  # 超采样画布

# 1513 机型（机头朝上），5 列 × 4 行
SHAPE = {
    0: [2],
    1: [0, 1, 2, 3, 4],
    2: [2],
    3: [1, 2, 3],
}


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))


def make(size):
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # 背景：圆角矩形渐变
    top, bottom = (76, 132, 255), (24, 72, 205)
    radius = int(S * 0.22)
    grad = Image.new("RGBA", (1, S))
    gd = ImageDraw.Draw(grad)
    for y in range(S):
        gd.point((0, y), lerp(top, bottom, y / (S - 1)))
    grad = grad.resize((S, S))

    mask = Image.new("L", (S, S), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, S - 1, S - 1], radius=radius, fill=255)
    img.paste(grad, (0, 0), mask)

    # 顶部高光
    hl = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ImageDraw.Draw(hl).ellipse([-S * 0.35, -S * 0.9, S * 1.35, S * 0.45], fill=(255, 255, 255, 38))
    img = Image.alpha_composite(img, Image.composite(hl, Image.new("RGBA", (S, S), (0, 0, 0, 0)), mask))

    # 飞机：5×4 格，居中。小尺寸不画格缝（否则会糊）
    small = size <= 48
    cell = int(S * (0.155 if small else 0.148))
    gap = 0 if small else max(2, int(cell * 0.10))
    gw, gh = 5 * cell, 4 * cell
    x0 = (S - gw) // 2
    y0 = (S - gh) // 2
    d = ImageDraw.Draw(img)
    r = 0 if small else int(cell * 0.18)
    for row, cols in SHAPE.items():
        for c in cols:
            x = x0 + c * cell
            y = y0 + row * cell
            d.rounded_rectangle([x + gap, y + gap, x + cell - gap, y + cell - gap], radius=r,
                                fill=(255, 255, 255, 245))

    # 机头强调色，小尺寸下更好认
    hx = x0 + 2 * cell
    d.rounded_rectangle([hx + gap, y0 + gap, hx + cell - gap, y0 + cell - gap], radius=r,
                        fill=(255, 214, 92, 255))

    return img.resize((size, size), Image.LANCZOS)


def main():
    base = sys.argv[1].rstrip("/")
    for name, size in (("icon-32.png", 32), ("icon-180.png", 180), ("icon-192.png", 192)):
        img = make(size)
        path = f"{base}/{name}"
        img.save(path, "PNG", optimize=True)
        import os
        print(f"  {name}  {size}x{size}  {os.path.getsize(path)} bytes")


if __name__ == "__main__":
    main()
