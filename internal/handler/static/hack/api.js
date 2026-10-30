export function apiKey() { try { return localStorage.getItem('apiKey') || '' } catch { return '' } }
let signedOut = () => {}
export function onSignedOut(fn) { signedOut = fn }
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
