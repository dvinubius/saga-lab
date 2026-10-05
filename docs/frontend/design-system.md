# Visual style

The pages of sagas are built in the **Dinu Barbu** brand system. This file is
the authority for how that system is used here: for this app's UI, **this file
wins** over the upstream system, and departures are written down here rather
than smuggled into the markup or the CSS. A UI change that contradicts this
file updates it in the same change.

**Where the system comes from:** `/Users/dinu/work/DinuBarbu/design-system`.
`readme.md` is the written spec, `tokens/` the values, `components/core/`
reference implementations. It is also available as a Claude skill named
`dinu-barbu-design`. Treat it as the origin of the brand rather than as a
rulebook: the palette, the two typefaces and the shape language are the brand
and should stay recognisably the same everywhere.

**Where this file comes from:** hooklook, the webhook inspector
(`/Users/dinu/work/AGENTIC-LEARNING/Go/webhook-inspector/.agents/design-system.md`).
Its adaptations were worked out for a data-rich product UI, and sagas takes
them over wherever they apply. Where sagas departs from hooklook, this file
says so.

## Where it lives

The pages are server-rendered Go templates in
`internal/transferservice/pages.html`. There is no frontend build and no
framework. Everything else sits in `internal/transferservice/static/`, is
embedded into the Transfer Service binary and served under `/static/`:

| File | What it is |
| --- | --- |
| `brand.css` | The brand's color, type, radius and transition tokens, vendored from upstream `tokens/`, apart from the theme-default adaptation (adaptation 1). |
| `app.css` | Values this app needs and the brand does not have, the shared text roles, and every component style. |
| `theme.js` | The theme switch. |
| `fresh.js` | Reloads a page the browser restores from its back/forward cache. |
| `fonts/` | The four subset `.woff2` files built by the design system's `build-webfonts.py`, with the two OFL licences. |
| `favicon.svg` | The product mark on an Ink tile — dark in both themes, because a browser tab is not part of the page. |
| `sagas-logo-row.png` | The 1920 × 1080 link preview for `og:image` and `twitter:image`: the mark beside the wordmark, on Ink. |

The raw brand *values* in `brand.css` stay in step with upstream — Ink, Paper,
Ember and the typefaces are the brand itself. Values this app adds live in
`app.css`, where they are visibly this app's own: `--text-dim`, the data
colors, `--danger`, the code ramp, `--surface-float`, `--float-hairline`,
`--shade-hairline`, `--surface-shade-2`, and the layout sizes.

Pages show live balances and statuses, so they are never stale: they are
served `no-store`, and `fresh.js` reloads one the browser restores from its
back/forward cache. Going back from a transfer to the home page shows the
current balances and Transfers list; the amount field is not restored.

Fonts are served with a year-long immutable cache; CSS and JS are not cached.
A transfer page whose evidence is not yet complete, and the home page while a
transfer is pending, reload themselves once a second, and fonts fetched again on every reload would flash.
Changing a font file therefore means renaming it.

## The binding rules

**Color.** True-neutral base plus one warm accent at two lightnesses. Ink
`#141414` / Paper `#FAFAFA`. Exactly one muted grey per theme: Stone `#6E6E6E`
on light, Muted on Dark `#9A9A9A` on dark — there is no fainter second tier.
This app adds one tier *between* body and muted rather than below it; see
adaptation 6.

**Accent pair rule:** Ember `#A8500F` on light, Ember Light `#DE8A42` on dark.
Never the reverse.

**Expanded palette.** Taken over from hooklook. Every hue comes as a
light-theme / dark-theme pair, like the accent:

| | Light | Dark | Role here |
| --- | --- | --- | --- |
| Ember | `#A8500F` | `#DE8A42` | brand, the primary action, quiet-link rules |
| Teal | `#087581` | `#5BC8D0` | primary data and syntax color; a state's hue (rule 14) |
| Violet | `#6B5D91` | `#AFA3CF` | secondary data and syntax color — nothing spends it yet |
| Brick | `#A03028` | `#E0756A` | `--danger`: a rejected value or refused submission (rule 13); a failed state's hue (rule 14) |

Stone, Ink and Paper carry the overwhelming majority of the interface. Teal
and Violet are for syntax on code surfaces and for data marks (a chart, a
timeline), and are declared so that the first of those does not have to
invent them. The one exception is a state that needs a hue (rule 14).

**Accent dosage** is binding: at most ~2% of any composition, never on running
text, never the sole carrier of a UI state. Several accent elements may share
a view as long as none competes for the eye. Here the accent is spent on the
**Transfer** button, the dot of the selected scenario radio, the rule under a
quiet link (**Follow pending transfer →**, the links in a transfer's In depth
section, **→ dinubarbu.com**), the hub of the product mark and the
brackets of the footer wordmark. A state that uses it always carries a text
label too.

**Type.** Space Grotesk (headings, body, the wordmarks; 400/500/700) and Azeret
Mono (everything technical: IDs, amounts in lists, service names, timestamps,
labels; 400/500 only). The mono tokens are already sized at 0.93× nominal.
Space Grotesk has no true italic — emphasis is weight or accent, never
synthesized slant. Headings are 500 at −0.022em. Sentence case everywhere,
except the machine's own asides, which are lowercase; section labels are set
in caps by CSS — see adaptation 10.

**Shape.** 2px corners on controls and surfaces (adaptation 5). No shadows,
inner or outer. No gradients, textures or background imagery. Separation is
1px hairlines (`#E6E6E6` light / `#2C2C2C` dark, non-text only) and flat
neutral fills. Cards are a neutral fill (`--surface-card`) with no border and
no shadow — the two balance cards, and a transfer's In depth section, which
holds the links that leave the page for raw evidence (**View as JSON →** and
**Explore the trace in Grafana →**, both of which open in a new tab). A section on a
card keeps its own label and gap and takes the card's 16px × 18px padding.

**Code and terminal surfaces** follow the theme — hooklook's departure from
the brand, which keeps them dark in both. On dark pages code sits on Panel
`#1C1C1C` with a hairline; on light pages on Shade `#F0F0F0` with no border.
No code surface exists yet; message payloads and traces are the likely first
ones, and they use the `--code-*` tokens in `app.css`:

| Tier | Light | Dark | For |
| --- | --- | --- | --- |
| name | Violet `#6B5D91` | Violet `#AFA3CF` | JSON keys |
| value | Teal `#087581` | Teal `#5BC8D0` | JSON strings, numbers, literals |
| emphasis | Ink `#141414` | Paper `#FAFAFA` | emphasis outside highlighted bodies |
| body | `#3D3D3D` | Muted Strong `#B5B5B5` | text, output |
| recede | Stone `#6E6E6E` | Muted on Dark `#9A9A9A` | punctuation, comments |
| accent | Ember `#A8500F` | Ember Light `#DE8A42` | at most one line, often none |

Highlighted content is rendered as text through the template — captured bytes
never reach the page as markup. An inline `<code>` inside a mono row (the
transfer ID, the trace ID) takes no fill of its own.

**Motion.** None is defined in the brand. Default to no animation; nothing
bounces, pulses or spins. Hover changes on buttons and links — color,
background, border — ease over 150ms (`--transition-state` from the brand,
spent through `--hover-transition`). List rows change instantly. Something
in progress says what it is waiting for in words, and the page reloads itself
until it is done; there is no spinner.

**Buttons and links.** Primary: solid accent fill — Paper text on light, Ink
text on dark (Paper on Ember Light is too faint). Its hover is
`--accent-on-hover`: Ember Hover `#8A420C` on light, Ember Light Hover
`#E49F64` on dark. *Departure from hooklook,* which predates the upstream dark
hover and goes to Paper instead. Secondary: body text in a 1px hairline
outline that goes to body text on hover — **← Back** on the transfer page,
directly under the balances, whose arrow points the way it goes. Quiet link:
body text over a 1px accent bottom border; inline it takes a trailing `→`
(**Follow pending transfer →**), in the footer a leading
one (**→ dinubarbu.com**), as in hooklook. Never underline a button. A
disabled control keeps its shape and drops to `--disabled-opacity` with
`cursor: not-allowed`.

**Focus.** The brand defines no focus style. Not the accent — a focus ring is
a state the accent would then carry alone. `:focus-visible` is a 2px outline
in body text. A text field shows focus by turning its border and a 1px inset
outline to the muted grey.

**Glyphs.** The brand has no icon system and no emoji: Unicode does icon duty —
`↳` `·` `→` `←` `×` `✓` `//` `[ ]`, all of which ship in the vendored font
subsets. `×` and `✓` are a valence pair, used together, never as a lone
decorative tick. Apart from the product mark (adaptation 3), the only drawn icons are the theme switch's sun and moon, the
GitHub mark in the footer, and the info mark after a History entry that has an
explanation (a circled "i", 14px, muted at rest and body on hover or focus),
all drawn inline in the template in `currentColor`. The info mark is drawn
because `ⓘ` is outside the vendored font subsets.

## Adaptations for this app

1. **Dark is the default.** The design system ships light as the bare `:root`
   default with dark under `[data-theme="dark"]`. `brand.css` instead declares
   the semantic aliases under both explicit theme attributes, and the page
   always carries one: the server renders `data-theme="dark"`, and
   `theme.js`, loaded blocking in the head, switches to light before the first
   paint when `sagas.theme` in `localStorage` says so. That ordering
   matters here more than in hooklook: a pending transfer page reloads every
   second, and a page that painted dark first would flicker on each reload.
   Raw palette values are unchanged.

2. **The page frame** is hooklook's. A top bar holds the product wordmark on
   the left and the theme switch on the right, over a hairline. The footer,
   over a hairline, holds the personal wordmark `[ Dinu Barbu ]` on the left,
   the muted `↳ dvinubius` credit with the GitHub mark dead centre — linking
   to the source — and the quiet `→ dinubarbu.com` link on the right.

   *Departure from hooklook:* hooklook's frame is exactly one viewport high
   and its workspace scrolls inside it. The pages of sagas are documents, so the
   frame is *at least* one viewport high: the footer sits at the bottom of a
   short page and below the content of a long one, and the page scrolls as a
   whole.

   The frame is `--shell-width` (1180px) wide, as in hooklook. The content
   inside it is a single column of `--content-width` (600px), left-aligned
   under the wordmark. Sections sit 40px apart; the items in a section 14px
   apart. Where two sections need a firmer break, a hairline centred in the
   gap divides them.

3. **The product wordmark carries a mark**, hooklook-style. "sagas" in
   Space Grotesk 500 at 24px with the wordmark's −0.018em tracking, in body
   text, after the mark. It wears no brackets — those belong to the personal
   wordmark in the footer. It is the page's `h1` and links home.

   The mark is a pair of nodes on a looping path around a hub: two rounded
   squares on the diagonal, top-right and bottom-left, joined anticlockwise by
   an arrow over the top into the bottom-left node and one under the bottom
   back into the top-right node, with a dot between them. It is drawn inline
   in the template on a 24-unit grid, stroke 2 with round caps and joins:

   - nodes 7 × 7, corner radius 1.75, at `(15.5,1.5)` and `(1.5,15.5)`
   - top arrow `M12.5 5H9a4 4 0 0 0-4 4v3`, head `M7 10l-2 2-2-2`
   - bottom arrow: the top one turned 180° about `(12,12)`
   - hub: a filled dot of radius 2 at `(12,12)`
   - viewBox `0.5 0.5 23 23` — square and centred on the strokes

   Nodes and arrows are body text; the hub is the accent.
   As in hooklook, the mark is 1.15em square, baseline-aligned, 0.38em before
   the name, and pushed down by half its height less 0.343em, which puts its
   centre on the middle of the name's ink.

4. **An expanded palette and theme-aware code surfaces**, both described
   above.

5. **Softened corners.** Controls — buttons, fields, the theme switch — take
   `--radius-control`; surfaces — cards, code blocks, floats —
   `--radius-surface`. Both are the brand's `--radius` (2px), which upstream
   adopted after hooklook introduced it. Rows in a list or table stay square:
   they are parted by hairlines, not boxed.

6. **Three text tiers on the page, not two.** With only body and muted,
   everything that was not primary fell the whole way to muted — running prose
   included, which on light is 4.89:1 and below AA at 13px. The middle tier is
   the code ramp's body value, lifted out as `--text-dim`. There is still no
   tier *fainter* than the one muted grey.

   | Tier | Light | Dark | Used for |
   | --- | --- | --- | --- |
   | `--text-body` | Ink, 17.6:1 | Paper, 17.6:1 | the wordmarks, titles, section labels, balance figures, the name column of a row |
   | `--text-dim` | `#3D3D3D`, 10.4:1 | `#B5B5B5`, 9.0:1 | prose, control labels, units, statuses, fact values, table values |
   | `--text-muted` | Stone, 4.9:1 | Muted on Dark, 6.6:1 | the theme switch at rest, the footer credit, placeholders, the info mark at rest, a float's edge |

   **A label outranks what sits beside it:** "Bank A" is body, its "credits"
   is dim; a fact's name is body, its value dim.

7. **Floating layers** — popovers, menus, dialogs — take hooklook's
   treatment: `--surface-float` as their fill (a step off the page on dark;
   the page itself on light, where there is no room), a Stone edge on light
   and Muted on Dark on dark, and `--float-hairline` redefined as `--hairline`
   for their subtree. A fill inside a float mixes from `--surface-float`
   toward `--text-body`. A modal keeps a hairline rather than a Stone edge.

   The first is the explanation behind a History entry's info mark (`.float`):
   a native popover, opened by clicking the mark and closed by clicking
   elsewhere or Esc, at most 360px wide, holding prose set like a `.note`. It
   is anchored under the mark where the browser supports anchor positioning,
   and centred in the viewport where it does not. A page that is still
   reloading itself closes it on the next reload.

8. **A fill steps off its own ground, not off the page.** `--surface-shade` is
   a fill whose ground is the page — the hover of a row in the Transfers list.
   `--surface-shade-2` is a fill inside a filled region, and
   `--shade-hairline` the hairline on one; a filled region (hooklook's request
   list) is not used here yet, but these are what it takes when it is.

9. **Display sizes above the brand's tokens.** The brand's tokens cover running
   text and are used for it: `--text-small` for prose and buttons,
   `--text-mono-meta` for rows, facts and control labels,
   `--text-mono-micro` for the footer credit and table column headings. A few
   display roles take their own size:

   | Size | Role |
   | --- | --- |
   | 30px (`--text-heading`) | a balance figure |
   | 24px sans | the product wordmark |
   | 20px | the page title ("Transfer of 25 credits") |
   | 16px | section labels (TRANSFERS, HISTORY), and the footer wordmark |

   This is a record of what this app settled on, not a scale anything else
   has to adopt. A token is still the first thing to reach for where one fits;
   a display role is allowed its own number, and a new one is added here.

10. **Section labels are set in caps.** The brand sets mono meta-labels
    lowercase; naming a region of a working screen is a different job, and
    caps with 0.12em tracking separate a label from what sits beside it
    without spending brightness or color. `.caps` is the class; the template
    keeps the text in sentence case and the uppercasing is CSS, so a screen
    reader is not handed shouting. `.caps` carries no size: a section label
    adds `.section-label` (16px), a card label and a table column heading
    take theirs from the component.

11. **Shared roles live in `app.css`, and a role carries its tier** when its
    meaning implies one:

    | Role | Set as | Used for |
    | --- | --- | --- |
    | `.caps` | mono, 500, 0.12em, uppercase, body | section labels, card labels, table column headings; no size |
    | `.fact` | mono, `--text-mono-meta`, dim | a control label, a unit beside a figure |
    | `.note` | inherited sans, `--text-small`, `--leading-small`, dim | prose meant to be read: the home page's intro, the History explanation |
    | `.dim` | dim, nothing else | a value inside a row that is already mono |
    | `.sr-only` | visually hidden | text for screen readers only |

    hooklook has two more, to take over unchanged when they are first
    needed: `.comment` (mono, `--text-mono-meta`, muted, 0.01em — the
    machine's own `//` asides) and `.micro` (mono, `--text-mono-micro`, size
    only). Text
    doing the same job in two places gets a role here rather than a second
    declaration.

12. **Lists and tables are rows parted by hairlines.** One item per row, a
    hairline above the first and under each, square, mono at
    `--text-mono-meta` with code leading, 6px × 12px padding — hooklook's
    request headers, generalised. Names are body, values dim;
    column headings are `.caps` at `--text-mono-micro`.

    A row that leads somewhere is a whole-row link whose hover is
    `--surface-shade`, applied instantly.

13. **Form controls.** A field is mono at `--text-mono-meta` on the page
    surface with a hairline border, at `--row-height` (36px), like a button.
    A radio choice is a borderless fieldset whose visually hidden legend
    names it; each option is a `.fact` label after its native radio. The
    selected radio's dot is the accent, through `accent-color`; the filled
    native radio marks the selection on its own, so the accent never carries
    it alone. A rejected value or a refused submission is explained under
    the control in Brick, sans at `--text-small` — the text says what is
    wrong; the color only marks it. A disabled field or radio choice, like a
    disabled button, drops to `--disabled-opacity`.

14. **Statuses are words, not colors.** The brand has one accent and no status
    palette. A transfer's status and its history steps take no hue:
    "Completed" and "Waiting for Bank A to debit" differ in words alone, and
    so do the Evidence row's "Being collected" and "Complete".

    A state that does need a hue takes Brick for a failure and Teal
    otherwise, and keeps its words, so the hue is never the only signal. Not
    the accent: it sits too close to Brick on the light theme.
