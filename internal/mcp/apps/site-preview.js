(function () {
  'use strict';
  const app = window.SHApp, $ = id => document.getElementById('preview-' + id);
  let link = '', frame, full = false, copyTimer, framesBlocked = false, source = '', name = '';
  document.addEventListener('securitypolicyviolation', event => {
    if (event.effectiveDirective === 'frame-src') {
      framesBlocked = true; frame?.remove(); frame = null; $('tile').hidden = false;
    }
  });
  function https(value) {
    if (typeof value !== 'string') return '';
    try { const url = new URL(value); return url.protocol === 'https:' && !url.username && !url.password ? value : ''; }
    catch { return ''; }
  }
  function sizePreview() {
    if (!frame) return;
    const scale = Math.min(1, $('visual').clientWidth / 1280);
    frame.style.width = full ? '100%' : '1280px';
    frame.style.height = full ? '100%' : (280 / scale) + 'px';
    frame.style.transform = full ? 'none' : 'scale(' + scale + ')';
  }
  function display(mode) {
    const next = mode === 'fullscreen', changed = next !== full;
    full = next;
    document.body.classList.toggle('preview-fullscreen', full);
    $('fullbar').hidden = !full;
    $('details').hidden = full;
    if (changed || (!frame && source && !framesBlocked)) {
      frame?.remove(); frame = null;
      if (source && !framesBlocked) {
        frame = document.createElement('iframe'); frame.title = name;
        frame.setAttribute('sandbox', full ? 'allow-scripts' : '');
        frame.setAttribute('credentialless', '');
        frame.setAttribute('referrerpolicy', 'no-referrer');
        frame.inert = !full; frame.tabIndex = full ? 0 : -1;
        frame.style.pointerEvents = full ? 'auto' : 'none';
        frame.setAttribute('aria-hidden', String(!full));
        frame.src = SHFrameURL(source); $('visual').append(frame);
      }
    }
    if (changed) $(full ? 'back' : 'full').focus();
    sizePreview();
  }
  function context(update = app.hostContext) {
    $('full').hidden = !app.hostContext.availableDisplayModes?.includes('fullscreen');
    if (update.displayMode !== undefined) display(update.displayMode);
  }
  async function requestMode(mode) {
    if (!app.hostContext.availableDisplayModes?.includes(mode)) return;
    try {
      const result = await app.requestDisplayMode(mode);
      if (result?.mode === 'inline' || result?.mode === 'fullscreen') display(result.mode);
    } catch { /* A refused or unsupported request leaves the current card in place. */ }
  }
  app.on('host-context-changed', context);
  app.on('tool-result', result => {
    const preview = result.structuredContent?.site_preview;
    if (!preview || typeof preview !== 'object' || Array.isArray(preview)) return;
    const site = result.isError ? null : result.structuredContent;
    frame?.remove(); frame = null;
    clearTimeout(copyTimer); $('copy').textContent = 'Copy link';
    $('copy-fallback').hidden = true; $('status').textContent = '';
    $('card').hidden = !site;
    if (!site) return;
    name = typeof site.name === 'string' ? site.name : '';
    link = https(site.url);
    $('title').textContent = name; $('tile').textContent = name;
    $('open').disabled = !link; $('copy').disabled = !link;
    source = !framesBlocked && app.hostCapabilities?.sandbox?.csp?.frameDomains?.length !== 0 && site.site_preview?.open_to_anyone === true
      ? SHFrameURL(site.url) || SHFrameURL(site.site_preview.canonical_url) : '';
    $('tile').hidden = !!source;
    display(full ? 'fullscreen' : 'inline');
  });
  $('open').onclick = async () => {
    try { await app.openLink(link); }
    catch { $('status').textContent = 'Could not open the link. Copy it to open in your browser.'; }
  };
  $('copy').onclick = async () => {
    try {
      await navigator.clipboard.writeText(link);
      $('copy-fallback').hidden = true;
      $('copy').textContent = 'Copied';
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => { $('copy').textContent = 'Copy link'; }, 1500);
    } catch {
      $('copy-fallback').hidden = false;
      $('link').value = link;
      $('link').focus(); $('link').select();
    }
  };
  $('full').onclick = () => requestMode('fullscreen');
  $('back').onclick = () => requestMode('inline');
  new ResizeObserver(sizePreview).observe($('visual'));
  app.start().then(() => context()).catch(() => { $('card').hidden = true; });
})();
