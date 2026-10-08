# Design system

Saga Lab's UI is built in the **Dinu Barbu** brand system
(`/Users/dinu/work/DinuBarbu/design-system`, also the `dinu-barbu-design`
skill). This file is the system for this app and wins over upstream. A UI
change that departs from it updates the rule here in the same change — the
system grows; it does not collect exceptions.

## Tokens

`static/brand.css` holds the brand's tokens, vendored from upstream `tokens/`;
its raw values stay in step with upstream. `static/app.css` holds every token
this app adds, the shared text roles and the components.

Dark is the default theme. Semantic tokens are declared under both
`[data-theme="light"]` and `[data-theme="dark"]`; the page always carries one,
and a stored light preference is applied before first paint.

## Color

True-neutral base plus one warm accent. Ink `#141414` / Paper `#FAFAFA`.

**Text tiers.** Three, and none fainter than muted:

| Token | Light | Dark | For |
| --- | --- | --- | --- |
| `--text-body` | Ink | Paper | titles, labels, names, figures |
| `--text-dim` | `#3D3D3D` | `#B5B5B5` | prose, values, statuses, control labels, units |
| `--text-muted` | Stone `#6E6E6E` | `#9A9A9A` | placeholders, idle icons, idle data marks, edges of floats and pills |

**Rank.** A label outranks its value: name body, value dim. Where the value
is the point — a balance, a count, the amount — the number is body (weight 500)
and its name drops: muted beside a display figure or in a summary line.

**Highlight.** `--highlight` marks what playback has lit — nodes, moving
messages, the current state and message, the timeline's progress and ticks.
Body on light; `#F3F6F8` on dark, a faintly cool white as luminous as
`#F0F6FB` but half as saturated, so lit marks sit apart from Paper text.

**Accent.** Ember `#A8500F` on light, Ember Light `#DE8A42` on dark, never the
reverse; `--accent-on-hover` is `#8A420C` / `#E49F64`. At most ~2% of a view,
never on running text, never the sole carrier of a state. It is spent on: the
primary button, the selected radio's dot, quiet-link rules, the product mark's
hub, the footer wordmark's brackets, and text selection (`--accent-glyph`).
Data marks never take it.

**Hues.** Every hue is a light / dark pair:

| Token | Light | Dark | For |
| --- | --- | --- | --- |
| `--teal` | `#087581` | `#5BC8D0` | a non-failure state; JSON values |
| `--violet` | `#6B5D91` | `#AFA3CF` | secondary data marks; JSON keys |
| `--danger` | `#A03028` | `#E0756A` | errors; a failure state |

A state that needs a hue takes `--danger` for failure and `--teal` otherwise,
and always keeps its words. In History a scenario may mark the entry showing
its simulated fault in danger and the entry showing the system avoiding the
fault's consequence in teal. A data mark (diagram, timeline) draws idle parts
muted, active parts body, and the active path or progress in body at 2px. In
the playback diagram the same hues mark the paths of those entries — a fault
in danger, its avoidance in teal — and a hued mark from a past step drops to
55% opacity. A moving message keeps body color whatever its path's hue.

**Surfaces and edges.**

| Token | Light | Dark | For |
| --- | --- | --- | --- |
| `--surface-page` | Paper | Ink | the page, fields |
| `--surface-panel` | `#FFFFFF` | `#191919` | panel fill |
| `--surface-panel-head` | `#F2F2F2` | `#202020` | panel header and footer strips |
| `--surface-card` | `#EDEDED` | `#262626` | card fill |
| `--surface-shade` | `#F0F0F0` | `#1B1B1B` | hovered or current row on the page |
| `--panel-shade` | `#F4F4F4` | `#232323` | `--surface-shade` inside a panel |
| `--surface-shade-2` | shade mixed 3.5% toward body | ← | a fill inside a filled region |
| `--surface-float` | page | card | floating layers |
| `--control-fill` / `-hover` | `#EDEDED` / `#E0E0E0` | `#2A2A2A` / `#343434` | tonal buttons |
| `--hairline` | `#E6E6E6` | `#2C2C2C` | separators, field borders |
| `--shade-hairline` | `#DDDDDD` | `#303030` | a hairline on a filled region |
| `--float-hairline` | hairline | `#3A3A3A` | `--hairline` inside a float |
| `--panel-edge` | `#DADADA` | `#333333` | panel frames, unselected choices |
| `--control-edge` | `#ADADAD` | `#555555` | secondary buttons, the selected choice |

Edges climb in firmness: hairline → panel edge → control edge → muted. A fill
steps off its own ground, so nested regions redefine `--surface-shade` and
`--hairline` rather than adding one-off values.

## Type

Space Grotesk (sans: headings, prose, buttons, wordmarks; 400/500/700) and
Azeret Mono (everything technical: IDs, amounts, labels, timestamps, rows;
400/500). No synthesized italic: emphasis is weight. Headings are 500 at
`--track-heading`, wordmarks at `--track-wordmark`. Sentence case in markup;
caps only through CSS.

| Role | Set as |
| --- | --- |
| product wordmark | sans 24px |
| page title | sans 32px (home), 20px (other pages) |
| display figure | mono 20px, 500, untracked, tabular |
| figure label | mono 18px, muted |
| lead prose | sans 16px |
| footer wordmark | sans 16px |
| primary button | sans 14px |
| prose, buttons, errors | `--text-small` (13px) |
| fields in a card | mono 13px |
| rows, facts, control labels, panel heads | `--text-mono-meta` (12px) |
| column headings, strip labels, footer credit, timestamps in a row | `--text-mono-micro` (11px) |

**Text roles** (`app.css`) carry their tier:

| Class | Set as | For |
| --- | --- | --- |
| `.caps` | mono 500, 0.12em, uppercase, body; no size | panel heads, column headings, strip labels (dim) |
| `.fact` | mono, `--text-mono-meta`, dim | control labels, units, short status lines |
| `.note` | sans, `--text-small`, `--leading-small`, dim | prose meant to be read |
| `.dim` | dim only | a value inside an already-mono row |
| `.sr-only` | visually hidden | screen-reader text |

Text doing the same job in two places gets a role rather than a second
declaration.

## Shape

`--radius-control` and `--radius-surface`, both the brand's 2px. Rows in lists
and tables are square. The one round shape is the status pill. One shadow: the
playback diagram's raised surface. No gradients, no textures; otherwise
separation is hairlines and flat fills.

## Components

**Frame.** A sticky top bar (product wordmark, theme switch) and a sticky
footer (personal wordmark `[ Dinu Barbu ]`, credit, quiet link), each over a
hairline on the page fill; the page scrolls between them.

**Panel.** Every region of a page is a panel: `--panel-edge` frame,
`--radius-surface`, `--surface-panel` fill, opening with a header strip that
names it (`.caps` on `--surface-panel-head` over a panel-edge rule). Prose and
forms sit in the panel's padding; rows run to its edges. A panel whose rows
name themselves drops its head. A summary closes a panel as a footer strip on
the head fill with a panel-edge rule above.

**Card.** The page's main form sits in a card instead: `--surface-card`, no
edge, no head; its primary button names it.

**Stage.** An interactive visualization (playback) sits on the page, unframed,
under an 18px `.title` heading that parts it from the panels above. The diagram and the
playback details share a row, 656px and 336px, as do the panels above. The
diagram sits raised: 16px padding (28px at the sides of Overview and
Detailed; Sequence widens to fill), `--radius-surface` and a soft shadow, on
`#F4F4F4` in light; in dark, on the panel fill with a 5% white top-edge
highlight, since a shadow alone does not read on Ink. Below it the view strip
sits
centered above the centered controls: first step, step back, play, step
forward, last step. The diagram has three views of equal height — Overview,
Detailed and Sequence — so switching moves nothing else. While playback is not
ready, the wait takes the diagram's place, the details side stays empty, and
the view strip and controls are disabled.

**Rows.** Lists and tables are rows parted by hairlines, mono at
`--text-mono-meta` with `--leading-code`. Names body, values dim; column
headings `.caps` at micro. A row that leads somewhere is a whole-row link
whose hover is `--surface-shade`. Rows tracking progress: the current row is
shaded (Paper on light — quieter than hover — and `--panel-shade` on dark),
rows not yet reached drop to `--disabled-opacity`.

**Buttons.** All at `--row-height` (40px) unless noted, sans, never
underlined.
- Primary — accent fill, Paper text on light, Ink on dark, hover
  `--accent-on-hover`. A form's one call to action: 14px, spans the form,
  taller than `--row-height`.
- Secondary — body text in a `--control-edge` outline, going to body on hover.
  Inside a row it is compact: 28px tall, 12px. An action on a whole panel
  sits below it, spanning its width.
- Tonal — `--control-fill`, no edge, going to `--control-fill-hover`.
- Tonal outlined — `--control-fill` inside a `--control-edge` outline, going
  to `--control-fill-hover` and a body outline; playback controls.
- Segmented strip — a row of joined choices with no radio dots, each boxed in
  `--panel-edge`; the active one takes `--control-fill`, a `--control-edge`
  outline and body text at 500, the others dim text that goes to body on hover.
- Icon-only — square, with `aria-label` and `title` naming the action.
- Quiet link — body text over a 1px accent bottom border; inline it takes a
  trailing `→`, in the footer a leading one.

**Fields.** Mono on `--surface-page` with a hairline border, at
`--row-height`; placeholder muted.

**Radio choices.** A borderless fieldset with a `.fact` legend; each option a
`.fact` label after its native radio, boxed in `--panel-edge`, the selected
one in `--control-edge`. The dot takes the accent via `accent-color`.

**Errors.** Under the control they concern, danger, sans `--text-small`. The
words say what is wrong; the color only marks it.

**Status pill.** Statuses are words, not colors. Mono 500 body text inside a
`--text-muted` border with fully rounded ends; no hue.

**Float.** Popovers and other floating layers: `--surface-float` fill,
`--text-muted` edge, `--radius-surface`, `--hairline` redefined as
`--float-hairline` inside. Content set like a `.note`.

**Waiting.** Something in progress says in words what it waits for (a
`.fact`). A wait that fills a region shows the ring spinner — hairline ring,
body arc — centered on the hub of the product mark, drawn as a 6% watermark
with its hub in `--accent-frame`; the words sit below the spinner. A value not
yet known shows a small spinner in its place. A waiting page updates in place
and never reloads, so the spinner keeps turning until its wait ends.

**Code surfaces** follow the theme: `--code-bg` (Shade on light, Panel
`#1C1C1C` with `--code-edge` hairline on dark). Keys violet, values teal,
`--code-emphasis` for emphasis, `--code-body` for text, `--code-recede` for
punctuation and comments, the accent on at most one line. Highlighted content
is rendered as text, never markup.

## States

**Hover.** Buttons and links change color, background and border over
`--hover-transition` (150ms). Row links change instantly.

**Focus.** `:focus-visible` is a 2px body-text outline — never the accent. A
field shows focus by turning its border and a 1px inset outline muted.

**Submitting.** A form whose answer is the page it sits on (top-up, reset
all) posts in the background and updates that page in place; its button is
disabled until the answer arrives.

**Disabled.** Keeps its shape, drops to `--disabled-opacity`, `cursor:
not-allowed`, and keeps its edge on hover.

## Motion

Hover transitions, the waiting spinner, and playback. Nothing bounces or
pulses.

Playback runs on one clock. Each step's motion takes three quarters of its
dwell, clamped to 0.45–1.4 s; playback slows both by 1.75 except for a step
spent waiting. A message's path draws itself hop by hop with cubic ease-in-out
while the message rides its tip, and a node lights as the message arrives. A
failure draws a short danger stub along the edge it could not take, ending in
a perpendicular bar. The timeline fills continuously. Pause freezes all of it;
stepping forward animates; stepping back is instant. Node and state color
changes ease over 220 ms.

## Glyphs and icons

No emoji, no icon set: Unicode does icon duty — `↳ · → ← × ✓ // [ ]`, all in
the vendored font subsets. Arrows point the way they go. `×` and `✓` are a
valence pair, never a lone decorative tick. Draw an icon only when no glyph
fits or the glyph is outside the subsets: inline SVG in `currentColor`; control
icons 16px on a 24-unit grid, stroke 2, round caps and joins, unfilled. The
product mark is body text with an accent hub.

Diagram glyphs, drawn the same way at stroke 1.6: the Transfer Service as an
orchestrator fanning out to four nodes, the broker and its queues as a
horizontal cylinder (the message-channel pipe), a bank as a pedimented
building with columns. A message is an envelope in Overview and a filled token
in Detailed.
