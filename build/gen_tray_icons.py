#!/usr/bin/env python3
"""Draws the three single-colour tray icons (assets/tray-*.png): the app's ring and keyhole in grey, amber and green."""
from PIL import Image, ImageDraw

SIZE, SCALE = 64, 8
COLOURS = {"off": (139, 149, 161), "busy": (249, 115, 22), "on": (34, 197, 94)}

for name, colour in COLOURS.items():
    n = SIZE * SCALE
    im = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    c = n / 2
    d.ellipse([c - 0.46 * n, c - 0.46 * n, c + 0.46 * n, c + 0.46 * n], outline=colour + (255,), width=int(0.12 * n))
    d.ellipse([c - 0.10 * n, c - 0.17 * n, c + 0.10 * n, c + 0.03 * n], fill=colour + (255,))
    d.polygon([(c - 0.065 * n, c - 0.02 * n), (c + 0.065 * n, c - 0.02 * n), (c + 0.10 * n, c + 0.24 * n), (c - 0.10 * n, c + 0.24 * n)], fill=colour + (255,))
    im.resize((SIZE, SIZE), Image.LANCZOS).save(f"assets/tray-{name}.png")
