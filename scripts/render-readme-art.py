#!/usr/bin/env python3
"""Render the supplied escaped ANSI block art without fonts or external assets."""
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / 'docs' / 'assets'
SOURCE = ASSETS / 'we4zl-dialup.source.txt'
GLYPHS = str.maketrans({'ﾜ': '▄', 'ﾟ': '▀', 'ﾛ': '█', 'ｰ': '░', 'ｱ': '▒', 'ｲ': '▓'})
COLORS = ['#000000', '#aa0000', '#00aa00', '#aa5500', '#0000aa', '#aa00aa', '#00aaaa', '#aaaaaa',
          '#555555', '#ff5555', '#55ff55', '#ffff55', '#5555ff', '#ff55ff', '#55ffff', '#ffffff']
ESCAPE = re.compile(r'\x1b\[([0-9;]*)([mK])')


def main():
    source = SOURCE.read_text(encoding='utf-8').translate(GLYPHS)
    ansi = re.sub(r'\[([0-9;]*[mK])', '\x1b[\\1', source)
    (ASSETS / 'we4zl-dialup.ans').write_text(ansi, encoding='utf-8')
    rows = []
    foreground, background, bold = 7, 0, False
    for line in ansi.splitlines():
        cells, offset = [], 0
        while offset < len(line):
            command = ESCAPE.match(line, offset)
            if command:
                values, action = command.groups()
                if action == 'm':
                    for value in map(int, (values or '0').split(';')):
                        if value == 0:
                            foreground, background, bold = 7, 0, False
                        elif value == 1:
                            bold = True
                        elif value == 22:
                            bold = False
                        elif 30 <= value <= 37:
                            foreground = value - 30
                        elif 40 <= value <= 47:
                            background = value - 40
                        else:
                            raise ValueError(f'Unsupported SGR: {value}')
                elif values not in ('', '0') or background != 0:
                    raise ValueError('Only black erase-to-end-of-line is supported')
                offset = command.end()
                continue
            glyph = line[offset]
            if glyph not in ' ▀▄█░▒▓':
                raise ValueError(f'Unexpected block-art character: {glyph!r}')
            cells.append((glyph, foreground + (8 if bold else 0), background))
            offset += 1
        rows.append(cells)
    while rows and not rows[-1]:
        rows.pop()
    columns = max(80, max(map(len, rows)))
    width, height = columns * 8 + 32, len(rows) * 16 + 32
    svg = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
           f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="title desc">',
           '<title id="title">we4zl-dialup ANSI artwork</title>',
           '<desc id="desc">The supplied block art rendered in a classic sixteen-color terminal palette.</desc>',
           '<defs>']
    for color, fill in enumerate(COLORS):
        for shade in range(1, 4):
            svg.append(f'<pattern id="s{shade}c{color}" width="4" height="4" patternUnits="userSpaceOnUse">')
            for y in range(4):
                for x in range(4):
                    level = ((0, 2), (3, 1))[y % 2][x % 2]
                    if level < shade:
                        svg.append(f'<rect x="{x}" y="{y}" width="1" height="1" fill="{fill}"/>')
            svg.append('</pattern>')
    svg.extend(['</defs>', f'<rect width="{width}" height="{height}" fill="#000000"/>',
                '<g shape-rendering="crispEdges">'])
    for row, cells in enumerate(rows):
        for column, (glyph, fg, bg) in enumerate(cells):
            x, y = 16 + column * 8, 16 + row * 16
            if bg:
                svg.append(f'<rect x="{x}" y="{y}" width="8" height="16" fill="{COLORS[bg]}"/>')
            if glyph == ' ':
                continue
            h = 8 if glyph in '▀▄' else 16
            if glyph == '▄':
                y += 8
            fill = f'url(#s{"░▒▓".index(glyph)+1}c{fg})' if glyph in '░▒▓' else COLORS[fg]
            svg.append(f'<rect x="{x}" y="{y}" width="8" height="{h}" fill="{fill}"/>')
    svg.extend(['</g>', '</svg>'])
    (ASSETS / 'we4zl-dialup.svg').write_text('\n'.join(svg) + '\n', encoding='utf-8')
    print(f'Rendered {columns} columns × {len(rows)} rows; UTF-8 ANSI and SVG written.')


if __name__ == '__main__':
    main()
