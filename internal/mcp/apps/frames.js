/* One URL boundary for every live-site iframe. */
(function (root) {
  'use strict';
  root.SHFrameURL = value => {
    if (typeof value !== 'string' || !/^https:\/\/[a-z0-9-]+(?:\.[a-z0-9-]+)*\.simple-host\.app(?:[/?#]|$)/i.test(value)) return '';
    try {
      const url = new URL(value);
      if (url.protocol !== 'https:' || url.username || url.password || url.port ||
          !/^[a-z0-9-]+(?:\.[a-z0-9-]+)*\.simple-host\.app$/.test(url.hostname)) return '';
      url.searchParams.set('_sh_preview', '1');
      return url.href;
    } catch { return ''; }
  };
})(globalThis);
