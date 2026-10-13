# Branding for the Explorer

Source of truth: the `otfabric/website` repo (`src/styles/tokens.css`, `src/assets/logo-*.svg`, `public/favicon.svg`, `public/icons/*.svg`, `src/components/site/*`), read from the local checkout on 2026-10-13. Values marked **proposed** do not exist on the website yet; they fill gaps the Explorer needs and should be added to the website tokens too.

## 1. Identity in one line

Slate base, teal "thread" accent, copper "wire" secondary. Dark is the default theme. Product name: **OT Fabric SunSpec Explorer**; wordmark style "**OT** Fabric" (Barlow Semi Condensed 500, "OT" at 700).

## 2. Tokens (copy into `frontend/src/styles/tokens.css`)

```css
@import '@fontsource/barlow-semi-condensed/500.css';
@import '@fontsource/barlow-semi-condensed/600.css';
@import '@fontsource/barlow-semi-condensed/700.css';
@import '@fontsource/ibm-plex-sans/400.css';
@import '@fontsource/ibm-plex-sans/400-italic.css';
@import '@fontsource/ibm-plex-sans/500.css';
@import '@fontsource/ibm-plex-sans/600.css';
@import '@fontsource/ibm-plex-mono/400.css';
@import '@fontsource/ibm-plex-mono/500.css';

:root {
  --ot-font-display: 'Barlow Semi Condensed', 'Arial Narrow', Arial, sans-serif;
  --ot-font-body: 'IBM Plex Sans', 'Segoe UI', system-ui, sans-serif;
  --ot-font-mono: 'IBM Plex Mono', ui-monospace, Menlo, Consolas, monospace;

  /* Dark (default), from the website */
  --ot-bg: #11181f;
  --ot-surface: #18222c;
  --ot-line: #2a3743;
  --ot-fg: #e6eaee;
  --ot-fg-strong: #ffffff;
  --ot-muted: #94a3b0;
  --ot-accent: #3fc1c6;
  --ot-accent-soft: #123a3d;
  --ot-accent-strong: #dcefef;
  --ot-wire: #e08a4e;
  --ot-header-bg: #18222c;

  /* Proposed additions (not on the website yet) */
  --ot-blue: #8ab0e8;        /* third logo hue; info / third series */
  --ot-danger: #f07178;      /* errors, read failures */
  --ot-wire-text: #e08a4e;   /* copper usable for small text */
  --ot-control-line: #94a3b0;/* borders of inputs/buttons (3:1 needed) */
}

:root[data-theme='light'] {
  --ot-bg: #ffffff;
  --ot-surface: #f4f6f8;
  --ot-line: #d9dfe4;
  --ot-fg: #2a3743;
  --ot-fg-strong: #17212b;
  --ot-muted: #5a6875;
  --ot-accent: #0a7c80;
  --ot-accent-soft: #dcefef;
  --ot-accent-strong: #0b4f52;
  --ot-wire: #b35a1f;
  --ot-header-bg: #f4f6f8;

  --ot-blue: #2f5d9e;
  --ot-danger: #b42318;
  --ot-wire-text: #9a4a15;   /* #b35a1f is only 4.41:1 on the grey surface */
  --ot-control-line: #5a6875;
}

.ot-label { /* small uppercase mono label: column headers, mode badges */
  font: 500 0.72rem/1 var(--ot-font-mono);
  text-transform: uppercase;
  letter-spacing: 0.12em;
  color: var(--ot-muted);
}
```

Theme switching is `data-theme` on `:root` (website behavior). Default dark; follow `prefers-color-scheme` on first run, then remember the user's toggle.

## 3. Typography mapping

| Use | Font |
|---|---|
| App title, window header, section headings, wordmark | Barlow Semi Condensed 500 to 700 |
| Labels, descriptions, buttons, dialogs | IBM Plex Sans 400/500/600 |
| Values, register addresses, raw hex, units, URLs, JSON, status chips | IBM Plex Mono 400/500 |
| Column headers, mode badge, small captions | `.ot-label` |

Fonts come from `@fontsource/*` npm packages (bundled woff2). Never load fonts from a CDN: the desktop app and the gateway must work offline.

## 4. Logo and app icon

- The mark is a woven fabric: three rounded horizontal "weft" bars (blue, teal, copper) over three vertical "warp" bars, masked for over/under weave, rotated -45 degrees, `viewBox="0 0 48 48"`.
- Two files, swapped by theme exactly like the website `Logo.astro`: `logo-dark.svg` (weft `#8AB0E8`, `#3FC1C6`, `#E08A4E`; warp `#E6EAEE`) and `logo-light.svg` (weft `#2F5D9E`, `#0A7C80`, `#B35A1F`; warp `#17212B`). Copy both from `website/src/assets/`.
- **Strip the `<metadata>` element** (an embedded C2PA manifest, about 7.7 kB of base64) when copying: 9.3 kB becomes about 1.6 kB. Keep the geometry untouched.
- **The application's own icon is the protocol's icon, not the weave mark** (user decision 2026-10-14): OT Fabric will have several GUIs, one per protocol, and each is recognized by the icon its protocol has on the website. For the Explorer that is `website/public/icons/go-sunspec.svg` (sun over a solar panel, copper `#B35A1F` on the `#F6E6DA` tile), copied with the `<metadata>` element stripped to `frontend/src/assets/sunspec.svg`. It is used for:
  - the program icon: `cmd/sunspec-explorer/appicon.png`, rendered by `packaging/icons/render_appicon.py` in the layout macOS expects (the rounded tile at 824 px, centred on a transparent 1024 px canvas; a full-bleed square would sit oversized and square-cornered in the Dock). The macOS bundle derives its `.icns` from it, and it is embedded as window and Dock icon;
  - the favicon of the web build (`frontend/public/favicon.svg`);
  - the header, at 32 px, left of the product name.
- **Header**: protocol icon, then the product name "**SunSpec Explorer**" as the main item (Barlow Semi Condensed 600, 1.45rem), then a small "powered by [weave mark, 16 px] **OT** Fabric" in the muted color (the weave mark swaps with the theme as above; wordmark at 14 px, "OT" at weight 700). The weave mark and the wordmark are the brand's signature; the product is what the header names.
- Web build (served by the daemon): `<link rel="icon" href="/favicon.svg">`, `<meta name="theme-color" content="#17212B">` (the value the website uses), `<meta name="color-scheme" content="dark light">`, `<title>OT Fabric SunSpec Explorer</title>`. Same tokens and bundled fonts; no external requests (the CSP allows `'self'` only).

## 5. Icons

There are two icon conventions on the website; reuse both. There is no third-party icon library (`package.json` has none), so do not add one.

**Project icon** (`public/icons/<repo>.svg`, shown at 32 to 40 px; referenced by the `icon` field in `src/data/projects.json`):
- `viewBox="0 0 48 48"`, rounded tile `rx="11"`, glyph in a 24x24 box at `translate(12 12)`, outline only: `fill="none" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"`.
- Tile tint and stroke are paired by domain:

| Domain | Tile fill | Stroke | Used by |
|---|---|---|---|
| General protocol (teal) | `#DCEFEF` | `#0A7C80` | go-modbus, go-opcua, go-bacnet, go-s7comm |
| Energy / power systems (copper) | `#F6E6DA` | `#B35A1F` | go-sunspec, go-iec104, go-iec61850, go-mms |
| Transport layer (blue) | `#E3EAF5` | `#2F5D9E` | go-cotp, go-tpkt, go-serial |
| Discovery (neutral) | `#E6EAEE` | (own) | go-otfp |

- Glyph-on-tile contrast: copper 3.92:1, teal 4.19:1 (fine for graphics, 3:1).
- The Explorer does not get an icon of its own: it uses `go-sunspec.svg` (section 4). A future GUI for another protocol uses that protocol's icon in the same way.

**UI icons** (inline SVG in components): `viewBox="0 0 24 24"`, rendered 18 to 22 px, `stroke="currentColor" stroke-width="1.8"`, round caps and joins, no fill (the only filled icon is the GitHub mark). Examples in `ThemeToggle.astro` (sun, moon) and `SiteHeader.astro` (menu). Create the Explorer's UI icons in this style as small React components in `frontend/src/icons/`: connect, disconnect, refresh/read, play, stop, record, export, search, chevron, warning, error, browser, local, copy. Icon-only buttons need `aria-label` and a tooltip.

## 6. Color semantics for this app

Brand colors alone do not cover the states the Explorer shows. Never carry meaning by color alone: pair every state with an icon and a text label.

| State | Treatment |
|---|---|
| Primary action, selection, live/OK | `--ot-accent` (text on `--ot-accent-soft`: `--ot-accent-strong`) |
| Stale reading, warning, "not implemented" | `--ot-wire-text` (copper) plus a clock/warning icon |
| Read failure, connection error | `--ot-danger` plus an error icon and message |
| Informational, third chart series | `--ot-blue` |
| Mode badge (LOCAL in the desktop application, BROWSER in the web UI) | `.ot-label` style with text, not color-coded |
| Dividers, row separators | `--ot-line` (decorative, 1.3:1 by design) |
| Input and button borders | `--ot-control-line` (must reach 3:1) |

## 7. Accessibility numbers (computed 2026-10-13)

| Pair | Ratio | Verdict |
|---|---|---|
| Dark: fg on bg / muted on surface / accent on surface / wire on surface | 14.8 / 6.2 / 7.4 / 6.1 | pass |
| Dark: blue `#8AB0E8` on surface, danger `#F07178` on surface | 7.3 / 5.6 | pass |
| Light: fg on bg / muted on surface | 12.2 / 5.3 | pass |
| Light: accent `#0A7C80` on surface | 4.61 | passes AA narrowly |
| Light: wire `#B35A1F` on surface `#F4F6F8` | **4.41** | **fails AA for small text**; use `--ot-wire-text` `#9A4A15` (5.76 on surface, 6.24 on white) |
| Light: blue `#2F5D9E`, danger `#B42318` on surface | 6.1 / 6.1 | pass |
| Decorative lines (`--ot-line`) | 1.3 | fine for dividers only |

Table density: IBM Plex Mono for values (monospaced digits align without extra CSS), 13 to 14 px minimum body size, full keyboard navigation, visible focus ring using `--ot-accent`.

## 8. Website follow-ups (do in M4, in the `website` repo, only when the user asks)

- Add `sunspec-explorer` to `src/data/projects.json`: `category: "ui"` (the roadmap entry `ui-apps`, "Graphical tools built on the same libraries. None are public yet.", already exists as `planned`), `protocols: ["sunspec","modbus"]`, `status` `planned` until the repo is public, `icon: "go-sunspec"`.
- Add the proposed semantic tokens (`--ot-blue`, `--ot-danger`, `--ot-wire-text`, `--ot-control-line`) to `src/styles/tokens.css` so website and apps stay in sync; consider publishing the tokens as one shared file.
- Link from `src/content/protocols/sunspec.mdx` once released.
