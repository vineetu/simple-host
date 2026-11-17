/* One URL boundary for every live-site iframe. */
(function (root) {
  'use strict';
  const domains = root.window?.SHPreviewConfig?.frameDomains || [];
  root.SHFrameURL = value => {
    if (typeof value !== 'string' || !/^https:\/\//i.test(value) || value.includes('\\')) return '';
    try {
      const url = new URL(value), authority = value.split(/[/?#]/)[2];
      if (authority.includes('@')) return '';
      if (url.protocol !== 'https:' || url.username || url.password || !domains.some(domain => {
        if (!domain.startsWith('https://*.')) return url.origin === domain;
        const suffix = domain.slice('https://*'.length);
        return !authority.includes(':') && url.hostname.endsWith(suffix) &&
          /^[a-z0-9-]+(?:\.[a-z0-9-]+)*$/.test(url.hostname.slice(0, -suffix.length));
      })) return '';
      url.searchParams.set('_sh_preview', '1');
      return url.href;
    } catch { return ''; }
  };
})(globalThis);
