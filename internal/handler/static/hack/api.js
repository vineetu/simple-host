export function apiKey() { try { return localStorage.getItem('apiKey') || '' } catch { return '' } }
let signedOut = () => {}
export function onSignedOut(fn) { signedOut = fn }
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
