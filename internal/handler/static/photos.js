(function () {
  'use strict';
  const $ = id => document.getElementById(id);
  const status = text => { $('status').textContent = text; };
  const shrink = file => SHPhotos.shrink(file, { maxBytes: Number($('photo-page').dataset.maxBytes), maxDimension: Number($('photo-page').dataset.maxDimension) });
  const selected = new URLSearchParams(location.search).get('site') || '';
  const account = new URLSearchParams(location.search).get('account') || '';
  const photoToken = new URLSearchParams(location.hash.slice(1)).get('t');
  window.addEventListener('hashchange', () => {
    if (location.hash) location.reload();
  });
  if (photoToken !== null) history.replaceState({ photoLinkUsed: true }, '', location.pathname + location.search);
  else if (history.state?.photoLinkUsed) {
    status('This photo link was used in this tab. Ask your chat for a new link if needed.');
    return;
  }
  let apiKey = photoToken !== null ? '' : localStorage.getItem('apiKey');
  let card, busy = false, photos = [];
  const slug = SHPhotos.slug;
  const size = bytes => bytes >= 1000000 ? (bytes / 1000000).toFixed(1) + ' MB' : Math.max(1, Math.round(bytes / 1000)) + ' KB';
  function element(tag, text, parent) {
    const node = document.createElement(tag);
    if (text) node.textContent = text;
    if (parent) parent.appendChild(node);
    return node;
  }
  async function request(path, options = {}) {
    const response = await fetch(path, { ...options, credentials: photoToken !== null ? 'omit' : 'same-origin', headers: { ...(photoToken !== null ? { 'X-Photo-Token': photoToken } : { 'X-API-Key': apiKey || '' }), ...options.headers } });
    const body = await response.json();
    if (!response.ok) throw Object.assign(new Error(body.error || 'Could not save. Try again.'), { status: response.status, body });
    return body;
  }
  function lock(on) {
    busy = on;
    $('files').disabled = on;
    $('site').disabled = on || photoToken !== null;
    $('save').disabled = on || !photos.length || !$('site').value;
    $('photos').querySelectorAll('button,input').forEach(node => { node.disabled = on; });
  }
  async function loadPhotoLink() {
    const link = await request('/v1/photo-links');
    const option = element('option', link.site, $('site')); option.value = link.site;
    $('site').hidden = true;
    document.querySelector('label[for="site"]').hidden = true;
    element('p', 'Site: ' + link.site, $('fixed-site'));
    $('signin').hidden = true; $('editor').hidden = false;
    lock(false);
  }
  async function loadSites() {
    try {
      const [me, data] = await Promise.all([request('/v1/me'), request('/v1/sites')]);
      const sites = (Array.isArray(data) ? data : data.data || []).filter(site => site.user_id === me.id);
      $('site').replaceChildren();
      for (const site of sites) {
        const option = element('option', site.name, $('site'));
        option.value = site.name;
      }
      const differentAccount = account && account !== me.handle;
      if (differentAccount) {
        $('site').selectedIndex = -1;
        status('This link is for ' + account + '; you are signed in as ' + me.handle + '.');
      } else if (selected && sites.some(site => site.name === selected)) $('site').value = selected;
      else if (selected) {
        $('site').selectedIndex = -1;
        status('Choose a site you can edit.');
      }
      $('signin').hidden = true;
      $('editor').hidden = !sites.length;
      if (!sites.length && !differentAccount) status('You have no sites yet. Open Your sites to get started.');
      lock(false);
    } catch (error) {
      if (error.status === 401) { apiKey = ''; await signIn(); }
      else status(error.message);
    }
  }
  async function signIn() {
    $('signin').hidden = false;
    const providers = await fetch('/v1/auth/oauth/providers').then(r => r.json()).catch(() => ({}));
    card = window.SHSignIn.mount($('signin'), {
      skin: 'ink', title: 'Sign in to add photos', product: 'Simple Host',
      providers: providers.providers || [], oidcLabel: providers.oidc_label || '', emailEnabled: providers.email_enabled !== false,
      // Typed codes complete on this URL. Provider returns use the dashboard's
      // existing nonce-checked flow, then return to this exact photo selection.
      requestCode: async email => {
        const response = await fetch('/v1/auth', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email }) });
        const body = await response.json();
        if (!response.ok) throw Object.assign(new Error(body.error), { status: response.status, body });
        return body;
      },
      verifyCode: async (email, code) => {
        const result = await window.shVerifySignIn({ email, code, name: 'dashboard sign-in' }, { askHandle: info => card.askHandle(info) });
        if (!result.ok || !result.body.api_key) throw Object.assign(new Error(result.body.error || 'Sign-in failed.'), { status: result.status, body: result.body });
        apiKey = result.body.api_key;
        localStorage.setItem('apiKey', apiKey);
        await loadSites();
        return result.body;
      },
      startProvider: async provider => {
        const bytes = crypto.getRandomValues(new Uint8Array(32));
        const b64 = value => btoa(String.fromCharCode(...value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
        const nonce = b64(bytes);
        const hash = b64(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(nonce))));
        sessionStorage.setItem('sh-google-nonce', nonce);
        sessionStorage.setItem('sh-photos-return', '/photos' + location.search);
        location.assign('/v1/auth/oauth/' + encodeURIComponent(provider) + '?return_to=' + encodeURIComponent(location.origin + '/?cn=' + hash));
      }
    });
  }
  function showPhoto(photo, original) {
    const row = element('div', '', $('photos')); row.className = 'photo';
    const img = element('img', '', row); img.alt = 'Photo preview';
    photo.url = URL.createObjectURL(photo.blob); img.src = photo.url;
    const details = element('div', '', row);
    const label = element('label', 'Photo name', details);
    const input = element('input', '', label); input.value = slug(original.name); input.maxLength = 60; input.setAttribute('aria-describedby', 'name-hint');
    element('small', original.name + ': ' + size(original.size) + ' → ' + size(photo.blob.size) + ' (' + photo.ext.slice(1) + ')', details);
    photo.input = input;
    const remove = element('button', 'Remove', details); remove.type = 'button'; remove.className = 'btn btn-secondary';
    remove.addEventListener('click', () => { URL.revokeObjectURL(photo.url); photos = photos.filter(item => item !== photo); row.remove(); lock(false); });
  }
  async function choose(files) {
    if (busy) return;
    if (files.length + photos.length > 30) { status('Choose up to 30 photos at a time.'); return; }
    lock(true);
    const failures = [];
    for (const file of files) {
      status('Preparing ' + file.name + '…');
      try { const photo = await shrink(file); photos.push(photo); showPhoto(photo, file); lock(true); }
      catch (error) { failures.push(file.name + ': ' + error.message); }
    }
    $('files').value = '';
    lock(false);
    status(failures.length ? failures.join(' ') : photos.length + ' photos ready to save.');
  }
  async function copy(text, button) {
    try { await navigator.clipboard.writeText(text); button.textContent = 'Copied'; }
    catch (_) {
      const box = element('textarea', text, button.parentNode); box.readOnly = true; box.focus(); box.select();
      status('Select and copy the text shown here.');
    }
  }
  $('files').addEventListener('change', event => choose(Array.from(event.target.files)));
  $('site').addEventListener('change', () => {
    history.replaceState({}, '', '/photos?site=' + encodeURIComponent($('site').value)); lock(false);
  });
  for (const name of ['dragenter', 'dragover']) $('drop').addEventListener(name, event => { event.preventDefault(); $('drop').classList.add('drag'); });
  for (const name of ['dragleave', 'drop']) $('drop').addEventListener(name, event => { event.preventDefault(); $('drop').classList.remove('drag'); });
  $('drop').addEventListener('drop', event => choose(Array.from(event.dataTransfer.files)));
  $('save').addEventListener('click', async () => {
    if (busy || !photos.length || !$('site').value) return;
    const data = new FormData();
    for (const photo of photos) {
      const name = slug(photo.input.value); photo.input.value = name;
      data.append('photos', photo.blob, name + photo.ext);
    }
    lock(true); status('Saving photos…');
    try {
      const result = await request(photoToken !== null ? '/v1/photo-links' : '/v1/sites/' + encodeURIComponent($('site').value) + '/photos', { method: 'POST', body: data });
      $('results').replaceChildren();
      for (const photo of result.photos) {
        const row = element('div', '', $('results')); row.className = 'result';
        const link = element('a', photo.url, row); link.href = photo.url;
        const button = element('button', 'Copy address', row); button.className = 'btn btn-secondary';
        button.addEventListener('click', () => copy(photo.url, button));
      }
      $('summary').textContent = 'I added ' + result.photos.map(photo => photo.path).join(' and ') + ' to the site ' + result.site + '.';
      $('editor').hidden = true; $('done').hidden = false;
      status('Saved as version ' + result.version + '.');
      photos.forEach(photo => URL.revokeObjectURL(photo.url)); photos = [];
    } catch (error) { status(error.message); }
    finally { lock(false); }
  });
  $('again').addEventListener('click', event => {
    event.preventDefault();
    $('photos').replaceChildren(); $('results').replaceChildren();
    $('done').hidden = true; $('editor').hidden = false;
    status('Choose more photos.'); lock(false);
  });
  $('copy-summary').addEventListener('click', () => copy($('summary').textContent, $('copy-summary')));
  window.addEventListener('pagehide', () => photos.forEach(photo => URL.revokeObjectURL(photo.url)));
  (photoToken !== null ? loadPhotoLink() : apiKey ? loadSites() : signIn()).catch(error => status(error.message));
})();
