# Owner app design rules

Check every screen against this list. The catalogue and `make web-check` enforce the starred ones.

**Look**
- Light only. The system font for every word on every screen. The hand font (Caveat) is the simple-host wordmark's alone: never a heading, tab, chip, button, table head or label.
- One type scale, set in `tokens.css` and named as roles in `app.css`: `type-page` (24 semibold, a page's title), `type-card` (28 semibold, a card's title), `type-section` (18 semibold, a section's heading), `type-label` (14 medium, labels, table heads, stat labels); body and controls 16, secondary lines 14, badges and fine print 13. Text pages use 34 for their title. Nothing is heavier than 600: hierarchy comes from size, spacing and colour. Titles are 600 with tight tracking; buttons 500; site names in lists 600; labels and table heads 500 in the muted colour; body 400; stat numbers 600. Only the wordmark is bold.
- Colours, sizes, radii and shadows come only from `app/src/styles/tokens.css` (Tailwind's own palette is switched off). Teal is the one accent; coral appears only in the wordmark; red only for errors and destructive actions.
- Paper cards with the hand-drawn edge (`Card`) for a single task (sign-in, a dialog, choosing a name); plain sections with a `type-section` heading for lists and numbers. Forms and their sections are at most 720 px wide.

**Layout**
- Signed-in screens use `AppShell`: side rail from 700 px, top bar plus bottom bar on a phone. Signed-out screens use `PageShell` (header, footer) with one centred card.
- One layout for every width: right at 390 and at 1280. * No sideways scroll, nothing cut off or off screen.
- Lists are `DataTable`: a table from 700 px, one card per row on a phone, with search, filter chips and a sort list above. Sort by pressing a column head too. Filter chips and tabs stay on one row and scroll sideways on a phone (`data-scroll-x`); they never wrap.
- A thing with many parts (a site, the account) is one page with its parts as tabs; the open tab is the address's `#hash`, so it can be linked to. Its name, address and state stay at the top, with its one filled button (Open site).
- Destructive actions sit apart at the bottom of their tab, in a red-edged section.
- Reserve space for what is loading (`Skeleton`) so nothing jumps when it arrives.

**Behaviour**
- One primary (filled) button per screen or dialog; everything else is secondary.
- Every list has an empty state that says why it is empty and what to do; every load has a loading state and an error state with Try again.
- Toasts: one at a time (a new one replaces it), above the bottom bar and the safe area on a phone, bottom left of the content on a wide screen.
- Reversible actions just happen and offer Undo in the toast. Ask first (`AskDialog`) only when an action needs input (a reason), reaches other people (opening a private site, deleting a site visitors use, changing the name in every address) or cannot be undone (revoking a key, deleting saved data or the account).
- Forms check as you submit and say what is wrong under the field, in plain words, with the fix. Server refusals are shown as they come (`fix`).
- Plain words: say what happens ("Take down", "Ask for access"), never internal terms (no "hop", "operator token", "resource").

**Access** *
- 44 × 44 px tap targets (links inside a sentence are exempt); inputs are 16 px text. A checkbox or radio is the whole row it sits on (`Check`, `RadioCards`), not the small box drawn beside the words.
- axe (WCAG 2.1 A and AA) clean; every control has a name; dialogs and menus are Radix (focus trap, Escape, focus return); tabs move with the arrow keys.
- Colour is never the only signal: badges carry words, errors carry an icon and text.

**Code**
- `components/ui/` holds the primitives (button, input and field, card, dialog, tabs, data-table, badge and chips, choice (check, radio cards), notice, meter, bars, copy, toast, menu, skeleton, empty-state, stat, page-shell). Screens use only these and the tokens; no hand-made controls or raw colours.
- `app-shell/` is the signed-in frame; `screens/<area>/` one folder per area with its data hooks; `lib/api.ts` the one way to call the service.
- Pages the server writes (gates, status, terms) take `styles/pages.css`, built from the same tokens, and load no script.
