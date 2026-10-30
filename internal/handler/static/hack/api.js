import { toast } from './ui.js'
export function apiKey() { try { return localStorage.getItem('apiKey') || '' } catch { return '' } }
let signedOut = () => {}
export function onSignedOut(fn) { signedOut = fn }
// Replaces every screen's own `e.message === 'signed out'` check (api()'s
// 401 throws exactly that Error) so a future change to how that signal is
// carried only needs to change this one place.
export function isSignedOut(e) { return e instanceof Error && e.message === 'signed out' }
// Calls that add or remove an event from the signed-in person's membership
// list (create, accept an organiser invite, join as participant/judge,
// delete). app.js caches /v1/hack/events for the session; it listens here
// to invalidate that cache instead of every screen having to remember to.
let eventsChanged = () => {}
export function onEventsChanged(fn) { eventsChanged = fn }
const MEMBERSHIP_MUTATIONS = [
  { method: 'POST', re: /^\/v1\/hack\/events$/ },
  { method: 'POST', re: /^\/v1\/hack\/organiser\/[^/]+$/ },
  { method: 'POST', re: /^\/v1\/hack\/join\/[^/]+$/ },
  { method: 'POST', re: /^\/v1\/hack\/judge\/[^/]+$/ },
  { method: 'DELETE', re: /^\/v1\/hack\/events\/[^/]+$/ }
];
function isMembershipMutation(method, path) {
  return MEMBERSHIP_MUTATIONS.some(m => m.method === method && m.re.test(path));
}
export async function api(method, path, body) {
    var headers = { 'Content-Type': 'application/json' };
    var k = apiKey();
    if (k) headers['X-API-Key'] = k;
    var opts = { method: method, headers: headers };
    if (body !== undefined) opts.body = JSON.stringify(body);
    var res = await fetch(path, opts);
    var data = {};
    try { data = await res.json(); } catch (e) {}
    if (res.status === 401) { signedOut(); throw new Error('signed out'); }
    if (res.ok && isMembershipMutation(method, path)) eventsChanged();
    return { ok: res.ok, status: res.status, data: data };
  }
  // Screenshot bytes are not JSON. The key still goes out the same way as api().
// Sign out of this browser: ends its key on the server, clears what it keeps
// locally and leaves for the landing. Shared by the sidebar's Sign out and the
// account screen (deleted=true skips the server call, the key is already gone).
export async function signOut(ask, deleted = false) {
  let warn = false
  if (!deleted) {
    try { const r = await api('POST', '/v1/me/sign-out'); warn = !(r.ok || r.status === 400) }
    catch (e) { warn = !isSignedOut(e) }
  }
  for (const key of ['apiKey','sh-email-nonce']) { try { localStorage.removeItem(key) } catch {} }
  for (const key of ['sh-google-nonce','sh-connect-nonce']) { try { sessionStorage.removeItem(key) } catch {} }
  if (warn) await ask({title:'Signed out of this browser, but the server could not be reached to end its key.',body:'If this is a shared computer, sign in again and choose "Sign out everywhere".',confirmLabel:'Done'})
  location.replace('/')
}
export async function apiBytes(method, path, body, contentType) {
    var headers = { 'X-SH-CSRF': '1' };
    var k = apiKey();
    if (k) headers['X-API-Key'] = k;
    if (contentType) headers['Content-Type'] = contentType;
    var res = await fetch(path, { method: method, headers: headers, body: body });
    var data = {};
    try { data = await res.json(); } catch (e) {}
    if (res.status === 401) { signedOut(); throw new Error('signed out'); }
    return { ok: res.ok, status: res.status, data: data };
  }

export const listEvents = () => api('GET', '/v1/hack/events')
export const getEvent = slug => api('GET', '/v1/hack/events/' + encodeURIComponent(slug))
export const me = () => api('GET', '/v1/me')

// Downloads a CSV/archive/etc the same way the classic app does: a plain
// <a href> can't carry the X-API-Key header, so fetch the bytes by hand and
// hand the browser a blob to save instead. Shows its own error toast near
// `btn` (or its closest section) on failure; returns true/false so a caller
// that wants its own success toast (e.g. "Downloaded x.csv") can show one --
// most callers don't need to and just ignore the return value.
export async function downloadFile(path, filename, btn) {
  if (btn) btn.disabled = true
  try {
    const headers = {}
    const k = apiKey()
    if (k) headers['X-API-Key'] = k
    let res
    try { res = await fetch(path, { headers }) }
    catch { toast('Cannot reach the server.', btn, true); return false }
    if (res.status === 401) { signedOut(); return false }
    if (!res.ok) {
      let msg = 'Could not download the file.'
      try { const body = await res.json(); if (body && body.error) msg = body.error } catch {}
      toast(msg, btn, true)
      return false
    }
    let blob
    try { blob = await res.blob() }
    catch { toast('Could not read the downloaded file.', btn, true); return false }
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.append(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
    return true
  } finally {
    if (btn) btn.disabled = false
  }
}
