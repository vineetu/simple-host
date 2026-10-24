/* Shared by /photos and the MCP photo picker. No network or account access. */
(function (root) {
  'use strict';
  const slug = name => name.toLowerCase().replace(/\.[^.]+$/, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60) || 'photo';
  const dataURL = blob => new Promise((resolve, reject) => {
    const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.onerror = reject; reader.readAsDataURL(blob);
  });
  const unreadable = () => new Error('Could not read this photo');
  function checkDimensions(width, height) {
    if (!Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width < 1 || height < 1 || width * height > 50000000) throw unreadable();
  }
  function codecKind(file, head) {
    const ascii = String.fromCharCode(...head.subarray(0, 256));
    if (/\.(heic|heif)$/i.test(file.name) || /image\/(heic|heif)/i.test(file.type) || (ascii.slice(4, 8) === 'ftyp' && /heic|heix|hevc|heif|mif1|msf1/.test(ascii.slice(8)) && !/avif|avis/.test(ascii.slice(8)))) return 'heif';
    if (/\.tiff?$/i.test(file.name) || /tiff/i.test(file.type) || ascii.startsWith('II*\0') || ascii.startsWith('MM\0*')) return 'tiff';
    return 'native';
  }
  // Read container headers before asking any browser decoder to allocate pixels.
  // Unknown formats and animations are refused rather than decoded without a bound.
  function nativeDimensions(buffer) {
    const b = new Uint8Array(buffer), v = new DataView(buffer);
    const text = (at, n) => String.fromCharCode(...b.subarray(at, at + n));
    const u16 = (at, le = false) => v.getUint16(at, le), u32 = (at, le = false) => v.getUint32(at, le);
    let found = false;
    const size = (w, h) => { checkDimensions(w, h); found = true; };
    if (text(0, 8) === '\x89PNG\r\n\x1a\n') {
      if (u32(8) !== 13 || text(12, 4) !== 'IHDR') throw unreadable();
      size(u32(16), u32(20));
      for (let at = 8; at < b.length;) {
        const n = u32(at), kind = text(at + 4, 4);
        if (at + 12 + n > b.length || kind === 'acTL') throw unreadable();
        at += 12 + n;
      }
    } else if (b[0] === 255 && b[1] === 216) {
      for (let at = 2; at < b.length;) {
        if (b[at++] !== 255) throw unreadable();
        while (b[at] === 255) at++;
        const marker = b[at++];
        if (marker === 0xda || marker === 0xd9) break;
        const n = u16(at);
        if (n < 2 || at + n > b.length) throw unreadable();
        if (marker >= 0xc0 && marker <= 0xcf && ![0xc4, 0xc8, 0xcc].includes(marker)) {
          if (found || n < 8) throw unreadable();
          size(u16(at + 5), u16(at + 3));
        }
        at += n;
      }
    } else if (text(0, 4) === 'RIFF' && text(8, 4) === 'WEBP') {
      if (u32(4, true) + 8 !== b.length) throw unreadable();
      let image = false;
      for (let at = 12; at < b.length;) {
        const kind = text(at, 4), n = u32(at + 4, true), p = at + 8;
        if (p + n > b.length || kind === 'ANIM' || kind === 'ANMF') throw unreadable();
        const u24 = o => b[o] + b[o+1]*256 + b[o+2]*65536;
        if (kind === 'VP8X') {
          if (n !== 10 || b[p] & 2) throw unreadable();
          size(1 + u24(p + 4), 1 + u24(p + 7));
        } else if (kind === 'VP8 ') {
          if (image || n < 10 || b[p] & 1 || text(p + 3, 3) !== '\x9d\x01\x2a') throw unreadable();
          size(u16(p + 6, true) & 16383, u16(p + 8, true) & 16383); image = true;
        } else if (kind === 'VP8L') {
          if (image || n < 5 || b[p] !== 47) throw unreadable();
          const bits = u32(p + 1, true);
          size(1 + (bits & 16383), 1 + ((bits >>> 14) & 16383)); image = true;
        }
        at = p + n + (n & 1);
      }
      if (!image) throw unreadable();
    } else if (/^GIF8[79]a$/.test(text(0, 6))) {
      size(u16(6, true), u16(8, true));
      let at = 13 + (b[10] & 128 ? 3 * (1 << ((b[10] & 7) + 1)) : 0), frames = 0;
      const skipBlocks = () => { while (at < b.length && b[at]) at += 1 + b[at]; if (at >= b.length) throw unreadable(); at++; };
      while (at < b.length && b[at] !== 0x3b) {
        const kind = b[at++];
        if (kind === 0x21) { at++; skipBlocks(); }
        else if (kind === 0x2c) {
          if (++frames > 1) throw unreadable();
          size(u16(at + 4, true), u16(at + 6, true));
          const flags = b[at + 8]; at += 9 + (flags & 128 ? 3 * (1 << ((flags & 7) + 1)) : 0);
          at++; skipBlocks();
        } else throw unreadable();
      }
      if (frames !== 1) throw unreadable();
    } else if (text(0, 2) === 'BM') {
      const dib = u32(14, true);
      if (dib === 12) size(u16(18, true), u16(20, true));
      else {
        if (dib < 40 || 14 + dib > b.length || [4, 5].includes(u32(30, true))) throw unreadable();
        size(v.getInt32(18, true), Math.abs(v.getInt32(22, true)));
      }
    } else if (text(4, 4) === 'ftyp' && /avif/.test(text(8, Math.min(u32(0) - 8, 256)))) {
      // Still AVIF: check every item extent, including grid output and tiles.
      // Track-based sequences need a different header parser and are refused.
      let pixels = 0;
      const boxes = (start, end, depth = 0) => {
        if (depth > 4) throw unreadable();
        for (let at = start; at < end;) {
          let n = u32(at), header = 8;
          const kind = text(at + 4, 4);
          if (n === 1) { n = u32(at + 8) * 4294967296 + u32(at + 12); header = 16; }
          if (n === 0) n = end - at;
          if (n < header || n > end - at || kind === 'moov') throw unreadable();
          const p = at + header;
          if (kind === 'ispe') {
            if (n < header + 12) throw unreadable();
            const w = u32(p + 4), h = u32(p + 8); size(w, h); pixels += w * h;
            if (pixels > 50000000) throw unreadable();
          }
          if (['meta', 'iprp', 'ipco'].includes(kind)) boxes(p + (kind === 'meta' ? 4 : 0), at + n, depth + 1);
          at += n;
        }
      };
      boxes(0, b.length);
    }
    if (!found) throw unreadable();
  }
  function deadline(promise, end, cancel = () => {}, late = () => {}) {
    return new Promise((resolve, reject) => {
      let expired = false;
      const timer = setTimeout(() => { expired = true; cancel(); reject(unreadable()); }, Math.max(0, end - Date.now()));
      promise.then(value => { if (expired) late(value); else { clearTimeout(timer); resolve(value); } }, error => { if (!expired) { clearTimeout(timer); reject(error); } });
    });
  }
  async function nativeDecode(file, end) {
    if (typeof createImageBitmap === 'function') {
      try {
        const bitmap = await deadline(createImageBitmap(file, { imageOrientation: 'from-image' }), end, undefined, bitmap => bitmap.close());
        try { checkDimensions(bitmap.width, bitmap.height); } catch (error) { bitmap.close(); throw error; }
        return { source: bitmap, width: bitmap.width, height: bitmap.height, orientation: 1, close: () => bitmap.close() };
      } catch (_) { if (Date.now() >= end) throw unreadable(); }
    }
    const reader = new FileReader();
    const url = await deadline(new Promise((resolve, reject) => {
      reader.onload = () => resolve(reader.result); reader.onerror = reject; reader.readAsDataURL(file);
    }), end, () => reader.abort());
    const img = new Image();
    try {
      await deadline(new Promise((resolve, reject) => { img.onload = resolve; img.onerror = reject; img.src = url; }), end, () => { img.src = ''; });
      checkDimensions(img.naturalWidth, img.naturalHeight);
      return { source: img, width: img.naturalWidth, height: img.naturalHeight, orientation: 1, close: () => { img.src = ''; } };
    } catch (_) { img.src = ''; throw unreadable(); }
  }
  async function workerDecode(buffer, kind, options, end) {
    const worker = options.workerFactory ? options.workerFactory() : new Worker('/photo-codecs/v3/decoder.js');
    try {
      return await deadline(new Promise((resolve, reject) => {
        worker.onmessage = event => event.data.error ? reject(unreadable()) : resolve(event.data);
        worker.onerror = () => reject(unreadable());
        worker.postMessage({ buffer, kind }, [buffer]);
      }), end, () => worker.terminate(), result => result.bitmap?.close());
    } finally { worker.terminate(); }
  }
  async function decode(file, options = {}) {
    if (file.size > 80000000) throw unreadable();
    const end = Date.now() + 20000;
    const buffer = await deadline(file.arrayBuffer(), end);
    const kind = codecKind(file, new Uint8Array(buffer));
    if (kind === 'native') { try { nativeDimensions(buffer); } catch (_) { throw unreadable(); } }
    if (kind === 'native' && !options.workerFactory && typeof Worker !== 'function') return nativeDecode(file, end);
    let decoded;
    try { decoded = await workerDecode(buffer, kind, options, end); }
    catch (error) {
      if (kind !== 'native' || Date.now() >= end) throw error;
      return nativeDecode(file, end);
    }
    if (decoded.nativeUnsupported) return nativeDecode(file, end);
    if (decoded.bitmap) {
      const bitmap = decoded.bitmap;
      return { source: bitmap, width: bitmap.width, height: bitmap.height, orientation: 1, close: () => bitmap.close() };
    }
    const canvas = document.createElement('canvas');
    canvas.width = decoded.width; canvas.height = decoded.height;
    canvas.getContext('2d').putImageData(new ImageData(new Uint8ClampedArray(decoded.buffer), decoded.width, decoded.height), 0, 0);
    return { source: canvas, width: canvas.width, height: canvas.height, orientation: decoded.orientation, close: () => { canvas.width = canvas.height = 1; } };
  }
  function draw(canvas, source, longSide) {
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
  async function shrink(file, options = {}) {
    const byteCeiling = Math.min(950000, Math.floor((options.maxBytes || 1000000) * 0.95));
    const byteTarget = Math.min(300000, byteCeiling);
    const longSide = Math.min(1600, options.maxDimension || 2048);
    const decoded = await decode(file, options);
    const canvas = document.createElement('canvas');
    try { draw(canvas, decoded, longSide); } finally { decoded.close(); }
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
  root.SHPhotos = { shrink, slug, dataURL, codecKind };
})(globalThis);
