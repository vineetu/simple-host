/* MCP Apps bridge: talks only to the chat app that shows this screen. */
(function () {
  'use strict';
  var next = 0, waiting = {}, handlers = {}, origin = '*';
  function send(m) { m.jsonrpc = '2.0'; window.parent.postMessage(m, origin); }
  function request(method, params) {
    var id = ++next;
    return new Promise(function (resolve, reject) {
      waiting[id] = { resolve: resolve, reject: reject, method: method };
      send({ id: id, method: method, params: params });
    });
  }
  window.addEventListener('message', function (e) {
    var m = e.data;
    if (e.source !== window.parent || !m || m.jsonrpc !== '2.0') return;
    if (!m.method && waiting[m.id]) {
      var w = waiting[m.id]; delete waiting[m.id];
      if (w.method === 'ui/initialize' && e.origin && e.origin !== 'null') origin = e.origin;
      if (m.error) w.reject(new Error(m.error.message || 'The chat app could not do that.')); else w.resolve(m.result);
      return;
    }
    if (handlers[m.method]) handlers[m.method](m.params || {});
  });
  function text(r) { var t = (r.content || []).filter(function (c) { return c.type === 'text'; })[0]; return t ? t.text : ''; }
  window.SH = {
    on: function (name, fn) { handlers['ui/notifications/' + name] = fn; },
    call: function (name, args) {
      return request('tools/call', { name: name, arguments: args }).then(function (r) {
        if (r.isError) throw new Error(text(r) || 'That did not work. Try again.');
        return r;
      });
    },
    open: function (url) { return request('ui/open-link', { url: url }); },
    start: function () {
      return request('ui/initialize', { protocolVersion: '2026-01-26', appInfo: { name: 'Simple Host', version: '2' }, appCapabilities: {} }).then(function () {
        send({ method: 'ui/notifications/initialized', params: {} });
        new ResizeObserver(function () {
          send({ method: 'ui/notifications/size-changed', params: { height: Math.ceil(document.body.getBoundingClientRect().height) } });
        }).observe(document.body);
      });
    }
  };
})();
