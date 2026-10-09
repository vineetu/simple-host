// The setup helper at /setup. Everything happens in this page: the settings
// lists are the two settings.json files next to this script (generated from
// each product's code, see docs/advanced/README.md), and secrets are never
// asked for, only named as blanks.
(function () {
  'use strict';

  var FILES = { small: '/setup/small-box-settings.json', ent: '/setup/enterprise-settings.json' };
  // The release the installer pins (VERSION in deploy/install/install.sh),
  // the commit its tag points at, and install.sh's sha256 in that commit.
  // The command fetches the installer by commit (a tag can be moved) and
  // checks the hash before running it, so what it installs is fixed, not
  // whatever main holds that day. After tagging a new release, set all
  // three: git rev-parse vX.Y.Z^{commit}, and
  // git show vX.Y.Z:deploy/install/install.sh | sha256sum. A Go test
  // (TestSetupHelperInstallerRelease) checks them against the tag.
  var INSTALLER_RELEASE = 'v0.7.11';
  var INSTALLER_COMMIT = 'f5eec65771b050922673057636cd258552e91626';
  var INSTALLER_SHA256 = '85d5a5d6a6dada19dfda70e27d53b04dd21dfbbfae0555a8eddfc5bde51c4ff9';
  var INSTALL_URL = 'https://raw.githubusercontent.com/vineetu/simple-host/' + INSTALLER_COMMIT + '/deploy/install/install.sh';
  // Enterprise installs into a cluster the operator already runs, from this
  // pinned chart (app v0.9.3). The page never provisions a cluster.
  var ENT_CHART = 'oci://ghcr.io/vineetu/charts/simple-host-enterprise';
  var ENT_CHART_VERSION = '0.2.1';
  var ENT_CHART_SHA256 = '9eae4d8756fe49b1ef33a24bb7a6830a8636044ccfdb754cc44fd4f475a3bf98';
  var ENT_CHART_FILE = 'charts/simple-host-enterprise-' + ENT_CHART_VERSION + '.tgz';
  // Where a small box is recommended to run. A referral link: the page says so.
  var UPCLOUD_SIGNUP = 'https://signup.upcloud.com/?promo=JF2WCV';
  // What install.sh writes when its flag is not given (deploy/install/install.sh).
  var INSTALLER_DEFAULTS = { KEEP_VERSIONS: '1', MAX_ARCHIVE_MB: '100' };

  // <setupBasics> Settings the basic questions cover, per product; the rest
  // are Advanced. The assistant's server keeps the same lists and provider
  // ids used by the form.
  var SMALL_BASIC = ['SITE_DOMAIN', 'CONTENT_HOST', 'RESEND_API_KEY', 'MAIL_FROM', 'GOOGLE_OAUTH_CLIENT_ID', 'GOOGLE_OAUTH_CLIENT_SECRET'];
  var ENT_BASIC = ['PUBLIC_BASE_URL', 'SECURE_MODE', 'ADMIN_EMAILS', 'OIDC_ISSUER', 'OIDC_CLIENT_ID', 'OIDC_CLIENT_SECRET',
    'ALLOWED_EMAIL_DOMAINS', 'OWNER_CERTS', 'OWNER_CERT_ISSUER', 'SMTP_URL', 'SMTP_FROM', 'SESSION_SIGNING_KEY',
    'BACKUP_STORAGE_ENDPOINT', 'BACKUP_STORAGE_REGION', 'BACKUP_STORAGE_BUCKET', 'BACKUP_STORAGE_ACCESS_KEY_ID',
    'BACKUP_STORAGE_SECRET_ACCESS_KEY', 'DB_HOST', 'DB_PORT', 'DB_NAME', 'DB_USER', 'DB_PASSWORD', 'DB_APP_PASSWORD',
    'DB_SSL_ROOT_CERT', 'DB_DSN', 'TRUSTED_PROXY_CIDRS'];

  var IDPS = [
    { id: 'okta', name: 'Okta', issuer: 'https://YOUR-ORG.okta.com' },
    { id: 'entra', name: 'Microsoft Entra ID', issuer: 'https://login.microsoftonline.com/YOUR-TENANT-ID/v2.0' },
    { id: 'google', name: 'Google Workspace', issuer: 'https://accounts.google.com' },
    { id: 'keycloak', name: 'Keycloak', issuer: 'https://YOUR-HOST/realms/YOUR-REALM' },
    { id: 'other', name: 'Another OIDC provider', issuer: '' }
  ];
  var BUCKETS = [
    { id: 'aws', name: 'AWS S3', endpoint: 'https://s3.us-east-1.amazonaws.com', region: 'us-east-1' },
    { id: 'gcs', name: 'Google Cloud Storage', endpoint: 'https://storage.googleapis.com', region: 'us-central1' },
    { id: 'oci', name: 'Oracle Cloud', endpoint: 'https://YOUR-NAMESPACE.compat.objectstorage.us-ashburn-1.oraclecloud.com', region: 'us-ashburn-1' },
    { id: 'upcloud', name: 'UpCloud', endpoint: 'https://YOUR-ENDPOINT.upcloudobjects.com', region: '' },
    { id: 'other', name: 'Another S3-compatible store', endpoint: '', region: '' }
  ];
  // How the same chart is installed, and where Postgres comes from.
  var OUTPUTS = [
    { id: 'helm', name: 'Helm' },
    { id: 'yaml', name: 'Kubernetes YAML' }
  ];
  var PG_MODES = [
    { id: 'external', name: 'Existing Postgres' },
    { id: 'incluster', name: 'Install Postgres in this cluster' }
  ];
  // </setupBasics>

  // presetOf: v is empty or one of the list's own template values, so
  // picking another provider may replace it; anything the person typed stays.
  function presetOf(list, key, v) {
    return !v || list.some(function (p) { return p[key] === v; });
  }
  // pickIdp and pickBucket answer the provider questions, from the list or
  // the form: the template issuer, endpoint and region follow only
  // where the person has not typed their own. With UpCloud the Postgres port
  // becomes UpCloud's managed Postgres port, 11569, while it is still 5432
  // (and goes back when another provider is picked).
  function pickIdp(b, id) {
    b.idp = id;
    IDPS.forEach(function (p) { if (p.id === id && presetOf(IDPS, 'issuer', b.issuer)) b.issuer = p.issuer; });
  }
  function pickBucket(b, id) {
    b.bucket = id;
    BUCKETS.forEach(function (p) {
      if (p.id !== id) return;
      if (presetOf(BUCKETS, 'endpoint', b.endpoint)) b.endpoint = p.endpoint;
      if (presetOf(BUCKETS, 'region', b.region)) b.region = p.region;
    });
    if (id === 'upcloud' && b.dbPort === '5432') b.dbPort = '11569';
    else if (id !== 'upcloud' && b.dbPort === '11569') b.dbPort = '5432';
  }

  function idpOf(id) { return IDPS.filter(function (p) { return p.id === id; })[0] || {}; }
  function bucketOf(id) { return BUCKETS.filter(function (p) { return p.id === id; })[0] || {}; }

  var S = {
    product: 'small', mode: 'basic', step: 0, area: 0,
    data: {},
    values: { small: {}, ent: {} },      // NAME -> value, only where it differs from the default
    secrets: { small: {}, ent: {} },     // NAME -> true: list it as a blank to fill in
    basic: {
      small: { where: 'upcloud', domain: '', content: '', email: '', codes: true, google: false, mailFrom: '', googleId: '' },
      ent: { host: '', admins: '', idp: 'okta', issuer: IDPS[0].issuer, clientId: '', domains: '', certs: 'auto', issuerName: '',
        smtp: false, smtpFrom: '', bucket: 'other', endpoint: '', region: '', bucketName: '',
        creds: 'keys', postgresMode: 'external', dbHost: '', dbPort: '5432', dbName: 'simplehost', dbUser: 'simplehost',
        dbSize: '5Gi', dbStorageClass: '', proxies: '', output: 'helm', context: '', namespace: 'simple-host',
        ingressClass: '', tlsSecret: 'simple-host-tls' }
    },
    errors: {},
  };

  // ?product=enterprise or ?product=small-box (the links on the enterprise and
  // hosted pages) preselects the first choice.
  try {
    var want = new URLSearchParams(location.search).get('product');
    if (want === 'enterprise') S.product = 'ent';
    else if (want === 'small-box') S.product = 'small';
    // ?cloud= is ignored. Enterprise always targets a cluster that already exists.
  } catch (e) { /* keep the default */ }

  var app = document.getElementById('app');

  // ── Small DOM helpers ──
  function el(tag, attrs, kids) {
    var n = document.createElement(tag);
    if (attrs) {
      for (var k in attrs) {
        if (!Object.prototype.hasOwnProperty.call(attrs, k) || attrs[k] == null || attrs[k] === false) continue;
        if (k === 'text') n.textContent = attrs[k];
        else if (k.slice(0, 2) === 'on') n.addEventListener(k.slice(2), attrs[k]);
        else if (k === 'checked' || k === 'value' || k === 'disabled') n[k] = attrs[k];
        else n.setAttribute(k, attrs[k] === true ? '' : attrs[k]);
      }
    }
    (kids || []).forEach(function (c) {
      if (c == null || c === false) return;
      n.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    });
    return n;
  }
  function uid(s) { return 'f-' + s.replace(/[^A-Za-z0-9_-]/g, '_'); }

  // ── Values ──
  function settings() { return S.data[S.product].settings; }
  function byName(name) {
    var all = settings();
    for (var i = 0; i < all.length; i++) if (all[i].name === name) return all[i];
    return null;
  }
  function defaultOf(s) {
    if (S.product === 'small' && INSTALLER_DEFAULTS[s.name] != null) return INSTALLER_DEFAULTS[s.name];
    // "<...>" defaults are derived from another setting: empty means that.
    return s.default.charAt(0) === '<' ? '' : s.default;
  }
  function valueOf(s) {
    var v = S.values[S.product][s.name];
    return v == null ? defaultOf(s) : v;
  }
  function setValue(s, v) {
    if (v === defaultOf(s)) delete S.values[S.product][s.name];
    else S.values[S.product][s.name] = v;
  }

  // Go durations: 1h30m, 500ms, 1.25s. Returns milliseconds, or NaN.
  function parseDur(t) {
    var m = /^((\d+(\.\d+)?)(ns|us|ms|s|m|h))+$/.exec(t);
    if (!m) return NaN;
    var unit = { ns: 1e-6, us: 1e-3, ms: 1, s: 1e3, m: 6e4, h: 3.6e6 }, total = 0, re = /(\d+(?:\.\d+)?)(ns|us|ms|s|m|h)/g, p;
    while ((p = re.exec(t))) total += parseFloat(p[1]) * unit[p[2]];
    return total;
  }
  function rateParts(t) {
    var sep = S.product === 'small' ? ',' : '/';
    var i = t.indexOf(sep);
    if (i < 1) return null;
    var burst = t.slice(0, i).trim(), every = parseDur(t.slice(i + 1).trim());
    if (!/^\d+$/.test(burst) || isNaN(every)) return null;
    return { burst: parseInt(burst, 10), every: every };
  }
  function numeric(x) { return typeof x === 'number'; }

  // <setupKind> How a value is written and checked: number (also the small
  // box's _MINUTES/_DAYS durations), duration, rate, choice, or text (free
  // text and secrets).
  function setupKind(s) {
    if (s.type === 'int') return 'number';
    if (s.type === 'duration') return (numeric(s.min) || numeric(s.max) || /^\d+$/.test(s.default)) ? 'number' : 'duration';
    if (s.type === 'rate') return 'rate';
    if (s.type === 'bool' || s.type === 'enum') return 'choice';
    return 'text';
  }
  // </setupKind>

  // tidy writes a value the way the server reads it, where the typing only
  // differs in spacing or case: 1,000 or 1 000 as 1000, 8 H as 8h.
  function tidy(s, v) {
    var k = setupKind(s);
    if (k === 'number' && /^[\d\s,_]+$/.test(v)) return v.replace(/[\s,_]/g, '');
    if (k === 'duration') return v.replace(/\s+/g, '').toLowerCase();
    if (k === 'rate') return v.replace(/\s+/g, '').toLowerCase();
    return v;
  }
  // validate returns an error sentence, or '' when the value is acceptable.
  function validate(s, v) {
    var k = setupKind(s);
    if (INVISIBLE.test(v)) return INVISIBLE_MSG;
    if (/[\r\n]/.test(v)) return 'One line only.';
    if (k !== 'text' && /[^\x20-\x7e]/.test(v)) return 'Plain letters, digits and punctuation only.';
    if (k === 'number') {
      if (!/^\d+$/.test(v)) return 'A whole number.';
      var n = parseInt(v, 10);
      if (numeric(s.min) && n < s.min) return 'At least ' + s.min + '.';
      if (numeric(s.max) && n > s.max) return 'At most ' + s.max + '.';
    } else if (k === 'duration') {
      var d = parseDur(v);
      if (isNaN(d)) return 'A duration like 30m, 8h or 720h (no days: write days as hours).';
      if (s.min && d < parseDur(s.min)) return 'At least ' + s.min + '.';
      if (s.max && d > parseDur(s.max)) return 'At most ' + s.max + '.';
    } else if (k === 'rate') {
      var r = rateParts(v);
      if (!r) return 'Like ' + s.default + ': how many at once, then one more every interval.';
      if (r.burst < 1 || r.burst > 100000) return 'The first number is 1 to 100000.';
      if (s.loosest) {
        var l = rateParts(s.loosest);
        if (r.burst > l.burst || r.every < l.every) return 'This limit guards sign-in: it can be stricter, but no looser than ' + s.loosest + '.';
      }
    } else if (s.allowed && s.allowed.indexOf(v) < 0) {
      return 'One of ' + s.allowed.join(', ') + '.';
    }
    return '';
  }

  // ── Progress ──
  function renderProgress() {
    var lis = document.querySelectorAll('#progress li');
    var steps = S.mode === 'advanced' ? [0, 1, 2, 3] : [0, 1, 3];
    for (var i = 0; i < lis.length; i++) {
      lis[i].className = (i === S.step ? 'on' : (i < S.step ? 'done' : ''));
      lis[i].hidden = steps.indexOf(i) < 0;
    }
    var last = lis.length && lis[lis.length - 1].querySelector('.n');
    if (last) last.textContent = 'Your files';
  }

  function go(step) {
    S.step = step;
    render();
    window.scrollTo(0, 0);
    var h = app.querySelector('h2');
    if (h) { h.setAttribute('tabindex', '-1'); h.focus({ preventScroll: true }); }
  }

  function load(product) {
    if (S.data[product]) return Promise.resolve();
    return fetch(FILES[product], { credentials: 'omit' }).then(function (r) {
      if (!r.ok) throw new Error(r.status);
      return r.json();
    }).then(function (d) { S.data[product] = d; });
  }

  function render() {
    renderProgress();
    app.textContent = '';
    [renderChoose, renderBasics, renderAdvanced, renderOutput][S.step]();
  }

  // ── Step 1: what to set up ──
  function choice(name, value, current, title, sub, onpick) {
    return el('label', { class: 'choice' }, [
      el('input', { type: 'radio', name: name, value: value, checked: current === value, onchange: function () { onpick(value); } }),
      el('b', { text: title }), el('span', { text: sub })
    ]);
  }
  // redrawChoose answers a question on the first step that changes which
  // questions follow it: the step is drawn again with focus kept on the answer.
  function redrawChoose(name, set) {
    return function (v) {
      if (set) set(v); else S[name] = v;
      render();
      var r = app.querySelector('input[name="' + name + '"][value="' + v + '"]');
      if (r) r.focus();
    };
  }
  function renderChoose() {
    var err = el('p', { class: 'err', role: 'alert', hidden: true });
    app.appendChild(el('div', { class: 'card' }, [
      el('h2', { text: 'What are you setting up?' }),
      el('fieldset', null, [
        el('legend', { class: 'note', text: 'Choose one.' }),
        el('div', { class: 'choices' }, [
          choice('product', 'small', S.product, 'Small box', 'One server with Docker: self-hosting, a team or a hackathon. Runs on 1 CPU and 1 GB of RAM.', redrawChoose('product')),
          choice('product', 'ent', S.product, 'Enterprise', 'An existing Kubernetes cluster (1.30 or later), behind your own sign-in, with Postgres and a bucket you already have.', redrawChoose('product'))
        ])
      ])
    ]));
    if (S.product === 'ent') {
      var b = S.basic.ent;
      app.appendChild(el('div', { class: 'card', id: 'where' }, [
        el('h2', { text: 'How do you install it?' }),
        el('p', { class: 'lede', text: 'Enterprise installs into a Kubernetes cluster you already run (1.30 or later), with an ingress controller already there. Automatic certificates and in-cluster Postgres also need cert-manager. It does not create a cluster, a cloud account, or a control plane.' }),
        el('fieldset', null, [
          el('legend', { class: 'note', text: 'The same chart either way. Helm installs it, or Helm renders Kubernetes YAML you apply yourself.' }),
          el('div', { class: 'choices' }, OUTPUTS.map(function (x) {
            return choice('output', x.id, b.output, x.name,
              x.id === 'helm' ? 'Download a chart to inspect, dry-run and install with Helm.' : 'helm template writes simple-host.yaml, then kubectl apply.',
              redrawChoose('output', function (v) { b.output = v; }));
          }))
        ])
      ]));
    }
    app.appendChild(el('div', { class: 'card' }, [
      el('h2', { text: 'How much do you want to decide?' }),
      el('fieldset', null, [
        el('legend', { class: 'note', text: 'You can go back and change this.' }),
        el('div', { class: 'choices' }, [
          choice('mode', 'basic', S.mode, 'Basic', 'A few questions. Everything else keeps the default, which is what simple-host.app runs.', function (v) { S.mode = v; renderProgress(); }),
          choice('mode', 'advanced', S.mode, 'Advanced', 'The basics, then every setting, area by area, each with its default already chosen.', function (v) { S.mode = v; renderProgress(); })
        ])
      ])
    ]));
    app.appendChild(el('div', { class: 'nav' }, [el('span'), el('button', { class: 'btn solid', type: 'button', text: 'Next', onclick: function () {
      load(S.product).then(function () { go(1); }).catch(function () {
        err.hidden = false; err.textContent = 'The settings list did not load. Reload the page and try again.';
      });
    } })]));
    app.appendChild(err);
  }

  // ── Step 2: the basics ──
  function textField(key, obj, label, opts) {
    opts = opts || {};
    var id = uid(key);
    var input = el('input', { id: id, type: opts.type || 'text', value: obj[key], placeholder: opts.placeholder || '', autocomplete: 'off', spellcheck: 'false',
      'aria-describedby': opts.help ? id + '-h' : null,
      oninput: function () { obj[key] = input.value.trim(); if (opts.oninput) opts.oninput(); } });
    if (S.errors[key]) input.className = 'bad';
    return el('div', { class: 'field' }, [
      el('label', { for: id }, [label, opts.name ? el('span', { class: 'name', text: opts.name }) : null]),
      opts.help ? el('p', { class: 'help', id: id + '-h', text: opts.help }) : null,
      input,
      S.errors[key] ? el('p', { class: 'err', text: S.errors[key] }) : null
    ]);
  }
  function radios(key, obj, label, options, onchange) {
    return el('div', { class: 'field' }, [
      el('fieldset', null, [
        el('legend', { class: 'label', text: label }),
        el('div', { class: 'opts' }, options.map(function (o) {
          return el('label', { class: 'opt' }, [
            el('input', { type: 'radio', name: uid(key), value: o[0], checked: String(obj[key]) === o[0], onchange: function () {
              obj[key] = o[0] === 'true' ? true : o[0] === 'false' ? false : o[0];
              if (onchange) onchange();
            } }), o[1]
          ]);
        }))
      ])
    ]);
  }
  function secretNote(name, text) {
    return el('div', { class: 'field' }, [
      el('div', { class: 'label' }, [text, el('span', { class: 'name', text: name })]),
      el('p', { class: 'secret', text: 'A secret: this page leaves it blank in the file for you to fill in on your side.' })
    ]);
  }
  // The checks below catch an obvious typo and anything that could break a
  // generated file or command (spaces, quotes, backslashes); every form a
  // real install uses passes. HOST is any hostname, lower-case: one label
  // (postgres), a Kubernetes service (pg-rw.db, postgres.db.svc.cluster.local),
  // an internal or public name, or an IPv4 address.
  var LABEL = '[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?';
  var HOST = new RegExp('^(?=.{1,253}$)' + LABEL + '(\\.' + LABEL + ')*$');
  // PUBLIC_HOST is a name on the internet, as Let's Encrypt takes it (the small box).
  var PUBLIC_HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$/;
  var IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
  // An address: anything@anything, without spaces, commas, quotes, brackets
  // backslashes or shell characters; the domain may be internal (platform@corp).
  var EMAIL = /^[^\s@,;"\\<>()\[\]`${}|&]+@[^\s@,;"'\\<>()\[\]`${}|&]+$/;
  // A proxy address or range for TRUSTED_PROXY_CIDRS (the server checks it exactly at start).
  var CIDR = /^(\d{1,3}(\.\d{1,3}){3}(\/([0-9]|[12][0-9]|3[0-2]))?|[0-9A-Fa-f]*:[0-9A-Fa-f:.]*(\/([0-9]{1,2}|1[01][0-9]|12[0-8]))?)$/;
  // hostPort reads a host as people write it: host, host:port, an IPv4 or
  // IPv6 address ([fd00::1]:5432 or plain fd00::1), with a scheme, a path or
  // a trailing dot dropped. Returns { host, port, ip, v6 }, or null when it
  // cannot be a host.
  function hostPort(v) {
    v = String(v).trim().toLowerCase().replace(/^[a-z][a-z0-9+.-]*:\/\//, '').replace(/[\/?#].*$/, '');
    var m = /^\[([0-9a-f:.]+)\](?::(\d{1,5}))?$/.exec(v);
    if (!m && /^[0-9a-f:.]+$/.test(v) && (v.match(/:/g) || []).length >= 2) m = [v, v, ''];
    if (m) return /::.*::|:::/.test(m[1]) || m[1].indexOf(':') < 0 ? null : { host: m[1], port: m[2] || '', ip: true, v6: true };
    m = /^([^:]+)(?::(\d{1,5}))?$/.exec(v);
    if (!m) return null;
    var h = m[1].replace(/\.$/, '');
    if (!HOST.test(h) || (m[2] && (+m[2] < 1 || +m[2] > 65535))) return null;
    return { host: h, port: m[2] || '', ip: IPV4.test(h), v6: false };
  }
  // INVISIBLE: a control character or an invisible one (zero width, direction
  // marks, byte order mark: any Unicode format character), usually pasted
  // along with the text. Refused in every field.
  var INVISIBLE = /[\p{Cc}\p{Cf}]/u;
  var INVISIBLE_MSG = 'It has an invisible character in it (such as a zero-width space, often pasted with the text): type it again.';
  // splitList reads a list typed with commas, semicolons or spaces.
  function splitList(v) { return String(v).split(/[\s,;]+/).map(function (x) { return x.trim(); }).filter(Boolean); }
  function emailsOk(v) { return splitList(v).every(function (a) { return EMAIL.test(a); }); }
  // A company email domain: a hostname, with a leading @ dropped.
  function domainList(v) { return splitList(v).map(function (d) { return d.toLowerCase().replace(/^@/, '').replace(/\.$/, ''); }); }
  // sh quotes a value for a shell command: bare when it has only characters
  // no shell treats specially, else in single quotes.
  function sh(v) {
    v = String(v);
    return /^[A-Za-z0-9@%+=:,.\/_-]+$/.test(v) ? v : "'" + v.replace(/'/g, "'\\''") + "'";
  }

  // costNote is what it costs to run, for the cloud the bucket is on: the
  // /costs calculator's estimate at its default sizes (costs/calc.js with
  // costs/prices.json), with a link to put in your own numbers.
  var COST_CLOUDS = { aws: 'aws', gcs: 'gcp', upcloud: 'upcloud' };
  var costPrices = null;
  function costNote(bucket) {
    var id = COST_CLOUDS[bucket], p = el('p', { class: 'note' });
    var link = function (text) { return el('a', { href: '/costs' + (id && id !== 'aws' ? '?provider=' + id : ''), text: text }); };
    var plain = function () { p.textContent = ''; p.appendChild(document.createTextNode('What it costs to run on AWS, Azure or Google Cloud: ')); p.appendChild(link('the cost calculator')); p.appendChild(document.createTextNode('.')); };
    plain();
    if (!id || !window.SHCosts) return p;
    var show = function () {
      try {
        var e = window.SHCosts.estimate(costPrices, id, {}), d = costPrices.model.defaults;
        p.textContent = '';
        p.appendChild(document.createTextNode('Running cost on ' + e.provider.name + ': about $' + e.total.toFixed(2) + ' a month for ' + d.people + ' people on a cluster you already have (list prices, checked ' + costPrices.checked + '). '));
        p.appendChild(link('Put in your own numbers'));
        p.appendChild(document.createTextNode('.'));
      } catch (err) { plain(); }
    };
    if (costPrices) show();
    else fetch('/costs/prices.json', { credentials: 'omit' }).then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d) { costPrices = d; show(); } }).catch(function () {});
    return p;
  }

  // upcloudOffer is the recommendation: why, the sign-up button (a referral
  // link, and the page says so) and what to do there.
  function upcloudOffer() {
    return el('div', { class: 'upcloud' }, [
      el('p', { class: 'note', style: 'margin:0 0 12px', text: 'The smallest UpCloud server (1 CPU, 1 GB, about $4/month) runs Simple Host comfortably; we test on it.' }),
      el('a', { class: 'btn solid cta', href: UPCLOUD_SIGNUP, target: '_blank', rel: 'noopener', text: 'Create your UpCloud account — $25 in credits' }),
      el('p', { class: 'fine', text: 'Referral link. New accounts through this link get $25 of UpCloud credit; their terms apply.' }),
      el('ol', { class: 'steps' }, [
        el('li', { text: 'Create your UpCloud account.' }),
        el('li', { text: 'In the UpCloud control panel, create an API token (Account → API tokens; recommended) or an API user (a sub-account with API access allowed). ' + UPCLOUD_LIMIT + ' It stays with you; this page never asks for it.' }),
        el('li', { text: 'Answer the questions on this page.' }),
        el('li', { text: 'On the last step, set the token (or the API user) in your terminal (the page gives the command) and copy the prompt into your AI agent. It creates the server, sets it up and checks it.' })
      ])
    ]);
  }
  function renderWhere(b) {
    var more = el('div');
    var draw = function () {
      more.textContent = '';
      more.appendChild(b.where === 'upcloud' ? upcloudOffer()
        : el('p', { class: 'note', text: 'A fresh Ubuntu server (24.04 LTS) with 1 CPU, 1 GB of RAM and about 25 GB of disk, a public IPv4 address, ports 80 and 443 open, and SSH with sudo.' }));
    };
    draw();
    return el('div', { class: 'card', id: 'where' }, [
      el('h2', { text: 'Where it runs' }),
      el('fieldset', null, [
        el('legend', { class: 'note', text: 'Your own server, at a cloud provider or anywhere with a public address.' }),
        el('div', { class: 'choices' }, [
          choice('where', 'upcloud', b.where, 'UpCloud (recommended)', 'A new server there, created by your AI agent with your UpCloud API token or API user.', function (v) { b.where = v; draw(); }),
          choice('where', 'server', b.where, 'A server I already have', 'Any fresh Ubuntu server you can SSH into.', function (v) { b.where = v; draw(); })
        ])
      ]),
      el('div', { style: 'height:14px' }),
      more
    ]);
  }

  // The quick path's questions: what cannot be found out or defaulted.
  var ISSUER_HELP = 'Okta: https://<your-org>.okta.com · Microsoft Entra ID: https://login.microsoftonline.com/<tenant-id>/v2.0 · Google Workspace: https://accounts.google.com · Keycloak: https://<host>/realms/<realm>';
  // normIssuer writes an issuer URL the one way the server compares it:
  // lower-case host, no :443, no trailing slash.
  function normIssuer(u) {
    try {
      var x = new URL(u);
      if (x.protocol !== 'https:' || x.search || x.hash || x.username || x.password) return u;
      return 'https://' + x.hostname.toLowerCase() + (x.port && x.port !== '443' ? ':' + x.port : '') + x.pathname.replace(/\/+$/, '');
    } catch (e) { return u; }
  }
  // Google's host in any spelling (case, a trailing dot, a port, a path)
  // counts as Google, so company email domains are always asked for it.
  function isGoogle(issuer) {
    try { return new URL(issuer).hostname.toLowerCase().replace(/\.$/, '') === 'accounts.google.com'; }
    catch (e) { return false; }
  }
  // An https URL with no spaces, quotes or backslashes, at most 2048 characters.
  var ISSUER = /^https:\/\/[^\s\/"\\?#@]+(\/[^\s"\\?#]*)?$/i;
  function renderBasics() {
    var box = el('div', { class: 'card' });
    var b = S.basic[S.product];
    if (S.product === 'small') {
      app.appendChild(renderWhere(b));
      box.appendChild(el('h2', { text: 'Your small box' }));
      box.appendChild(el('p', { class: 'lede', text: 'Two names pointing at your server: one for Simple Host itself, one for the sites people publish. Keeping them apart means a published page can never reach the dashboard. No domain yet? Leave it empty: the server asks for it after the install.' }));
      box.appendChild(textField('domain', b, 'Domain', { name: 'SITE_DOMAIN · --host', placeholder: 'hack.example.com', help: byName('SITE_DOMAIN').description,
        oninput: function () { var c = document.getElementById(uid('content')); if (c && !b.contentTouched) { c.placeholder = b.domain ? 'sites.' + b.domain : 'sites.hack.example.com'; } } }));
      box.appendChild(textField('content', b, 'Sites hostname', { name: 'CONTENT_HOST · --content', placeholder: b.domain ? 'sites.' + b.domain : 'sites.hack.example.com',
        help: 'Leave empty for sites.<domain>. ' + byName('CONTENT_HOST').description, oninput: function () { b.contentTouched = !!b.content; } }));
      box.appendChild(textField('email', b, 'Your email, for certificate notices (optional)', { name: '--email', type: 'email', placeholder: 'you@example.com',
        help: 'Let’s Encrypt writes here if a certificate is about to expire.' }));
      var signin = el('div');
      var drawSignin = function () {
        signin.textContent = '';
        signin.appendChild(radios('codes', b, 'Sign-in with an emailed code', [['true', 'Yes, send codes by email'], ['false', 'No']], drawSignin));
        if (b.codes) {
          signin.appendChild(secretNote('RESEND_API_KEY', 'Resend API key'));
          signin.appendChild(el('p', { class: 'note', text: byName('RESEND_API_KEY').description + ' Create one at resend.com after verifying your domain there.' }));
          signin.appendChild(el('div', { style: 'height:14px' }));
          signin.appendChild(textField('mailFrom', b, 'Send email from', { name: 'MAIL_FROM', placeholder: 'Simple Host <noreply@' + (b.domain || 'hack.example.com') + '>', help: byName('MAIL_FROM').description }));
        }
        signin.appendChild(radios('google', b, 'Sign-in with Google', [['true', 'Yes'], ['false', 'No']], drawSignin));
        if (b.google) {
          signin.appendChild(el('p', { class: 'note' }, ['In Google Cloud, create an OAuth client (Web application) with the redirect URI ',
            el('code', { text: 'https://' + (b.domain || '<domain>') + '/v1/auth/oauth/google/callback' }), '.']));
          signin.appendChild(el('div', { style: 'height:14px' }));
          signin.appendChild(textField('googleId', b, 'Google client ID (optional here)', { name: 'GOOGLE_OAUTH_CLIENT_ID', placeholder: '1234-abc.apps.googleusercontent.com',
            help: 'Not a secret. Leave it empty to fill it in later.' }));
          signin.appendChild(secretNote('GOOGLE_OAUTH_CLIENT_SECRET', 'Google client secret'));
        }
        if (!b.codes && !b.google) {
          signin.appendChild(el('p', { class: 'note', text: 'With neither, only the admin key (the installer prints it; it is kept in /opt/simple-host/.env on the server) can sign in, at /admin. People you hand keys to can still publish.' }));
        }
      };
      drawSignin();
      box.appendChild(el('h3', { text: 'Sign-in and email', style: 'margin-top:26px' }));
      box.appendChild(signin);
    } else {
      box.appendChild(el('h2', { text: 'Your Enterprise install' }));
      box.appendChild(el('p', { class: 'lede', text: 'The values for your existing Kubernetes cluster. Leave any of them empty: it keeps its default, or is left as a marked blank in the file for you to fill in later. Secrets (the client secret, database passwords, keys) are always blanks in secrets.env.' }));
      box.appendChild(textField('context', b, 'kubectl context (optional)', { help: 'An existing cluster in your kubeconfig. Empty uses your current context when you run the commands.' }));
      box.appendChild(textField('namespace', b, 'Namespace', { placeholder: 'simple-host' }));
      box.appendChild(textField('host', b, 'Address', { name: 'PUBLIC_BASE_URL', placeholder: 'sites.example.com', help: byName('PUBLIC_BASE_URL').description }));
      box.appendChild(textField('admins', b, 'Admin emails', { name: 'ADMIN_EMAILS', placeholder: 'platform@example.com, alex@example.com', help: byName('ADMIN_EMAILS').description }));
      box.appendChild(el('h3', { text: 'Sign-in (OIDC)', style: 'margin-top:26px' }));
      var idpSel = el('select', { id: uid('idp'), onchange: function () {
        pickIdp(b, idpSel.value);
        render();
      } }, IDPS.map(function (p) { return el('option', { value: p.id, text: p.name }); }));
      idpSel.value = b.idp;
      box.appendChild(el('div', { class: 'field' }, [el('label', { for: uid('idp'), text: 'Identity provider' }), idpSel]));
      box.appendChild(textField('issuer', b, 'Issuer URL', { name: 'OIDC_ISSUER', placeholder: 'https://login.example.com', help: byName('OIDC_ISSUER').description }));
      box.appendChild(textField('clientId', b, 'Client ID', { name: 'OIDC_CLIENT_ID', help: 'Register a web application with the redirect URI https://' + (b.host || '<address>') + '/auth/callback.' }));
      box.appendChild(secretNote('OIDC_CLIENT_SECRET', 'Client secret'));
      box.appendChild(textField('domains', b, 'Company email domains' + (b.idp === 'google' ? '' : ' (optional)'), { name: 'ALLOWED_EMAIL_DOMAINS', placeholder: 'example.com', help: byName('ALLOWED_EMAIL_DOMAINS').description }));
      box.appendChild(el('h3', { text: 'Site certificates', style: 'margin-top:26px' }));
      box.appendChild(radios('certs', b, 'Each owner’s *.<owner>.<address> certificate', [['auto', 'cert-manager issues them'], ['manual', 'I issue them myself']], render));
      if (b.certs === 'auto') box.appendChild(textField('issuerName', b, 'cert-manager ClusterIssuer', { name: 'OWNER_CERT_ISSUER', placeholder: 'internal-ca', help: byName('OWNER_CERT_ISSUER').description }));
      box.appendChild(el('h3', { text: 'Email', style: 'margin-top:26px' }));
      box.appendChild(radios('smtp', b, 'Email owners about sites nobody uses', [['false', 'No email'], ['true', 'Through our SMTP relay']], render));
      if (b.smtp) {
        box.appendChild(secretNote('SMTP_URL', 'SMTP relay URL'));
        box.appendChild(textField('smtpFrom', b, 'Send email from', { name: 'SMTP_FROM', placeholder: 'Simple Host <hosting@example.com>', help: byName('SMTP_FROM').description }));
      }
      box.appendChild(el('h3', { text: 'Where data lives', style: 'margin-top:26px' }));
      box.appendChild(costNote(b.bucket));
      var bSel = el('select', { id: uid('bucket'), onchange: function () {
        pickBucket(b, bSel.value);
        render();
      } }, BUCKETS.map(function (p) { return el('option', { value: p.id, text: p.name }); }));
      bSel.value = b.bucket;
      box.appendChild(el('div', { class: 'field' }, [el('label', { for: uid('bucket'), text: 'Bucket provider' }), bSel]));
      box.appendChild(textField('endpoint', b, 'Bucket endpoint', { name: 'BACKUP_STORAGE_ENDPOINT', help: byName('BACKUP_STORAGE_ENDPOINT').description }));
      var upcloud = b.bucket === 'upcloud';
      box.appendChild(textField('region', b, 'Region', { name: 'BACKUP_STORAGE_REGION', placeholder: upcloud ? 'europe-2' : 'us-east-1',
        help: upcloud ? 'The Object Storage service’s region, as in its endpoint (such as europe-2).' : 'The region the bucket lives in.' }));
      box.appendChild(textField('bucketName', b, 'Bucket name', { name: 'BACKUP_STORAGE_BUCKET', placeholder: 'simple-host-sites', help: byName('BACKUP_STORAGE_BUCKET').description }));
      box.appendChild(radios('creds', b, 'Bucket credentials', [['keys', 'Access keys (in secrets.env)'], ['identity', 'Existing AWS-compatible workload identity']], render));
      if (b.creds === 'identity') box.appendChild(el('p', { class: 'note', text: 'Use an identity already configured for the AWS SDK credential chain. Add its enterprise.serviceAccount.annotations and enterprise.podLabels or enterprise.podAnnotations to values.yaml as your platform requires. Native Azure and GCS identities are not supported; use S3/HMAC keys for those stores.' }));
      box.appendChild(el('h3', { text: 'Postgres', style: 'margin-top:26px' }));
      box.appendChild(el('div', { class: 'choices' }, PG_MODES.map(function (p) {
        return choice('postgresMode', p.id, b.postgresMode, p.name, p.id === 'external' ? 'Connect over verified TLS using your database CA.' : 'A persistent Postgres for evaluation; arrange backups before production use.', function (v) { b.postgresMode = v; render(); });
      })));
      if (b.postgresMode === 'external') {
      box.appendChild(textField('dbHost', b, 'Postgres host', { name: 'DB_HOST', placeholder: upcloud ? 'public-….db.upclouddatabases.com' : 'postgres.db.svc.cluster.local',
        help: 'Any hostname, service name or IP address, with :port if it is not 5432 ([fd00::1]:5432 for IPv6). A managed Postgres with point-in-time recovery; nothing in the package backs up the database.' +
          (upcloud ? ' On UpCloud’s managed Postgres, use the public-… hostname (the component whose route is public): the plain one resolves to a private address from outside UpCloud.' : '') }));
      box.appendChild(textField('dbPort', b, 'Postgres port', { name: 'DB_PORT', type: 'number', placeholder: '5432',
        help: upcloud ? 'UpCloud’s managed Postgres listens on 11569, not 5432 (filled in when you picked UpCloud).' : null }));
      } else {
        box.appendChild(el('p', { class: 'note', text: 'The chart installs one Postgres with TLS from cert-manager and a persistent volume. cert-manager and a working StorageClass must already exist. It does not configure database backups or high availability.' }));
        box.appendChild(textField('dbSize', b, 'Database volume size', { placeholder: '5Gi' }));
        box.appendChild(textField('dbStorageClass', b, 'StorageClass (optional)', { help: 'Empty uses the cluster default.' }));
      }
      box.appendChild(textField('dbName', b, 'Database name', { name: 'DB_NAME', placeholder: 'simplehost' }));
      box.appendChild(textField('dbUser', b, 'Owning role', { name: 'DB_USER', placeholder: 'simplehost', help: byName('DB_USER').description }));
      box.appendChild(el('h3', { text: 'Ingress', style: 'margin-top:26px' }));
      box.appendChild(textField('ingressClass', b, 'IngressClass (optional)', { help: 'Empty uses the cluster default. The ingress controller must already exist.' }));
      box.appendChild(textField('tlsSecret', b, 'Dashboard TLS Secret', { placeholder: 'simple-host-tls', help: b.certs === 'auto' ? 'cert-manager writes the certificate here using your existing ClusterIssuer.' : 'An existing Secret for the address and its wildcard. You also manage each owner’s wildcard certificate and ingress route.' }));
      box.appendChild(textField('proxies', b, 'Ingress controller’s pod range (optional)', { name: 'TRUSTED_PROXY_CIDRS', placeholder: byName('TRUSTED_PROXY_CIDRS').default,
        help: 'The addresses your ingress controller’s pods get, so rate limits and logs see each person’s address, not the ingress’s. ' +
          'Read them with kubectl -n <ingress namespace> get pod -o wide and give the pod network range they fall in, like 192.168.0.0/16' +
          (upcloud ? ' (UpCloud’s Kubernetes gives pods addresses from 192.168.0.0/16)' : '') +
          (b.bucket === 'aws' ? ' (with an AWS ALB there are no proxy pods: give the VPC’s CIDR)' : '') +
          '. Empty keeps the default, every private range, which trusts any pod in the cluster to name the client.' }));
    }
    app.appendChild(box);
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () { S.errors = {}; go(0); } }),
      el('button', { class: 'btn solid', type: 'button', text: S.mode === 'advanced' ? 'Next: every setting' : 'Show my files', onclick: function () {
        if (!checkBasics()) { render(); var bad = app.querySelector('.bad'); if (bad) bad.focus(); return; }
        S.area = 0;
        go(S.mode === 'advanced' ? 2 : 3);
      } })
    ]));
  }

  // checkBasics tidies the basic answers and checks the ones given. Nothing
  // is required: an empty answer is its default, a marked blank in the files
  // or a value to fill in before installing. What is refused is
  // an obvious typo, or anything that could break the files or the command.
  function checkBasics() {
    var b = S.basic[S.product], e = {}, h;
    var invisible = Object.keys(b).filter(function (k) { return typeof b[k] === 'string' && INVISIBLE.test(b[k]); });
    // An issuer typed without https:// gets it; a template (YOUR-…) is
    // not an answer, so it is left empty.
    var issuer = function () {
      if (/YOUR-/.test(b.issuer) && presetOf(IDPS, 'issuer', b.issuer)) b.issuer = '';
      if (!b.issuer) return;
      if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(b.issuer)) b.issuer = 'https://' + b.issuer;
      // The typed value is checked as typed; only a value that passes is
      // normalised (host case, :443, trailing slashes).
      if (!ISSUER.test(b.issuer) || b.issuer.length > 2048) e.issuer = 'An https:// URL, with no spaces.';
      else {
        b.issuer = normIssuer(b.issuer);
        if (/YOUR-/.test(b.issuer)) e.issuer = 'Replace the YOUR-… part with yours.';
        else if (isGoogle(b.issuer) && b.issuer !== 'https://accounts.google.com') e.issuer = 'Google’s issuer is exactly https://accounts.google.com.';
        else if (/^https:\/\/login\.microsoftonline\.com\/(common|organizations|consumers)(\/|$)/i.test(b.issuer)) e.issuer = 'Use your own tenant’s issuer: https://login.microsoftonline.com/<tenant-id>/v2.0.';
      }
    };
    var clientId = function () {
      if (b.clientId && (/[\s"\\]/.test(b.clientId) || b.clientId.length > 512)) e.clientId = 'The client ID as your identity provider shows it.';
    };
    var admins = function () {
      b.admins = splitList(b.admins).join(', ');
      if (!emailsOk(b.admins)) e.admins = 'Email addresses separated by commas.';
    };
    var domains = function () {
      b.domains = domainList(b.domains).join(',');
      if (!domainList(b.domains).every(function (d) { return HOST.test(d); })) e.domains = 'Domains like example.com, separated by commas.';
    };
    if (S.product === 'small') {
      // A public name: Let's Encrypt issues its certificates.
      var pub = function (key, what) {
        h = b[key] ? hostPort(b[key]) : null;
        if (!b[key]) return;
        if (!h) e[key] = 'A ' + what + ' like hack.example.com.';
        else if (h.ip) e[key] = 'A name, not an IP address: point the name at the server’s address.';
        else if (h.port) e[key] = 'Without a port: the box answers on 80 and 443.';
        else if (!PUBLIC_HOST.test(h.host)) e[key] = 'A ' + what + ' like hack.example.com.';
        else b[key] = h.host;
      };
      pub('domain', 'domain name');
      pub('content', 'hostname');
      if (!e.content && b.content && !b.domain) e.content = 'The sites hostname goes with a domain: fill in the domain too, or leave this empty (the server asks for both after the install).';
      if (!e.content && b.content && b.content === b.domain) e.content = 'A different hostname, like sites.' + b.domain + '.';
      if (b.email && !EMAIL.test(b.email)) e.email = 'An email address, or leave it empty.';
      if (b.mailFrom && /[\r\n`]/.test(b.mailFrom)) e.mailFrom = 'One line, with no backticks.';
      if (b.googleId && !/^[A-Za-z0-9._-]+$/.test(b.googleId)) e.googleId = 'The client ID as Google shows it.';
    } else {
      // An internal DNS name is fine; Kubernetes Ingress hosts have no port.
      h = b.host ? hostPort(b.host) : null;
      if (b.host) {
        if (h && !h.ip && (!h.port || h.port === '443')) b.host = h.host;
        else if (h && h.port) e.host = 'Without a port: the ingress serves HTTPS on 443.';
        else e.host = h ? 'A name, not an IP address: everyone gets a name under it.' : 'A hostname like sites.example.com.';
      }
      issuer();
      clientId();
      admins();
      domains();
      b.issuerName = b.issuerName.toLowerCase();
      if (b.certs === 'auto' && b.issuerName && !/^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(b.issuerName)) e.issuerName = 'The ClusterIssuer’s name, like internal-ca.';
      if (b.smtp && b.smtpFrom && !EMAIL.test((b.smtpFrom.match(/<([^>]+)>\s*$/) || [0, b.smtpFrom])[1].trim())) e.smtpFrom = 'An address, like Simple Host <hosting@example.com>.';
      if (b.smtp && /[\r\n`]/.test(b.smtpFrom)) e.smtpFrom = 'One line, with no backticks.';
      if (/YOUR-/.test(b.endpoint) && presetOf(BUCKETS, 'endpoint', b.endpoint)) b.endpoint = '';
      if (b.endpoint && !/^[a-z][a-z0-9+.-]*:\/\//i.test(b.endpoint)) b.endpoint = 'https://' + b.endpoint;
      if (b.endpoint && !/^https:\/\/[^\s"'\\`]+$/i.test(b.endpoint)) e.endpoint = /^http:/i.test(b.endpoint) ? 'An https:// endpoint.' : 'The bucket’s https:// endpoint, with no spaces.';
      else if (/YOUR-/.test(b.endpoint)) e.endpoint = 'Replace the YOUR-… part with yours.';
      if (b.bucketName && !/^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/.test(b.bucketName)) e.bucketName = 'A bucket name: letters, digits, dots, - and _.';
      if (b.region && !/^[A-Za-z0-9._-]{1,64}$/.test(b.region)) e.region = 'The region, like us-east-1.';
      // The Postgres host as people write it; a port in it is the port.
      h = b.dbHost ? hostPort(b.dbHost) : null;
      if (b.postgresMode === 'external' && b.dbHost) {
        if (/@/.test(b.dbHost)) e.dbHost = 'The hostname only: leave the user and password out (the password goes in secrets.env).';
        else if (/^[0-9a-f:.]*:[0-9a-f]*:[0-9a-f:.]*:\d{4,5}$/i.test(b.dbHost.trim()) && !/^\[/.test(b.dbHost.trim())) e.dbHost = 'Put an IPv6 address in brackets, with the port after: [fd00::1]:5432.';
        else if (!h) e.dbHost = 'The hostname only (with :port if it is not 5432), like postgres.db.svc.cluster.local, 10.0.0.5 or [fd00::1]:5432.';
        else { b.dbHost = h.v6 ? '[' + h.host + ']' : h.host; if (h.port) b.dbPort = h.port; }
      }
      b.dbPort = String(b.dbPort == null ? '' : b.dbPort).trim();
      if (b.postgresMode === 'external' && b.dbPort && (!/^\d+$/.test(b.dbPort) || +b.dbPort < 1 || +b.dbPort > 65535)) e.dbPort = '1 to 65535.';
      if (b.dbName && !/^[A-Za-z0-9_.-]{1,63}$/.test(b.dbName)) e.dbName = 'Letters, digits, _ and -.';
      if (b.dbUser && !/^[A-Za-z0-9_.-]{1,63}$/.test(b.dbUser)) e.dbUser = 'Letters, digits, _ and -.';
      if (b.namespace && !/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(b.namespace)) e.namespace = 'A namespace: lower-case letters, digits and hyphens, up to 63 characters.';
      ['ingressClass', 'tlsSecret', 'dbStorageClass'].forEach(function (k) {
        if (b[k] && (b[k].length > 253 || !b[k].split('.').every(function (v) { return /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(v); }))) e[k] = 'A Kubernetes name: lower-case letters, digits, dots and hyphens.';
      });
      if (/[\r\n]/.test(b.context)) e.context = 'One context name on one line.';
      if (b.postgresMode === 'incluster' && b.dbSize && !/^[1-9][0-9]*(Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$/.test(b.dbSize)) e.dbSize = 'A positive storage size, like 5Gi.';
      b.proxies = splitList(b.proxies).join(',');
      if (b.proxies && !b.proxies.split(',').every(function (x) { return CIDR.test(x); })) e.proxies = 'Ranges like 192.168.0.0/16, separated by commas.';
    }
    // Any typed answer with an invisible character in it, as typed (before
    // tidying could drop it), whatever else.
    invisible.forEach(function (k) { e[k] = INVISIBLE_MSG; });
    S.errors = e;
    return Object.keys(e).length === 0;
  }

  // ── Step 3: every setting, area by area ──
  // These settings are owned by the chart's Kubernetes resources.
  var K8S_FIXED = ['PORT', 'HTTPS_REDIRECT_PORT', 'OWNER_INGRESS_TEMPLATE', 'DB_SSLMODE', 'DB_INSECURE_ALLOWED', 'BACKUP_STORAGE_INSECURE_ALLOWED', 'OIDC_INSECURE_ALLOWED', 'DB_INCLUSTER_EVALUATION'];
  function advancedSettings() {
    var basic = S.product === 'small' ? SMALL_BASIC : ENT_BASIC;
    return settings().filter(function (s) {
      if (basic.indexOf(s.name) >= 0) return false;
      if (S.product === 'small' && !s.small_box) return false;
      if (S.product === 'ent' && K8S_FIXED.indexOf(s.name) >= 0) return false;
      return true;
    });
  }
  function areas() {
    var list = advancedSettings();
    return S.data[S.product].groups.map(function (g) {
      return { group: g, settings: list.filter(function (s) { return s.group === g.id; }) };
    }).filter(function (a) { return a.settings.length > 0; });
  }

  function settingField(s) {
    var id = uid(s.name), errBox = el('p', { class: 'err', hidden: true, id: id + '-e' });
    var head = el('div', { class: 'label' }, [el('span', { text: s.name }),
      s.security_sensitive ? el('span', { class: 'tag', text: 'security' }) : null]);
    var help = el('p', { class: 'help', text: s.description });
    var def = defaultOf(s);
    var control;
    if (s.type === 'secret') {
      var req = s.required;
      control = el('label', { class: 'opt' }, [el('input', { type: 'checkbox', checked: req || !!S.secrets[S.product][s.name], disabled: req,
        onchange: function (ev) { if (ev.target.checked) S.secrets[S.product][s.name] = true; else delete S.secrets[S.product][s.name]; } }),
        req ? 'Required: listed in the file as a blank' : 'List it in the file as a blank to fill in']);
      return el('div', { class: 'field' }, [head, help, control]);
    }
    if (s.allowed && s.allowed.length <= 6) {
      var opts = el('div', { class: 'opts', role: 'radiogroup', 'aria-labelledby': id + '-l' });
      s.allowed.forEach(function (a) {
        opts.appendChild(el('label', { class: 'opt' }, [el('input', { type: 'radio', name: id, value: a, checked: valueOf(s) === a,
          onchange: function () { setValue(s, a); } }), a + (a === def ? ' (default)' : '')]));
      });
      head.id = id + '-l';
      return el('div', { class: 'field' }, [head, help, opts]);
    }
    var numberLike = setupKind(s) === 'number';
    var input = el('input', { id: id, type: 'text', inputmode: numberLike ? 'numeric' : null, value: valueOf(s), placeholder: s.default || '(empty)',
      autocomplete: 'off', spellcheck: 'false', 'aria-describedby': id + '-h ' + id + '-e' });
    help.id = id + '-h';
    var check = function () {
      // Empty keeps the default, as an unset variable does.
      var v = tidy(s, input.value.trim());
      var msg = v === '' ? '' : validate(s, v);
      if (v === '') v = def;
      errBox.hidden = !msg; errBox.textContent = msg; input.className = msg ? 'bad' : '';
      if (!msg) setValue(s, v); else delete S.values[S.product][s.name];
      input.dataset.bad = msg ? '1' : '';
      return !msg;
    };
    input.addEventListener('input', check);
    var skip = el('button', { class: 'skip', type: 'button', text: 'Skip', title: 'Keep the default', onclick: function () {
      input.value = def; check(); } });
    head = el('label', { for: id, class: 'label' }, [el('span', { text: s.name }), s.security_sensitive ? el('span', { class: 'tag', text: 'security' }) : null]);
    var rangeText = rangeOf(s);
    if (rangeText) help.textContent += ' ' + rangeText;
    return el('div', { class: 'field' }, [head, help, el('div', { class: 'row' }, [input, skip]), errBox]);
  }
  function rangeOf(s) {
    if (s.type === 'rate') return s.loosest ? 'Stricter freely; loosest ' + s.loosest + '.' : '';
    var u = s.unit && s.unit !== 'Go duration' ? ' ' + s.unit : '';
    if (s.min != null && s.max != null) return 'Allowed: ' + s.min + ' to ' + s.max + u + '.';
    if (s.min != null) return 'Allowed: at least ' + s.min + u + '.';
    if (s.max != null) return 'Allowed: at most ' + s.max + u + '.';
    return u ? 'In' + u + '.' : '';
  }

  function renderAdvanced() {
    var list = areas(), a = list[S.area];
    var card = el('div', { class: 'card' }, [
      el('div', { class: 'area-head' }, [el('h2', { text: a.group.name }), el('span', { class: 'count', text: 'Area ' + (S.area + 1) + ' of ' + list.length })]),
      el('p', { class: 'note', style: 'margin:0 0 18px', text: 'Each setting starts at its default. Change what you need; Skip puts the default back.' })
    ]);
    a.settings.forEach(function (s) { card.appendChild(settingField(s)); });
    app.appendChild(card);
    var bad = function () { return app.querySelector('input[data-bad="1"]'); };
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () { if (S.area > 0) { S.area--; go(2); } else go(1); } }),
      el('span', { class: 'row' }, [
        S.area < list.length - 1 ? el('button', { class: 'btn', type: 'button', text: 'Skip to my files', onclick: function () { if (!bad()) go(3); else bad().focus(); } }) : null,
        el('button', { class: 'btn solid', type: 'button', text: S.area < list.length - 1 ? 'Next: ' + list[S.area + 1].group.name : 'Show my files', onclick: function () {
          if (bad()) { bad().focus(); return; }
          if (S.area < list.length - 1) { S.area++; go(2); } else go(3);
        } })
      ])
    ]));
  }

  // ── Step 4: the files ──
  function envLine(name, value) {
    // Compose and kustomize read the rest of the line as the value; quoting
    // would become part of it.
    return name + '=' + value;
  }
  var SECRET_HINTS = {
    SESSION_SIGNING_KEY: 'generate: echo "k1:$(openssl rand -base64 32)"',
    DB_PASSWORD: 'the EXISTING owning role’s password for external Postgres; for a new in-cluster database generate once with openssl rand -hex 24; never the same as DB_APP_PASSWORD',
    DB_APP_PASSWORD: 'a new password for the application role (openssl rand -hex 24)',
    BACKUP_ENVELOPE_KEY: 'generate: echo "k1:$(openssl rand -base64 32)"; escrow it before first use',
    OIDC_CLIENT_SECRET: 'from your identity provider',
    SMTP_URL: 'smtp://user:password@host:587 (STARTTLS) or smtps://host:465',
    RESEND_API_KEY: 'your Resend API key (re_...)',
    GOOGLE_OAUTH_CLIENT_SECRET: 'from the Google OAuth client'
  };
  function secretBlock(names) {
    var out = [];
    names.forEach(function (n) {
      var s = byName(n);
      out.push('# Fill in: ' + (SECRET_HINTS[n] || (s ? s.description : '')));
      out.push(n + '=');
    });
    return out;
  }

  function build() {
    var p = S.product, b = S.basic[p], chosen = [], cfg = [], secrets = [];
    var changed = S.values[p];
    var advNames = advancedSettings().map(function (s) { return s.name; });
    var extra = [];
    Object.keys(changed).forEach(function (n) { if (advNames.indexOf(n) >= 0) extra.push(n); });
    extra.sort(function (x, y) { return advNames.indexOf(x) - advNames.indexOf(y); });
    var optionalSecrets = Object.keys(S.secrets[p]).filter(function (n) { return advNames.indexOf(n) >= 0; });

    // blanks: what the person fills in, named in one line above the files.
    var blanks = [];
    var fillIn = function (name, hint, label) { blanks.push(label + ' (' + name + ')'); return '# Fill in: ' + hint + '\n' + name + '='; };
    if (p === 'small') {
      // No domain yet: the installer starts the box in setup mode, where the
      // domain is chosen in the browser (install.sh without --host).
      var content = b.domain ? b.content || 'sites.' + b.domain : '';
      var flags = b.domain ? ['--host', sh(b.domain), '--content', sh(content)] : [];
      chosen.push(['Where it runs', targetOf('small').name], ['Domain', b.domain || 'chosen on the server after the install'],
        ['Sites hostname', content || 'chosen with the domain']);
      if (b.email) { flags.push('--email', sh(b.email)); chosen.push(['Certificate notices', b.email]); }
      var env = [];
      extra.forEach(function (n) {
        var s = byName(n);
        if (s.install_flag) { flags.push(s.install_flag, sh(changed[n])); }
        else env.push(envLine(n, changed[n]));
        chosen.push([n, changed[n] + '  (default ' + (defaultOf(s) || 'none') + ')']);
      });
      var fill = [];
      if (b.codes) {
        fill.push('RESEND_API_KEY');
        env.unshift(b.mailFrom || b.domain ? envLine('MAIL_FROM', b.mailFrom || 'Simple Host <noreply@' + b.domain + '>')
          : fillIn('MAIL_FROM', 'the sender, like Simple Host <noreply@your-domain>, on a domain verified with Resend', 'Send email from'));
        chosen.push(['Sign-in', 'emailed codes']);
      }
      if (b.google) {
        env.push(b.googleId ? envLine('GOOGLE_OAUTH_CLIENT_ID', b.googleId) : fillIn('GOOGLE_OAUTH_CLIENT_ID', 'the Google OAuth client ID', 'Google client ID'));
        fill.push('GOOGLE_OAUTH_CLIENT_SECRET');
        chosen.push(['Sign-in', 'Google']);
      }
      if (!b.codes && !b.google) chosen.push(['Sign-in', 'admin key only']);
      fill = fill.concat(optionalSecrets);
      var SECRET_LABELS = { RESEND_API_KEY: 'Resend API key', GOOGLE_OAUTH_CLIENT_SECRET: 'Google client secret' };
      fill.forEach(function (n) { blanks.push(SECRET_LABELS[n] ? SECRET_LABELS[n] + ' (' + n + ')' : n); });
      var envText = env.concat(secretBlock(fill)).join('\n');
      var cmd = 'f=$(mktemp) && curl -fsSL ' + INSTALL_URL + ' -o "$f" && printf \'%s  %s\\n\' ' + INSTALLER_SHA256 + ' "$f" | sha256sum -c --quiet - && sudo bash "$f"' + (flags.length ? ' ' + flags.join(' ') : '');
      return { chosen: chosen, cmd: cmd, env: envText ? '# Simple Host settings (https://simple-host.app/setup)\n' + envText + '\n' : '', blanks: blanks };
    }

    var val = function (n) { var s = byName(n); return s ? valueOf(s) : ''; };
    var required = function (key, label) { if (!b[key]) blanks.push(label); return b[key]; };
    var values = {
      host: required('host', 'Address'),
      oidc: {
        issuer: required('issuer', 'Issuer URL'), clientId: required('clientId', 'Client ID'),
        adminEmails: splitList(required('admins', 'Admin emails')).map(function (v) { return v.toLowerCase(); }).join(','), allowedEmailDomains: domainList(b.domains).join(','),
        scopes: val('OIDC_SCOPES'), sessionTTL: val('SESSION_TTL'), sessionIdle: val('SESSION_IDLE')
      },
      postgres: { mode: b.postgresMode, database: b.dbName || 'simplehost', user: b.dbUser || 'simplehost' },
      storage: {
        endpoint: required('endpoint', 'Bucket endpoint'), region: required('region', 'Bucket region'), bucket: required('bucketName', 'Bucket name'),
        prefix: val('BACKUP_STORAGE_PREFIX'), sse: val('BACKUP_SSE'), sseKeyId: val('BACKUP_SSE_KEY_ID')
      },
      certificates: { createIssuer: false, issuer: b.certs === 'auto' ? required('issuerName', 'cert-manager ClusterIssuer') : '', ownerCerts: b.certs, ownerHostsInterval: val('OWNER_HOSTS_INTERVAL') },
      ingress: { className: b.ingressClass, tlsSecret: b.tlsSecret || 'simple-host-tls' },
      trustedProxyCIDRs: b.proxies,
      secrets: { existingSecret: 'simple-host-secrets' },
      extraConfig: {}
    };
    if ((isGoogle(b.issuer) || b.idp === 'google') && !b.domains) blanks.push('Company email domains');
    if (b.postgresMode === 'external') values.postgres.external = { host: required('dbHost', 'Postgres host'), port: +(b.dbPort || '5432'), sslmode: 'verify-full' };
    else values.postgres.incluster = { size: b.dbSize || '5Gi', storageClass: b.dbStorageClass };
    if (b.smtp) values.extraConfig.SMTP_FROM = required('smtpFrom', 'Send email from');
    var mapped = ['OIDC_SCOPES', 'SESSION_TTL', 'SESSION_IDLE', 'BACKUP_STORAGE_PREFIX', 'BACKUP_SSE', 'BACKUP_SSE_KEY_ID', 'OWNER_HOSTS_INTERVAL'];
    extra.forEach(function (n) {
      if (mapped.indexOf(n) < 0) values.extraConfig[n] = changed[n];
      chosen.push([n, changed[n] + '  (default ' + (byName(n).default || 'none') + ')']);
    });
    if (b.creds === 'identity') {
      values.serviceAccount = { annotations: {} };
      values.podAnnotations = {};
      values.podLabels = {};
    }
    secrets = ['OIDC_CLIENT_SECRET', 'SESSION_SIGNING_KEY', 'DB_PASSWORD', 'DB_APP_PASSWORD', 'BACKUP_ENVELOPE_KEY'];
    if (b.creds === 'keys') secrets.push('BACKUP_STORAGE_ACCESS_KEY_ID', 'BACKUP_STORAGE_SECRET_ACCESS_KEY');
    if (b.smtp) secrets.push('SMTP_URL');
    secrets = secrets.concat(optionalSecrets.filter(function (n) { return secrets.indexOf(n) < 0; }));
    chosen.unshift(['Install with', b.output === 'yaml' ? 'Kubernetes YAML' : 'Helm'], ['Cluster', b.context || 'your current kubectl context'],
      ['Namespace', b.namespace || 'simple-host'], ['Address', b.host || 'to fill in'], ['Postgres', b.postgresMode === 'external' ? b.dbHost || 'existing, host to fill in' : 'in this cluster'],
      ['Site certificates', b.certs === 'auto' ? 'cert-manager (' + (b.issuerName || 'to fill in') + ')' : 'issued by you']);
    var flags = ' --namespace "$NAMESPACE"' + (b.postgresMode === 'external' ? ' --set-file enterprise.postgres.external.caCert=db-ca.crt' : '');
    var commands = [
      '#!/usr/bin/env bash',
      '# Run in bash, from the directory containing your completed files.',
      'set -e',
      'CONTEXT=' + (b.context ? sh(b.context) : '"$(kubectl config current-context)"'),
      'NAMESPACE=' + sh(b.namespace || 'simple-host'),
      'kubectl --context "$CONTEXT" cluster-info',
      'kubectl --context "$CONTEXT" create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f -',
      '# Fill secrets.env locally once; keep it private and reuse it on upgrades.',
      '# Never regenerate SESSION_SIGNING_KEY, BACKUP_ENVELOPE_KEY or database passwords on upgrade.',
      'chmod 600 secrets.env',
      'if ! kubectl --context "$CONTEXT" -n "$NAMESPACE" get secret simple-host-secrets >/dev/null 2>&1; then',
      '  kubectl --context "$CONTEXT" -n "$NAMESPACE" create secret generic simple-host-secrets --from-env-file=secrets.env',
      'fi'
    ];
    if (b.output === 'yaml') commands.push(
      'helm template simple-host .' + flags + ' > simple-host.yaml',
      'kubectl --context "$CONTEXT" -n "$NAMESPACE" apply --dry-run=server -f simple-host.yaml',
      'kubectl --context "$CONTEXT" -n "$NAMESPACE" apply -f simple-host.yaml');
    else commands.push('helm upgrade --install simple-host .' + flags + ' --kube-context "$CONTEXT" --wait --timeout 10m');
    commands.push('kubectl --context "$CONTEXT" -n "$NAMESPACE" rollout status deployment/simple-host --timeout=300s');
    return {
      chosen: chosen, blanks: blanks,
      chart: enterpriseChart(),
      ignore: '# Keep local credentials and rendered output out of Helm chart files.\nsecrets.env\ndb-ca.crt\nsimple-host.yaml\nchart-review/\n',
      readme: enterpriseReadme(b, flags),
      config: '# Simple Host Enterprise — existing Kubernetes, chart ' + ENT_CHART_VERSION + '\n' +
        (blanks.length ? '# Fill in before installing: ' + blanks.join(', ') + '.\n' : '') +
        '# Settings belong under the enterprise dependency alias.\n' +
        '# Secrets are managed separately and reused on upgrades.\n' + valuesYAML({ enterprise: values }, ''),
      secrets: '# secrets.env: fill locally, chmod 600, never commit. Keep the same file and Secret for upgrades.\n' + secretBlock(secrets).join('\n') + '\n',
      commands: commands.join('\n') + '\n'
    };
  }

  // JSON-quoted scalar strings are YAML strings too; no interpolation or tags.
  function valuesYAML(obj, indent) {
    return Object.keys(obj).map(function (k) {
      var v = obj[k], nested = v && typeof v === 'object';
      return indent + k + ':' + (nested && Object.keys(v).length ? '\n' + valuesYAML(v, indent + '  ') : ' ' + JSON.stringify(v) + '\n');
    }).join('');
  }

  function enterpriseChart() {
    return 'apiVersion: v2\ntype: application\nname: simple-host-enterprise-install\nversion: 0.1.0\n' +
      'description: Simple Host Enterprise configured for an existing Kubernetes cluster.\n' +
      'dependencies:\n  - name: simple-host-enterprise\n    version: "' + ENT_CHART_VERSION + '"\n' +
      '    repository: "' + ENT_CHART.slice(0, ENT_CHART.lastIndexOf('/')) + '"\n    alias: enterprise\n';
  }
  function enterpriseReadme(b, flags) {
    var lines = [
      '# Simple Host Enterprise — review and install', '',
      'Extract this ZIP into a private local directory. It is a standard Helm chart: Chart.yaml pins the Enterprise dependency, values.yaml configures it under enterprise:, and charts/ contains the published dependency archive. No script is required and no dependency download is needed to inspect or install this package.', '',
      '## Inspect the chart', '',
      'Read Chart.yaml and values.yaml. Inspect the dependency templates and defaults by extracting its archive into a separate review directory:', '',
      FENCE + 'sh', 'mkdir -p chart-review && tar -xzf ' + ENT_CHART_FILE + ' -C chart-review', FENCE, '',
      'The bundled archive SHA-256 is ' + ENT_CHART_SHA256 + '. Verify the downloaded bytes:', '',
      FENCE + 'sh', 'printf \'%s  %s\\n\' ' + sh(ENT_CHART_SHA256) + ' ' + sh(ENT_CHART_FILE) + ' | sha256sum -c -', FENCE, '',
      'The dependency was published at ' + ENT_CHART + ' version ' + ENT_CHART_VERSION + '. If you intentionally change that version in Chart.yaml, use helm dependency update to fetch the newly selected chart.', '',
      '## Complete the configuration', '',
      'Fill any missing non-secret values in values.yaml. Keep settings under enterprise:. Use your existing ingress, OIDC application and S3-compatible bucket. Automatic certificates and in-cluster Postgres require cert-manager already installed.', '',
      b.postgresMode === 'external' ? 'Save your database provider\'s real CA as db-ca.crt. The commands pass it as enterprise.postgres.external.caCert and verify the database hostname over TLS.' : 'Confirm a working StorageClass exists. The in-cluster Postgres has persistent storage and TLS; database backup and high availability are your responsibility.', '',
      'Fill secrets.env locally, keep it outside Git, and reuse the same keys on upgrades. The selected existingSecret is simple-host-secrets. An existing secret-management operator can supply that Secret instead. .helmignore excludes secrets.env, db-ca.crt and rendered output from chart files.', '',
      '## Lint, render and dry-run before installing', '',
      'Run these commands from the extracted directory. Client dry-run simulates the install without applying resources:', '',
      FENCE + 'sh',
      'CONTEXT=' + (b.context ? sh(b.context) : '"$(kubectl config current-context)"'),
      'NAMESPACE=' + sh(b.namespace || 'simple-host'),
      'helm lint .' + flags,
      'helm template simple-host .' + flags + ' > simple-host.yaml',
      'helm install simple-host .' + flags + ' --kube-context "$CONTEXT" --dry-run=client',
      FENCE, '',
      'Review simple-host.yaml before applying it. The rendered manifests reference the existing Secret; credentials do not need to be created just to lint or render.', '',
      '## Prepare the namespace and persistent Secret', '',
      'After review, confirm the Kubernetes context. Create the namespace if needed and create the Secret only if your organisation has not already supplied it:', '',
      FENCE + 'sh',
      'kubectl --context "$CONTEXT" create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f -',
      'chmod 600 secrets.env',
      'if ! kubectl --context "$CONTEXT" -n "$NAMESPACE" get secret simple-host-secrets >/dev/null 2>&1; then kubectl --context "$CONTEXT" -n "$NAMESPACE" create secret generic simple-host-secrets --from-env-file=secrets.env; fi',
      FENCE, '',
      'Complete DNS, dashboard certificates and the OIDC callback before installing. See https://github.com/vineetu/simple-host-enterprise/blob/main/docs/install-kubernetes.md.', '',
      '## Install and upgrade with Helm', '',
      FENCE + 'sh',
      'helm install simple-host .' + flags + ' --kube-context "$CONTEXT" --wait --timeout 10m',
      FENCE, '',
      'After editing values.yaml, upgrade the same release from this local chart directory:', '',
      FENCE + 'sh',
      'helm upgrade simple-host .' + flags + ' --kube-context "$CONTEXT" --wait --timeout 10m',
      FENCE, '',
      'Keep the persistent Secret unchanged unless you are deliberately rotating credentials. Use these local-chart commands for upgrades; the upstream chart\'s notes may describe direct OCI installs with a different values layout.', ''
    ];
    if (b.output === 'yaml') lines.push(
      '## Apply rendered Kubernetes YAML instead', '',
      'After preparing the namespace and Secret, render and review simple-host.yaml using the command above, then validate it against the existing cluster and apply:', '',
      FENCE + 'sh',
      'kubectl --context "$CONTEXT" -n "$NAMESPACE" apply --dry-run=server -f simple-host.yaml',
      'kubectl --context "$CONTEXT" -n "$NAMESPACE" apply -f simple-host.yaml', FENCE, '',
      'Use one deployment method for this installation: Helm manages its release, while kubectl manages the YAML you apply.', '');
    lines.push('## Optional script and final checks', '',
      'install.sh is a convenience wrapper for the selected ' + (b.output === 'yaml' ? 'render/apply' : 'Helm install/upgrade') + ' path. Read it first; running it creates the namespace/Secret if needed and installs the resources.', '',
      'Confirm the deployment is ready, sign in, publish a site and run the smoke checks in simple-host-setup.md.', '');
    return lines.join('\n');
  }

  function copyButton(getText, label) {
    var btn = el('button', { class: 'btn small', type: 'button', text: label || 'Copy', onclick: function () {
      var t = getText(), done = function () { btn.textContent = 'Copied'; setTimeout(function () { btn.textContent = label || 'Copy'; }, 1500); };
      if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).then(done, function () {});
    } });
    return btn;
  }
  function downloadFile(name, blob) {
    var url = URL.createObjectURL(blob), a = el('a', { href: url, download: name });
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  }
  function downloadButton(name, getText) {
    return el('button', { class: 'btn small', type: 'button', text: 'Download', onclick: function () {
      downloadFile(name, new Blob([getText()], { type: 'text/plain;charset=utf-8' }));
    } });
  }
  // ZIP's STORE method needs no compression library. Text and bundled chart bytes each
  // UTF-8 entry has its CRC-32, local header and central-directory record.
  function zipFiles(files) {
    var encoder = new TextEncoder(), local = [], directory = [], offset = 0, directorySize = 0;
    files.forEach(function (file) {
      var name = encoder.encode(file.name), data = file.bytes || encoder.encode(file.text), crc = 0xffffffff;
      for (var i = 0; i < data.length; i++) {
        crc ^= data[i];
        for (var bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
      }
      crc = (crc ^ 0xffffffff) >>> 0;
      var header = new Uint8Array(30 + name.length), h = new DataView(header.buffer);
      h.setUint32(0, 0x04034b50, true); h.setUint16(4, 20, true);
      h.setUint16(6, 0x0800, true); // UTF-8, uncompressed, no data descriptor.
      h.setUint16(12, 33, true); // 1980-01-01, a valid deterministic DOS date.
      h.setUint32(14, crc, true); h.setUint32(18, data.length, true); h.setUint32(22, data.length, true);
      h.setUint16(26, name.length, true); header.set(name, 30);
      var entry = new Uint8Array(46 + name.length), e = new DataView(entry.buffer);
      e.setUint32(0, 0x02014b50, true); e.setUint16(4, 20, true); e.setUint16(6, 20, true);
      e.setUint16(8, 0x0800, true); e.setUint16(14, 33, true);
      e.setUint32(16, crc, true); e.setUint32(20, data.length, true); e.setUint32(24, data.length, true);
      e.setUint16(28, name.length, true); e.setUint32(42, offset, true); entry.set(name, 46);
      local.push(header, data); directory.push(entry);
      offset += header.length + data.length; directorySize += entry.length;
    });
    var end = new Uint8Array(22), z = new DataView(end.buffer);
    z.setUint32(0, 0x06054b50, true); z.setUint16(8, files.length, true); z.setUint16(10, files.length, true);
    z.setUint32(12, directorySize, true); z.setUint32(16, offset, true);
    return new Blob(local.concat(directory, [end]), { type: 'application/zip' });
  }
  function block(title, text, file, collapsed) {
    var actions = el('span', { class: 'acts' }, [copyButton(function () { return text; }), file ? downloadButton(file, function () { return text; }) : null]);
    return el(collapsed ? 'details' : 'div', { class: 'out', 'data-file': file || null }, collapsed ? [
      el('summary', { text: title }), actions, el('pre', { text: text, tabindex: '0' })
    ] : [
      el('h3', null, [el('span', { text: title }), actions]), el('pre', { text: text, tabindex: '0' })
    ]);
  }

  // ── Set it up with your AI agent ──
  // Where it runs. Each target is what the machine needs and, where the
  // agent creates the machine, how. A small box offers UpCloud (the agent
  // creates the server with the person's API user) or a server they already
  // have; another platform is one more entry here and one more choice in
  // renderWhere. Enterprise runs on the company's own cluster.
  var TARGETS = {
    small: [
      { id: 'upcloud', name: 'UpCloud', needs: 'a new UpCloud server that you create in my UpCloud account: the smallest plan with 1 CPU and 1 GB of RAM, Ubuntu Server 24.04 LTS, a public IPv4 address' },
      { id: 'server', name: 'Your own server', needs: 'a fresh Ubuntu server with 1 CPU, 1 GB of RAM and about 25 GB of disk, a public IPv4 address, ports 80 and 443 open to the internet (in the cloud’s firewall too), and SSH with sudo' }
    ],
    ent: [{ id: 'kubernetes', name: 'Kubernetes', needs: 'an existing Kubernetes cluster (1.30 or later), kubectl and Helm 3.14+, an existing ingress controller, cert-manager for automatic certificates or in-cluster Postgres, an existing Postgres 16 or the optional in-cluster database, an S3-compatible bucket with versioning on, and the company’s OIDC provider' }]
  };
  function targetOf(p) {
    var list = TARGETS[p], want = p === 'small' ? S.basic.small.where : '';
    for (var i = 0; i < list.length; i++) if (list[i].id === want) return list[i];
    return list[0];
  }
  // UPCLOUD_TOKEN_CREDS and UPCLOUD_CREDS are the lines the person runs in
  // their own terminal before starting their agent, one or the other: an API
  // token (recommended; upctl reads UPCLOUD_TOKEN), or an API user's name and
  // password. Secrets are read without echo (read -s, with a trap that turns
  // echo back on if Ctrl-C stops it) and exported, so they never pass through
  // this page, the agent's chat or the shell history. bash and zsh alike.
  var UPCLOUD_TOKEN_CREDS = "printf 'UpCloud API token: '; trap 'stty echo 2>/dev/null' INT; read -rs UPCLOUD_TOKEN; trap - INT; echo; export UPCLOUD_TOKEN";
  var UPCLOUD_CREDS = "printf 'UpCloud API username: '; read -r UPCLOUD_USERNAME; printf 'UpCloud API password: '; trap 'stty echo 2>/dev/null' INT; read -rs UPCLOUD_PASSWORD; trap - INT; echo; export UPCLOUD_USERNAME UPCLOUD_PASSWORD";
  // What to do with the credentials afterwards, on the page and in the prompt.
  var UPCLOUD_UNSET = 'unset UPCLOUD_TOKEN UPCLOUD_USERNAME UPCLOUD_PASSWORD';
  var UPCLOUD_LIMIT = 'Keep it limited: give a token an expiry, give an API user only the server permissions it needs and, if you can, allow only your own IP address.';
  // dnsNames are the A records the installer needs: the domain, and the
  // sites hostname, which a wildcard covers when it sits under the domain.
  function dnsRecords(domain, content) {
    var recs = [domain, '*.' + domain];
    if (content.slice(-(domain.length + 1)) !== '.' + domain) recs.push(content);
    return recs;
  }
  // dnsText says which records to add, the same way in the prompt and in
  // the steps by hand.
  function dnsText(recs, content) {
    return 'A records pointing at the server’s public IPv4 address: ' + recs.join(', ') + (recs.length === 2 ? ' (the wildcard covers ' + content + ')' : '');
  }
  var FENCE = '```';
  // handoff is the whole block for the person's own AI agent: what the
  // machine needs (and on UpCloud, how to create it), the steps with this
  // page's files in them, how to check the result, and where to get help.
  // Secrets are blanks, as in the files; UpCloud's credentials are only
  // ever environment variables the person set in their own terminal.
  function handoff(r, target) {
    var p = S.product, b = S.basic[p], t = target || targetOf(p);
    var origin = location.origin, product = p === 'small' ? 'small-box' : 'enterprise';
    var L = [], n = 1;
    L.push(p === 'small' ? (t.id === 'upcloud' ? '# Set up Simple Host on a small box at UpCloud' : '# Set up Simple Host on a small box') : '# Set up Simple Host Enterprise on Kubernetes', '');
    L.push('These are instructions for you, my AI agent, from ' + origin + '/setup. Work through them in order. Stop at the first step that fails and show me its exact error. Never print, log or commit a secret. Have me enter secrets locally into the file, not into chat.', '');
    L.push('Where it runs: ' + t.needs + '.', '');
    if (p === 'small') {
      // With no domain on the page, the agent asks for it: <domain> and
      // <sites> stand for the answers.
      var domain = b.domain || '<domain>', content = b.domain ? b.content || 'sites.' + b.domain : '<sites>', recs = dnsRecords(domain, content);
      if (!b.domain) L.push(n++ + '. Domain: ask me for the domain Simple Host lives at (like hack.example.com) and the hostname for published sites (sites.<domain> unless I say otherwise). Below, <domain> and <sites> stand for my answers.', '');
      var ssh = '', host = 'the server';
      if (t.id === 'upcloud') {
        ssh = 'ssh -o StrictHostKeyChecking=accept-new -i ~/.ssh/simple-host root@<the server’s IPv4>';
        L.push(n++ + '. UpCloud credentials: before starting you I set them in this terminal, either an API token as UPCLOUD_TOKEN (preferred) or an API user (a sub-account with API access) as UPCLOUD_USERNAME and UPCLOUD_PASSWORD. Use them only from the environment: never ask me to paste them into this chat, never print them, and never write them to a file, a log or a command line. Check with `{ test -n "$UPCLOUD_TOKEN" || { test -n "$UPCLOUD_USERNAME" && test -n "$UPCLOUD_PASSWORD"; }; } && echo set`. If neither is set, stop and ask me to quit you, run one of these in the terminal (the token, or the API user), and start you again:', '', FENCE + 'sh', UPCLOUD_TOKEN_CREDS, FENCE, '', FENCE + 'sh', UPCLOUD_CREDS, FENCE, '');
        L.push(n++ + '. Tools: use `upctl`, UpCloud’s command-line tool (https://github.com/UpCloudLtd/upcloud-cli; it reads UPCLOUD_TOKEN, or the username and password), installing it if it is missing, or the API at https://api.upcloud.com/1.3 with curl reading the credentials from its standard input, never its arguments. With the token: `printf \'header = "Authorization: Bearer %s"\\n\' "$UPCLOUD_TOKEN" | curl -fsS -K - https://api.upcloud.com/1.3/account`. With the API user: `printf \'header = "Authorization: Basic %s"\\n\' "$(printf \'%s:%s\' "$UPCLOUD_USERNAME" "$UPCLOUD_PASSWORD" | base64 | tr -d \'\\n\')" | curl -fsS -K - https://api.upcloud.com/1.3/account`. Confirm access with `upctl account show` (or that request). Check each command’s current flags with `--help` rather than guessing.', '');
        L.push(n++ + '. SSH key: use ~/.ssh/simple-host if it exists; otherwise create it with `ssh-keygen -t ed25519 -N \'\' -f ~/.ssh/simple-host -C simple-host`. Never overwrite an existing key.', '');
        L.push(n++ + '. Create the server:');
        L.push('   - Zone: list them (`upctl zone list`, or `GET /1.3/zone`) and ask me which one is nearest the people who will use it.');
        L.push('   - Plan: list the plans (`upctl server plans`, or `GET /1.3/plan`) and take the smallest with at least 1 CPU and 1 GB of RAM: STARTER-1xCPU-1GB today; if it is gone, its current equivalent. Prices are only in the API: `GET /1.3/price` has, for each zone, an entry `server_plan_<plan>` whose `price` is in cents per hour (times 730 for a month; about $4 a month for the smallest at list price). Tell me the plan and its monthly price before creating anything.');
        L.push('   - Image: the plain Ubuntu Server 24.04 LTS template (`GET /1.3/storage/template`; `upctl storage list --template` can come back empty, so use the API). Two templates carry that name; the one with NVIDIA drivers and CUDA needs a 20 GB disk, so take the other.');
        L.push('   - Disk: the plan’s included storage size, tier `standard` (the small plans refuse any other tier with TIER_INVALID).');
        L.push('   - Login user root with the public key ~/.ssh/simple-host.pub, metadata on, one public IPv4 interface, title and hostname simple-host.');
        L.push('   - Create it once, then poll until its state is `started`. Its address is the one where access is `public` and family `IPv4` (never the 10.x utility address). Tell me the server’s UUID and address. SSH can refuse connections for a minute or so after `started`: retry every few seconds for up to two minutes before calling it a failure.');
        L.push('   - UpCloud’s firewall is off for a new server. If it is on, allow ports 22, 80 and 443 in.', '');
        host = 'the server (`' + ssh + '`)';
      }
      L.push(n++ + '. DNS: at my domain’s DNS provider, ' + dnsText(recs, content) + '. If you can manage that DNS provider from here, ask me before changing anything; otherwise tell me the exact records to add and wait for me. Check that `dig +short ' + (b.domain ? sh(domain) : domain) + '` and `dig +short ' + (b.domain ? sh(content) : content) + '` both print the address. Certificates are issued on the first visit, so both names must point at the server first.', '');
      L.push(n++ + '. Install: on ' + host + ', run this' + (b.domain ? '' : ' with --host <domain> --content <sites> added at the end (each in single quotes)') + '. It installs Docker, starts Simple Host from the release it pins (' + INSTALLER_RELEASE + ') and prints the admin key: give it to me. The server keeps it in /opt/simple-host/.env (ADMIN_API_KEY) and re-running the command prints it again; do not copy it anywhere else.', '', FENCE + 'sh', r.cmd, FENCE, '');
      if (r.env) {
        L.push(n++ + '. Settings: add these lines to the end of /opt/simple-host/.env on the server (it needs sudo). Ask me for each blank value; do not invent one. Then run `cd /opt/simple-host && sudo docker compose up -d`.', '', FENCE, r.env.replace(/\n$/, ''), FENCE, '');
      }
      L.push(n++ + '. Check it works:');
      L.push('   - `curl -sS -o /dev/null -w \'%{http_code}\\n\' ' + (b.domain ? sh('https://' + domain + '/healthz') : 'https://<domain>/healthz') + '` prints 200.');
      L.push('   - `cd /opt/simple-host && sudo docker compose ps`, on the server, shows every service running.');
      L.push('   - `curl -sS -o /dev/null -w \'%{http_code}\\n\' ' + (b.domain ? sh('https://' + content + '/') : 'https://<sites>/') + '` prints a status code with no certificate error.');
      L.push('   - `cd /opt/simple-host && sudo docker compose exec -T app simple-host version`, on the server, names the release (' + INSTALLER_RELEASE + ').', '');
      L.push(n++ + '. Tell me: the admin page, https://' + domain + '/admin (open it and paste the admin key there)' + (t.id === 'upcloud' ? ', the server’s UUID, address, plan and zone. Then remind me to run `' + UPCLOUD_UNSET + '` in this terminal (or close it) once you are done with UpCloud, and to keep the token or API user limited (a token with an expiry, an API user with only server permissions) and, if I can, to my own IP address' : '') + '.', '');
    } else {
      var addr = b.host || '<address>';
      L.push('1. Use my existing Kubernetes cluster. Read https://github.com/vineetu/simple-host-enterprise/blob/main/docs/install-kubernetes.md. Do not provision a cluster. Confirm the context and namespace before applying. Use the pinned chart ' + ENT_CHART_VERSION + '.', '');
      L.push('2. Download and extract the chart ZIP. Inspect Chart.yaml, README.md and the bundled dependency templates. Save values.yaml below' + (r.blanks.length ? ' and ask me for the missing non-secret values: ' + r.blanks.join(', ') : '') + '.', '', FENCE + 'yaml', r.config.trimEnd(), FENCE, '');
      L.push('3. Save secrets.env outside Git with mode 600. Have me fill provider credentials in my local terminal, never in chat. Generate the new signing, envelope and application-role keys locally once, following the hints. External Postgres needs its EXISTING owning-role password. Keep the file and Kubernetes Secret on upgrades; do not rotate keys as part of an upgrade. Back up the envelope key before first use.', '', FENCE, r.secrets.trimEnd(), FENCE, '');
      if (b.postgresMode === 'external') L.push('4. Save the database provider’s real CA certificate as db-ca.crt. The chart mounts it and verifies the database hostname over TLS.', '');
      else L.push('4. Confirm cert-manager and a working StorageClass already exist. This installs one persistent Postgres for evaluation; database backup and high availability are not included.', '');
      if (b.creds === 'identity') L.push('Configure the existing AWS-compatible identity using enterprise.serviceAccount.annotations and enterprise.podLabels/podAnnotations in values.yaml. Native Azure/GCS identity credentials are not supported by the S3 client.', '');
      L.push('5. Point ' + addr + ' and the required wildcard DNS records at the existing ingress. Register https://' + addr + '/auth/callback with the OIDC provider. ' + (b.certs === 'auto' ? 'The existing ClusterIssuer must support wildcard certificates (DNS-01 or a company CA).' : 'Provide the dashboard TLS Secret for the address and *.<address>, plus each owner’s wildcard certificate and ingress route as described in the install guide.'), '');
      L.push('6. Read README.md and use its helm lint, helm template and helm install --dry-run=client commands to inspect the local chart before applying anything. After review, prepare the namespace/Secret and use helm install or helm upgrade on the local chart directory. If I prefer the convenience script, review it before running it. Existing secrets are left intact; deliberate secret changes require updating the Secret and restarting affected workloads.', '', r.readme, '');
      L.push('7. Check https://' + addr + '/readyz returns HTTP 200. An admin signs in at https://' + addr + '/auth/login, confirms is_admin at /api/me and mints a Full key on /dashboard (INSTALL.md HUMAN STEP D). Clone https://github.com/vineetu/simple-host-enterprise and run `make smoke BASE=' + sh('https://' + addr) + ' KEY_FILE="$HOME/.simple-host-install-key"`. Every smoke check must pass. For an internal CA, set CURL_CA_BUNDLE to the system CAs plus the company CA. Verify owner-host TLS and publishing too.', '');
    }
    L.push('If anything fails and the output does not tell you how to fix it, tell me and show me the error.');
    return L.join('\n') + '\n';
  }

  // fillLine is the one line naming what is left to fill in, or null.
  function fillLine(blanks, tail, lead) {
    if (!blanks.length && !tail) return null;
    return el('p', { class: 'fill', role: 'note', text: (lead || 'Fill in before you run it: ') + blanks.concat(tail ? [tail] : []).join(', ') + '.' });
  }

  function renderOutput() {
    var b = S.basic[S.product], r = build(), card = el('div', { class: 'card', id: 'files' }), t = targetOf(S.product), agent = handoff(r);
    // On UpCloud the agent does the work: its block comes first, with the
    // command that sets the API user in the person's own terminal.
    if (t.id === 'upcloud') {
      app.appendChild(el('div', { class: 'card', id: 'agent' }, [
        el('h2', { text: 'Set it up on UpCloud with your AI agent' }),
        el('p', { class: 'note', style: 'margin:0 0 4px', text: 'Your agent creates the server in your UpCloud account, points your names at it, installs Simple Host with your choices and checks it. It asks you before spending anything and for every secret.' }),
        el('ol', { class: 'steps' }, [
          el('li', null, ['An UpCloud account with an API token (recommended) or an API user (a sub-account with API access). No account yet? ',
            el('a', { href: UPCLOUD_SIGNUP, target: '_blank', rel: 'noopener', text: 'Create your UpCloud account' }),
            el('span', { class: 'fine-inline', text: ' (referral link: $25 of credit for new accounts; their terms apply)' }), '.']),
          el('li', { text: 'In the terminal you use your agent in, run one of these: the first for an API token, the second for an API user. It asks for the token (or the name and password; secrets are not shown) and keeps it in that terminal only: not on this page, not in your agent’s chat, not in your shell history.' })
        ]),
        block('In your terminal: API token (recommended)', UPCLOUD_TOKEN_CREDS),
        block('Or: API user', UPCLOUD_CREDS),
        el('ol', { class: 'steps', start: '3' }, [el('li', { text: 'Start your AI agent in that terminal and give it this.' })]),
        block('For your AI agent', agent, 'simple-host-setup.md'),
        el('p', { class: 'note', style: 'margin:8px 0 0' }, ['When the server is up, run ', el('code', { text: UPCLOUD_UNSET }), ' in that terminal, or close it: until then every program started there can read the token or API user. ' + UPCLOUD_LIMIT])
      ]));
    }
    if (S.product === 'small') {
      var sb = S.basic.small, sContent = sb.content || 'sites.' + sb.domain;
      card.appendChild(el('h2', { text: t.id === 'upcloud' ? 'Or do it by hand' : 'Your small box' }));
      card.appendChild(el('ol', { class: 'steps' }, [
        t.id === 'upcloud' ? el('li', { text: 'In the UpCloud control panel, create the server: the smallest plan with 1 CPU and 1 GB of RAM, the plain Ubuntu Server 24.04 LTS image, your SSH key.' }) : null,
        el('li', { text: sb.domain ? 'At your domain’s DNS provider, add ' + dnsText(dnsRecords(sb.domain, sContent), sContent) + '. The server is a fresh Ubuntu server with ports 80 and 443 open.'
          : 'At your domain’s DNS provider, add A records pointing at the server’s public IPv4 address for your domain and *.<your domain>. The server is a fresh Ubuntu server with ports 80 and 443 open.' }),
        el('li', { text: sb.domain ? 'On the server, run the install command below. It installs Docker, starts Simple Host and prints the admin key: keep it. The server keeps it in /opt/simple-host/.env, and re-running the command prints it again. Sign in with it at /admin.'
          : 'On the server, run the install command below. It installs Docker, starts Simple Host and prints a setup address and password: open it, choose your domain there, and keep the admin key it gives you. Sign in with it at /admin.' }),
        r.env ? el('li', null, ['Open ', el('code', { text: 'sudo nano /opt/simple-host/.env' }), ', paste the settings at the end, fill in any blanks, save, then run ',
          el('code', { text: 'cd /opt/simple-host && sudo docker compose up -d' }), '. Re-running the installer (also how you upgrade) keeps them.']) : null
      ]));
      card.appendChild(el('div', { style: 'height:16px' }));
      var fl = fillLine(r.blanks, '', 'Fill in before you save the settings: ');
      if (fl) card.appendChild(fl);
      card.appendChild(block('Install command', r.cmd));
      if (r.env) card.appendChild(block('Settings for /opt/simple-host/.env', r.env, 'simple-host.env'));
      else card.appendChild(el('p', { class: 'note', text: 'No settings file needed: everything else keeps its default.' }));
    } else {
      card.appendChild(el('h2', { text: 'Your Enterprise Helm chart' }));
      var bundle = [{ name: 'Chart.yaml', text: r.chart }, { name: 'values.yaml', text: r.config },
        { name: 'secrets.env', text: r.secrets }, { name: 'README.md', text: r.readme },
        { name: '.helmignore', text: r.ignore }, { name: 'install.sh', text: r.commands },
        { name: 'simple-host-setup.md', text: agent }];
      var downloadError = el('p', { class: 'err', hidden: true, role: 'alert' });
      var zipButton = el('button', { class: 'btn solid', type: 'button', id: 'download-zip', text: 'Download ZIP', onclick: async function () {
        zipButton.disabled = true; zipButton.textContent = 'Preparing ZIP…'; downloadError.hidden = true;
        try {
          var response = await fetch('/setup/' + ENT_CHART_FILE);
          if (!response.ok) throw new Error('Could not load the bundled chart. Please retry the download.');
          var chartBytes = new Uint8Array(await response.arrayBuffer());
          downloadFile('simple-host-enterprise-setup.zip', zipFiles(bundle.concat([{ name: ENT_CHART_FILE, bytes: chartBytes }])));
        } catch (error) {
          downloadError.textContent = 'Could not download the chart ZIP. Please retry.'; downloadError.hidden = false;
        } finally {
          zipButton.disabled = false; zipButton.textContent = 'Download ZIP';
        }
      } });
      card.appendChild(zipButton); card.appendChild(downloadError);
      card.appendChild(el('p', { class: 'note', text: 'A standard Helm chart with Chart.yaml, your values, the pinned Enterprise dependency and a README. Inspect the templates, lint, render and dry-run before installing. install.sh is optional. Your settings stay in your browser.' }));
      card.appendChild(el('ol', { class: 'steps' }, [
        el('li', { text: 'Extract the ZIP and inspect Chart.yaml, values.yaml and the dependency in charts/. Expand the file previews below or follow the README to inspect its templates.' }),
        el('li', { text: 'Complete the missing values. Settings are under enterprise:, matching the dependency alias. Use your existing cluster, ingress, OIDC application and S3-compatible bucket.' }),
        el('li', { text: b.postgresMode === 'external' ? 'Save the database CA as db-ca.crt. The Helm commands pass it with --set-file enterprise.postgres.external.caCert=db-ca.crt.' : 'Check the StorageClass and cert-manager for in-cluster Postgres and certificates.' }),
        el('li', { text: 'Use the README commands to run helm lint, helm template and helm install --dry-run=client. Review the rendered Kubernetes resources before applying them.' }),
        el('li', { text: 'After review, supply the persistent Secret and install with helm install. Use helm upgrade for later configuration changes. The script is available if you prefer it.' })
      ]));
      card.appendChild(el('p', { class: 'note' }, [
        el('a', { href: 'https://github.com/vineetu/simple-host-enterprise/tree/chart-v' + ENT_CHART_VERSION + '/deploy/helm/simple-host-enterprise', target: '_blank', rel: 'noopener', text: 'View Enterprise chart source' }),
        ' · ', el('a', { href: '/setup/' + ENT_CHART_FILE, download: 'simple-host-enterprise-' + ENT_CHART_VERSION + '.tgz', text: 'Download dependency chart' })
      ]));
      card.appendChild(fillLine(r.blanks, 'the secrets in secrets.env'));
      card.appendChild(block('Chart.yaml — pinned Enterprise dependency', r.chart, 'Chart.yaml', true));
      card.appendChild(block('values.yaml', r.config, 'values.yaml', true));
      card.appendChild(block('secrets.env (fill locally once)', r.secrets, 'secrets.env', true));
      card.appendChild(block('README.md — inspect, dry-run and install', r.readme, 'README.md', true));
      card.appendChild(block('.helmignore — excludes local credentials', r.ignore, '.helmignore', true));
      card.appendChild(block(b.output === 'yaml' ? 'install.sh — optional render/apply script' : 'install.sh — optional install/upgrade script', r.commands, 'install.sh', true));
    }
    app.appendChild(card);
    if (t.id !== 'upcloud') {
      app.appendChild(el('div', { class: 'card', id: 'agent' }, [
        el('h2', { text: 'Set it up with your AI agent' }),
        el('p', { class: 'note', style: 'margin:0 0 4px', text: 'Rather have your AI agent do it? Copy this into the agent you use in your terminal. It has what the machine needs, every step with your files in it, and how to check the result. Secrets stay blanks: the agent asks you for them.' }),
        block(S.product === 'ent' ? 'simple-host-setup.md — for your AI agent' : 'For your AI agent', agent, 'simple-host-setup.md', S.product === 'ent')
      ]));
    }
    app.appendChild(el('div', { class: 'card' }, [
      el('h2', { text: 'What you chose' }),
      el('ul', { class: 'summary' }, r.chosen.map(function (c) { return el('li', null, [el('span', { text: c[0] }), el('span', { text: c[1] })]); })),
      el('p', { class: 'note', text: 'Everything not listed keeps its default. Every setting is explained, with recipes, in the advanced settings docs.' })
    ]));
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () {
        if (S.mode === 'advanced') { S.area = areas().length - 1; go(2); } else go(1);
      } }),
      el('button', { class: 'btn', type: 'button', text: 'Start over', onclick: function () { location.reload(); } })
    ]));
  }

  render();
})();
