(function () {
  'use strict';
  function occupiedPaths(files) {
    const occupied = new Map();
    for (const file of files) {
      occupied.set(file.path, false);
      const parts = file.path.split('/');
      for (let i = 1; i < parts.length; i++) occupied.set(parts.slice(0, i).join('/'), true);
    }
    return occupied;
  }
  function photoPath(occupied, name) {
    const dot = name.lastIndexOf('.'), stem = name.slice(0, dot), ext = name.slice(dot);
    let path = name;
    for (let n = 2; occupied.has(path); n++) {
      if (occupied.get(path)) throw new Error('A folder uses this photo name. Choose another name.');
      const suffix = '-' + n;
      path = stem.slice(0, 60 - suffix.length) + suffix + ext;
    }
    occupied.set(path, false);
    return path;
  }
  globalThis.SHPhotoBatch = { occupiedPaths, photoPath };
  if (typeof document === 'undefined') return;
  const $ = id => document.getElementById(id), app = SHApp, config = SHPhotoConfig;
  let site = '', owner = '', namespace = '', busy = false, ready = false, photos = [], saved = 0, finished = false, archive = false;
  const status = text => { $('status').textContent = text; };
  function lock() {
    $('add').disabled = busy || !ready || !site;
    $('add').hidden = finished || archive || busy || photos.filter(photo => !photo.failed).length >= 30;
    for (const photo of photos) { photo.remove.hidden = !!photo.saved; photo.remove.disabled = busy; }
    $('change').disabled = busy || !ready || saved > 0;
    $('sites').disabled = busy || saved > 0;
    $('finish').disabled = busy || !site || !(photos.some(photo => photo.blob && !photo.saved) || (saved > 0 && photos.every(photo => photo.saved)));
  }
  function selectSite(name, team = '') {
    if (site !== name || owner !== team) for (const photo of photos) { delete photo.path; delete photo.attempted; }
    site = name; owner = team; $('site').textContent = name + (namespace ? ' · ' + namespace : ''); lock();
  }
  const where = () => owner ? { site, owner } : { site };
  const store = rest => 'storage/files/' + encodeURIComponent(namespace) + '/objects' + rest;
  function element(tag, text, parent, className) {
    const node = document.createElement(tag); node.textContent = text; if (className) node.className = className;
    parent.appendChild(node); return node;
  }
  function workerFactory() {
    const base = config.assetOrigin + '/assets/photo-codecs/v3/';
    // Cross-origin Worker(url) is forbidden. Blob workers inherit the host CSP;
    // The capability probe checks imports and Wasm under that inherited policy.
    const source = 'self.photoCodecBase=' + JSON.stringify(base) + ';importScripts(' + JSON.stringify(base + 'decoder.js?apps=1') + ');';
    const url = URL.createObjectURL(new Blob([source], { type: 'text/javascript' }));
    try {
      const worker = new Worker(url);
      const terminate = worker.terminate.bind(worker);
      worker.terminate = () => { terminate(); URL.revokeObjectURL(url); };
      return worker;
    } catch (error) { URL.revokeObjectURL(url); throw error; }
  }
  let codecSupport, workerSupport = false;
  function supportsCodecs() {
    return codecSupport ||= new Promise(resolve => {
      let worker, timer;
      const finish = supported => { clearTimeout(timer); worker?.terminate(); resolve(supported); };
      try {
        worker = workerFactory();
        timer = setTimeout(() => finish(false), 3000);
        worker.onmessage = event => { workerSupport = true; finish(event.data.ready === true); };
        worker.onerror = () => finish(false);
        worker.postMessage({ kind: 'probe' });
      } catch (_) { finish(false); }
    });
  }
  // Only the site, namespace and error state are consumed. Never parse, retain, or use the link.
  const hideArchive = () => { archive = true; ready = false; document.body.hidden = true; };
  app.on('tool-input', params => {
    const args = params.arguments || {};
    if (args.kind === 'archive') { hideArchive(); return; } // no namespace: the result names it (photos)
    if (archive || finished || busy) return;
    if (typeof args.namespace === 'string') namespace = args.namespace;
    if (typeof args.site === 'string') selectSite(args.site, typeof args.owner === 'string' ? args.owner : '');
  });
  app.on('tool-result', params => {
    if (params.structuredContent?.kind === 'archive') hideArchive();
    if (archive || finished || busy) return;
    if (typeof params.structuredContent?.namespace === 'string' && params.structuredContent.namespace) { namespace = params.structuredContent.namespace; selectSite(site, owner); }
    // A host without interactive tool calls keeps the tool's existing chat text.
    if (!app.hostCapabilities?.serverTools) { document.body.hidden = true; ready = false; return; }
    ready = !params.isError;
    if (!ready) status('Could not save. Try again.');
    lock();
  });
  app.on('tool-cancelled', () => { ready = false; lock(); });
  $('change').addEventListener('click', async () => {
    busy = true; lock();
    try {
      const result = await app.callTool('list_sites');
      $('sites').replaceChildren();
      for (const item of result.sites || []) {
        const option = element('option', item.name, $('sites')); option.value = item.name; option.dataset.owner = item.owner || '';
        if (item.name === site && (item.owner || '') === owner) option.selected = true;
      }
      $('sites').hidden = false; $('sites').focus();
    } catch (error) { status(error.message); }
    finally { busy = false; lock(); }
  });
  $('sites').addEventListener('change', () => { const option = $('sites').selectedOptions[0]; selectSite(option.value, option.dataset.owner); $('sites').hidden = true; });
  $('add').addEventListener('click', () => $('files').click());
  $('files').addEventListener('change', async event => {
    const files = Array.from(event.target.files); event.target.value = '';
    if (busy || !files.length) return;
    if (files.length + photos.filter(photo => !photo.failed).length > 30) { status('Choose up to 30 photos at a time.'); return; }
    busy = true; lock(); $('add').hidden = true; $('actions').hidden = false;
    const supported = await supportsCodecs();
    for (const [index, file] of files.entries()) {
      if (!supported) {
        try {
          const head = new Uint8Array(await file.slice(0, 256).arrayBuffer());
          if (SHPhotos.codecKind(file, head) !== 'native') throw new Error('Use the photos link in the chat for HEIC or TIFF.');
        } catch (error) {
          $('fallback').hidden = false; status(error.message); continue;
        }
      }
      status('Shrinking photos... ' + (index + 1) + ' of ' + files.length);
      const row = element('div', '', $('photos'));
      const image = element('img', '', row, 'photo-visual'); image.alt = 'Photo preview'; image.hidden = true;
      element('div', file.name, row, 'photo-name');
      const progress = element('progress', '', row, 'progress'); progress.max = 100; progress.setAttribute('aria-label', file.name);
      const label = element('div', 'Shrinking', row, 'progress-label');
      const remove = element('button', 'Remove', row, 'text-button'); remove.type = 'button'; remove.disabled = true;
      remove.setAttribute('aria-label', 'Remove ' + file.name);
      const photo = { name: file.name, label, progress, remove }; photos.push(photo);
      remove.addEventListener('click', () => {
        if (busy || photo.saved) return;
        photos = photos.filter(item => item !== photo); row.remove();
        if (saved > 0 && photos.every(item => item.saved)) status('');
        $('fallback').hidden = !photos.some(item => item.failed); lock();
      });
      try {
        Object.assign(photo, await SHPhotos.shrink(file, { ...config, ...(workerSupport ? { workerFactory } : {}) }));
        image.onerror = () => { image.hidden = true; };
        image.src = await SHPhotos.dataURL(photo.blob); image.hidden = false;
        progress.value = 100; label.textContent = 'Shrunk · ready';
      } catch (_) {
        photo.failed = true; delete photo.blob;
        progress.value = 0; label.textContent = 'Could not read this photo';
        $('fallback').hidden = false;
      }
    }
    busy = false; lock();
  });
  function markSaved(photo) {
    if (!photo.saved) { photo.saved = true; saved++; }
    photo.label.textContent = 'Added'; photo.progress.value = 100;
  }
  function complete() {
    finished = true; status('');
    for (const id of ['selection', 'sites', 'status', 'add', 'photos', 'actions', 'fallback']) $(id).hidden = true;
    $('done').textContent = 'Added ' + saved + (saved === 1 ? ' photo' : ' photos') + ' to ' + namespace; $('done').hidden = false;
    photos = [];
  }
  $('finish').addEventListener('click', async () => {
    if (busy || !site) return;
    if (saved > 0 && photos.every(photo => photo.saved)) { complete(); lock(); return; }
    busy = true; lock(); status('Saving photos...');
    let remaining = photos.filter(photo => photo.blob && !photo.saved);
    try {
      const files = [];
      for (let after = '', more = true; more;) {
        const page = await app.callTool('data', { ...where(), path: store('?after=' + encodeURIComponent(after)) });
        files.push(...(page.items || []).map(item => ({ path: item.path, size: item.bytes })));
        after = page.next_after || ''; more = !!after;
      }
      // Reconcile a lost reply before assigning any new names. Retry the same
      // reserved path when the previous write cannot be confirmed.
      for (const photo of remaining) {
        if (photo.attempted && files.some(file => file.path === photo.path && file.size === photo.blob.size)) markSaved(photo);
      }
      remaining = remaining.filter(photo => !photo.saved);
      const occupied = occupiedPaths(files);
      for (const photo of remaining) if (photo.path) occupied.set(photo.path, false);
      for (const photo of remaining) {
        photo.path ||= photoPath(occupied, SHPhotos.slug(photo.name) + photo.ext);
        photo.base64 ||= (await SHPhotos.dataURL(photo.blob)).split(',')[1];
      }
      // One call per photo, straight into the namespace's file store.
      for (const photo of remaining) {
        photo.attempted = true; photo.label.textContent = 'Saving'; photo.progress.removeAttribute('value');
        try {
          await app.callTool('data', { ...where(), method: 'PUT', path: store('/' + photo.path.split('/').map(encodeURIComponent).join('/')), body_base64: photo.base64, type: photo.blob.type || 'image/jpeg' });
          markSaved(photo);
        } catch (error) {
          photo.label.textContent = error.message; photo.progress.value = 0;
          throw error;
        }
      }
      complete();
    } catch (error) { status(error.message); }
    finally { busy = false; lock(); }
  });
  app.start().catch(error => status(error.message));
})();
