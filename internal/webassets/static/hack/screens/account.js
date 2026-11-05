import { saveNext, startGoogle } from '../app.js'
import { loadConnectSteps, stepsHTML, wireAddresses } from '../connect-steps.js'

// Connect your AI app (B): the same one-address MCP connection as
// /get-started and the Website screen's publish card, with the current
// person's connection status from GET /v1/me/connections.
function connectAgoText (iso) {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(diff)) return ''
  if (diff < 60000) return 'just now'
  const m = Math.floor(diff / 60000)
  if (m < 60) return m + ' minute' + (m === 1 ? '' : 's') + ' ago'
  const h = Math.floor(m / 60)
  if (h < 24) return h + ' hour' + (h === 1 ? '' : 's') + ' ago'
  const d = Math.floor(h / 24)
  return d + ' day' + (d === 1 ? '' : 's') + ' ago'
}

export async function render(ctx) {
  const { main, api: client, ui, me } = ctx
  const api = client.api
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  let accountEmail = '', accountConfirm = '', accountGoogleOn = false, accountGoogleLinked = false, accountOidcLabel = ''
  const signOut = (deleted = false) => client.signOut(ui.confirm, deleted)
  if (me.team || me.username === 'admin' || !(me.username || me.email)) {
    main.innerHTML = '<div class="note">Sign in with your email to open your account.</div>'
    return
  }
  main.innerHTML = `<div class="ph"><div class="t"><h1>Account</h1></div></div>
<section class="card"><h2>Your name</h2><form id="acct-name-form"><div class="field"><label class="sr-only" for="acct-name">Name</label><div class="control-row"><input class="input" id="acct-name" type="text" maxlength="100"><button class="btn primary" id="acct-name-save">Save</button></div><p class="help">Shown to teams, organisers and judges.</p><p class="help" id="acct-name-err"></p></div></form></section>
<section class="card" id="acct-connect-card"><h2>Connect your AI app</h2><p class="sub" id="acct-connect-status">Loading…</p><div class="copy" id="acct-connect-url"></div><nav class="app-tabs" id="acct-connect-tabs" aria-label="How you connect"></nav><div id="acct-connect-steps"></div><p class="help"><a href="/get-started#other-ai-apps">Steps for other AI apps</a></p></section>
<section class="card"><h2>Email</h2><div id="acct-email-row"><p id="acct-email-text">You sign in with <b id="acct-email"></b>. A change needs a code from this address and one from the new one.</p><div class="form-actions" id="acct-email-actions"><button class="btn" id="acct-email-open">Change email</button></div></div>
<form id="acct-email-form" hidden><div class="field"><label>New email<input class="input" id="acct-email-input" type="email" required autocomplete="email"></label></div><div class="form-actions"><button class="btn primary" id="acct-email-send">Send codes</button><button class="btn" id="acct-email-cancel" type="button">Cancel</button></div></form>
<form id="acct-code-form" hidden><p>Enter the codes sent to <span id="acct-code-to"></span> and <span id="acct-current-to"></span>.</p><div class="field"><label>New email code<input class="input" id="acct-code" inputmode="numeric" autocomplete="one-time-code" required pattern="[0-9]{6}"></label></div><div class="field"><label>Current email code<input class="input" id="acct-current-code" inputmode="numeric" required pattern="[0-9]{6}"></label></div><div class="form-actions"><button class="btn primary" id="acct-code-go">Change email</button><button class="btn" id="acct-code-cancel" type="button">Cancel</button></div></form></section>
<section class="card" id="acct-password-card" hidden><h2>Password</h2><p class="help" id="acct-password-login" hidden></p><form id="acct-password-form"><div class="field"><label for="acct-password-current">Current password<input class="input" id="acct-password-current" type="password" autocomplete="current-password" required></label></div><div class="field"><label for="acct-password-new">New password<input class="input" id="acct-password-new" type="password" autocomplete="new-password" minlength="8" required></label></div><div class="field"><label for="acct-password-confirm">Confirm new password<input class="input" id="acct-password-confirm" type="password" autocomplete="new-password" minlength="8" required></label></div><p class="help">At least 8 characters. Your other keys and sessions are not affected.</p><div class="form-actions"><button class="btn primary" id="acct-password-save">Change password</button></div><p class="help" id="acct-password-err"></p></form></section>
<section class="card"><h2>Google sign-in</h2><div id="acct-ids"></div><p class="help" id="acct-google-hint" hidden>Use the Google account with your sign-in email.</p><div class="form-actions"><button class="btn" id="acct-google" hidden>Link Google</button></div></section>
<section class="card"><h2>Preferences</h2><label class="check"><input type="checkbox" id="acct-alerts"><span>Email me after each new sign-in or connected app, so I notice one that wasn’t me</span></label><div class="setting"><div class="t"><div class="t-row"><b>Email me when something waits on me</b><span class="saved" id="acct-wait-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span id="acct-wait-help">Team requests and invites, application answers, and changes to your team or its site. Organisers also hear about new applications. At most one email of each kind every 15 minutes.</span></div><div class="ctl"><button class="toggle" type="button" role="switch" aria-checked="false" id="acct-wait" aria-label="Email me when something waits on me" disabled></button></div></div></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>Make a new account key</b><span>The old key stops working at once, including in connected AI apps.</span></div><button class="btn danger" id="rotate-key">New key</button></div><div class="setting"><div class="t"><b>Sign out everywhere</b><span>Every key stops working and connected apps are disconnected. This browser keeps the new key.</span></div><button class="btn danger" id="acct-everywhere">Sign out everywhere</button></div><div class="setting" id="acct-delete-row"><div class="t"><b>Delete your account</b><span>Delete your account and all its data at once and for good. Blocked while you’re organising an event that hasn’t ended.</span></div><button class="btn danger" id="acct-delete-open">Delete account</button></div><div class="setting" id="acct-delete" hidden><div class="t"><div class="field"><label for="acct-delete-input">Type <b id="acct-delete-word"></b> to confirm. This cannot be undone.</label><input class="input" id="acct-delete-input" type="email" autocomplete="off"></div><div class="form-actions"><button class="btn danger" id="acct-delete-go" disabled>Delete account</button><button class="btn" id="acct-delete-cancel">Cancel</button></div></div></div></section>`
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
    input.setAttribute('aria-label', 'Your new account key')
    // control-row (kit "Form layout"): key, Copy and Done on one row, same height.
    const row = document.createElement('div')
    row.className = 'control-row'
    row.append(input,ui.copyButton(key),done)
    box.append(label,row)
    main.append(box)
    input.focus()
  }
  $('rotate-key').addEventListener('click', async () => {
    if (!await ui.confirm({title:'Make a new account key?',body:'The old key stops working at once, including in connected AI apps.',confirmLabel:'New key',danger:true})) return
    try {
      const r = await api('POST','/v1/me/api-key/rotate')
      if (!r.ok || !r.data.api_key) { setLive(accountErr(r), 'err'); return }
      localStorage.setItem('apiKey',r.data.api_key)
      if (window.shAuthRender) window.shAuthRender()
      await showKey(r.data.api_key)
    } catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err') }
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
    // accountEmail/accountConfirm are the account's "identifying word" for
    // the delete-account confirmation and the Google-link prompt, where
    // login is the right word for a password account (its username is a
    // synthetic, never-shown placeholder there). The Email card below is
    // different: an admin-created password account can change its email
    // (ApplyEmailChange updates users.username), so that card must show
    // the real address when one exists, never login.
    accountEmail = String((me && (me.login || me.email || me.username)) || '');
    accountConfirm = (me && me.handle) ? String(me.handle) : accountEmail;
    var login = (me && me.login) || '';
    var username = (me && me.username) || '';
    var realEmail = (me && me.email) || (username.indexOf('@') !== -1 ? username : '');
    $('acct-name').value = (me && me.display_name) || '';
    $('acct-name-err').textContent = '';
    if (realEmail) {
      $('acct-email-text').innerHTML = 'You sign in with <b id="acct-email"></b>. A change needs a code from this address and one from the new one.';
      $('acct-email').textContent = realEmail;
      $('acct-email-actions').hidden = false;
    } else {
      $('acct-email-text').textContent = 'Your account has no email address.';
      $('acct-email-actions').hidden = true;
    }
    $('acct-delete-word').textContent = accountEmail;
    $('acct-alerts').checked = !(me && me.signin_alerts === false);
    $('acct-password-card').hidden = !(me && me.has_password === true);
    var loginLine = $('acct-password-login');
    if (loginLine) {
      loginLine.hidden = !login;
      loginLine.textContent = login ? ('You sign in with ' + login + '.') : '';
    }
    showEmailChange('row');
    showDeleteAccount(false);
  }
  async function loadIdentities() {
    var box = $('acct-ids');
    box.textContent = '';
    var r;
    try { r = await api('GET', '/v1/me/identities'); }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', box); return; }
    if (!r.ok) { setLive(accountErr(r, 'Could not load linked sign-ins'), 'err', box); return; }
    var list = (r.data && r.data.identities) || [];
    accountGoogleLinked = false;
    if (!list.length) box.appendChild(ui.el('p', { class: 'muted', text: 'No other sign-in is linked.' }));
    list.forEach(function (i) {
      var who = i.provider === 'github' ? 'GitHub' : i.provider === 'oidc' ? (accountOidcLabel || 'your company account') : 'Google';
      if (i.provider === 'google') accountGoogleLinked = true;
      var email = i.email || '';
      var when = ui.dateOnly(i.linked_at) || '';
      var row = ui.el('div', { class: 'setting' });
      var line = who + ' sign-in: ' + (email || 'no email shared');
      if (when) line += ', linked ' + when;
      line += '. This ' + who + ' account can sign in to yours.';
      row.appendChild(ui.el('span', { text: line }));
      var btn = ui.el('button', { type: 'button', class: 'btn danger', text: 'Unlink' });
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
      if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn);
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
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', $('acct-name-save'));
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
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', $('acct-email-send'));
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
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', $('acct-code-go'));
    } finally { $('acct-code-go').disabled = false; }
  });
  $('acct-password-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    setLive('');
    $('acct-password-err').textContent = '';
    var current = $('acct-password-current').value;
    var next = $('acct-password-new').value;
    var confirmed = $('acct-password-confirm').value;
    if (next.length < 8) { $('acct-password-err').textContent = 'New password must be at least 8 characters.'; return; }
    if (next !== confirmed) { $('acct-password-err').textContent = 'New passwords do not match.'; return; }
    $('acct-password-save').disabled = true;
    try {
      var r = await api('POST', '/v1/me/password', { current_password: current, new_password: next });
      if (!r.ok) { $('acct-password-err').textContent = accountErr(r, 'Could not change password'); return; }
      $('acct-password-current').value = '';
      $('acct-password-new').value = '';
      $('acct-password-confirm').value = '';
      setLive('Password changed', 'ok', $('acct-password-save'));
    } catch (err) {
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', $('acct-password-save'));
    } finally { $('acct-password-save').disabled = false; }
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
      if (!client.isSignedOut(err)) { box.checked = !want; setLive('Cannot reach the server.', 'err', box); }
    } finally { box.disabled = false; }
  });
  $('acct-everywhere').addEventListener('click', async function () {
    var ok = await ui.confirm({
      title: 'Sign out everywhere?',
      body: 'Every key on this account stops working, and connected apps (ChatGPT, Claude, Grok) are disconnected, and every sign-in on your sites ends. This browser stays signed in. Your AI apps will need the new key.',
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
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', $('acct-everywhere'));
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
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', go);
      go.disabled = false;
    }
  });


  // Email me when something waits on me (atlas 2026-11-02): on by default.
  // A key-only account has no address, so the switch stays off and says why.
  function paintWait(p) {
    var t = $('acct-wait');
    var on = !!(p && p.email_updates && p.email_possible);
    t.classList.toggle('on', on);
    t.setAttribute('aria-checked', String(on));
    t.disabled = !(p && p.email_possible);
    if (p && !p.email_possible) $('acct-wait-help').textContent = "Your account has no email address, so there's nothing to email.";
  }
  async function loadWait() {
    try {
      var r = await api('GET', '/v1/hack/preferences');
      if (r.ok) paintWait(r.data);
    } catch (e) {}
  }
  $('acct-wait').addEventListener('click', async function () {
    var t = $('acct-wait');
    var want = !t.classList.contains('on');
    t.disabled = true;
    setLive('');
    try {
      var r = await api('PATCH', '/v1/hack/preferences', { email_updates: want });
      if (!r.ok) { setLive(accountErr(r, 'Could not save'), 'err', t); t.disabled = false; return; }
      paintWait(r.data);
      var saved = $('acct-wait-saved');
      saved.hidden = false;
      clearTimeout(saved._t);
      saved._t = setTimeout(function () { saved.hidden = true; }, 3000);
    } catch (err) {
      t.disabled = false;
      if (!client.isSignedOut(err)) setLive('Cannot reach the server.', 'err', t);
    }
  });
  // ---- Connect your AI app (B) ----
  async function loadConnectCard() {
    const urlBox = $('acct-connect-url')
    const tabsEl = $('acct-connect-tabs')
    const stepsBox = $('acct-connect-steps')
    const statusEl = $('acct-connect-status')
    const address = location.origin + '/mcp'
    urlBox.innerHTML = ''
    const addrInput = document.createElement('input')
    addrInput.className = 'input mono'
    addrInput.readOnly = true
    addrInput.value = address
    addrInput.setAttribute('aria-label', 'Connector address')
    urlBox.append(addrInput, ui.copyButton(address, 'Copy'))

    const doc = await loadConnectSteps()
    const apps = ((doc && doc.apps) || []).filter(a => a.tab)
    let tab = apps[0] && apps[0].id
    function paintTab() {
      tabsEl.innerHTML = apps.map(a => '<a href="#" data-acct-app-tab="' + ui.esc(a.id) + '" aria-current="' + (a.id === tab ? 'true' : 'false') + '">' + ui.esc(a.label) + '</a>').join('')
      const app = apps.find(a => a.id === tab)
      stepsBox.innerHTML = app ? stepsHTML(ui.esc, app) : ''
      wireAddresses(ui, stepsBox, address)
      tabsEl.querySelectorAll('[data-acct-app-tab]').forEach(a => a.addEventListener('click', e => {
        e.preventDefault()
        tab = a.getAttribute('data-acct-app-tab')
        paintTab()
      }))
    }
    if (apps.length) paintTab()

    try {
      const r = await api('GET', '/v1/me/connections')
      if (!r.ok) { statusEl.textContent = ''; return }
      const list = (r.data && r.data.connections) || []
      if (!list.length) { statusEl.textContent = 'Not connected yet.'; return }
      list.sort((a, b) => new Date(b.last_used_at || b.connected_at || 0) - new Date(a.last_used_at || a.connected_at || 0))
      const top = list[0]
      statusEl.textContent = 'Connected: ' + (top.name || 'an AI app') + (top.last_used_at ? ' · last used ' + connectAgoText(top.last_used_at) : '') + '.'
    } catch (e) { if (!client.isSignedOut(e)) statusEl.textContent = '' }
  }

  paintAccount(me)
  await Promise.all([loadGoogleProvider(), loadIdentities(), loadWait(), loadConnectCard()])
}
