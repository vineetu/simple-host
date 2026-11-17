// The /costs page: reads the inputs (and the address bar, so an estimate can
// be linked), prices every provider with calc.js against prices.json, and
// draws the 2,000-people example, the comparison, the chosen provider's line
// items, how traffic changes the bill, the sizing rules and the sources.
// Runs entirely in the browser; its only request is prices.json.
(function () {
  'use strict';
  var C = window.SHCosts;
  var NUMS = ['people', 'sites', 'views', 'viewMB', 'siteMB', 'savedMB', 'uploadsGB'];
  var prices = null, chosen = 'aws';

  function $(id) { return document.getElementById(id); }
  function el(tag, attrs, kids) {
    var n = document.createElement(tag);
    if (attrs) Object.keys(attrs).forEach(function (k) {
      if (k === 'text') n.textContent = attrs[k];
      else if (k === 'style') n.style.cssText = attrs[k];
      else n.setAttribute(k, attrs[k]);
    });
    (kids || []).forEach(function (k) { if (k != null) n.appendChild(typeof k === 'string' ? document.createTextNode(k) : k); });
    return n;
  }
  var usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 });
  var num = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });
  var whole = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });
  function money(n) { return usd.format(n); }
  // perPerson: dollars from $1, cents below, a tenth of a cent under 10¢.
  function perPerson(n) {
    if (n >= 1) return money(n);
    var c = n * 100;
    return (c < 10 ? c.toFixed(1).replace(/\.0$/, '') : Math.round(c)) + '¢';
  }
  function catColor(c) { return 'var(--c-' + c + ')'; }
  function pct(part, total) { return total > 0 ? Math.round(100 * part / total) : 0; }
  function levelName(id) { var l = C.trafficLevel(prices.model, id); return l ? l.name : 'Custom'; }

  function readInputs() {
    var inp = {};
    NUMS.forEach(function (k) { inp[k] = $('in-' + k).value; });
    inp.traffic = $('in-traffic').value;
    inp.existing = $('in-existing').checked;
    inp.ingress = $('in-ingress').checked;
    inp.network = $('in-private').checked ? 'private' : 'internet';
    inp.ha = $('in-ha').checked;
    return inp;
  }
  function fromURL() {
    var q = new URLSearchParams(location.search), d = prices.model.defaults;
    NUMS.forEach(function (k) {
      var v = q.get(k);
      $('in-' + k).value = v !== null && v !== '' && isFinite(Number(v)) && Number(v) >= 0 ? v : d[k];
    });
    var t = q.get('traffic');
    $('in-traffic').value = t && (t === 'custom' || C.trafficLevel(prices.model, t)) ? t : d.traffic;
    if (!q.get('traffic') && (q.has('views') || q.has('viewMB'))) $('in-traffic').value = 'custom';
    $('in-existing').checked = q.has('cluster') ? q.get('cluster') !== 'new' : !!d.existing;
    $('in-ingress').checked = q.has('ingress') ? q.get('ingress') !== 'new' : !!d.ingress;
    $('in-private').checked = q.has('network') ? q.get('network') === 'private' : d.network === 'private';
    $('in-ha').checked = q.has('ha') ? q.get('ha') === '1' : !!d.ha;
    var p = q.get('provider');
    if (p && prices.providers.some(function (x) { return x.id === p; })) chosen = p;
  }
  function toURL(inp) {
    var q = new URLSearchParams(), d = prices.model.defaults;
    ['people', 'sites', 'siteMB', 'savedMB', 'uploadsGB'].forEach(function (k) { if (String(inp[k]) !== String(d[k]) && inp[k] !== '') q.set(k, inp[k]); });
    if (inp.traffic !== d.traffic) q.set('traffic', inp.traffic);
    if (inp.traffic === 'custom') { q.set('views', inp.views); q.set('viewMB', inp.viewMB); }
    if (!inp.existing) q.set('cluster', 'new');
    if (inp.existing && !inp.ingress) q.set('ingress', 'new');
    if (inp.network !== d.network) q.set('network', inp.network);
    if (inp.ha) q.set('ha', '1');
    if (chosen !== 'aws') q.set('provider', chosen);
    var s = q.toString();
    try { history.replaceState(null, '', location.pathname + (s ? '?' + s : '')); } catch (e) {}
  }

  // syncTraffic shows what a traffic level means and fills page views and
  // data per page view from it; typing either one switches to Custom.
  function syncTraffic() {
    var l = C.trafficLevel(prices.model, $('in-traffic').value);
    if (l) {
      var people = Math.max(1, Math.round(Number($('in-people').value) || 1));
      $('in-views').value = people * l.pages_per_day * prices.model.working_days_per_month;
      $('in-viewMB').value = l.mb_per_page;
      $('traffic-hint').textContent = l.pages_per_day + ' pages a person a working day, ' + l.mb_per_page + ' MB each';
    } else {
      $('traffic-hint').textContent = 'Your own page views and size';
    }
    $('in-ingress').disabled = !$('in-existing').checked;
  }

  function bar(est, max) {
    var b = el('div', { class: 'bar', role: 'img', 'aria-label': C.CATEGORIES.map(function (c) { return c[1] + ' ' + money(est.cats[c[0]]); }).join(', ') });
    C.CATEGORIES.forEach(function (c) {
      var v = est.cats[c[0]];
      if (v > 0 && max > 0) b.appendChild(el('i', { style: 'width:' + (100 * v / max).toFixed(3) + '%;background:' + catColor(c[0]), title: c[1] + ': ' + money(v) }));
    });
    return b;
  }
  function row(est, alt, max) {
    var p = est.provider, inp = est.sizing.inputs;
    var btn = el('button', { type: 'button', class: 'prow', 'aria-pressed': String(p.id === chosen), 'data-provider': p.id }, [
      el('div', { class: 'pname' }, [el('b', { text: p.name }), el('span', { text: inp.existing ? 'Added to your ' + p.cluster + ' cluster' : p.kubernetes })]),
      bar(est, max),
      el('div', { class: 'amt' }, [
        el('b', { text: money(est.total) }),
        el('span', { text: perPerson(est.perPerson) + ' a person' }),
        el('span', { class: 'other', text: (inp.existing ? 'New cluster: ' : 'On a cluster you have: ') + money(alt.total) })
      ])
    ]);
    btn.addEventListener('click', function () {
      chosen = p.id; render();
      var d = $('detail');
      if (d && d.scrollIntoView && window.innerWidth < 760) d.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
    return btn;
  }

  function example() {
    var ex = prices.model.example, base = readInputs();
    $('ex-title').textContent = 'Example: ' + whole.format(ex.people) + ' people';
    $('ex-sub').textContent = whole.format(ex.sites) + ' sites, ' + (base.existing ? 'on a Kubernetes cluster you already run' : 'on a new cluster') +
      (base.network === 'private' ? ', traffic over a private link' : ', served over the internet') + '. Per person a month, and the monthly total.';
    var head = el('tr', null, [el('th', { text: 'Traffic' })].concat(ex.providers.map(function (id) {
      var p = prices.providers.filter(function (x) { return x.id === id; })[0];
      return el('th', { text: p.name });
    })));
    var body = el('tbody', null, ex.levels.map(function (lv) {
      var l = C.trafficLevel(prices.model, lv);
      return el('tr', null, [el('td', null, [el('b', { text: l.name }), el('span', { text: l.pages_per_day + ' pages a day, ' + l.mb_per_page + ' MB' })])].concat(ex.providers.map(function (id) {
        var inp = {};
        Object.keys(base).forEach(function (k) { inp[k] = base[k]; });
        inp.people = ex.people; inp.sites = ex.sites; inp.traffic = lv;
        var e = C.estimate(prices, id, inp);
        return el('td', null, [el('b', { text: perPerson(e.perPerson) }), el('span', { text: money(e.total) + ' a month' })]);
      })));
    }));
    var t = $('ex-table');
    t.textContent = '';
    t.appendChild(el('thead', null, [head]));
    t.appendChild(body);
    $('example').hidden = false;
  }

  function trafficTable(est) {
    var base = est.sizing.inputs, rows = prices.model.traffic_levels.map(function (l) {
      var inp = {};
      Object.keys(base).forEach(function (k) { inp[k] = base[k]; });
      inp.traffic = l.id;
      return C.estimate(prices, est.provider.id, inp);
    });
    if (base.traffic === 'custom') rows.push(est);
    var tb = el('tbody', null, rows.map(function (e) {
      var s = e.sizing, share = pct(e.cats.traffic, e.total);
      return el('tr', s.inputs.traffic === base.traffic ? { class: 'on' } : null, [
        el('td', { class: 'w', text: levelName(s.inputs.traffic) }),
        el('td', { class: 'n', text: num.format(s.egressGB) + ' GB' }),
        el('td', { class: 'n', text: money(e.total) }),
        el('td', { class: 'n', text: perPerson(e.perPerson) }),
        el('td', { class: 'n' }, [share + '%', el('span', { class: 'share', 'aria-hidden': 'true' }, [el('i', { style: 'width:' + share + '%' })])])
      ]);
    }));
    return el('div', { class: 'tscroll' }, [el('table', null, [
      el('thead', null, [el('tr', null, [el('th', { text: 'Traffic' }), el('th', { class: 'n', text: 'Data out a month' }), el('th', { class: 'n', text: 'Monthly' }), el('th', { class: 'n', text: 'Per person' }), el('th', { class: 'n', text: 'Share for traffic' })])]),
      tb])]);
  }

  function detail(est) {
    var p = est.provider, s = est.sizing, inp = s.inputs, box = $('detail');
    box.textContent = '';
    box.hidden = false;
    box.appendChild(el('h2', { text: p.name + ': ' + (inp.existing ? 'added to your ' + p.cluster + ' cluster' : 'a new cluster, ' + p.kubernetes) }));
    box.appendChild(el('p', { text: 'Region: ' + p.region + '. ' + levelName(inp.traffic) + ' traffic, ' + (inp.network === 'private' ? 'over a private link' : 'over the internet') + (inp.ha ? ', with high availability.' : '.') }));
    box.appendChild(el('div', { class: 'sumline' }, [
      el('div', null, [el('b', { text: money(est.total) }), el('span', { text: 'a month' })]),
      el('div', null, [el('b', { text: perPerson(est.perPerson) }), el('span', { text: 'a person a month, for ' + whole.format(inp.people) + (inp.people === 1 ? ' person' : ' people') })]),
      el('div', null, [el('b', { text: pct(est.cats.traffic, est.total) + '%' }), el('span', { text: 'of it is traffic' })])
    ]));
    var tb = el('tbody');
    C.CATEGORIES.forEach(function (c) {
      var its = est.items.filter(function (i) { return i.cat === c[0]; });
      if (!its.length) return;
      tb.appendChild(el('tr', { class: 'cat' }, [el('td', { colspan: '2' }, [el('i', { style: 'background:' + catColor(c[0]) }), c[1]]), el('td', { class: 'n', text: money(est.cats[c[0]]) })]));
      its.forEach(function (i) {
        tb.appendChild(el('tr', null, [el('td', { class: 'w', text: i.what }), el('td', { text: i.detail || '' }), el('td', { class: 'n', text: money(i.usd) })]));
      });
    });
    tb.appendChild(el('tr', { class: 'total' }, [el('td', { colspan: '2', text: 'Total a month' }), el('td', { class: 'n', text: money(est.total) })]));
    box.appendChild(el('div', { class: 'tscroll' }, [el('table', null, [
      el('thead', null, [el('tr', null, [el('th', { text: 'Line item' }), el('th', { text: 'Price' }), el('th', { class: 'n', text: 'Monthly' })])]), tb])]));
    box.appendChild(el('p', { text: 'Check it in the provider’s own calculator: ' + p.calculator.note + ' Use the line items above.' }));
    box.appendChild(el('a', { class: 'cta', href: p.calculator.url, target: '_blank', rel: 'noopener', text: 'Open the ' + p.calculator.name }));
    if (p.proof) box.appendChild(el('p', { class: 'proof', text: p.proof }));
    box.appendChild(el('h3', { text: 'How traffic changes it' }));
    box.appendChild(el('p', { text: 'The same ' + whole.format(inp.people) + ' people and ' + whole.format(inp.sites) + ' sites on ' + p.name + ' at each traffic level. Everything else stays nearly flat; traffic is what grows.' }));
    box.appendChild(trafficTable(est));
  }

  function assumptions(est) {
    var s = est.sizing, dl = $('assume');
    dl.textContent = '';
    var now = {
      'Replicas': s.replicas + ' replicas.',
      'Memory': num.format(s.memGiB) + ' GiB on a new cluster; ' + num.format(s.shareMemGiB) + ' GiB added to yours.',
      'CPU': num.format(s.cpu) + ' CPU on a new cluster; ' + num.format(s.shareCPU) + ' CPU added to yours.',
      'Your cluster': s.inputs.existing ? est.items[0].detail + '.' : null,
      'New cluster': s.inputs.existing ? null : est.nodes.count + ' × ' + est.nodes.name + ' on ' + est.provider.name + '.',
      'Database': 'The ~' + s.dbTier + ' GB plan: ' + est.db + ' on ' + est.provider.name + '.',
      'Database storage': num.format(s.dbGB) + ' GB.',
      'Bucket': num.format(s.bucketGB) + ' GB.',
      'Traffic levels': whole.format(s.inputs.views) + ' page views a month.',
      'Traffic': num.format(s.egressGB) + ' GB a month.'
    };
    prices.model.assumptions.forEach(function (a) {
      dl.appendChild(el('dt', { text: a[0] }));
      dl.appendChild(el('dd', null, [a[1] + (now[a[0]] ? ' ' : ''), now[a[0]] ? el('b', { text: 'For you: ' + now[a[0]] }) : null]));
    });
    $('assumptions').hidden = false;
  }

  function sources() {
    var dates = C.checkedDates(prices).sort();
    var oldest = dates[0], newest = dates[dates.length - 1];
    var asof = $('asof');
    asof.textContent = 'List prices as of ' + (oldest === newest ? oldest : oldest + ' to ' + newest) +
      ', in US dollars, on demand, excluding tax, in the region named for each provider. Your bill may differ: prices change, regions differ, commitments and discounts lower them, and your real traffic is yours to measure. Every figure on this page comes from one file, ';
    asof.appendChild(el('a', { href: '/costs/prices.json', text: 'prices.json' }));
    asof.appendChild(document.createTextNode(', which names the page each price was read from and the day it was checked.'));
    var box = $('srclist');
    box.textContent = '';
    prices.providers.forEach(function (p) {
      var urls = [];
      (function walk(o) {
        if (o && typeof o === 'object') Object.keys(o).forEach(function (k) {
          if ((k === 'source' || k === 'backup_source') && typeof o[k] === 'string') { if (urls.indexOf(o[k]) < 0) urls.push(o[k]); }
          else walk(o[k]);
        });
      })(p);
      box.appendChild(el('h3', { text: p.name }));
      box.appendChild(el('ul', { class: 'src' }, urls.map(function (u) { return el('li', null, [el('a', { href: u, target: '_blank', rel: 'noopener', text: u.replace(/^https:\/\//, '') })]); })));
    });
    $('sources').hidden = false;
  }

  function render() {
    syncTraffic();
    var inp = readInputs();
    var alt = {};
    Object.keys(inp).forEach(function (k) { alt[k] = inp[k]; });
    alt.existing = !inp.existing;
    var ests = prices.providers.map(function (p) { return { e: C.estimate(prices, p.id, inp), a: C.estimate(prices, p.id, alt) }; });
    var max = Math.max.apply(null, ests.map(function (x) { return x.e.total; }));
    var main = $('rows-main'), other = $('rows-other');
    main.textContent = ''; other.textContent = '';
    main.appendChild(el('div', { class: 'rows' }, ests.filter(function (x) { return x.e.provider.group === 'main'; }).map(function (x) { return row(x.e, x.a, max); })));
    var others = ests.filter(function (x) { return x.e.provider.group !== 'main'; });
    $('other-head').hidden = !others.length;
    if (others.length) other.appendChild(el('div', { class: 'rows' }, others.map(function (x) { return row(x.e, x.a, max); })));
    var legend = $('legend');
    legend.textContent = '';
    C.CATEGORIES.forEach(function (c) { legend.appendChild(el('li', null, [el('i', { style: 'background:' + catColor(c[0]) }), c[1]])); });
    var est = ests.filter(function (x) { return x.e.provider.id === chosen; })[0] || ests[0];
    example();
    detail(est.e);
    assumptions(est.e);
    toURL(inp);
  }

  fetch('/costs/prices.json', { credentials: 'omit' }).then(function (r) {
    if (!r.ok) throw new Error(r.status);
    return r.json();
  }).then(function (data) {
    prices = data;
    var sel = $('in-traffic');
    prices.model.traffic_levels.forEach(function (l) { sel.appendChild(el('option', { value: l.id, text: l.name })); });
    sel.appendChild(el('option', { value: 'custom', text: 'Custom' }));
    fromURL();
    var form = $('inputs');
    form.addEventListener('input', function (e) {
      if (e.target && (e.target.id === 'in-views' || e.target.id === 'in-viewMB')) $('in-traffic').value = 'custom';
      render();
    });
    form.addEventListener('change', render);
    form.addEventListener('submit', function (e) { e.preventDefault(); });
    $('ex-use').addEventListener('click', function () {
      $('in-people').value = prices.model.example.people;
      $('in-sites').value = prices.model.example.sites;
      if ($('in-traffic').value === 'custom') $('in-traffic').value = prices.model.defaults.traffic;
      render();
    });
    sources();
    render();
  }).catch(function () {
    $('compare-note').textContent = 'The prices could not be loaded. Reload the page to try again.';
  });
})();
