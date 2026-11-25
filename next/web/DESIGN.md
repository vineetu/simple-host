# Owner app design rules

Check every screen against this list. The catalogue and `make web-check` enforce the starred ones.

**Look**
- Light only. System font for all text; the hand fonts (Caveat for titles and the main button, Kalam for tabs, chips and small buttons) are the old Simple Host's voice, never for body text.
- Colours, sizes, radii and shadows come only from `app/src/styles/tokens.css` (Tailwind's own palette is switched off). Teal is the one accent; coral appears only in the wordmark; red only for errors and destructive actions.
- Paper cards with the hand-drawn edge (`Card`) for a single task (sign-in, a dialog); plain sections with a hand-lettered title for lists and numbers.

**Layout**
- Signed-in screens use `AppShell`: side rail from 700 px, top bar plus bottom bar on a phone. Signed-out screens use `PageShell` (header, footer) with one centred card.
- One layout for every width: right at 390 and at 1280. * No sideways scroll, nothing cut off or off screen.
- Lists are `DataTable`: a table from 700 px, one card per row on a phone, with search, filter chips and a sort list above. Sort by pressing a column head too.
- Reserve space for what is loading (`Skeleton`) so nothing jumps when it arrives.

**Behaviour**
- One primary (filled) button per screen or dialog; everything else is secondary.
- Every list has an empty state that says why it is empty and what to do; every load has a loading state and an error state with Try again.
- Reversible actions just happen and offer Undo in the toast. Ask first (`AskDialog`) only when an action needs input (a reason) or reaches other people.
- Forms check as you submit and say what is wrong under the field, in plain words, with the fix. Server refusals are shown as they come (`fix`).
- Plain words: say what happens ("Take down", "Ask for access"), never internal terms (no "hop", "operator token", "resource").

**Access** *
- 44 × 44 px tap targets (links inside a sentence are exempt); inputs are 16 px text.
- axe (WCAG 2.1 A and AA) clean; every control has a name; dialogs and menus are Radix (focus trap, Escape, focus return); tabs move with the arrow keys.
- Colour is never the only signal: badges carry words, errors carry an icon and text.

**Code**
- `components/ui/` holds the primitives (button, input, card, dialog, tabs, data-table, badge and chips, toast, menu, skeleton, empty-state, stat, page-shell). Screens use only these and the tokens; no hand-made controls or raw colours.
- `app-shell/` is the signed-in frame; `screens/<area>/` one folder per area with its data hooks; `lib/api.ts` the one way to call the service.
- Pages the server writes (gates, status, terms) take `styles/pages.css`, built from the same tokens, and load no script.
