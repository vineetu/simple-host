(function () {
  'use strict';
  const $ = id => document.getElementById(id);
  const status = text => { $('status').textContent = text; };
  const byteCeiling = Math.min(950000, Math.floor(Number($('photo-page').dataset.maxBytes) * 0.95));
  const byteTarget = Math.min(300000, byteCeiling);
  const longSide = Math.min(1600, Number($('photo-page').dataset.maxDimension));
  const selected = new URLSearchParams(location.search).get('site') || '';
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
  const slug = name => name.toLowerCase().replace(/\.[^.]+$/, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60) || 'photo';
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
      if (selected && sites.some(site => site.name === selected)) $('site').value = selected;
      else if (selected) {
        $('site').selectedIndex = -1;
        status('Choose a site you can edit.');
      }
      $('signin').hidden = true;
      $('editor').hidden = !sites.length;
      if (!sites.length) status('You have no sites yet. Open Your sites to get started.');
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
        sessionStorage.setItem('sh-photos-return', '/photos' + (selected ? '?site=' + encodeURIComponent(selected) : ''));
        location.assign('/v1/auth/oauth/' + encodeURIComponent(provider) + '?return_to=' + encodeURIComponent(location.origin + '/?cn=' + hash));
      }
    });
  }
  async function nativeDecode(file) {
    if (typeof createImageBitmap === 'function') {
      try {
        const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
        return { source: bitmap, width: bitmap.width, height: bitmap.height, orientation: 1, close: () => bitmap.close() };
      } catch (_) { /* Older browsers and HEIC may need another decoder. */ }
    }
    const url = URL.createObjectURL(file);
    const img = new Image();
    try {
      await new Promise((resolve, reject) => { img.onload = resolve; img.onerror = reject; img.src = url; });
      return { source: img, width: img.naturalWidth, height: img.naturalHeight, orientation: 1, close: () => { img.src = ''; URL.revokeObjectURL(url); } };
    } catch (_) { URL.revokeObjectURL(url); throw new Error('Native decode failed.'); }
  }
  async function decode(file) {
    // Only File objects from the picker/drop handler enter decoding; never URLs.
    if (file.size > 80000000) throw new Error('Could not read this photo');
    const head = new Uint8Array(await file.slice(0, 256).arrayBuffer());
    const ascii = String.fromCharCode(...head);
    const heif = /\.(heic|heif)$/i.test(file.name) || /image\/(heic|heif)/i.test(file.type) || (ascii.slice(4, 8) === 'ftyp' && /heic|heix|hevc|heif|mif1|msf1/.test(ascii.slice(8)));
    const tiff = /\.tiff?$/i.test(file.name) || /tiff/i.test(file.type) || ascii.startsWith('II*\0') || ascii.startsWith('MM\0*');
    // These parsers always run off the UI thread, even on browsers with native support.
    if (!heif && !tiff) {
      const decoded = await nativeDecode(file);
      if (decoded.width * decoded.height > 50000000) { decoded.close(); throw new Error('Could not read this photo'); }
      return decoded;
    }
    const buffer = await file.arrayBuffer();
    const worker = new Worker('/photo-codecs/v2/decoder.js');
    let timeout;
    try {
      const decoded = await new Promise((resolve, reject) => {
        timeout = setTimeout(() => { worker.terminate(); reject(new Error('Could not read this photo')); }, 20000);
        worker.onmessage = event => event.data.error ? reject(new Error(event.data.error)) : resolve(event.data);
        worker.onerror = () => reject(new Error('Could not read this photo'));
        worker.postMessage({ buffer, kind: heif ? 'heif' : 'tiff' }, [buffer]);
      });
      const canvas = document.createElement('canvas');
      canvas.width = decoded.width; canvas.height = decoded.height;
      canvas.getContext('2d').putImageData(new ImageData(new Uint8ClampedArray(decoded.buffer), decoded.width, decoded.height), 0, 0);
      return { source: canvas, width: canvas.width, height: canvas.height, orientation: decoded.orientation, close: () => { canvas.width = canvas.height = 1; } };
    } finally { clearTimeout(timeout); worker.terminate(); }
  }
  function draw(canvas, source) {
    const swapped = source.orientation >= 5 && source.orientation <= 8;
    const ratio = Math.min(1, longSide / Math.max(source.width, source.height));
    const w = Math.max(1, Math.round(source.width * ratio)), h = Math.max(1, Math.round(source.height * ratio));
    canvas.width = swapped ? h : w; canvas.height = swapped ? w : h;
    const ctx = canvas.getContext('2d');
    // TIFF orientation is separate; native decode and libheif apply theirs.
    const transforms = { 2: [-1,0,0,1,w,0], 3: [-1,0,0,-1,w,h], 4: [1,0,0,-1,0,h], 5: [0,1,1,0,0,0], 6: [0,1,-1,0,h,0], 7: [0,-1,-1,0,h,w], 8: [0,-1,1,0,0,w] };
    if (transforms[source.orientation]) ctx.setTransform(...transforms[source.orientation]);
    ctx.drawImage(source.source, 0, 0, w, h);
  }
  const blobFrom = (canvas, type, quality) => new Promise((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('Could not make a small copy of this photo.')), type, quality));
  async function shrink(file) {
    const decoded = await decode(file);
    const canvas = document.createElement('canvas');
    try { draw(canvas, decoded); } finally { decoded.close(); }
    try {
      let type = 'image/webp';
      let blob = await blobFrom(canvas, type, 0.8);
      if (blob.type !== type) { type = 'image/jpeg'; blob = await blobFrom(canvas, type, 0.8); }
      for (const quality of [0.7, 0.6, 0.5, 0.4]) {
        if (blob.size <= byteTarget) break;
        blob = await blobFrom(canvas, type, quality);
      }
      // Difficult noise-heavy images may need fewer pixels as well.
      while (blob.size > byteTarget && Math.max(canvas.width, canvas.height) > Math.min(640, longSide / 2)) {
        const smaller = document.createElement('canvas');
        smaller.width = Math.max(1, Math.round(canvas.width * 0.8));
        smaller.height = Math.max(1, Math.round(canvas.height * 0.8));
        smaller.getContext('2d').drawImage(canvas, 0, 0, smaller.width, smaller.height);
        canvas.width = smaller.width; canvas.height = smaller.height;
        canvas.getContext('2d').drawImage(smaller, 0, 0);
        smaller.width = smaller.height = 1;
        blob = await blobFrom(canvas, type, 0.7);
      }
      if (blob.type !== type || blob.size >= byteCeiling) throw new Error('Could not make this photo small enough. Try another copy.');
      return { blob, ext: type === 'image/webp' ? '.webp' : '.jpg' };
    } finally { canvas.width = canvas.height = 1; }
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
