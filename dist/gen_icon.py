# -*- coding: utf-8 -*-
"""生成飞牛应用图标：圆角矩形 + 1513 飞机图形。"""

import os
from PIL import Image, ImageDraw

PKG = r"planeduel\dist\pkg\planeduel"
S = 1024  # 超采样画布

# 1513 机型（机头朝上），5 列 x 4 行
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

    # 飞机：5x4 格，居中，白色带细缝
    cell = int(S * 0.148)
    gap = max(2, int(cell * 0.10))
    gw, gh = 5 * cell, 4 * cell
    x0 = (S - gw) // 2
    y0 = (S - gh) // 2
    d = ImageDraw.Draw(img)
    for r, cols in SHAPE.items():
        for c in cols:
            x = x0 + c * cell
            y = y0 + r * cell
            d.rounded_rectangle(
                [x + gap, y + gap, x + cell - gap, y + cell - gap],
                radius=int(cell * 0.18),
                fill=(255, 255, 255, 245),
            )

    # 机头加一点强调色，便于在 64px 下辨识
    hx = x0 + 2 * cell
    d.rounded_rectangle(
        [hx + gap, y0 + gap, hx + cell - gap, y0 + cell - gap],
        radius=int(cell * 0.18),
        fill=(255, 214, 92, 255),
    )

    return img.resize((size, size), Image.LANCZOS)


def main():
    out = {
        os.path.join(PKG, "ICON_256.PNG"): 256,
        os.path.join(PKG, "ICON.PNG"): 64,
        os.path.join(PKG, "app", "ui", "images", "icon_256.png"): 256,
        os.path.join(PKG, "app", "ui", "images", "icon_64.png"): 64,
    }
    # 各自从大画布降采样，保证 64px 也干净
    for path, size in out.items():
        os.makedirs(os.path.dirname(path), exist_ok=True)
        img = make(size)
        img.save(path, "PNG", optimize=True)
        print(f"{path}  {size}x{size}  {os.path.getsize(path)} bytes")


if __name__ == "__main__":
    main()
