// The picker behind a one-time upload link (/upload/<token>). Photos are made
// smaller here (photo-process.js), other files go as they are, and everything
// is sent once to PUT /v1/uploads/<token>. No sign-in: the link is the right.
(function () {
  'use strict';
  const $ = id => document.getElementById(id);
  const status = (text, bad) => { $('status').textContent = text; $('status').classList.toggle('bad', !!bad); };
  const token = location.pathname.split('/').filter(Boolean).pop() || '';
  const MAX_BYTES = 1000000, MAX_SIDE = 2048;
  const size = n => n >= 1000000 ? (n / 1000000).toFixed(1) + ' MB' : Math.max(1, Math.round(n / 1000)) + ' KB';
  let items = [], busy = false;
  function el(tag, text, parent) {
    const node = document.createElement(tag);
    if (text) node.textContent = text;
    if (parent) parent.appendChild(node);
    return node;
  }
  function lock(on) {
    busy = on;
    $('files').disabled = on;
    $('save').disabled = on || !items.length;
    $('photos').querySelectorAll('button,input').forEach(n => { n.disabled = on; });
  }
  const isPhoto = f => /^image\//.test(f.type) || /\.(heic|heif|tiff?|avif|bmp)$/i.test(f.name);
  function show(item, original) {
    const row = el('div', '', $('photos')); row.className = 'photo';
    if (item.photo) {
      const img = el('img', '', row); img.alt = 'Preview';
      item.url = URL.createObjectURL(item.blob); img.src = item.url;
    } else el('div', '', row).className = 'file';
    const box = el('div', '', row);
    const label = el('label', 'Name', box);
    const input = el('input', '', label); input.value = item.name; input.maxLength = 60; input.setAttribute('aria-describedby', 'name-hint');
    el('p', original.name + ': ' + size(original.size) + (item.photo ? ' → ' + size(item.blob.size) : ''), box).className = 'muted';
    item.input = input;
    const rm = el('button', 'Remove', box); rm.type = 'button';
    rm.addEventListener('click', () => { if (item.url) URL.revokeObjectURL(item.url); items = items.filter(x => x !== item); row.remove(); lock(false); });
  }
  async function choose(files) {
    if (busy) return;
    if (files.length + items.length > 30) { status('Choose up to 30 at a time.', true); return; }
    lock(true);
    const failed = [];
    for (const file of files) {
      status('Preparing ' + file.name + '…');
      try {
        let item;
        if (isPhoto(file)) {
          const p = await SHPhotos.shrink(file, { maxBytes: MAX_BYTES, maxDimension: MAX_SIDE });
          item = { photo: true, blob: p.blob, ext: p.ext, name: SHPhotos.slug(file.name) };
        } else {
          const m = file.name.match(/\.[a-z0-9]{1,8}$/i);
          item = { photo: false, blob: file, ext: m ? m[0].toLowerCase() : '', name: SHPhotos.slug(file.name) };
        }
        items.push(item); show(item, file);
      } catch (e) { failed.push(file.name + ': ' + e.message); }
    }
    $('files').value = '';
    lock(false);
    status(failed.length ? failed.join(' ') : items.length + ' ready to save.', failed.length > 0);
  }
  async function save() {
    if (busy || !items.length) return;
    const form = new FormData();
    for (const item of items) {
      const name = SHPhotos.slug(item.input.value); item.input.value = name;
      form.append('files', item.blob, name + item.ext);
    }
    lock(true); status('Saving…');
    try {
      const res = await fetch('/v1/uploads/' + encodeURIComponent(token), { method: 'PUT', body: form, credentials: 'omit' });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.fix || 'Could not save. Try again.');
      const saved = (body.files || []).map(f => typeof f === 'string' ? { name: f } : f);
      $('results').replaceChildren();
      for (const f of saved) {
        const li = el('li', '', $('results'));
        const a = el('a', f.url || f.name, li); a.className = 'grow';
        if (f.url) a.href = f.url;
        const copy = el('button', 'Copy address', li);
        copy.addEventListener('click', () => copyText(f.url || f.name, copy));
      }
      $('summary').textContent = 'I added ' + (saved.map(f => f.name).join(', ') || items.length + ' files') + ' to the site.';
      $('editor').hidden = true; $('done').hidden = false;
      status('Saved. This link has now been used.');
      items.forEach(x => x.url && URL.revokeObjectURL(x.url)); items = [];
    } catch (e) { status(e.message, true); lock(false); }
  }
  async function copyText(text, button) {
    try { await navigator.clipboard.writeText(text); button.textContent = 'Copied'; }
    catch (_) { status('Select the text and copy it.'); }
  }
  $('files').addEventListener('change', e => choose(Array.from(e.target.files)));
  ['dragenter', 'dragover'].forEach(n => $('drop').addEventListener(n, e => { e.preventDefault(); $('drop').classList.add('drag'); }));
  ['dragleave', 'drop'].forEach(n => $('drop').addEventListener(n, e => { e.preventDefault(); $('drop').classList.remove('drag'); }));
  $('drop').addEventListener('drop', e => choose(Array.from(e.dataTransfer.files)));
  $('save').addEventListener('click', save);
  $('copy-summary').addEventListener('click', () => copyText($('summary').textContent, $('copy-summary')));
  if (!token) { $('editor').hidden = true; status('This link is not complete. Ask your chat for a new one.', true); }
})();
