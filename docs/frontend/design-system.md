# Visual style

Saga Lab's pages are built in the **Dinu Barbu** brand system. This file is
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
Its adaptations were worked out for a data-rich product UI, and Saga Lab takes
them over wherever they apply. Where Saga Lab departs from hooklook, this file
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
| `playback.js` | Steps a ready transfer page's playback through its History rows and lights each row's path in the diagram (adaptation 15). |
| `fonts/` | The four subset `.woff2` files built by the design system's `build-webfonts.py`, with the two OFL licences. |
| `favicon.svg` | The product mark on an Ink tile — dark in both themes, because a browser tab is not part of the page. |
| `saga-lab-logo-row.png` | The 1920 × 1080 link preview for `og:image` and `twitter:image`: the mark beside the wordmark, on Ink. |

The raw brand *values* in `brand.css` stay in step with upstream — Ink, Paper,
Ember and the typefaces are the brand itself. Values this app adds live in
`app.css`, where they are visibly this app's own: `--text-dim`, the data
colors, `--danger`, the code ramp, `--surface-float`, `--float-hairline`,
`--shade-hairline`, `--surface-shade-2`, the panel and control tokens
(adaptation 16), and the layout sizes.

Pages show live balances and statuses, so they are never stale: they are
served `no-store`, and `fresh.js` reloads one the browser restores from its
back/forward cache. Going back from a transfer to the home page shows the
current balances and Transfer history; the amount field is not restored.

Fonts are served with a year-long immutable cache; CSS and JS are not cached.
A transfer page whose replay is not yet ready, and the home page while a
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
| Teal | `#087581` | `#5BC8D0` | primary data and syntax color; a state's hue, a scenario's avoided issue (adaptation 14); the playback diagram's travelled path (adaptation 15) |
| Violet | `#6B5D91` | `#AFA3CF` | secondary data and syntax color — nothing spends it yet |
| Brick | `#A03028` | `#E0756A` | `--danger`: a rejected value or refused submission (adaptation 13); a failed state's hue, a scenario's simulated fault (adaptation 14), Bank B's "unavailable" in the playback diagram (adaptation 15) |

Stone, Ink and Paper carry the overwhelming majority of the interface. Teal
and Violet are for syntax on code surfaces and for data marks (a chart, a
timeline), and are declared so that the first of those does not have to
invent them. The one exception is a state that needs a hue (adaptation 14).

**Accent dosage** is binding: at most ~2% of any composition, never on running
text, never the sole carrier of a UI state. Several accent elements may share
a view as long as none competes for the eye. Here the accent is spent on the
**Transfer** button, the dot of the selected scenario radio, the rule under a
quiet link (**Follow pending transfer →**, a transfer's **JSON →** and
**Grafana →** links, **→ dinubarbu.com**), the hub of the product mark and the
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
inner or outer. No gradients or textures; the one background image is the
product mark behind the waiting playback (adaptation 15). Separation is
1px hairlines (`#E6E6E6` light / `#2C2C2C` dark, non-text only) and flat
neutral fills. Every region of a page is a **panel** (adaptation 16): a
framed surface with a header strip that names it.

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

**Motion.** None is defined in the brand. Nothing bounces or pulses, and the
one thing that spins is the waiting playback's spinner (adaptation 15).
Hover changes on buttons and links — color, background, border — ease over
150ms (`--transition-state` from the brand, spent through
`--hover-transition`). List rows change instantly. Something in progress says
what it is waiting for in words, and the page reloads itself until it is done.
Playback (adaptation 15) steps from entry to entry, and each step changes
instantly.

**Buttons and links.** Primary: solid accent fill — Paper text on light, Ink
text on dark (Paper on Ember Light is too faint). Its hover is
`--accent-on-hover`: Ember Hover `#8A420C` on light, Ember Light Hover
`#E49F64` on dark. *Departure from hooklook,* which predates the upstream dark
hover and goes to Paper instead. Secondary: body text in a 1px
`--control-edge` outline (`#ADADAD` light / `#555555` dark — firmer than a
hairline, so it reads against the page and a panel alike) that goes to body
text on hover — **← Back** on the transfer page, whose arrow points the way
it goes, and **+100 credits** in the home page's Bank A row, disabled while a
transfer is pending (the pending note under the transfer form says why). A
disabled secondary keeps its edge on hover. The playback controls have their
own style (adaptation 15). Quiet link:
body text over a 1px accent bottom border; inline it takes a trailing `→`
(**Follow pending transfer →**), in the footer a leading
one (**→ dinubarbu.com**), as in hooklook. The links that leave a transfer
page for raw evidence sit at the end of the fact they belong to, in the row's
mono, with the trailing `→`: **JSON →** after the Transfer ID and
**Grafana →** after the Trace ID. Both open in a new tab. Never underline a button. A
disabled control keeps its shape and drops to `--disabled-opacity` with
`cursor: not-allowed`.

**Focus.** The brand defines no focus style. Not the accent — a focus ring is
a state the accent would then carry alone. `:focus-visible` is a 2px outline
in body text. A text field shows focus by turning its border and a 1px inset
outline to the muted grey.

**Glyphs.** The brand has no icon system and no emoji: Unicode does icon duty —
`↳` `·` `→` `←` `×` `✓` `//` `[ ]`, all of which ship in the vendored font
subsets. `×` and `✓` are a valence pair, used together, never as a lone
decorative tick. Apart from the product mark (adaptation 3) and the playback diagram (adaptation 15), the only drawn icons are the theme switch's sun and moon, the
GitHub mark in the footer, the info mark after a History entry that has an
explanation (a circled "i", 14px, muted at rest and body on hover or focus),
and the playback controls' step, pause, play and replay icons (16px on a
24-unit grid, stroke 2 with round caps and joins, unfilled), all drawn inline
in the template in `currentColor`. The info mark is drawn
because `ⓘ` is outside the vendored font subsets.

## Adaptations for this app

1. **Dark is the default.** The design system ships light as the bare `:root`
   default with dark under `[data-theme="dark"]`. `brand.css` instead declares
   the semantic aliases under both explicit theme attributes, and the page
   always carries one: the server renders `data-theme="dark"`, and
   `theme.js`, loaded blocking in the head, switches to light before the first
   paint when `saga-lab.theme` in `localStorage` says so. That ordering
   matters here more than in hooklook: a pending transfer page reloads every
   second, and a page that painted dark first would flicker on each reload.
   Raw palette values are unchanged.

2. **The page frame** is hooklook's. A top bar holds the product wordmark on
   the left and the theme switch on the right, over a hairline. The footer,
   over a hairline, holds the personal wordmark `[ Dinu Barbu ]` on the left,
   the muted `↳ dvinubius` credit with the GitHub mark dead centre — linking
   to the source — and the quiet `→ dinubarbu.com` link on the right.

   As in hooklook, the top bar and the footer are always visible and the
   content scrolls between them. *Departure from hooklook:* hooklook's frame
   is exactly one viewport high and its workspace scrolls inside it; Saga
   Lab's page scrolls as a whole, under a sticky top bar and over a sticky
   footer, both on the page fill. The frame is at least one viewport high,
   so the footer sits at the bottom of a short page.

   The frame is `--shell-width` (1180px) wide, as in hooklook. The content
   inside it is a single column of `--content-width` (680px): centred on the
   home page, left-aligned under the wordmark on a transfer page.

   The home page opens with its title, "Inter-Bank Transfer", 12px over
   short `.note` paragraphs at 14px (at most 600px wide) saying what the lab
   demonstrates and what a visitor can do. 40px under them, a row of
   two columns 40px apart, aligned at the top: the transfer form, filling the
   rest of the row, and YOUR CREDITS, 240px wide, holding a row per bank set
   like a transfer's balances panel (adaptation 12): the name 18px mono and
   muted, the figure 20px mono, and under Bank A's row **+100 credits**,
   right-aligned. 40px under the row, TRANSFER HISTORY spans the column.

   The transfer form is a **card**, not a panel: one `--surface-card` fill
   with no edge and no header strip, 24px padding, its parts 18px apart. It
   opens with its title, "Transfer Bank A → Bank B", in sans at 16px, weight
   500. "Amount (credits)" sits 8px above its field, whose placeholder reads
   "Enter amount"; labels and field are
   13px. The field, the scenario options and the **Transfer** button span the
   card; the button is 15px, uppercased in CSS and tracked 0.08em, with 12px × 24px padding, 12px further from the
   options than the form's 12px gap, taller than
   `--row-height`.

   A transfer page opens with **← Back** and, 20px to its right, the
   scenario's name as the page title. Under them, a 1040px row: the TRANSFER
   DETAILS panel, 600px wide, and the balances panel, 320px wide and centred
   in the rest of the row. Then the status line and, 14px under it, the
   HISTORY panel, 1040px wide. Like History, the row reaches past the
   content column. Sections sit 40px apart; the items in a section 14px
   apart.

3. **The product wordmark carries a mark**, hooklook-style. "Saga Lab" in
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
   `--radius-control`; surfaces — panels, code blocks, floats —
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
   a fill whose ground is the page — the hover of a transfer row in the Transfer history,
   and the current History row during playback.
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
   | 24px sans | the product wordmark |
   | 32px | the home page title, "Inter-Bank Transfer" |
   | 20px | a transfer's page title (its scenario), and a balance figure |
   | 18px mono | a bank's name in YOUR CREDITS and a transfer's balances panel |
   | 16px | the footer wordmark, the transfer card's title |

   This is a record of what this app settled on, not a scale anything else
   has to adopt. A token is still the first thing to reach for where one fits;
   a display role is allowed its own number, and a new one is added here.

10. **Section labels are set in caps.** The brand sets mono meta-labels
    lowercase; naming a region of a working screen is a different job, and
    caps with 0.12em tracking separate a label from what sits beside it
    without spending brightness or color. `.caps` is the class; the template
    keeps the text in sentence case and the uppercasing is CSS, so a screen
    reader is not handed shouting. `.caps` carries no size: a panel head,
    the status line's label, the Outcome
    footer's label and a table column heading take theirs from the
    component.

11. **Shared roles live in `app.css`, and a role carries its tier** when its
    meaning implies one:

    | Role | Set as | Used for |
    | --- | --- | --- |
    | `.caps` | mono, 500, 0.12em, uppercase, body | panel heads, table column headings; no size |
    | `.fact` | mono, `--text-mono-meta`, dim | a control label, a unit beside a figure |
    | `.note` | inherited sans, `--text-small`, `--leading-small`, dim | prose meant to be read: the home page's introduction, the History explanation |
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

    The home page's **Transfer history** lists the visitor's transfers,
    newest first, as a table headed SCENARIO, AMOUNT, RESULT. Each row is a
    whole-row link to the transfer: the scenario cell's link is stretched
    over the row. Top-ups are not listed. With no transfers yet the panel
    holds "No transfers yet." as a `.note`.

    In History, a bank's committed step is followed by the balance it
    reported, `· 100 → 75`, and a rejection by its unchanged balance, `· 0`.
    The figure is a value beside a label, so it is dim (`.dim`), and it takes
    no hue: a refund restoring the source reads in its numbers alone.

    In History, a bank's steps leave the bank out of the label, because the
    column already names it: "Committed the debit", not "Bank A committed
    the debit". The playback details show the full sentence, since they have
    no column.

    Once a transfer is ready, its outcome is shown in two places. The
    **balances panel** on the right of TRANSFER DETAILS has no head: a row
    per bank, its name (18px mono, muted) on the left and "100 → 75 credits"
    on the right — the figures 20px mono, the before figure dim, the arrow
    muted. Bank B after a debit rejection reads "Not involved". Until the
    transfer is ready, the same panel shows each bank's current balance. A
    value never reported is "—".

    The **Outcome** footer closes the HISTORY panel, 16px under the table's
    last hairline, on the header strip's fill with an edge above: OUTCOME,
    then one line of items — "Debit 1 attempt, 1 effect", "Credit …",
    "Refund …" only when a refund was issued, and "Duplicates suppressed 0",
    parted by History's ` · `, dim. The names are muted, the numbers body. A command never issued reads
    "not issued". No count takes a hue: a redelivery stands out by its
    numbers (2 attempts, 1 effect), not by color.

13. **Form controls.** A field is mono at `--text-mono-meta` on the page
    surface with a hairline border, at `--row-height` (36px), like a button.
    A radio choice is a borderless fieldset whose legend names it as a
    `.fact`, 8px above the options; each option is a `.fact` label after its
    native radio, boxed by a `--panel-edge` border at 8px × 12px, 8px from
    the next; the selected option's border steps up to `--control-edge`. In the transfer form the happy path reads "Happy path (no
    issues)"; elsewhere it is "Happy path". The
    selected radio's dot is the accent, through `accent-color`; the filled
    native radio marks the selection on its own, so the accent never carries
    it alone. A rejected value or a refused submission is explained under
    the control in Brick, sans at `--text-small` — the text says what is
    wrong; the color only marks it. A top-up refused while a transfer is
    pending is explained under **+100 credits**, inside the Bank A row. A disabled field or radio choice, like a
    disabled button, drops to `--disabled-opacity`.

14. **Statuses are words, not colors.** The brand has one accent and no status
    palette. A transfer's status and its history steps take no hue:
    "Completed" and "Waiting for Bank A to debit" differ in words alone.

    On a transfer page the status sits on its own line above the HISTORY
    panel: STATUS in `.caps` at `--text-mono-micro`, dim, then the status in
    mono, weight 500. A transfer rejected by Bank A adds the bank's reason:
    "Rejected by Bank A (insufficient funds)". A refunded transfer gives
    none — "Refunded after Bank B rejected the credit" already says why.
    While awaiting admission, the status line says “Another visitor is trying
    this demo. Yours will start automatically when it's your turn.” It uses
    the ordinary status text, with no promised duration or state hue.
    The Transfer history uses the same label, and the pending form remains disabled.

    A state that does need a hue takes Brick for a failure and Teal
    otherwise, and keeps its words, so the hue is never the only signal. Not
    the accent: it sits too close to Brick on the light theme.

    In History, a scenario may spend the same pair on what it demonstrates:
    Brick on the entry showing the fault or issue the scenario simulates,
    Teal on the entry showing how the system correctly avoids the trouble
    that fault would typically cause.

15. **Playback.** The HISTORY panel opens with the playback, above the
    History table, parted from it by a full-width hairline; its padding is
    28px above, 16px at the sides and 32px below. It has two columns, 40px
    apart. The left one, 564px wide, holds the participant diagram,
    described below, and under it only the controls, centred on the diagram.
    The right one holds the timeline, across its full width, and under it the
    details, centred:

    - the current entry's real timestamp in UTC and its real gap to the next
      entry ("+4.980 s to the next entry", or "last entry"), as a `.fact`;
    - the current entry as History shows it — cause, attempt, title, label
      and balance change, with the scenario's Brick and Teal marks — in mono
      at `--text-mono-meta`, with the bank named in the label;
    - the entry's explanation, if it has one, as a `.note`, in place of the
      info mark.

    Playback never starts on its own: a ready page rests paused on the first
    entry, with the middle button on Play.

    **While the replay is not ready** the panel already has its final shape,
    so nothing moves when it arrives. The diagram is drawn idle — every node
    and edge muted, no arrows — and the three controls are disabled, the
    middle one on Play. In place of the timeline and details, a 28px ring
    spinner (a hairline ring with a body-text arc, turning every 0.8 s) sits
    over the product mark, 150px and at 6% opacity, with "Preparing
    playback" as a `.fact` under it. The page reloads every second, which
    restarts the spinner. This is the one spinner in the app.

    **The timeline** has one dot per History row, placed by when playback
    reaches it — each row's start is the sum of the dwells before it — so a
    long real gap reads as a long stretch, within the dwell's 700 ms to 4 s
    bounds. Its track is a 1px muted line; the stretch already played is
    Teal at 2px, up to the current dot. Dots are 7px, body when played and
    muted when not; the current one is 11px and Teal, like the diagram's
    travelled path. Like the path, it changes instantly with the entry, and
    it is hidden from screen readers, which read the details.

    **The controls** are three separate buttons, 12px apart: two labelled
    **Step**, and between them a square icon-only button that is Pause, Play
    or Replay. They are not secondaries. The Step buttons are tonal — a
    `--control-fill` with no border, going to `--control-fill-hover` — and
    the middle one is inverted neutral: a body-text fill with the icon in the
    page color, going to dim on hover. Not the accent, which belongs to
    **Transfer**. A step button carries a drawn skip icon, a triangle
    pointing to a bar, on the side it goes towards, and its `aria-label` and
    `title` say which way: "Step back", "Step forward". The middle button
    shows two bars for Pause, a triangle for Play and an anticlockwise arrow
    for Replay, and its `aria-label` and `title` name the action. Stepping
    pauses. At either end the step that would leave the rows is disabled.
    At the end the panel rests on the last entry and the middle button is
    **Replay**.

    Each entry stays on screen for its real gap to the next, at least 700 ms
    and at most 4 s; the server computes this and the script only reads it.

    History stays the complete record and follows the panel: the current row
    takes `--surface-shade` on dark and Paper on light, where the panel is
    white, rows not yet played drop to `--disabled-opacity`
    (like a disabled control, adaptation 13), and played rows look normal. Every row
    is played at the end. Two rows of one attempt still read as one: the
    shading and dimming apply per row and leave their joint unchanged.

    While the replay is not ready, the page reloads every second and shows
    the live History under the waiting playback.

    **The participant diagram** is drawn inline in the template as SVG,
    564 × 132: four nodes — Transfer Service, Message Broker, Bank A
    and Bank B — as 1px outlined boxes with 2px corners, like a surface, and
    their names in mono at `--text-mono-meta`, and three edges joining the
    Message Broker to each of the others. "Message Broker" is set on two
    lines, and its box is 8px taller than the others to hold them. The
    nodes span the diagram's full width: the Transfer Service at the left
    edge, the banks at the right one. Every message passes through the Broker,
    so the Transfer Service sits on its left and the banks are stacked on
    its right. For the current entry it lights the path the entry's message
    travelled, which the server computes per row: sender → Broker →
    receiver for a Saga step; Transfer Service → Broker for the requested,
    admitted and broker-confirmed entries; the bank → Broker for a lost
    acknowledgement; Broker → the bank for a suppressed redelivery; Broker →
    Bank B for delivery resuming.

    - Idle nodes and edges are muted.
    - The nodes on the path are body text.
    - The travelled edges are Teal at 2px, with an open Teal chevron at the
      receiving end, drawn like the arrowheads of the product mark. Teal
      because the path is a data mark; the accent is not spent.

    During the broker wait only the Broker is lit, and Bank B is marked
    "unavailable" centred just above its box, in the gap between the banks, in Brick like the waiting entry in History:
    the words carry it, the hue only marks it. The path is static. It
    changes instantly with the entry and nothing moves along it, because a
    travelling mark would suggest that messages take hundreds of
    milliseconds to cross the network.

16. **Panels.** Every region of a page is a panel: a 1px `--panel-edge`
    frame with `--radius-surface` corners around a `--surface-panel` fill,
    opening with a header strip that names it — `.caps` at
    `--text-mono-meta`, a 20px line, 8px × 16px padding, on
    `--surface-panel-head` over a `--panel-edge` rule. Prose and forms sit
    in the panel's 16px padding (`.panel-body`); rows and tables run to its
    edges, their first column indented 16px, and the last row of a list
    drops its hairline against the frame. Inside a panel `--surface-shade`
    becomes `--panel-shade`, so a hovered or current row still steps off its
    ground (adaptation 8).

    The one panel without a head is a transfer's balances panel, whose rows
    name themselves (adaptation 12).

    | Token | Light | Dark |
    | --- | --- | --- |
    | `--surface-panel` | `#FFFFFF` | `#191919` |
    | `--surface-panel-head` | `#F2F2F2` | `#202020` |
    | `--panel-edge` | `#DADADA` | `#333333` |
    | `--panel-shade` | `#F4F4F4` | `#232323` |
    | `--control-edge` | `#ADADAD` | `#555555` |
    | `--control-fill` | `#EDEDED` | `#2A2A2A` |
    | `--control-fill-hover` | `#E0E0E0` | `#343434` |

    The panel fill is a step off the page in both themes — lighter on dark,
    white on light — and subtler than the brand's `--surface-card`, which
    the app uses only in floats and the home page's transfer card. The header strip and frame carry
    the separation, so a panel needs no shadow.

    TRANSFER DETAILS is 600px: Amount (its value body, weight 500),
    Transfer ID and Trace ID, in a 100px name column; its rows sit on a 20px
    line, so their 1px rules land on whole pixels, and it closes 4px under
    its last row.
