// Shared helpers for the participant-team, participant-entry and
// participant-website screens. Not a shell file: owned by these three
// screens only (BUILD.md: "keep it inside your screen module or a new
// screens/_partB-util.js").
import * as ui from '../ui.js'

// team_not_found reads differently here than ui.ERROR_MESSAGES' default
// (written for the judge screens, about a judge's own assignment): here
// it's always about someone typing a join-by-code value that doesn't match.
const CODES = { ...ui.ERROR_MESSAGES, team_not_found: 'That team code does not match a team.' }

// errText(r) / errText(r, fallback): the shared ui.errText with this
// group's codes. Kept as a one-line adapter so every call site here keeps
// its old, single-argument shape.
export const errText = (r, fallback) => ui.errText(r, { codes: CODES, fallback })

// A write that finds the team frozen, taken down or the event closed:
// refresh so the page shows that state instead of retrying blindly.
export function isBlockedCode (code) {
  return code === 'submissions_closed' || code === 'team_site_taken_down' ||
    code === 'event_closed' || code === 'event_taken_down' || code === 'teams_locked'
}

// Only ever render http(s) links as real links (participant-entry's
// read-only Video/Code links, A14). round2-shared.md gives this a shared
// home now (ui.safeHref, identical check) -- re-exported under its old name
// so every call site in this file and its importers keeps working.
export const safeHref = ui.safeHref
