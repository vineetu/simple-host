// The setup helper at /setup. Everything happens in this page: the settings
// lists are the two settings.json files next to this script (generated from
// each product's code, see docs/advanced/README.md), nothing typed here is
// sent anywhere, and secrets are never asked for, only named as blanks.
(function () {
  'use strict';

  var FILES = { small: '/setup/small-box-settings.json', ent: '/setup/enterprise-settings.json' };
  var INSTALL_URL = 'https://raw.githubusercontent.com/vineetu/simple-host/main/deploy/install/install.sh';
  // What install.sh writes when its flag is not given (deploy/install/install.sh).
  var INSTALLER_DEFAULTS = { KEEP_VERSIONS: '1', MAX_ARCHIVE_MB: '100' };

  // Settings the basic questions cover, per product; the rest are Advanced.
  var SMALL_BASIC = ['SITE_DOMAIN', 'CONTENT_HOST', 'RESEND_API_KEY', 'MAIL_FROM', 'GOOGLE_OAUTH_CLIENT_ID', 'GOOGLE_OAUTH_CLIENT_SECRET'];
  var ENT_BASIC = ['PUBLIC_BASE_URL', 'SECURE_MODE', 'ADMIN_EMAILS', 'OIDC_ISSUER', 'OIDC_CLIENT_ID', 'OIDC_CLIENT_SECRET',
    'ALLOWED_EMAIL_DOMAINS', 'OWNER_CERTS', 'OWNER_CERT_ISSUER', 'SMTP_URL', 'SMTP_FROM', 'SESSION_SIGNING_KEY',
    'BACKUP_STORAGE_ENDPOINT', 'BACKUP_STORAGE_REGION', 'BACKUP_STORAGE_BUCKET', 'BACKUP_STORAGE_ACCESS_KEY_ID',
    'BACKUP_STORAGE_SECRET_ACCESS_KEY', 'DB_HOST', 'DB_PORT', 'DB_NAME', 'DB_USER', 'DB_PASSWORD', 'DB_APP_PASSWORD',
    'DB_SSL_ROOT_CERT', 'DB_DSN'];

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
    { id: 'upcloud', name: 'UpCloud', endpoint: 'https://YOUR-ENDPOINT.upcloudobjects.com', region: 'us-1' },
    { id: 'other', name: 'Another S3-compatible store', endpoint: '', region: 'us-east-1' }
  ];

  var S = {
    product: 'small', mode: 'basic', step: 0, area: 0,
    data: {},
    values: { small: {}, ent: {} },      // NAME -> value, only where it differs from the default
    secrets: { small: {}, ent: {} },     // NAME -> true: list it as a blank to fill in
    basic: {
      small: { domain: '', content: '', email: '', codes: true, google: false, mailFrom: '', googleId: '' },
      ent: { host: '', admins: '', idp: 'okta', issuer: IDPS[0].issuer, clientId: '', domains: '', certs: 'auto', issuerName: '',
        smtp: false, smtpFrom: '', bucket: 'aws', endpoint: BUCKETS[0].endpoint, region: BUCKETS[0].region, bucketName: '',
        creds: 'keys', dbHost: '', dbPort: '5432', dbName: 'simplehost', dbUser: 'simplehost' }
    },
    errors: {}
  };

  // ?product=enterprise or ?product=small-box (the links on the enterprise and
  // hosted pages) preselects the first choice.
  try {
    var want = new URLSearchParams(location.search).get('product');
    if (want === 'enterprise') S.product = 'ent';
    else if (want === 'small-box') S.product = 'small';
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
    var m = /^((\d+(\.\d+)?)(ns|us|µs|ms|s|m|h))+$/.exec(t);
    if (!m) return NaN;
    var unit = { ns: 1e-6, us: 1e-3, 'µs': 1e-3, ms: 1, s: 1e3, m: 6e4, h: 3.6e6 }, total = 0, re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g, p;
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

  // validate returns an error sentence, or '' when the value is acceptable.
  function validate(s, v) {
    if (/[\r\n]/.test(v)) return 'One line only.';
    if (s.type === 'int' || (s.type === 'duration' && numeric(s.min || s.max))) {
      if (!/^\d+$/.test(v)) return 'A whole number.';
      var n = parseInt(v, 10);
      if (numeric(s.min) && n < s.min) return 'At least ' + s.min + '.';
      if (numeric(s.max) && n > s.max) return 'At most ' + s.max + '.';
    } else if (s.type === 'duration') {
      var d = parseDur(v);
      if (isNaN(d)) return 'A duration like 30m, 8h or 720h (no days: write days as hours).';
      if (s.min && d < parseDur(s.min)) return 'At least ' + s.min + '.';
      if (s.max && d > parseDur(s.max)) return 'At most ' + s.max + '.';
    } else if (s.type === 'rate') {
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
  function renderChoose() {
    var err = el('p', { class: 'err', role: 'alert', hidden: true });
    app.appendChild(el('div', { class: 'card' }, [
      el('h2', { text: 'What are you setting up?' }),
      el('fieldset', null, [
        el('legend', { class: 'note', text: 'Choose one.' }),
        el('div', { class: 'choices' }, [
          choice('product', 'small', S.product, 'Small box', 'One server with Docker: self-hosting, a team or a hackathon. Runs on 1 CPU and 1 GB of RAM.', function (v) { S.product = v; }),
          choice('product', 'ent', S.product, 'Enterprise', 'Your company’s Kubernetes cluster, behind your own sign-in, with Postgres and an S3-compatible bucket.', function (v) { S.product = v; })
        ])
      ])
    ]));
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
  var HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$/;
  var EMAIL = /^[^\s@<>,"']+@[^\s@<>,"']+\.[^\s@<>,"']+$/;

  function renderBasics() {
    var box = el('div', { class: 'card' });
    var b = S.basic[S.product];
    if (S.product === 'small') {
      box.appendChild(el('h2', { text: 'Your small box' }));
      box.appendChild(el('p', { class: 'lede', text: 'Two names pointing at your server: one for Simple Host itself, one for the sites people publish. Keeping them apart means a published page can never reach the dashboard.' }));
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
          signin.appendChild(el('p', { class: 'note', text: 'With neither, only the admin key (printed once by the installer) can sign in. People you hand keys to can still publish.' }));
        }
      };
      drawSignin();
      box.appendChild(el('h3', { text: 'Sign-in and email', style: 'margin-top:26px' }));
      box.appendChild(signin);
    } else {
      box.appendChild(el('h2', { text: 'Your Enterprise install' }));
      box.appendChild(el('p', { class: 'lede', text: 'The values config.env needs. Secrets (the client secret, database passwords, keys) are left as blanks in secrets.env for you to fill in.' }));
      box.appendChild(textField('host', b, 'Address', { name: 'PUBLIC_BASE_URL', placeholder: 'sites.example.com', help: byName('PUBLIC_BASE_URL').description }));
      box.appendChild(textField('admins', b, 'Admin emails', { name: 'ADMIN_EMAILS', placeholder: 'platform@example.com, alex@example.com', help: byName('ADMIN_EMAILS').description }));
      box.appendChild(el('h3', { text: 'Sign-in (OIDC)', style: 'margin-top:26px' }));
      var idpSel = el('select', { id: uid('idp'), onchange: function () {
        b.idp = idpSel.value;
        IDPS.forEach(function (p) { if (p.id === b.idp) b.issuer = p.issuer; });
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
      var bSel = el('select', { id: uid('bucket'), onchange: function () {
        b.bucket = bSel.value;
        BUCKETS.forEach(function (p) { if (p.id === b.bucket) { b.endpoint = p.endpoint; b.region = p.region; } });
        render();
      } }, BUCKETS.map(function (p) { return el('option', { value: p.id, text: p.name }); }));
      bSel.value = b.bucket;
      box.appendChild(el('div', { class: 'field' }, [el('label', { for: uid('bucket'), text: 'Bucket provider' }), bSel]));
      box.appendChild(textField('endpoint', b, 'Bucket endpoint', { name: 'BACKUP_STORAGE_ENDPOINT', help: byName('BACKUP_STORAGE_ENDPOINT').description }));
      box.appendChild(textField('region', b, 'Region', { name: 'BACKUP_STORAGE_REGION' }));
      box.appendChild(textField('bucketName', b, 'Bucket name', { name: 'BACKUP_STORAGE_BUCKET', placeholder: 'simple-host-sites', help: byName('BACKUP_STORAGE_BUCKET').description }));
      box.appendChild(radios('creds', b, 'Bucket credentials', [['keys', 'Access keys (in secrets.env)'], ['identity', 'Workload identity (no keys)']]));
      box.appendChild(textField('dbHost', b, 'Postgres host', { name: 'DB_HOST', placeholder: 'postgres.internal.example.com', help: 'A managed Postgres with point-in-time recovery; nothing in the package backs up the database.' }));
      box.appendChild(textField('dbPort', b, 'Postgres port', { name: 'DB_PORT', type: 'number' }));
      box.appendChild(textField('dbName', b, 'Database name', { name: 'DB_NAME' }));
      box.appendChild(textField('dbUser', b, 'Owning role', { name: 'DB_USER', help: byName('DB_USER').description }));
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

  function checkBasics() {
    var b = S.basic[S.product], e = {};
    if (S.product === 'small') {
      b.domain = b.domain.toLowerCase();
      b.content = b.content.toLowerCase();
      if (!HOST.test(b.domain)) e.domain = 'A domain name like hack.example.com.';
      if (b.content && (!HOST.test(b.content) || b.content === b.domain)) e.content = 'A different hostname, like sites.' + (b.domain || 'hack.example.com') + '.';
      if (b.email && !EMAIL.test(b.email)) e.email = 'An email address, or leave it empty.';
      if (b.mailFrom && /[\r\n]/.test(b.mailFrom)) e.mailFrom = 'One line.';
      if (b.googleId && !/^[A-Za-z0-9._-]+$/.test(b.googleId)) e.googleId = 'The client ID as Google shows it.';
    } else {
      b.host = b.host.toLowerCase().replace(/^https?:\/\//, '').replace(/\/+$/, '');
      if (!HOST.test(b.host)) e.host = 'A hostname like sites.example.com.';
      if (!/^https:\/\/[^\s/]+/.test(b.issuer)) e.issuer = 'An https:// URL.';
      else if (/YOUR-/.test(b.issuer)) e.issuer = 'Replace the YOUR-… part with yours.';
      if (!b.clientId) e.clientId = 'The client ID from your identity provider.';
      if (b.admins && b.admins.split(',').some(function (a) { return !EMAIL.test(a.trim()); })) e.admins = 'Email addresses separated by commas.';
      if (!b.admins) e.admins = 'At least one admin, or nobody can approve anything.';
      if (b.idp === 'google' && !b.domains) e.domains = 'Required with Google, or anyone with a Google account could sign in.';
      if (b.certs === 'auto' && !/^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/.test(b.issuerName)) e.issuerName = 'The ClusterIssuer’s name, like internal-ca.';
      if (b.smtp && !EMAIL.test((b.smtpFrom.match(/<([^>]+)>/) || [0, b.smtpFrom])[1])) e.smtpFrom = 'An address, like Simple Host <hosting@example.com>.';
      if (!/^https:\/\/[^\s]+$/.test(b.endpoint) || /YOUR-/.test(b.endpoint)) e.endpoint = 'The bucket’s https:// endpoint.';
      if (!/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(b.bucketName)) e.bucketName = 'A bucket name.';
      if (!b.region) e.region = 'The bucket’s region.';
      if (!HOST.test(b.dbHost) && !/^\d+\.\d+\.\d+\.\d+$/.test(b.dbHost)) e.dbHost = 'The database host name.';
      if (!/^\d+$/.test(b.dbPort) || +b.dbPort < 1 || +b.dbPort > 65535) e.dbPort = '1 to 65535.';
      if (!/^[A-Za-z0-9_]+$/.test(b.dbName)) e.dbName = 'Letters, digits and _.';
      if (!/^[A-Za-z0-9_]+$/.test(b.dbUser)) e.dbUser = 'Letters, digits and _.';
    }
    S.errors = e;
    return Object.keys(e).length === 0;
  }

  // ── Step 3: every setting, area by area ──
  function advancedSettings() {
    var basic = S.product === 'small' ? SMALL_BASIC : ENT_BASIC;
    return settings().filter(function (s) {
      if (basic.indexOf(s.name) >= 0) return false;
      if (S.product === 'small' && !s.small_box) return false;
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
    var numberLike = s.type === 'int' || (s.type === 'duration' && numeric(s.min || s.max));
    var input = el('input', { id: id, type: 'text', inputmode: numberLike ? 'numeric' : null, value: valueOf(s), placeholder: s.default || '(empty)',
      autocomplete: 'off', spellcheck: 'false', 'aria-describedby': id + '-h ' + id + '-e' });
    help.id = id + '-h';
    var check = function () {
      // Empty keeps the default, as an unset variable does.
      var v = input.value.trim();
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
    DB_PASSWORD: 'the owning role’s password (openssl rand -hex 24); never the same as DB_APP_PASSWORD',
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

    if (p === 'small') {
      var content = b.content || 'sites.' + b.domain;
      var flags = ['--host', b.domain, '--content', content];
      chosen.push(['Domain', b.domain], ['Sites hostname', content]);
      if (b.email) { flags.push('--email', b.email); chosen.push(['Certificate notices', b.email]); }
      var env = [];
      extra.forEach(function (n) {
        var s = byName(n);
        if (s.install_flag) { flags.push(s.install_flag, changed[n]); }
        else env.push(envLine(n, changed[n]));
        chosen.push([n, changed[n] + '  (default ' + (defaultOf(s) || 'none') + ')']);
      });
      var fill = [];
      if (b.codes) {
        fill.push('RESEND_API_KEY');
        env.unshift(envLine('MAIL_FROM', b.mailFrom || 'Simple Host <noreply@' + b.domain + '>'));
        chosen.push(['Sign-in', 'emailed codes']);
      }
      if (b.google) {
        env.push(b.googleId ? envLine('GOOGLE_OAUTH_CLIENT_ID', b.googleId) : '# Fill in: the Google OAuth client ID\nGOOGLE_OAUTH_CLIENT_ID=');
        fill.push('GOOGLE_OAUTH_CLIENT_SECRET');
        chosen.push(['Sign-in', 'Google']);
      }
      if (!b.codes && !b.google) chosen.push(['Sign-in', 'admin key only']);
      fill = fill.concat(optionalSecrets);
      var envText = env.concat(secretBlock(fill)).join('\n');
      var cmd = 'curl -fsSL ' + INSTALL_URL + ' -o /tmp/install.sh && sudo bash /tmp/install.sh ' + flags.join(' ');
      return { chosen: chosen, cmd: cmd, env: envText ? '# Simple Host settings (https://simple-host.app/setup)\n' + envText + '\n' : '' };
    }

    var preset = [
      ['PUBLIC_BASE_URL', 'https://' + b.host], ['SECURE_MODE', 'true'],
      ['ADMIN_EMAILS', b.admins.split(',').map(function (x) { return x.trim().toLowerCase(); }).join(',')],
      ['OIDC_ISSUER', b.issuer.replace(/\/+$/, '')], ['OIDC_CLIENT_ID', b.clientId]
    ];
    if (b.domains) preset.push(['ALLOWED_EMAIL_DOMAINS', b.domains.split(',').map(function (x) { return x.trim().toLowerCase(); }).join(',')]);
    if (b.certs === 'auto') preset.push(['OWNER_CERT_ISSUER', b.issuerName]); else preset.push(['OWNER_CERTS', 'manual']);
    if (b.smtp) preset.push(['SMTP_FROM', b.smtpFrom]);
    preset.push(['DB_HOST', b.dbHost]);
    if (b.dbPort !== '5432') preset.push(['DB_PORT', b.dbPort]);
    preset.push(['DB_NAME', b.dbName], ['DB_USER', b.dbUser], ['DB_SSL_ROOT_CERT', '/etc/simple-host/db-ca/ca.crt'],
      ['BACKUP_STORAGE_ENDPOINT', b.endpoint], ['BACKUP_STORAGE_REGION', b.region], ['BACKUP_STORAGE_BUCKET', b.bucketName]);
    preset.forEach(function (kv) { cfg.push(envLine(kv[0], kv[1])); });
    extra.forEach(function (n) {
      cfg.push(envLine(n, changed[n]));
      chosen.push([n, changed[n] + '  (default ' + (byName(n).default || 'none') + ')']);
    });
    chosen.unshift(['Address', 'https://' + b.host], ['Sign-in', b.issuer], ['Bucket', b.bucketName + ' at ' + b.endpoint],
      ['Database', b.dbName + ' on ' + b.dbHost], ['Email', b.smtp ? 'SMTP relay' : 'none'],
      ['Site certificates', b.certs === 'auto' ? 'cert-manager (' + b.issuerName + ')' : 'issued by you']);
    secrets = ['OIDC_CLIENT_SECRET', 'SESSION_SIGNING_KEY', 'DB_PASSWORD', 'DB_APP_PASSWORD'];
    if (b.creds === 'keys') secrets.push('BACKUP_STORAGE_ACCESS_KEY_ID', 'BACKUP_STORAGE_SECRET_ACCESS_KEY');
    if (b.smtp) secrets.push('SMTP_URL');
    secrets = secrets.concat(optionalSecrets.filter(function (n) { return secrets.indexOf(n) < 0; }));
    return {
      chosen: chosen,
      config: '# Simple Host Enterprise: deploy/overlays/byo/config.env (https://simple-host.app/setup)\n' + cfg.join('\n') + '\n',
      secrets: '# deploy/overlays/byo/secrets.env: becomes the simple-host-secrets Secret. Never commit it.\n' + secretBlock(secrets).join('\n') + '\n'
    };
  }

  function copyButton(getText, label) {
    var btn = el('button', { class: 'btn small', type: 'button', text: label || 'Copy', onclick: function () {
      var t = getText(), done = function () { btn.textContent = 'Copied'; setTimeout(function () { btn.textContent = label || 'Copy'; }, 1500); };
      if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).then(done, function () {});
    } });
    return btn;
  }
  function downloadButton(name, getText) {
    return el('button', { class: 'btn small', type: 'button', text: 'Download', onclick: function () {
      var url = URL.createObjectURL(new Blob([getText()], { type: 'text/plain' }));
      var a = el('a', { href: url, download: name });
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
    } });
  }
  function block(title, text, file) {
    return el('div', { class: 'out' }, [
      el('h3', null, [el('span', { text: title }), el('span', { class: 'acts' }, [copyButton(function () { return text; }), file ? downloadButton(file, function () { return text; }) : null])]),
      el('pre', { text: text, tabindex: '0' })
    ]);
  }

  function renderOutput() {
    var r = build(), card = el('div', { class: 'card' });
    if (S.product === 'small') {
      card.appendChild(el('h2', { text: 'Your small box' }));
      card.appendChild(el('ol', { class: 'steps' }, [
        el('li', null, ['Point ', el('code', { text: S.basic.small.domain }), ' and ', el('code', { text: S.basic.small.content || 'sites.' + S.basic.small.domain }),
          ' at your server (an A record each), on a fresh Ubuntu server with ports 80 and 443 open.']),
        el('li', { text: 'On the server, run the install command below. It installs Docker, starts Simple Host and prints the admin key once: keep it.' }),
        r.env ? el('li', null, ['Open ', el('code', { text: 'sudo nano /opt/simple-host/.env' }), ', paste the settings at the end, fill in any blanks, save, then run ',
          el('code', { text: 'cd /opt/simple-host && sudo docker compose up -d' }), '. Re-running the installer (also how you upgrade) keeps them.']) : null
      ]));
      card.appendChild(el('div', { style: 'height:16px' }));
      card.appendChild(block('Install command', r.cmd));
      if (r.env) card.appendChild(block('Settings for /opt/simple-host/.env', r.env, 'simple-host.env'));
      else card.appendChild(el('p', { class: 'note', text: 'No settings file needed: everything else keeps its default.' }));
    } else {
      card.appendChild(el('h2', { text: 'Your Enterprise install' }));
      card.appendChild(el('ol', { class: 'steps' }, [
        el('li', null, ['Clone ', el('code', { text: 'github.com/vineetu/simple-host-enterprise' }), ' and save both files below into ', el('code', { text: 'deploy/overlays/byo/' }), '.']),
        el('li', { text: 'Fill in every blank in secrets.env (or have your External Secrets Operator create the simple-host-secrets Secret with those keys).' }),
        el('li', null, ['In the same folder, set the image digest in ', el('code', { text: 'kustomization.yaml' }), ', your address in ', el('code', { text: 'ingress-patch.yaml' }),
          ', and save your database’s CA certificate as ', el('code', { text: 'db-ca.crt' }), ' (INSTALL.md, section 5).']),
        el('li', null, ['Apply: ', el('code', { text: 'make install OVERLAY=deploy/overlays/byo' }), ' (installs cert-manager and an ingress controller if missing, checks the overlay, applies and waits). With kustomize alone: ',
          el('code', { text: 'kustomize build deploy/overlays/byo | kubectl apply -f - && kubectl -n simple-host rollout status deploy/simple-host' }), '.']),
        el('li', null, ['Changing a setting later: edit config.env, apply again, then ', el('code', { text: 'kubectl -n simple-host rollout restart deploy/simple-host' }), '.'])
      ]));
      card.appendChild(el('div', { style: 'height:16px' }));
      card.appendChild(block('config.env (the ConfigMap)', r.config, 'config.env'));
      card.appendChild(block('secrets.env (the Secret, blanks to fill in)', r.secrets, 'secrets.env'));
    }
    app.appendChild(card);
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
