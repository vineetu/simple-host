// Shared helpers for the participant-team, participant-entry and
// participant-website screens. Not a shell file: owned by these three
// screens only (BUILD.md: "keep it inside your screen module or a new
// screens/_partB-util.js").

// Error code -> plain copy, ported from hack-app.html's ERRORS map (only the
// codes these three screens can actually see).
export const ERRORS = {
  team_finding_off: 'Team finding is off.',
  team_action_unavailable: 'This team action is not available.',
  invalid_looking_for: 'Keep the looking-for note to one line, up to 120 characters.',
  invalid_intro: 'Keep your intro to one line, up to 120 characters.',
  invalid_title: 'The title must be one line, up to 80 characters.',
  invalid_tagline: 'The tagline must be one line, up to 160 characters.',
  invalid_description: "The description is too long or has characters we can't keep.",
  invalid_team_name: 'Give the team a name of up to 80 characters, on one line.',
  invalid_track: 'Choose a listed track.',
  already_in_team: 'You are already on a team.',
  team_full: 'That team is full.',
  team_name_taken: 'There is already a team with that name.',
  team_not_found: 'That team code does not match a team.',
  teams_locked: 'Teams are fixed now.',
  event_closed: 'This event has ended.',
  event_taken_down: 'This event has been taken down.',
  invalid_code: 'This link does not work any more.',
  rate_limited: 'Too many changes at once. Wait a moment and try again.',
  invalid_url: 'Enter a full link starting with https://',
  invalid_request: "Something in that request wasn't right. Reload and try again.",
  no_team: 'Join or start a team first.',
  submissions_closed: 'Entries are closed for your team. Ask the organiser if you need more time.',
  team_sites_not_ready: "Your team's address is still being set up. Try again in a few minutes.",
  image_too_large: 'The screenshot must be 2 MB or smaller.',
  unsupported_image: 'The screenshot must be a PNG, JPEG or WebP image.',
  team_site_taken_down: "The organiser has taken your team's site down.",
  deleted: 'That no longer exists.',
  not_found: 'That no longer exists.',
  invalid_events: 'Choose at least one notification event.',
  too_many_notifications: 'This site already has as many notifications as it can hold.',
  notification_access_ended: 'Notifications for this site are no longer available.',
  notifications_not_enabled: 'Notifications are unavailable on this server.'
}

export function errText (r, fallback) {
  const code = r && r.data && r.data.code
  if (code && ERRORS[code]) return ERRORS[code]
  if (r && r.status >= 500) return 'Something went wrong. Try again.'
  if (r && r.data && r.data.error) return String(r.data.error)
  return fallback || 'Something went wrong'
}

// A write that finds the team frozen, taken down or the event closed:
// refresh so the page shows that state instead of retrying blindly.
export function isBlockedCode (code) {
  return code === 'submissions_closed' || code === 'team_site_taken_down' ||
    code === 'event_closed' || code === 'event_taken_down' || code === 'teams_locked'
}

// Binary GET (screenshot, archive) needs the API key header by hand: api.js's
// api()/apiBytes() both expect a JSON body back. A 401 here is handled by
// refreshing through the shell, which runs the real sign-out flow.
export async function fetchBinary (ctx, path) {
  const headers = {}
  const key = ctx.api.apiKey()
  if (key) headers['X-API-Key'] = key
  let res
  try { res = await fetch(path, { headers }) } catch (e) { return { ok: false, status: 0 } }
  if (res.status === 401) { await ctx.refresh(); return { ok: false, status: 401, signedOut: true } }
  if (!res.ok) {
    let data = {}
    try { data = await res.json() } catch (e) {}
    return { ok: false, status: res.status, data }
  }
  const blob = await res.blob()
  return { ok: true, status: res.status, blob }
}

export async function downloadFile (ctx, path, filename, btn) {
  if (btn) btn.disabled = true
  try {
    const r = await fetchBinary(ctx, path)
    if (r.signedOut) return
    if (!r.ok) { ctx.ui.toast(errText(r, 'Could not download the file.'), btn && btn.parentElement, true); return }
    const url = URL.createObjectURL(r.blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.append(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } finally { if (btn) btn.disabled = false }
}

// Shows a private image (screenshot) that needs the API key to fetch. Keeps
// one blob URL alive per <img> and revokes the previous one.
export async function showImage (ctx, img, path) {
  const r = await fetchBinary(ctx, path)
  if (r.signedOut || !r.ok) { img.hidden = true; return }
  const prev = img.getAttribute('data-blob')
  const url = URL.createObjectURL(r.blob)
  img.src = url
  img.hidden = false
  img.setAttribute('data-blob', url)
  if (prev) URL.revokeObjectURL(prev)
}
