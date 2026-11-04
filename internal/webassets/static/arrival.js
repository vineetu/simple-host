/* Same-visit attribution. URL values only: no cookies or browser storage. */
(function () {
  'use strict';
  var keys = ['from', 'utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'referrer'];
  var values = new URLSearchParams(location.search);
  if (!/^[a-z0-9][a-z0-9-]{0,39}$/i.test(values.get('from') || '')) values = new URLSearchParams();
  if (values.has('from') && !values.has('referrer') && document.referrer) {
    try {
      var ref = new URL(document.referrer);
      if (ref.hostname !== location.hostname) values.set('referrer', ref.hostname);
    } catch (e) {}
  }
  function carry(raw) {
    var dest = new URL(raw, location.href);
    if (dest.origin !== location.origin || !values.has('from')) return raw;
    keys.forEach(function (key) {
      if (values.has(key) && !dest.searchParams.has(key)) dest.searchParams.set(key, values.get(key));
    });
    return dest.href;
  }
  window.SHArrival = { url: carry };
  // Navigation carries the label only while the person follows this visit's
  // links. A fresh tab or a later visit has no remembered attribution.
  function links() {
    document.querySelectorAll('a[href]').forEach(function (a) {
      var raw = a.getAttribute('href');
      if (raw && raw[0] !== '#' && !/^(mailto:|tel:|javascript:)/i.test(raw)) a.href = carry(raw);
    });
  }
  document.addEventListener('DOMContentLoaded', links);
  document.addEventListener('click', function (event) {
    var a = event.target.closest && event.target.closest('a[href]');
    if (a && a.getAttribute('href')[0] !== '#') a.href = carry(a.href);
  }, true);
}());
