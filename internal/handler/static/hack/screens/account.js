import { saveNext, startGoogle } from '../app.js'
export async function render(ctx) {
  const { main, api: client, ui, me } = ctx
  const api = client.api
  const $ = id => main.querySelector('#' + id)
  const stopped = e => e.message === 'signed out'
  const setLive = (msg, kind) => { if (msg) ui.toast(msg, main, kind === 'err') }
  let accountEmail = '', accountConfirm = '', accountGoogleOn = false, accountGoogleLinked = false, accountOidcLabel = ''
  function el(tag, attrs) { const node = document.createElement(tag); for (const [key,value] of Object.entries(attrs)) { if (key === 'text') node.textContent = value; else node.setAttribute(key,value) } return node }
  async function signOut(deleted = false) {
    let warn = false
    if (!deleted) {
      try { const r = await api('POST', '/v1/me/sign-out'); warn = !(r.ok || r.status === 400) }
      catch (e) { warn = !stopped(e) }
    }
    for (const key of ['apiKey','sh-email-nonce']) { try { localStorage.removeItem(key) } catch {} }
    for (const key of ['sh-google-nonce','sh-connect-nonce']) { try { sessionStorage.removeItem(key) } catch {} }
    if (warn) await ui.confirm({title:'Signed out of this browser, but the server could not be reached to end its key.',body:'If this is a shared computer, sign in again and choose "Sign out everywhere".',confirmLabel:'Done'})
    location.replace('/')
  }
  if (me.team || me.username === 'admin' || !(me.username || me.email)) {
    main.innerHTML = '<div class="note">Sign in with your email to open your account.</div>'
    return
  }
  main.innerHTML = `<div class="ph"><div class="t"><h1>Account</h1></div></div>
<section class="card"><h2>Name</h2><form id="acct-name-form"><div class="field"><label>Name shown to teams, organisers and judges<input class="input" id="acct-name" type="text" maxlength="100"></label><p class="help" id="acct-name-err"></p></div><button class="btn primary" id="acct-name-save">Save</button></form></section>
<section class="card"><h2>Email</h2><div id="acct-email-row"><p>You sign in with <b id="acct-email"></b>. A change needs a code from this address and one from the new one.</p><button class="btn" id="acct-email-open">Change email</button></div>
<form id="acct-email-form" hidden><div class="field"><label>New email<input class="input" id="acct-email-input" type="email" required autocomplete="email"></label></div><button class="btn primary" id="acct-email-send">Send codes</button> <button class="btn" id="acct-email-cancel" type="button">Cancel</button></form>
<form id="acct-code-form" hidden><p>Enter the codes sent to <span id="acct-code-to"></span> and <span id="acct-current-to"></span>.</p><div class="field"><label>New email code<input class="input" id="acct-code" inputmode="numeric" autocomplete="one-time-code" required pattern="[0-9]{6}"></label></div><div class="field"><label>Current email code<input class="input" id="acct-current-code" inputmode="numeric" required pattern="[0-9]{6}"></label></div><button class="btn primary" id="acct-code-go">Change email</button> <button class="btn" id="acct-code-cancel" type="button">Cancel</button></form></section>
<section class="card"><h2>Google sign-in</h2><div id="acct-ids"></div><p class="muted" id="acct-google-hint" hidden>Use the Google account with your sign-in email.</p><button class="btn" id="acct-google" hidden>Link Google</button></section>
<section class="card"><h2>Preferences</h2><label class="check"><input type="checkbox" id="acct-alerts"><span>Email me after each new sign-in or connected app, so I notice one that wasn’t me</span></label><p class="muted" id="theme-note"></p></section>
<section class="card"><h2>AI apps</h2><p>A connected AI app checks who’s signed in and which team’s site it’s publishing to using this account, automatically — nothing to set up here.</p></section>
<p><button class="btn" id="signout">Sign out</button> <a class="btn" href="${ui.esc(ui.classicURL())}">Use the old screens</a></p>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>Rotate your personal API key</b><span>The old one stops working at once, including for any connected AI app.</span></div><button class="btn danger" id="rotate-key">Rotate API key</button></div><div class="setting"><div class="t"><b>Sign out everywhere</b><span>Every key stops working and connected apps are disconnected. This browser keeps the new key.</span></div><button class="btn danger" id="acct-everywhere">Sign out everywhere</button></div><div class="setting" id="acct-delete-row"><div class="t"><b>Delete your account</b><span>Delete your account and all its data at once and for good. Blocked while you’re organising an event that hasn’t ended.</span></div><button class="btn danger" id="acct-delete-open">Delete my account</button></div><div class="setting" id="acct-delete" hidden><div class="t"><p>Type <b id="acct-delete-word"></b> to confirm. This cannot be undone.</p><label class="field">Your email<input class="input" id="acct-delete-input" type="email"></label><button class="btn danger" id="acct-delete-go" disabled>Delete my account</button> <button class="btn" id="acct-delete-cancel">Cancel</button></div></div></section>`
  $('signout').addEventListener('click', () => signOut())
  async function showKey(key) {
    const box = document.createElement('section')
    box.className = 'card'
    const label = document.createElement('p')
    label.textContent = 'This is the only key now. Copy it for your agent or CLI'
    const input = document.createElement('input')
    input.className = 'input mono'
    input.readOnly = true
    input.value = key
    const done = document.createElement('button')
    done.className = 'btn'
    done.textContent = 'Done'
    done.addEventListener('click', () => box.remove())
    box.append(label,input,ui.copyButton(key),done)
    main.append(box)
    input.focus()
  }
  $('rotate-key').addEventListener('click', async () => {
    if (!await ui.confirm({title:'Rotate API key?',body:'Your old key stops working right away, including for any connected AI app. Rotate it?',confirmLabel:'Rotate API key',danger:true})) return
    try {
      const r = await api('POST','/v1/me/api-key/rotate')
      if (!r.ok || !r.data.api_key) { setLive(accountErr(r), 'err'); return }
      localStorage.setItem('apiKey',r.data.api_key)
      if (window.shAuthRender) window.shAuthRender()
      await showKey(r.data.api_key)
    } catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err') }
  })
  function accountErr(r, fallback) {
    var msg = (r && r.data && typeof r.data.error === 'string' && r.data.error) ? r.data.error : (fallback || 'Something went wrong');
    if (accountConfirm && accountConfirm !== accountEmail && msg.indexOf(accountConfirm) !== -1) {
      msg = msg.split(accountConfirm).join('your account');
    }
    return msg.replace(/\bu-[0-9a-f]{4,}\b/gi, 'your account');
  }
  function showEmailChange(which) {
    $('acct-email-row').hidden = which !== 'row';
    $('acct-email-form').hidden = which !== 'email';
    $('acct-code-form').hidden = which !== 'code';
    if (which === 'email') { $('acct-email-input').value = ''; $('acct-email-input').focus(); }
    if (which === 'code') {
      $('acct-code').value = '';
      $('acct-current-code').value = '';
      $('acct-code').focus();
    }
  }
  function showDeleteAccount(open) {
    $('acct-delete').hidden = !open;
    $('acct-delete').style.display = open ? '' : 'none';
    $('acct-delete-row').hidden = open;
    $('acct-delete-row').style.display = open ? 'none' : '';
    $('acct-delete-input').value = '';
    $('acct-delete-go').disabled = true;
    if (open) $('acct-delete-input').focus();
  }
  function paintGoogleLink() {
    var on = accountGoogleOn && !accountGoogleLinked;
    $('acct-google').hidden = !on;
    $('acct-google-hint').hidden = !on;
  }
  function paintAccount(me) {
    accountEmail = String((me && (me.username || me.email)) || '');
    accountConfirm = (me && me.handle) ? String(me.handle) : accountEmail;
    $('acct-name').value = (me && me.display_name) || '';
    $('acct-name-err').textContent = '';
    $('acct-email').textContent = accountEmail;
    $('acct-delete-word').textContent = accountEmail;
    $('acct-alerts').checked = !(me && me.signin_alerts === false);
    showEmailChange('row');
    showDeleteAccount(false);
  }
  async function loadIdentities() {
    var box = $('acct-ids');
    box.textContent = '';
    var r;
    try { r = await api('GET', '/v1/me/identities'); }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', box); return; }
    if (!r.ok) { setLive(accountErr(r, 'Could not load linked sign-ins'), 'err', box); return; }
    var list = (r.data && r.data.identities) || [];
    accountGoogleLinked = false;
    if (!list.length) box.appendChild(el('p', { class: 'muted', text: 'No other sign-in is linked.' }));
    list.forEach(function (i) {
      var who = i.provider === 'github' ? 'GitHub' : i.provider === 'oidc' ? (accountOidcLabel || 'your company account') : 'Google';
      if (i.provider === 'google') accountGoogleLinked = true;
      var email = i.email || '';
      var when = '';
      var d = new Date(i.linked_at);
      if (!isNaN(d.getTime())) when = d.toLocaleDateString();
      var row = el('div', { class: 'setting' });
      var line = who + ' sign-in: ' + (email || 'no email shared');
      if (when) line += ', linked ' + when;
      line += '. This ' + who + ' account can sign in to yours.';
      row.appendChild(el('span', { text: line }));
      var btn = el('button', { type: 'button', class: 'btn danger', text: 'Unlink' });
      btn.setAttribute('data-id', i.id || '');
      btn.setAttribute('data-who', who + ' (' + (email || 'no email') + ')');
      row.appendChild(btn);
      box.appendChild(row);
    });
    paintGoogleLink();
  }
  function loadGoogleProvider() {
    return api('GET', '/v1/auth/oauth/providers').then(function (res) { return res.data; }).then(function (d) {
      var names = (d && d.providers) || [];
      accountGoogleOn = names.indexOf('google') >= 0;
      accountOidcLabel = (d && d.oidc_label) || '';
      paintGoogleLink();
    }).catch(function () {});
  }
  async function unlinkIdentity(btn) {
    var who = btn.getAttribute('data-who') || 'this sign-in';
    var id = btn.getAttribute('data-id') || '';
    if (!id) return;
    var ok = await ui.confirm({ title: 'Unlink ' + who + '?', body: 'It will no longer sign in to your account. You can still sign in with your email.', confirmLabel: 'Unlink', danger: true });
    if (!ok) return;
    setLive('');
    btn.disabled = true;
    var r;
    try { r = await api('DELETE', '/v1/me/identities/' + encodeURIComponent(id)); }
    catch (e) {
      if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn);
      btn.disabled = false;
      return;
    }
    if (!r.ok) { setLive(accountErr(r, 'Could not unlink'), 'err', btn); btn.disabled = false; return; }
    setLive('Unlinked', 'ok', $('acct-ids'));
    await loadIdentities();
  }
  $('acct-name-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    setLive('');
    $('acct-name-err').textContent = '';
    var name = $('acct-name').value.trim();
    $('acct-name-save').disabled = true;
    try {
      var r = await api('PATCH', '/v1/me', { display_name: name });
      if (!r.ok) { $('acct-name-err').textContent = accountErr(r, 'Could not save'); return; }
      me.display_name = name;
      $('acct-name').value = (r.data && typeof r.data.display_name === 'string') ? r.data.display_name : name;
      setLive('Saved', 'ok', $('acct-name-save'));
    } catch (err) {
      if (!stopped(err)) setLive('Cannot reach the server.', 'err', $('acct-name-save'));
    } finally { $('acct-name-save').disabled = false; }
  });
  $('acct-email-open').addEventListener('click', function () { setLive(''); showEmailChange('email'); });
  $('acct-email-cancel').addEventListener('click', function () { setLive(''); showEmailChange('row'); });
  $('acct-code-cancel').addEventListener('click', function () { setLive(''); showEmailChange('row'); });
  $('acct-email-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    setLive('');
    var email = $('acct-email-input').value.trim();
    if (!email) { setLive('Enter the new email.', 'err', $('acct-email-send')); return; }
    $('acct-email-send').disabled = true;
    try {
      var r = await api('POST', '/v1/me/email', { email: email });
      if (!r.ok) { setLive(accountErr(r, 'Could not send a code'), 'err', $('acct-email-send')); return; }
      $('acct-code-to').textContent = (r.data && r.data.email) || email;
      $('acct-current-to').textContent = (r.data && r.data.current_email) || accountEmail;
      showEmailChange('code');
    } catch (err) {
      if (!stopped(err)) setLive('Cannot reach the server.', 'err', $('acct-email-send'));
    } finally { $('acct-email-send').disabled = false; }
  });
  $('acct-code-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    setLive('');
    $('acct-code-go').disabled = true;
    try {
      var r = await api('POST', '/v1/me/email/verify', {
        code: $('acct-code').value.trim(),
        current_code: $('acct-current-code').value.trim()
      });
      if (!r.ok) { setLive(accountErr(r, 'Could not change your email'), 'err', $('acct-code-go')); return; }
      var next = (r.data && (r.data.email || r.data.username)) || '';
      if (next) {
        accountEmail = next;
        me.username = next; me.email = next;
        if (!accountConfirm || accountConfirm === $('acct-delete-word').textContent) accountConfirm = next;
        $('acct-email').textContent = next;
        $('acct-delete-word').textContent = next;
      }
      showEmailChange('row');
      setLive('Your sign-in email is now ' + (next || 'the new address') + '. Your other keys were signed out.', 'ok', $('acct-email-row'));
      await loadIdentities();
    } catch (err) {
      if (!stopped(err)) setLive('Cannot reach the server.', 'err', $('acct-code-go'));
    } finally { $('acct-code-go').disabled = false; }
  });
  $('acct-ids').addEventListener('click', function (e) {
    var btn = e.target.closest('button[data-id]');
    if (!btn || !$('acct-ids').contains(btn)) return;
    unlinkIdentity(btn);
  });
  $('acct-google').addEventListener('click', async function () {
    // Linking is just signing in with Google: the server attaches it to
    // whichever account's email matches the Google account's verified email.
    // Picking a Google account with a different email signs into THAT
    // account instead, silently, so this asks first rather than redirecting
    // straight out.
    var ok = await ui.confirm({
      title: 'Link Google?',
      body: 'Only continue with the Google account for ' + (accountEmail || 'this email') + '. Choosing a different Google account signs you into a different Simple Hack account instead.',
      confirmLabel: 'Continue to Google'
    });
    if (!ok) return;
    saveNext('/account');
    startGoogle();
  });
  $('acct-alerts').addEventListener('change', async function () {
    var box = $('acct-alerts');
    var want = box.checked;
    box.disabled = true;
    setLive('');
    try {
      var r = await api('PATCH', '/v1/me', { signin_alerts: want });
      if (!r.ok) {
        box.checked = !want;
        setLive(accountErr(r, 'Could not save'), 'err', box.closest('label') || box);
        return;
      }
      setLive(want ? 'Sign-in alerts on' : 'Sign-in alerts off', 'ok', box.closest('label') || box);
    } catch (err) {
      if (!stopped(err)) { box.checked = !want; setLive('Cannot reach the server.', 'err', box); }
    } finally { box.disabled = false; }
  });
  $('acct-everywhere').addEventListener('click', async function () {
    var ok = await ui.confirm({
      title: 'Sign out everywhere?',
      body: 'Every key on this account stops working, and connected apps (ChatGPT, Claude, Grok) are disconnected, and every sign-in on your sites ends. This browser stays signed in; your agents will need the new key.',
      confirmLabel: 'Sign out everywhere',
      danger: true
    });
    if (!ok) return;
    setLive('');
    $('acct-everywhere').disabled = true;
    try {
      var r = await api('POST', '/v1/me/api-key/rotate');
      if (!r.ok || !r.data || !r.data.api_key) {
        setLive(accountErr(r, 'Could not sign out everywhere'), 'err', $('acct-everywhere'));
        return;
      }
      try { localStorage.setItem('apiKey', r.data.api_key); } catch (e) {}
      if (window.shAuthRender) window.shAuthRender();
      setLive('Signed out everywhere. This browser has the new key.', 'ok', $('acct-everywhere'));
      await showKey(r.data.api_key)
    } catch (err) {
      if (!stopped(err)) setLive('Cannot reach the server.', 'err', $('acct-everywhere'));
    } finally { $('acct-everywhere').disabled = false; }
  });
  $('acct-delete-open').addEventListener('click', function () { setLive(''); showDeleteAccount(true); });
  $('acct-delete-cancel').addEventListener('click', function () { setLive(''); showDeleteAccount(false); });
  $('acct-delete-input').addEventListener('input', function () {
    var want = (accountEmail || '').toLowerCase();
    $('acct-delete-go').disabled = !want || $('acct-delete-input').value.trim().toLowerCase() !== want;
  });
  $('acct-delete-go').addEventListener('click', async function () {
    var go = $('acct-delete-go');
    go.disabled = true;
    setLive('');
    try {
      var r = await api('DELETE', '/v1/me', { confirm: accountConfirm || $('acct-delete-input').value.trim() });
      if (r.status !== 204) {
        setLive(accountErr(r, 'Could not delete your account'), 'err', go);
        go.disabled = $('acct-delete-input').value.trim().toLowerCase() !== (accountEmail || '').toLowerCase();
        return;
      }
      await ui.confirm({ title: 'Your account and all its data were deleted.', body: 'A confirmation is on its way to your email.', confirmLabel: 'Done' })
      signOut(true);
    } catch (err) {
      if (!stopped(err)) setLive('Cannot reach the server.', 'err', go);
      go.disabled = false;
    }
  });


  paintAccount(me)
  await Promise.all([loadGoogleProvider(), loadIdentities(), api('GET','/v1/hack/preferences').then(r => {
    if (r.ok) $('theme-note').textContent = 'Theme: ' + ({system:'System',light:'Light',dark:'Dark'}[r.data.theme] || 'System') + ' — Simple Hack is light-only today, this is saved for when that changes.'
  })])
}
