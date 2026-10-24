# The Jaggery logo

**Mark:** a closed jar, a lid and a body. The jar is the house: what's inside stays inside.
**Wordmark:** "jaggery", all lowercase, Alegreya Sans ExtraBold (800), tracking −0.02em (−0.035em at 48 px and up).

## Files

| File | Use |
|---|---|
| `jaggery-mark.svg` | Main mark, for dark backgrounds (cream lid, gold body) |
| `jaggery-mark-on-light.svg` | For cream or white backgrounds (brown lid, amber body) |
| `jaggery-mark-mono.svg` | One colour, uses `currentColor`. For CLI docs, print, and stamps. |
| `jaggery-icon.svg`, `icon-512.png` | App icon: gold tile with a dark jar. Use for README, GitHub avatar, social cards. |
| `jaggery-icon-dark.svg`, `favicon.svg`, `favicon-32.png` | Favicon: dark tile, so it shows on light and dark browser tabs |
| `apple-touch-icon-180.png` | iOS home screen (full-bleed; iOS rounds the corners) |
| `jaggery-mark-1024.png` | Transparent high-res mark for slides and press |

## Geometry (64 × 64 grid)

```
lid   rect x=17 y=4  w=30 h=10 rx=3
body  rect x=10 y=18 w=44 h=42 rx=13
gap   4 units between lid and body: keep it at every size; it's what makes the jar read as closed
```
- On app tiles the jar is scaled to 62 % and centred; the tile corner radius is 14/64 (22 %).
- Clear space is half the lid height (5 units) on all sides. Minimum size is 16 px.

## Colours

| Token | oklch (source) | Hex (sRGB) |
|---|---|---|
| Gold, body | oklch(0.80 0.12 75) | #EBB25F |
| Cream, lid | oklch(0.94 0.02 80) | #F2EADD |
| Molasses, dark tile / page | oklch(0.18 0.012 55) | #16100D |
| Dark jar on gold | oklch(0.22 0.025 55) | #24180F |
| Brown, lid on light | oklch(0.30 0.035 55) | #3C291D |
| Amber, body on light | oklch(0.62 0.13 65) | #BB731B |

## Don't
- Recolour the lid gold. The two-tone split is the logo.
- Add outlines, shadows or gradients, or put anything inside the jar.
- Close the gap between lid and body, or rotate or stretch the jar.
- Set the wordmark in capitals or in another font.

## Where it's used

- **README:** `icon-512.png` as the header image.
- **The page** (`internal/dashboard/index.html`): the mark inline beside the wordmark, and the dark-tile favicon inline as a data URL, so the page fetches nothing.
- **jaggery.dev:** `favicon.svg`, `favicon-32.png` and `apple-touch-icon-180.png`, and the mark beside the wordmark in the header.
- **The CLI** prints the plain name; the mark isn't drawn in ASCII.

The use of the name and logo by others is covered by [TRADEMARKS.md](../../TRADEMARKS.md).
