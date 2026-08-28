# Third-party logo notices

The SVG files under `svg/` are vendored brand marks, renamed to Starport
catalog IDs. The set is curated color-first: each brand uses its
lobehub `-color` variant when one exists, and the base (currentColor)
variant for brands whose official mark is monochrome. The gateway
serves this bundle first and falls back to catalog-carried bytes only
for IDs the bundle does not cover — the catalog set mixes monochrome
and color glyphs, so the curated bundle owns consistency.

Trademarks remain the property of their respective owners. The files are
used only to identify the corresponding provider or model author inside
the Starport console.

## @lobehub/icons-static-svg 1.94.0 — MIT

Source: https://github.com/lobehub/lobe-icons

All files under `svg/` except those listed in the simple-icons section
below.

```
MIT License

Copyright (c) 2023 LobeHub

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## simple-icons 16.28.0 — CC0-1.0

Source: https://github.com/simple-icons/simple-icons

Files: `svg/providers/hetzner.svg`.

The simple-icons project dedicates its icons to the public domain under
CC0 1.0 Universal: https://creativecommons.org/publicdomain/zero/1.0/

The path carries `fill="#D50C2D"`, the brand color simple-icons
documents for Hetzner. The upstream file ships the bare glyph with no
fill, which renders ink-black regardless of theme.
