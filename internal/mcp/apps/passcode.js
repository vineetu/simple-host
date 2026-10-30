(function () {
  'use strict';
  const app = window.SHApp, el = id => document.getElementById(id);
  const encoder = new TextEncoder(), decoder = new TextDecoder();
  let site = '', busy = false, closed = false, epoch = 0;
  const encode = bytes => btoa(String.fromCharCode(...new Uint8Array(bytes))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  const decode = text => Uint8Array.from(atob(text.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
  const aad = (action, name, session) => encoder.encode(action + '|' + name + '|' + session);

  function hide() {
    epoch++;
    el('code').value = '';
    el('new-code').value = '';
    el('revealed').hidden = true;
  }
  function teardown() { closed = true; hide(); }
  app.onTeardown(teardown);
  window.addEventListener('pagehide', teardown);
  el('hide').onclick = hide;

  function controls() {
    for (const id of ['show', 'set', 'generate', 'new-code']) el(id).disabled = busy || !site || closed;
  }
  function render(data) {
    if (closed || typeof data?.site !== 'string' || typeof data.passcode_protected !== 'boolean') return;
    if (site !== data.site) hide();
    site = data.site;
    let summary = site + ' has no passcode: anyone with its address can open it.';
    if (data.passcode_protected) {
      summary = site + ' asks visitors for a passcode';
      if (data.passcode_set_at) {
        const date = new Date(data.passcode_set_at);
        summary += ' (set ' + (Number.isNaN(date.getTime()) ? data.passcode_set_at : date.toLocaleDateString()) + ')';
      }
    }
    el('summary').textContent = summary;
    el('show').hidden = !data.passcode_protected;
    el('set-label').textContent = data.passcode_protected ? 'Change passcode' : 'Set passcode';
    el('form').hidden = false;
    controls();
  }
  app.on('tool-result', result => {
    if (busy || closed) return;
    if (result.isError) { el('status').textContent = result.content?.find(c => c.type === 'text')?.text || 'Could not load passcode settings.'; return; }
    render(result.structuredContent);
  });

  // Each attempt has a fresh, non-extractable private key and one server session.
  async function begin(name) {
    const pair = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
    const publicKey = await crypto.subtle.exportKey('raw', pair.publicKey);
    const result = await app.callTool('passcode_screen', { action: 'begin', site: name, client_key: encode(publicKey) });
    const server = await crypto.subtle.importKey('raw', decode(result.server_key), { name: 'ECDH', namedCurve: 'P-256' }, false, []);
    const shared = await crypto.subtle.deriveBits({ name: 'ECDH', public: server }, pair.privateKey, 256);
    const material = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey']);
    const key = await crypto.subtle.deriveKey({ name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(), info: encoder.encode('simple-host passcode screen v1') }, material, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
    return { session: result.session, key };
  }

  async function act(action) {
    if (busy || !site || closed) return;
    busy = true;
    el('code').value = '';
    el('revealed').hidden = true;
    el('status').textContent = '';
    controls();
    const attempt = ++epoch, name = site;
    try {
      const { session, key } = await begin(name);
      if (closed || attempt !== epoch) return;
      const args = { action, site: name, session };
      if (action === 'set') {
        const nonce = crypto.getRandomValues(new Uint8Array(12));
        const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce, additionalData: aad('set', name, session) }, key, encoder.encode(el('new-code').value));
        args.nonce = encode(nonce);
        args.ciphertext = encode(ciphertext);
      }
      if (closed || attempt !== epoch) return;
      const result = await app.callTool('passcode_screen', args);
      if (closed || attempt !== epoch) return;
      render(result);
      if (action !== 'show') {
        el('new-code').value = '';
        el('status').textContent = 'Passcode set.';
        // Optional host method; refusal must not undo or obscure a successful set.
        app.updateModelContext('The person set a new passcode on ' + name + ' in the passcode screen.').catch(() => {});
      }
      if (result.sealed) {
        const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: decode(result.sealed.nonce), additionalData: aad('show', name, session) }, key, decode(result.sealed.ciphertext));
        if (closed || attempt !== epoch) return;
        el('code').value = decoder.decode(plaintext);
        el('revealed').hidden = false;
      } else if (action === 'show') {
        el('status').textContent = result.note || 'No readable passcode. You can set a new one below.';
      }
    } catch (error) {
      if (!closed && attempt === epoch) el('status').textContent = error.message || 'Could not finish. Try again.';
    } finally { busy = false; controls(); }
  }
  el('show').onclick = () => act('show');
  el('generate').onclick = () => act('generate');
  el('form').onsubmit = event => { event.preventDefault(); act('set'); };
  el('copy').onclick = async () => {
    const attempt = epoch;
    try {
      await navigator.clipboard.writeText(el('code').value);
      if (!closed && attempt === epoch) el('status').textContent = 'Copied.';
    } catch (_) {
      if (!closed && attempt === epoch) { el('code').focus(); el('code').select(); el('status').textContent = 'Select and copy the passcode.'; }
    }
  };
  app.start().catch(() => { if (!closed) el('status').textContent = 'Could not open the passcode screen. Use the dashboard.'; });
})();
