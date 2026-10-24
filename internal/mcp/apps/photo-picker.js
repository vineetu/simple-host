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
    if (occupied.get('images') === false) throw new Error('The images path is a file. Rename it before adding photos.');
    const dot = name.lastIndexOf('.'), stem = name.slice(0, dot), ext = name.slice(dot);
    let path = 'images/' + name;
    for (let n = 2; occupied.has(path); n++) {
      if (occupied.get(path)) throw new Error('A folder uses this photo name. Choose another name.');
      const suffix = '-' + n;
      path = 'images/' + stem.slice(0, 60 - suffix.length) + suffix + ext;
    }
    occupied.set(path, false);
    return path;
  }
  // Largest-first packing avoids extra versions when the request cap is small.
  // Include the JSON envelope and reserve space for host-added MCP metadata.
  function batches(photos, site, maxBodyBytes) {
    const base = new TextEncoder().encode(JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'update_site', arguments: { site, keep_other_files: true, files_base64: {} } } })).length + 4096;
    const result = [];
    for (const photo of [...photos].sort((a, b) => b.base64.length - a.base64.length)) {
      const bytes = new TextEncoder().encode(JSON.stringify({ [photo.path]: photo.base64 })).length;
      if (base + bytes > maxBodyBytes) throw new Error('Could not make this photo small enough. Try another copy.');
      let batch = result.find(item => item.bytes + bytes <= maxBodyBytes);
      if (!batch) { batch = { bytes: base, photos: [] }; result.push(batch); }
      batch.bytes += bytes; batch.photos.push(photo);
    }
    return result.map(item => item.photos);
  }
  globalThis.SHPhotoBatch = { occupiedPaths, photoPath, batches };
  if (typeof document === 'undefined') return;
  const $ = id => document.getElementById(id), app = SHApp, config = SHPhotoConfig;
  let site = '', busy = false, ready = false, photos = [], saved = 0, finished = false, archive = false;
  const status = text => { $('status').textContent = text; };
  function lock() {
    $('add').disabled = busy || !ready || !site;
    $('add').hidden = finished || archive || busy || photos.filter(photo => !photo.failed).length >= 30;
    for (const photo of photos) { photo.remove.hidden = !!photo.saved; photo.remove.disabled = busy; }
    $('change').disabled = busy || !ready || saved > 0;
    $('sites').disabled = busy || saved > 0;
    $('finish').disabled = busy || !site || !(photos.some(photo => photo.blob && !photo.saved) || (saved > 0 && photos.every(photo => photo.saved)));
  }
  function selectSite(name) {
    if (site !== name) for (const photo of photos) { delete photo.path; delete photo.attempted; }
    site = name; $('site').textContent = name; lock();
  }
  function element(tag, text, parent, className) {
    const node = document.createElement(tag); node.textContent = text; if (className) node.className = className;
    parent.appendChild(node); return node;
  }
  function workerFactory() {
    const base = config.assetOrigin + '/photo-codecs/v3/';
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
  // Only the site/error state is consumed. Never parse, retain, or use photos_url.
  app.on('tool-input', params => {
    if (params.arguments?.archive === true) { archive = true; ready = false; document.body.hidden = true; return; }
    if (archive || finished || busy) return;
    if (typeof params.arguments?.site === 'string') selectSite(params.arguments.site);
  });
  app.on('tool-result', params => {
    if (archive || finished || busy) return;
    if (typeof params.structuredContent?.site === 'string') selectSite(params.structuredContent.site);
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
        const option = element('option', item.name, $('sites')); option.value = item.name;
      }
      $('sites').value = site; $('sites').hidden = false; $('sites').focus();
    } catch (error) { status(error.message); }
    finally { busy = false; lock(); }
  });
  $('sites').addEventListener('change', () => { selectSite($('sites').value); $('sites').hidden = true; });
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
    $('done').textContent = 'Added ' + saved + (saved === 1 ? ' photo' : ' photos') + ' to images/'; $('done').hidden = false;
    photos = [];
  }
  $('finish').addEventListener('click', async () => {
    if (busy || !site) return;
    if (saved > 0 && photos.every(photo => photo.saved)) { complete(); lock(); return; }
    busy = true; lock(); status('Saving photos...');
    let remaining = photos.filter(photo => photo.blob && !photo.saved);
    try {
      const current = await app.callTool('get_site', { site });
      if (!Array.isArray(current.files)) throw new Error('Could not read the site files. Try again.');
      // Reconcile a lost reply before assigning any new names. Retry the same
      // reserved path when the previous write cannot be confirmed.
      for (const photo of remaining) {
        if (photo.attempted && current.files.some(file => file.path === photo.path && file.size === photo.blob.size)) markSaved(photo);
      }
      remaining = remaining.filter(photo => !photo.saved);
      const occupied = occupiedPaths(current.files);
      for (const photo of remaining) if (photo.path) occupied.set(photo.path, false);
      for (const photo of remaining) {
        photo.path ||= photoPath(occupied, SHPhotos.slug(photo.name) + photo.ext);
        photo.base64 ||= (await SHPhotos.dataURL(photo.blob)).split(',')[1];
      }
      for (const batch of batches(remaining, site, config.maxBodyBytes)) {
        const files = {};
        for (const photo of batch) { files[photo.path] = photo.base64; photo.attempted = true; photo.label.textContent = 'Saving'; photo.progress.removeAttribute('value'); }
        try {
          await app.callTool('update_site', { site, keep_other_files: true, files_base64: files });
          for (const photo of batch) markSaved(photo);
        } catch (error) {
          for (const photo of batch) { photo.label.textContent = error.message; photo.progress.value = 0; }
          throw error;
        }
      }
      complete();
    } catch (error) { status(error.message); }
    finally { busy = false; lock(); }
  });
  app.start().catch(error => status(error.message));
})();
