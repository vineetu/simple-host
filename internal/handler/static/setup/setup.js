// The setup helper at /setup. Everything happens in this page: the settings
// lists are the two settings.json files next to this script (generated from
// each product's code, see docs/advanced/README.md), and secrets are never
// asked for, only named as blanks. Its own request is the optional
// check just before the files (POST /v1/setup/check): the product and the
// names and values of the changed numbers, durations, switches, choices and
// limits, never free text such as hostnames or emails. Where the server has no model
// backend, or the check fails or is skipped, the files are shown without it.
// The assistant (setup/assist.js, loaded only where the server offers it)
// reads and changes the form through window.shSetup, at the end.
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
  var INSTALLER_RELEASE = 'v0.7.4';
  var INSTALLER_COMMIT = '970c5afdb8b2bd105420bb5adeadabe0663da3ca';
  var INSTALLER_SHA256 = '8c579ef16eac0b5116a70e5f7d733b12411d21e16ee3e84e1d8bccaa8726fb6e';
  var INSTALL_URL = 'https://raw.githubusercontent.com/vineetu/simple-host/' + INSTALLER_COMMIT + '/deploy/install/install.sh';
  // The enterprise repo commit the cloud commands run: the line fetches
  // deploy/terraform/<cloud>/apply.sh at this commit and checks its sha256
  // before running it, and apply.sh fetches the module at the same commit, so
  // what it builds is fixed. After a release that changes deploy/terraform,
  // set all three: git rev-parse vX.Y.Z^{commit}, and
  // git show vX.Y.Z:deploy/terraform/<cloud>/apply.sh | sha256sum.
  var ENT_CLOUD_REF = '32f1920fc0b811771af9a5273ba0dd33626a1da2';
  var ENT_APPLY_SHA256 = { aws: '49b6c646280b8695445b2b1f47c4afd740f5f6827e4e2a8651981270f952acf8' };
  var ENT_RAW = 'https://raw.githubusercontent.com/vineetu/simple-host-enterprise/';
  // Where a small box is recommended to run. A referral link: the page says so.
  var UPCLOUD_SIGNUP = 'https://signup.upcloud.com/?promo=JF2WCV';
  // What install.sh writes when its flag is not given (deploy/install/install.sh).
  var INSTALLER_DEFAULTS = { KEEP_VERSIONS: '1', MAX_ARCHIVE_MB: '100' };

  // <setupBasics> Settings the basic questions cover, per product; the rest
  // are Advanced. The assistant's server keeps the same lists and provider
  // ids (setupassist.go; a Go test runs this block).
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
    { id: 'other', name: 'Another S3-compatible store', endpoint: '', region: 'us-east-1' }
  ];
  // Where Enterprise runs. A cloud here gets the quick path: one line for
  // that cloud's own browser shell, which runs the Terraform module in
  // deploy/terraform/<id> of the enterprise repo (cluster or yours, database,
  // bucket, secrets, certificates, Simple Host). Adding a cloud is one entry
  // here and its module. 'diy' (any other cluster) is the path that writes
  // config.env and secrets.env. tz picks the region preselected for the
  // visitor's time zone; the first match wins, else region. on: false keeps
  // a cloud out of the page (not shown, ?cloud= ignored) until its module has
  // been run end to end: a feature works fully or is not offered.
  var ALL_CLOUDS = [
    { id: 'aws', on: true, name: 'AWS', k8s: 'EKS', shell: 'AWS CloudShell', region: 'us-east-1', what: 'EKS, RDS and S3',
      regions: ['us-east-1', 'us-east-2', 'us-west-2', 'ca-central-1', 'sa-east-1', 'eu-west-1', 'eu-west-2', 'eu-central-1', 'eu-north-1', 'ap-south-1', 'ap-southeast-1', 'ap-southeast-2', 'ap-northeast-1'],
      tz: [['^America/(Los_Angeles|Vancouver|Tijuana|Phoenix|Denver|Boise|Edmonton)', 'us-west-2'], ['^America/(Toronto|Montreal|Halifax)', 'ca-central-1'], ['^America/(Sao_Paulo|Argentina|Santiago|Bogota|Lima|Montevideo)', 'sa-east-1'],
        ['^America/', 'us-east-1'], ['^Europe/(London|Dublin|Lisbon)', 'eu-west-2'], ['^Europe/(Stockholm|Helsinki|Oslo|Copenhagen|Tallinn|Riga|Vilnius)', 'eu-north-1'], ['^Europe/', 'eu-central-1'],
        ['^Asia/(Kolkata|Calcutta|Colombo|Kathmandu|Dhaka|Karachi)', 'ap-south-1'], ['^Asia/(Tokyo|Seoul)', 'ap-northeast-1'], ['^(Australia/|Pacific/Auckland)', 'ap-southeast-2'], ['^Asia/', 'ap-southeast-1']] },
    { id: 'azure', on: false, name: 'Azure', k8s: 'AKS', shell: 'Azure Cloud Shell', region: 'eastus', what: 'AKS, Azure Database for PostgreSQL and Blob Storage',
      regions: ['eastus', 'eastus2', 'centralus', 'westus2', 'westus3', 'canadacentral', 'brazilsouth', 'northeurope', 'westeurope', 'uksouth', 'germanywestcentral', 'swedencentral', 'centralindia', 'southeastasia', 'japaneast', 'australiaeast'],
      tz: [['^America/(Los_Angeles|Vancouver|Tijuana|Phoenix|Denver|Boise|Edmonton)', 'westus2'], ['^America/(Toronto|Montreal|Halifax)', 'canadacentral'], ['^America/(Sao_Paulo|Argentina|Santiago|Bogota|Lima|Montevideo)', 'brazilsouth'],
        ['^America/', 'eastus'], ['^Europe/(London|Dublin|Lisbon)', 'uksouth'], ['^Europe/(Stockholm|Helsinki|Oslo|Copenhagen|Tallinn|Riga|Vilnius)', 'swedencentral'], ['^Europe/(Berlin|Zurich|Vienna)', 'germanywestcentral'], ['^Europe/', 'westeurope'],
        ['^Asia/(Kolkata|Calcutta|Colombo|Kathmandu|Dhaka|Karachi)', 'centralindia'], ['^Asia/(Tokyo|Seoul)', 'japaneast'], ['^(Australia/|Pacific/Auckland)', 'australiaeast'], ['^Asia/', 'southeastasia']] }
  ];
  var CLOUDS = ALL_CLOUDS.filter(function (c) { return c.on; });
  // </setupBasics>

  // presetOf: v is empty or one of the list's own template values, so
  // picking another provider may replace it; anything the person typed stays.
  function presetOf(list, key, v) {
    return !v || list.some(function (p) { return p[key] === v; });
  }
  // pickIdp and pickBucket answer the provider questions, from the list or
  // the assistant: the template issuer, endpoint and region follow only
  // where the person has not typed their own. With UpCloud the Postgres port
  // becomes UpCloud's managed Postgres port, 11569, while it is still 5432
  // (and goes back when another provider is picked).
  function pickIdp(b, id) {
    b.idp = id;
    IDPS.forEach(function (p) { if (p.id === id && presetOf(IDPS, 'issuer', b.issuer)) b.issuer = p.issuer; });
  }
  // cloudById is the entry for a cloud id; null for 'diy'.
  function cloudById(id) {
    for (var i = 0; i < CLOUDS.length; i++) if (CLOUDS[i].id === id) return CLOUDS[i];
    return null;
  }
  // guessRegion is the cloud's region nearest the visitor's time zone.
  function guessRegion(c) {
    var tz = '';
    try { tz = Intl.DateTimeFormat().resolvedOptions().timeZone || ''; } catch (e) { /* keep the default */ }
    for (var i = 0; i < c.tz.length; i++) if (new RegExp(c.tz[i][0]).test(tz)) return c.tz[i][1];
    return c.region;
  }
  // pickCloud answers "Where will it run?": a cloud's region list replaces the
  // region unless it is already one of that cloud's.
  function pickCloud(b, id) {
    b.cloud = id;
    var c = cloudById(id);
    if (c && c.regions.indexOf(b.cloudRegion) < 0) b.cloudRegion = guessRegion(c);
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

  var S = {
    product: 'small', mode: 'basic', step: 0, area: 0,
    data: {},
    values: { small: {}, ent: {} },      // NAME -> value, only where it differs from the default
    secrets: { small: {}, ent: {} },     // NAME -> true: list it as a blank to fill in
    basic: {
      small: { where: 'upcloud', domain: '', content: '', email: '', codes: true, google: false, mailFrom: '', googleId: '' },
      ent: { host: '', admins: '', idp: 'okta', issuer: IDPS[0].issuer, clientId: '', domains: '', certs: 'auto', issuerName: '',
        smtp: false, smtpFrom: '', bucket: 'aws', endpoint: BUCKETS[0].endpoint, region: BUCKETS[0].region, bucketName: '',
        creds: 'keys', dbHost: '', dbPort: '5432', dbName: 'simplehost', dbUser: 'simplehost', proxies: '',
        cloud: 'diy', cluster: 'no', cloudRegion: '', clusterName: '' }
    },
    errors: {},
    // The check: key is the request it answered (so the same choices are not
    // checked twice), state running / review / done, note shown above the files.
    check: { key: '', state: '', findings: [], note: '', seq: 0 }
  };

  // ?product=enterprise or ?product=small-box (the links on the enterprise and
  // hosted pages) preselects the first choice.
  try {
    var want = new URLSearchParams(location.search).get('product');
    if (want === 'enterprise') S.product = 'ent';
    else if (want === 'small-box') S.product = 'small';
    // ?product=enterprise&cloud=aws (the links on the enterprise page)
    // preselects where it runs too.
    var wantCloud = new URLSearchParams(location.search).get('cloud');
    if (S.product === 'ent' && cloudById(wantCloud)) pickCloud(S.basic.ent, wantCloud);
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
  // text and secrets). The same as kind() in internal/handler/setupcheck.go;
  // a Go test runs this block against both settings lists.
  function setupKind(s) {
    if (s.type === 'int') return 'number';
    if (s.type === 'duration') return (numeric(s.min) || numeric(s.max) || /^\d+$/.test(s.default)) ? 'number' : 'duration';
    if (s.type === 'rate') return 'rate';
    if (s.type === 'bool' || s.type === 'enum') return 'choice';
    return 'text';
  }
  // </setupKind>

  // validate returns an error sentence, or '' when the value is acceptable.
  function validate(s, v) {
    var k = setupKind(s);
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

  // cloud is the chosen cloud when Enterprise runs on one (the quick path),
  // else null.
  function cloud() { return S.product === 'ent' ? cloudById(S.basic.ent.cloud) : null; }

  // ── Progress ──
  function renderProgress() {
    var lis = document.querySelectorAll('#progress li');
    var steps = S.mode === 'advanced' ? [0, 1, 2, 3] : [0, 1, 3];
    for (var i = 0; i < lis.length; i++) {
      lis[i].className = (i === S.step ? 'on' : (i < S.step ? 'done' : ''));
      lis[i].hidden = steps.indexOf(i) < 0;
    }
    var last = lis.length && lis[lis.length - 1].querySelector('.n');
    if (last) last.textContent = cloud() ? 'Your commands' : 'Your files';
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
    // Leaving the files step stops a check still running: its answer would
    // be for choices that may change.
    if (S.step !== 3 && S.check.state === 'running') stopCheck();
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
      // The quick path has no Basic/Advanced question: Advanced is an optional
      // link on its Basics step.
      if (cloud()) S.mode = 'basic';
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
          choice('product', 'ent', S.product, 'Enterprise', 'Your company’s Kubernetes cluster, behind your own sign-in, with Postgres and a bucket.', redrawChoose('product'))
        ])
      ])
    ]));
    var b = S.basic.ent, c = cloud();
    if (S.product === 'ent') {
      app.appendChild(el('div', { class: 'card', id: 'where' }, [
        el('h2', { text: 'Where will it run?' }),
        el('fieldset', null, [
          el('legend', { class: 'note', text: 'On ' + CLOUDS.map(function (x) { return x.name; }).join(' or ') + ', one command in the cloud’s own browser shell sets up everything in your account.' }),
          el('div', { class: 'choices' }, CLOUDS.map(function (x) {
            return choice('cloud', x.id, b.cloud, x.name, x.what + ', set up by one command in ' + x.shell + '.', redrawChoose('cloud', function (v) { pickCloud(b, v); }));
          }).concat([
            choice('cloud', 'diy', b.cloud, 'Something else / I’ll do it myself', 'Any Kubernetes cluster with your own Postgres and bucket. You get config.env and secrets.env to apply.', redrawChoose('cloud', function (v) { pickCloud(b, v); }))
          ]))
        ])
      ]));
    }
    if (c) {
      app.appendChild(el('div', { class: 'card', id: 'cluster' }, [
        el('h2', { text: 'Do you already have a Kubernetes cluster on ' + c.name + '?' }),
        el('fieldset', null, [
          el('legend', { class: 'note', text: 'Either way you get one command.' }),
          el('div', { class: 'choices' }, [
            choice('cluster', 'yes', b.cluster, 'Yes', 'Install into my ' + c.k8s + ' cluster. It needs cluster-admin access from ' + c.shell + '.', function (v) { b.cluster = v; }),
            choice('cluster', 'no', b.cluster, 'No', 'Create one for it: a small ' + c.k8s + ' cluster with 2 nodes.', function (v) { b.cluster = v; })
          ])
        ])
      ]));
    } else {
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
    }
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
  var EMAIL = /^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$/;
  // A proxy address or range for TRUSTED_PROXY_CIDRS (the server checks it exactly at start).
  var CIDR = /^(\d{1,3}(\.\d{1,3}){3}(\/([0-9]|[12][0-9]|3[0-2]))?|[0-9A-Fa-f]*:[0-9A-Fa-f:.]*(\/([0-9]{1,2}|1[01][0-9]|12[0-8]))?)$/;
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
  var ISSUER = /^https:\/\/[^\s\/"\\?#@]+(\/[^\s"\\?#]*)?$/;
  function renderCloudBasics(c) {
    var b = S.basic.ent, box = el('div', { class: 'card' });
    // The Basic path's template issuer (YOUR-ORG) is not an answer here.
    if (/YOUR-/.test(b.issuer) && presetOf(IDPS, 'issuer', b.issuer)) b.issuer = '';
    box.appendChild(el('h2', { text: 'Simple Host Enterprise on ' + c.name }));
    box.appendChild(el('p', { class: 'lede', text: 'Everything else is set up with its default. Secrets are made in your ' + c.name + ' account; this page never sees one.' }));
    box.appendChild(textField('host', b, 'Address', { name: 'PUBLIC_BASE_URL', placeholder: 'sites.example.com',
      help: 'Where Simple Host lives. Each person gets <name>.<address> and each site its own name under that, so use a domain of its own (like example-sites.com) or a name under one, not your company’s main domain.' }));
    box.appendChild(textField('admins', b, 'Admin emails', { name: 'ADMIN_EMAILS', placeholder: 'platform@example.com, alex@example.com', help: byName('ADMIN_EMAILS').description }));
    box.appendChild(el('h3', { text: 'Sign-in', style: 'margin-top:26px' }));
    box.appendChild(el('p', { class: 'note', style: 'margin:0 0 14px' }, ['Register a web application at your identity provider with the redirect URI ',
      el('code', { text: 'https://' + (b.host || '<address>') + '/auth/callback' }), '.']));
    box.appendChild(textField('issuer', b, 'Issuer URL', { name: 'OIDC_ISSUER', placeholder: 'https://your-org.okta.com', help: ISSUER_HELP,
      oninput: function () { var g = isGoogle(b.issuer); if (g !== !!b.shownDomains) { b.shownDomains = g; render(); var i = document.getElementById(uid('issuer')); if (i) { i.focus(); i.setSelectionRange(i.value.length, i.value.length); } } } }));
    box.appendChild(textField('clientId', b, 'Client ID', { name: 'OIDC_CLIENT_ID' }));
    if (isGoogle(b.issuer)) {
      b.shownDomains = true;
      if (!b.domains && b.admins) b.domains = b.admins.split(',').map(function (a) { return (a.split('@')[1] || '').trim().toLowerCase(); }).filter(function (d, i, all) { return d && all.indexOf(d) === i; }).join(',');
      box.appendChild(textField('domains', b, 'Company email domains', { name: 'ALLOWED_EMAIL_DOMAINS', placeholder: 'example.com', help: 'Required with Google: without it, anyone with a Google account could sign in.' }));
    }
    box.appendChild(el('p', { class: 'secret', text: 'The client secret is not asked for here. You type it into ' + c.shell + ' when the command asks, and it goes straight into your cloud’s secret store.' }));
    box.appendChild(el('h3', { text: 'Where', style: 'margin-top:26px' }));
    var rSel = el('select', { id: uid('cloudRegion'), onchange: function () { b.cloudRegion = rSel.value; } }, c.regions.map(function (r) { return el('option', { value: r, text: r }); }));
    if (c.regions.indexOf(b.cloudRegion) < 0) b.cloudRegion = guessRegion(c);
    rSel.value = b.cloudRegion;
    box.appendChild(el('div', { class: 'field' }, [el('label', { for: uid('cloudRegion'), text: 'Region' }),
      el('p', { class: 'help', text: 'Where the cluster, database and bucket live. Picked from your time zone; any region works.' }), rSel]));
    if (b.cluster === 'yes') {
      box.appendChild(textField('clusterName', b, 'Your ' + c.k8s + ' cluster’s name', { placeholder: c.id === 'aws' ? 'prod-eks' : 'prod-aks',
        help: c.id === 'aws' ? 'As aws eks list-clusters shows it, in the region above.' : 'As az aks list -o table shows it, in the region above.' }));
    }
    app.appendChild(box);
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () { S.errors = {}; go(0); } }),
      el('span', { class: 'row' }, [
        el('button', { class: 'btn', type: 'button', text: 'More settings (optional)', onclick: function () {
          if (!checkBasics()) { render(); var bad = app.querySelector('.bad'); if (bad) bad.focus(); return; }
          S.mode = 'advanced'; S.area = 0; go(2);
        } }),
        el('button', { class: 'btn solid', type: 'button', text: 'Show my commands', onclick: function () {
          if (!checkBasics()) { render(); var bad = app.querySelector('.bad'); if (bad) bad.focus(); return; }
          go(3);
        } })
      ])
    ]));
  }

  function renderBasics() {
    if (cloud()) { renderCloudBasics(cloud()); return; }
    var box = el('div', { class: 'card' });
    var b = S.basic[S.product];
    if (S.product === 'small') {
      app.appendChild(renderWhere(b));
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
          signin.appendChild(el('p', { class: 'note', text: 'With neither, only the admin key (the installer prints it; it is kept in /opt/simple-host/.env on the server) can sign in, at /admin. People you hand keys to can still publish.' }));
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
      box.appendChild(textField('region', b, 'Region', { name: 'BACKUP_STORAGE_REGION', placeholder: upcloud ? 'europe-2' : '',
        help: upcloud ? 'The Object Storage service’s region, as in its endpoint (such as europe-2).' : 'The region the bucket lives in.' }));
      box.appendChild(textField('bucketName', b, 'Bucket name', { name: 'BACKUP_STORAGE_BUCKET', placeholder: 'simple-host-sites', help: byName('BACKUP_STORAGE_BUCKET').description }));
      box.appendChild(radios('creds', b, 'Bucket credentials', [['keys', 'Access keys (in secrets.env)'], ['identity', 'Workload identity (no keys)']]));
      box.appendChild(textField('dbHost', b, 'Postgres host', { name: 'DB_HOST', placeholder: upcloud ? 'public-….db.upclouddatabases.com' : 'postgres.internal.example.com',
        help: 'A managed Postgres with point-in-time recovery; nothing in the package backs up the database.' +
          (upcloud ? ' On UpCloud’s managed Postgres, use the public-… hostname (the component whose route is public): the plain one resolves to a private address from outside UpCloud.' : '') }));
      box.appendChild(textField('dbPort', b, 'Postgres port', { name: 'DB_PORT', type: 'number',
        help: upcloud ? 'UpCloud’s managed Postgres listens on 11569, not 5432 (filled in when you picked UpCloud).' : null }));
      box.appendChild(textField('dbName', b, 'Database name', { name: 'DB_NAME' }));
      box.appendChild(textField('dbUser', b, 'Owning role', { name: 'DB_USER', help: byName('DB_USER').description }));
      box.appendChild(el('h3', { text: 'Ingress', style: 'margin-top:26px' }));
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
      el('button', { class: 'btn solid', type: 'button', text: S.mode === 'advanced' ? 'Next: every setting' : (cloud() ? 'Show my commands' : 'Show my files'), onclick: function () {
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
      if (b.mailFrom && /[\r\n`]/.test(b.mailFrom)) e.mailFrom = 'One line, with no backticks.';
      if (b.googleId && !/^[A-Za-z0-9._-]+$/.test(b.googleId)) e.googleId = 'The client ID as Google shows it.';
    } else if (cloud()) {
      var c = cloud();
      b.host = b.host.toLowerCase().replace(/^https?:\/\//, '').replace(/\/+$/, '');
      if (!HOST.test(b.host)) e.host = 'A hostname like sites.example.com.';
      // The typed value is checked as typed; only a value that passes is
      // normalised (host case, :443, trailing slashes).
      if (!ISSUER.test(b.issuer) || b.issuer.length > 2048) e.issuer = 'An https:// URL, with no spaces.';
      else {
        b.issuer = normIssuer(b.issuer);
        if (/YOUR-/.test(b.issuer)) e.issuer = 'Replace the YOUR-… part with yours.';
        else if (isGoogle(b.issuer) && b.issuer !== 'https://accounts.google.com') e.issuer = 'Google’s issuer is exactly https://accounts.google.com.';
      }
      if (!b.clientId) e.clientId = 'The client ID from your identity provider.';
      else if (/[\s"\\]/.test(b.clientId) || b.clientId.length > 512) e.clientId = 'The client ID as your identity provider shows it.';
      if (b.admins && b.admins.split(',').some(function (a) { return !EMAIL.test(a.trim()); })) e.admins = 'Email addresses separated by commas.';
      if (!b.admins) e.admins = 'At least one admin, or nobody can approve anything.';
      if (isGoogle(b.issuer)) {
        b.domains = b.domains.split(',').map(function (x) { return x.trim().toLowerCase(); }).filter(Boolean).join(',');
        if (!b.domains) e.domains = 'Required with Google, or anyone with a Google account could sign in.';
        else if (!b.domains.split(',').every(function (d) { return HOST.test(d); })) e.domains = 'Domains like example.com, separated by commas.';
      }
      if (c.regions.indexOf(b.cloudRegion) < 0) e.cloudRegion = 'Pick a region.';
      if (b.cluster === 'yes' && !(c.id === 'aws' ? /^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$/ : /^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$/).test(b.clusterName)) e.clusterName = 'The cluster’s name: letters, digits, - and _.';
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
      b.proxies = b.proxies.split(',').map(function (x) { return x.trim(); }).filter(Boolean).join(',');
      if (b.proxies && !b.proxies.split(',').every(function (x) { return CIDR.test(x); })) e.proxies = 'Ranges like 192.168.0.0/16, separated by commas.';
    }
    S.errors = e;
    return Object.keys(e).length === 0;
  }

  // ── Step 3: every setting, area by area ──
  // Settings the quick path's module sets itself (the Service's ports, the
  // Ingress it creates, verified TLS to the database).
  var QUICK_FIXED = ['PORT', 'HTTPS_REDIRECT_PORT', 'OWNER_INGRESS_TEMPLATE', 'DB_SSLMODE', 'DB_INSECURE_ALLOWED', 'BACKUP_STORAGE_INSECURE_ALLOWED', 'OIDC_INSECURE_ALLOWED', 'DB_INCLUSTER_EVALUATION', 'BACKUP_SSE', 'BACKUP_SSE_KEY_ID'];
  function advancedSettings() {
    var basic = S.product === 'small' ? SMALL_BASIC : ENT_BASIC, quick = !!cloud();
    return settings().filter(function (s) {
      if (basic.indexOf(s.name) >= 0) return false;
      if (S.product === 'small' && !s.small_box) return false;
      // On the quick path secrets live in the cloud's secret store, not in a
      // file with blanks: only plain settings are offered.
      if (quick && (s.type === 'secret' || QUICK_FIXED.indexOf(s.name) >= 0)) return false;
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
        S.area < list.length - 1 ? el('button', { class: 'btn', type: 'button', text: cloud() ? 'Skip to my commands' : 'Skip to my files', onclick: function () { if (!bad()) go(3); else bad().focus(); } }) : null,
        el('button', { class: 'btn solid', type: 'button', text: S.area < list.length - 1 ? 'Next: ' + list[S.area + 1].group.name : (cloud() ? 'Show my commands' : 'Show my files'), onclick: function () {
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
      var flags = ['--host', sh(b.domain), '--content', sh(content)];
      chosen.push(['Where it runs', targetOf('small').name], ['Domain', b.domain], ['Sites hostname', content]);
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
      var cmd = 'f=$(mktemp) && curl -fsSL ' + INSTALL_URL + ' -o "$f" && printf \'%s  %s\\n\' ' + INSTALLER_SHA256 + ' "$f" | sha256sum -c --quiet - && sudo bash "$f" ' + flags.join(' ');
      return { chosen: chosen, cmd: cmd, env: envText ? '# Simple Host settings (https://simple-host.app/setup)\n' + envText + '\n' : '' };
    }

    // The keys INSTALL.md says to leave as in config.env.example: written
    // here with their values (the default unless changed in Advanced), so
    // the file needs nothing from the example.
    var asExample = function (n) { var s = byName(n); return [n, s ? valueOf(s) : '']; };
    var preset = [
      ['PUBLIC_BASE_URL', 'https://' + b.host], ['SECURE_MODE', 'true'], asExample('PORT'), asExample('HTTPS_REDIRECT_PORT'),
      ['ADMIN_EMAILS', b.admins.split(',').map(function (x) { return x.trim().toLowerCase(); }).join(',')],
      ['OIDC_ISSUER', b.issuer.replace(/\/+$/, '')], ['OIDC_CLIENT_ID', b.clientId], asExample('OIDC_SCOPES')
    ];
    if (b.domains) preset.push(['ALLOWED_EMAIL_DOMAINS', b.domains.split(',').map(function (x) { return x.trim().toLowerCase(); }).join(',')]);
    preset.push(asExample('SESSION_TTL'), asExample('SESSION_IDLE'));
    if (b.certs === 'auto') preset.push(['OWNER_CERT_ISSUER', b.issuerName]); else preset.push(['OWNER_CERTS', 'manual']);
    if (b.smtp) preset.push(['SMTP_FROM', b.smtpFrom]);
    if (b.proxies) preset.push(['TRUSTED_PROXY_CIDRS', b.proxies]);
    preset.push(['DB_HOST', b.dbHost]);
    if (b.dbPort !== '5432') preset.push(['DB_PORT', b.dbPort]);
    preset.push(['DB_NAME', b.dbName], ['DB_USER', b.dbUser], asExample('DB_SSLMODE'), ['DB_SSL_ROOT_CERT', '/etc/simple-host/db-ca/ca.crt'],
      ['BACKUP_STORAGE_ENDPOINT', b.endpoint], ['BACKUP_STORAGE_REGION', b.region], ['BACKUP_STORAGE_BUCKET', b.bucketName],
      asExample('BACKUP_STORAGE_PREFIX'), asExample('BACKUP_SSE'));
    var written = {};
    preset.forEach(function (kv) { written[kv[0]] = true; cfg.push(envLine(kv[0], kv[1])); });
    extra.forEach(function (n) {
      if (!written[n]) cfg.push(envLine(n, changed[n]));
      chosen.push([n, changed[n] + '  (default ' + (byName(n).default || 'none') + ')']);
    });
    chosen.unshift(['Address', 'https://' + b.host], ['Sign-in', b.issuer], ['Bucket', b.bucketName + ' at ' + b.endpoint],
      ['Database', b.dbName + ' on ' + b.dbHost], ['Email', b.smtp ? 'SMTP relay' : 'none'],
      ['Site certificates', b.certs === 'auto' ? 'cert-manager (' + b.issuerName + ')' : 'issued by you']);
    secrets = ['OIDC_CLIENT_SECRET', 'SESSION_SIGNING_KEY', 'DB_PASSWORD', 'DB_APP_PASSWORD', 'BACKUP_ENVELOPE_KEY'];
    if (b.creds === 'keys') secrets.push('BACKUP_STORAGE_ACCESS_KEY_ID', 'BACKUP_STORAGE_SECRET_ACCESS_KEY');
    if (b.smtp) secrets.push('SMTP_URL');
    secrets = secrets.concat(optionalSecrets.filter(function (n) { return secrets.indexOf(n) < 0; }));
    return {
      chosen: chosen,
      config: '# Simple Host Enterprise: deploy/overlays/byo/config.env (https://simple-host.app/setup)\n' +
        '# Complete as it is: anything not listed keeps its default (docs/configuration.md); nothing else from config.env.example is needed.\n' + cfg.join('\n') + '\n',
      secrets: '# deploy/overlays/byo/secrets.env: becomes the simple-host-secrets Secret. Never commit it.\n' + secretBlock(secrets).join('\n') + '\n'
    };
  }

  // ── The quick path's output: terraform.tfvars and the one line ──
  // hcl writes a Terraform string.
  function hcl(v) {
    // A function as the replacement: in a string, "$$" would mean one "$".
    return '"' + String(v).replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\$\{/g, function () { return '$${'; }).replace(/%\{/g, function () { return '%%{'; }) + '"';
  }
  function list(v) { return v.split(',').map(function (x) { return x.trim().toLowerCase(); }).filter(Boolean); }
  function buildCloud(c) {
    var b = S.basic.ent, changed = S.values.ent, adv = advancedSettings().map(function (s) { return s.name; });
    var extra = Object.keys(changed).filter(function (n) { return adv.indexOf(n) >= 0; }).sort(function (x, y) { return adv.indexOf(x) - adv.indexOf(y); });
    var issuer = normIssuer(b.issuer), rows = [['create_cluster', b.cluster === 'no' ? 'true' : 'false']];
    if (b.cluster === 'yes') rows.push(['cluster_name', hcl(b.clusterName)]);
    rows.push(['region', hcl(b.cloudRegion)], ['base_domain', hcl(b.host)], ['admin_emails', '[' + list(b.admins).map(hcl).join(', ') + ']']);
    if (isGoogle(issuer)) rows.push(['allowed_email_domains', '[' + list(b.domains).map(hcl).join(', ') + ']']);
    rows.push(['oidc_issuer', hcl(issuer)], ['oidc_client_id', hcl(b.clientId)]);
    var w = 0;
    rows.forEach(function (r) { w = Math.max(w, r[0].length); });
    var L = ['# Simple Host Enterprise on ' + c.name + ': terraform.tfvars for deploy/terraform/' + c.id + ' (https://simple-host.app/setup)',
      '# No secrets: the client secret is typed in the shell, the rest are generated in your account.'];
    rows.forEach(function (r) { L.push(r[0] + new Array(w - r[0].length + 2).join(' ') + '= ' + r[1]); });
    var chosen = [['Where it runs', c.name + ', ' + b.cloudRegion], ['Cluster', b.cluster === 'yes' ? b.clusterName + ' (yours)' : 'a new ' + c.k8s + ' cluster'],
      ['Address', 'https://' + b.host], ['Sign-in', issuer]];
    if (extra.length) {
      L.push('extra_config = {');
      extra.forEach(function (n) {
        L.push('  ' + n + ' = ' + hcl(changed[n]));
        chosen.push([n, changed[n] + '  (default ' + (byName(n).default || 'none') + ')']);
      });
      L.push('}');
    }
    var tfvars = L.join('\n') + '\n';
    var b64 = btoa(unescape(encodeURIComponent(tfvars)));
    var cmd = 'f=$(mktemp) && curl -fsSL ' + ENT_RAW + ENT_CLOUD_REF + '/deploy/terraform/' + c.id + '/apply.sh -o "$f" && printf \'%s  %s\\n\' ' +
      ENT_APPLY_SHA256[c.id] + ' "$f" | sha256sum -c --quiet - && bash "$f" --ref ' + ENT_CLOUD_REF + ' --tfvars ' + b64;
    return { chosen: chosen, tfvars: tfvars, cmd: cmd };
  }
  var SECRET_CREDS = "printf 'Client secret: '; trap 'stty echo 2>/dev/null' INT; read -rs TF_VAR_oidc_client_secret; trap - INT; echo; export TF_VAR_oidc_client_secret";
  function shellUrl(c, region) {
    return c.id === 'aws' ? 'https://console.aws.amazon.com/cloudshell/home?region=' + region : 'https://shell.azure.com/bash';
  }
  // cloudHandoff is the block for an AI agent that works in a shell signed in
  // to the cloud: the same line, with the plan shown to the person first.
  function cloudHandoff(r, c) {
    var b = S.basic.ent, origin = location.origin, L = [];
    var who = c.id === 'aws' ? 'aws sts get-caller-identity' : 'az account show --query "{subscription:name, id:id, user:user.name}"';
    L.push('# Set up Simple Host Enterprise on ' + c.name, '');
    L.push('These are instructions for you, my AI agent, from ' + origin + '/setup. Work through them in order. Stop at the first step that fails and show me its exact error. Never print, log or commit a secret.', '');
    L.push('Where it runs: ' + c.name + ', region ' + b.cloudRegion + ', ' + (b.cluster === 'yes' ? 'my existing ' + c.k8s + ' cluster ' + b.clusterName + ' (I have cluster-admin access to it)' : 'a new ' + c.k8s + ' cluster that the command creates') + '. The command runs Terraform (the module deploy/terraform/' + c.id + ' of github.com/vineetu/simple-host-enterprise) and keeps its state in my own account.', '');
    L.push('1. Check this shell is signed in to the right account: `' + who + '`. Tell me the account and ask me to confirm it before going on.', '');
    L.push('2. The sign-in app’s client secret: before starting you I set it in this terminal as TF_VAR_oidc_client_secret. Check with `test -n "$TF_VAR_oidc_client_secret" && echo set`. If it is not set, stop and ask me to quit you, run this in the terminal and start you again (a re-run after a first successful one does not need it: the stored secret is kept):', '', FENCE + 'sh', SECRET_CREDS, FENCE, '');
    L.push('3. See what it will create: run this line with ` --plan` added at the end. It installs Terraform if it is missing, fetches the module at a pinned commit after checking its checksum, and prints the plan. Tell me how many resources it adds and ask me before going on.', '', FENCE + 'sh', r.cmd, FENCE, '');
    L.push('4. Apply: after I say yes, run the same line with ` --yes` added at the end. ' + (b.cluster === 'yes' ? 'A first install takes about 15 minutes' : 'A first install takes about 25 minutes (the cluster is most of it)') + '; on an install that exists it applies only the changes. If it stops, running the same line again picks up where it stopped. In ' + c.shell + ' the shell closes after about 20 minutes without a key press, which stops the work: remind me to press Enter in it every 10 minutes or so.', '');
    L.push('5. DNS: the first numbered step of its final output, "1. DNS:", either lists NS records for ' + b.host + ' to add (in the DNS zone of the domain above it, or as the name servers at the registrar if ' + b.host + ' is a domain of its own): tell me exactly which records to add and where, and wait for me; then check that `dig +short NS ' + sh(b.host) + '` prints them. Or it says ' + b.host + ' is already a Route 53 zone in this account, with nothing to add.', '');
    L.push('6. Check it works (certificates can take a few minutes after the DNS change):');
    L.push('   - `curl -fsS ' + sh('https://' + b.host + '/readyz') + '` prints {"status":"ok"}.');
    L.push('   - `curl -sS -o /dev/null -w \'%{http_code}\\n\' ' + sh('https://install-check.' + b.host + '/healthz') + '` prints a status code (401 or 404 is fine; a TLS or DNS error is not).', '');
    L.push('7. Tell me: sign in at https://' + b.host + '/auth/login with an admin email (' + list(b.admins).join(', ') + '), and where the generated secrets are kept (the secrets_location output). The envelope key there is the only way to read the sites: it must be backed up. Then remind me to close this terminal (or run `unset TF_VAR_oidc_client_secret`).', '');
    L.push(window.shSetupAssist
      ? 'If anything fails and the output does not tell you how to fix it, tell me: I can paste the error at ' + origin + '/setup?product=enterprise&cloud=' + c.id + '#help for help.'
      : 'If anything fails and the output does not tell you how to fix it, tell me and show me the error.');
    return L.join('\n') + '\n';
  }
  function renderCloudOutput(c) {
    var b = S.basic.ent, r = buildCloud(c);
    if (S.check.note) app.appendChild(el('p', { class: 'check-note', role: 'status', text: S.check.note }));
    app.appendChild(el('div', { class: 'card', id: 'commands' }, [
      el('h2', { text: 'Set it up on ' + c.name }),
      el('ol', { class: 'steps' }, [
        el('li', null, ['Open ', el('a', { href: shellUrl(c, b.cloudRegion), target: '_blank', rel: 'noopener', text: c.shell }),
          ' in the account it should run in, and paste this line. It asks for your sign-in app’s client secret (not shown as you type), shows what it will create, and waits for you to type yes. It takes about ' +
          (b.cluster === 'yes' ? '15' : '25') + ' minutes: keep the tab open, stay with it until it asks you to type yes, then press Enter every 10 minutes or so, because ' + c.shell + ' closes after about 20 minutes without a key press. If it closes, open it again and paste the same line: it picks up where it stopped. To change an install you already have, paste its new line the same way: it applies only the changes.'])
      ]),
      block('Paste into ' + c.shell, r.cmd),
      el('ol', { class: 'steps', start: '2' }, [
        el('li', null, ['DNS. Its final output starts with the records to add for ', el('code', { text: b.host }), '. Usually these are 4 NS records. If ', el('code', { text: b.host }),
          ' is under a domain you already manage (like sites.example.com under example.com), add them in that domain’s DNS zone. If it is a domain of its own (like example-sites.com), set them as its name servers at your registrar instead. If ',
          el('code', { text: b.host }), ' is already a Route 53 zone in this account, it says so and there is nothing to add. Certificates follow by themselves within minutes.']),
        el('li', null, ['Check it: this prints ', el('code', { text: '{"status":"ok"}' }), '. Then sign in at ',
          el('a', { href: 'https://' + b.host + '/auth/login', text: 'https://' + b.host + '/auth/login' }), ' with an admin email.'])
      ]),
      block('Check', 'curl -fsS ' + sh('https://' + b.host + '/readyz'))
    ]));
    app.appendChild(el('div', { class: 'card' }, [
      el('h2', { text: 'What you chose' }),
      el('ul', { class: 'summary' }, r.chosen.map(function (x) { return el('li', null, [el('span', { text: x[0] }), el('span', { text: x[1] })]); })),
      el('p', { class: 'note', text: 'Everything not listed keeps its default. Every setting is explained in the advanced settings docs.' })
    ]));
    app.appendChild(el('details', { class: 'card more', id: 'other-ways' }, [
      el('summary', null, [el('b', { text: 'Other ways to run this' }), el('span', { text: 'An AI agent, or the Terraform files directly.' })]),
      el('div', { id: 'agent' }, [
        el('h3', { text: 'With your AI agent', style: 'margin-top:18px' }),
        el('p', { class: 'note', style: 'margin:0 0 4px', text: 'In a Linux terminal (or ' + c.shell + ') signed in to ' + c.name + ', copy this into your AI agent. It shows you the plan before creating anything, and never sees the client secret.' }),
        block('For your AI agent', cloudHandoff(r, c), 'simple-host-setup.md')
      ]),
      el('div', { id: 'tfvars' }, [
        el('h3', { text: 'With your own Terraform', style: 'margin-top:18px' }),
        el('p', { class: 'note', style: 'margin:0 0 4px' }, ['The module is ', el('code', { text: 'deploy/terraform/' + c.id }),
          ' in github.com/vineetu/simple-host-enterprise at commit ', el('code', { text: ENT_CLOUD_REF.slice(0, 12) }), '; its README says how to run it from a pipeline. These are your answers:']),
        block('terraform.tfvars', r.tfvars, 'terraform.tfvars')
      ])
    ]));
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () {
        if (S.mode === 'advanced') { S.area = areas().length - 1; go(2); } else go(1);
      } }),
      el('button', { class: 'btn', type: 'button', text: 'Start over', onclick: function () { location.reload(); } })
    ]));
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
    ent: [{ id: 'kubernetes', name: 'Kubernetes', needs: 'the company’s Kubernetes cluster (1.30 or later) with an ingress controller and cert-manager, a managed Postgres with point-in-time recovery, and an S3-compatible bucket with versioning on' }]
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
    L.push('These are instructions for you, my AI agent, from ' + origin + '/setup. Work through them in order. Stop at the first step that fails and show me its exact error. Never print, log or commit a secret: ask me for each secret value and put it straight into the file.', '');
    L.push('Where it runs: ' + t.needs + '.', '');
    if (p === 'small') {
      var content = b.content || 'sites.' + b.domain, recs = dnsRecords(b.domain, content);
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
      L.push(n++ + '. DNS: at my domain’s DNS provider, ' + dnsText(recs, content) + '. If you can manage that DNS provider from here, ask me before changing anything; otherwise tell me the exact records to add and wait for me. Check that `dig +short ' + sh(b.domain) + '` and `dig +short ' + sh(content) + '` both print the address. Certificates are issued on the first visit, so both names must point at the server first.', '');
      L.push(n++ + '. Install: on ' + host + ', run this. It installs Docker, starts Simple Host from the release it pins (' + INSTALLER_RELEASE + ') and prints the admin key: give it to me. The server keeps it in /opt/simple-host/.env (ADMIN_API_KEY) and re-running the command prints it again; do not copy it anywhere else.', '', FENCE + 'sh', r.cmd, FENCE, '');
      if (r.env) {
        L.push(n++ + '. Settings: add these lines to the end of /opt/simple-host/.env on the server (it needs sudo). Ask me for each blank value; do not invent one. Then run `cd /opt/simple-host && sudo docker compose up -d`.', '', FENCE, r.env.replace(/\n$/, ''), FENCE, '');
      }
      L.push(n++ + '. Check it works:');
      L.push('   - `curl -sS -o /dev/null -w \'%{http_code}\\n\' ' + sh('https://' + b.domain + '/healthz') + '` prints 200.');
      L.push('   - `cd /opt/simple-host && sudo docker compose ps`, on the server, shows every service running.');
      L.push('   - `curl -sS -o /dev/null -w \'%{http_code}\\n\' ' + sh('https://' + content + '/') + '` prints a status code with no certificate error.');
      L.push('   - `cd /opt/simple-host && sudo docker compose exec -T app simple-host version`, on the server, names the release (' + INSTALLER_RELEASE + ').', '');
      L.push(n++ + '. Tell me: the admin page, https://' + b.domain + '/admin (open it and paste the admin key there)' + (t.id === 'upcloud' ? ', the server’s UUID, address, plan and zone. Then remind me to run `' + UPCLOUD_UNSET + '` in this terminal (or close it) once you are done with UpCloud, and to keep the token or API user limited (a token with an expiry, an API user with only server permissions) and, if I can, to my own IP address' : '') + '.', '');
    } else {
      L.push('1. Get the package: `git clone https://github.com/vineetu/simple-host-enterprise && cd simple-host-enterprise`. Its INSTALL.md is a runbook written for AI agents: follow it top to bottom, and use the two files below as the config.env and secrets.env it asks for. Name the kubectl context on every call.', '');
      L.push('2. Save this as deploy/overlays/byo/config.env:', '', FENCE, r.config.replace(/\n$/, ''), FENCE, '');
      L.push('3. Save this as deploy/overlays/byo/secrets.env and fill in every blank from its hint: generate what can be generated, ask me for the rest. Check that `git check-ignore deploy/overlays/byo/config.env deploy/overlays/byo/secrets.env` prints both paths before writing them.', '', FENCE, r.secrets.replace(/\n$/, ''), FENCE, '');
      L.push('4. In deploy/overlays/byo, set the image digest in kustomization.yaml and the address in ingress-patch.yaml, and save the database’s CA certificate as db-ca.crt (INSTALL.md, section 5).', '');
      L.push('5. Apply: `make install OVERLAY=deploy/overlays/byo INSTALL_CONTEXT=<the kubectl context>`, then `kubectl --context <the kubectl context> -n simple-host rollout status deploy/simple-host --timeout=300s`.', '');
      L.push('6. Check it works:');
      L.push('   - `curl -fsS ' + sh('https://' + b.host + '/readyz') + '` prints {"status":"ok"}.');
      L.push('   - `curl -sS -o /dev/null -w \'%{http_code}\\n\' ' + sh('https://install-check.' + b.host + '/healthz') + '` prints a status code (401 or 404 is fine; a TLS or DNS error is not).');
      L.push('   - An admin signs in at https://' + b.host + '/auth/login.', '');
      L.push('7. Finish as INSTALL.md sections 8 and 9 say: HUMAN STEP D (an admin confirms is_admin at /api/me and mints a Full key on /dashboard), then `make smoke BASE=' + sh('https://' + b.host) + ' KEY_FILE="$HOME/.simple-host-install-key"`, which must pass. ' +
        (b.certs === 'auto' ? 'If OWNER_CERT_ISSUER (' + b.issuerName + ') is an internal CA, the machine running make smoke must trust it, or its owner-host check fails with "not ready" (curl exit 60 or 35): run it with CURL_CA_BUNDLE set to a file holding the system CAs plus the company CA.' : 'The owner certificates you issue must be trusted by the machine running make smoke (for an internal CA, set CURL_CA_BUNDLE to a file holding the system CAs plus the company CA).'), '');
    }
    L.push(window.shSetupAssist
      ? 'If anything fails and the output does not tell you how to fix it, tell me: I can paste the error at ' + origin + '/setup?product=' + product + '#help for help.'
      : 'If anything fails and the output does not tell you how to fix it, tell me and show me the error.');
    return L.join('\n') + '\n';
  }

  // ── The optional check ──
  var CHECKABLE = { number: true, duration: true, choice: true, rate: true };
  // checkPayload is what the check is sent: the changed settings that are
  // numbers, durations, switches, choices or rates. null when there are none.
  function checkPayload() {
    var p = S.product, out = {}, n = 0;
    var adv = advancedSettings().map(function (s) { return s.name; });
    var add = function (name, v) {
      var s = byName(name);
      if (!s || !CHECKABLE[setupKind(s)] || v == null || v === '' || v === defaultOf(s)) return;
      out[name] = String(v); n++;
    };
    Object.keys(S.values[p]).forEach(function (k) { if (adv.indexOf(k) >= 0) add(k, S.values[p][k]); });
    if (p === 'ent' && !cloud()) {
      if (S.basic.ent.certs !== 'auto') add('OWNER_CERTS', 'manual');
      add('DB_PORT', S.basic.ent.dbPort);
    }
    return n ? { product: p === 'small' ? 'small-box' : 'enterprise', settings: out } : null;
  }
  function checkKey() { var pl = checkPayload(); return pl ? JSON.stringify(pl) : ''; }

  // stopCheck aborts a running check and forgets it, so its answer, if one
  // still arrives, is ignored and the next visit to the files checks again.
  function stopCheck() {
    if (S.check.ctl) S.check.ctl.abort();
    clearTimeout(S.check.timer);
    S.check = { key: '', state: '', findings: [], note: '', seq: S.check.seq + 1 };
  }

  // runCheck starts a check, aborting any still running: one request at a
  // time.
  function runCheck(payload, key) {
    if (S.check.state === 'running') stopCheck();
    var seq = ++S.check.seq, ctl = window.AbortController ? new AbortController() : null;
    S.check = { key: key, state: 'running', findings: [], note: '', seq: seq, ctl: ctl, timer: 0 };
    var finish = function (note, findings) {
      if (S.check.seq !== seq || S.check.state !== 'running') return;
      clearTimeout(S.check.timer);
      S.check.ctl = null;
      S.check.findings = findings || [];
      S.check.state = S.check.findings.length ? 'review' : 'done';
      S.check.note = note;
      if (S.step === 3) render();
    };
    S.check.timer = setTimeout(function () { if (ctl) ctl.abort(); finish('Check skipped.'); }, 30000);
    S.check.skip = function () { if (ctl) ctl.abort(); finish('Check skipped.'); };
    drawChecking();
    fetch('/v1/setup/check', { method: 'POST', credentials: 'omit', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload), signal: ctl ? ctl.signal : undefined })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (d) {
        var list = (d && Array.isArray(d.findings)) ? d.findings : [];
        finish(list.length ? '' : 'Checked: nothing to change.', list);
      })
      .catch(function () { finish('Check skipped.'); });
  }

  // drawChecking shows the running check (again, on a re-render: no new request).
  function drawChecking() {
    app.appendChild(el('div', { class: 'card checking' }, [
      el('h2', { text: 'Checking your choices' }),
      el('p', { class: 'note', text: 'A quick look for likely mistakes in the settings you changed. Only their names and values are sent.' }),
      el('div', { class: 'spinner', 'aria-hidden': 'true' }),
      el('button', { class: 'skip', type: 'button', text: 'Skip the check', onclick: function () { S.check.skip(); } })
    ]));
  }

  // applicable reports whether Apply can set every suggested value here: each
  // is a setting this helper writes, with a value its own form accepts.
  function applicable(f) {
    var names = f.suggest ? Object.keys(f.suggest) : [];
    if (!names.length) return false;
    var adv = advancedSettings().map(function (s) { return s.name; });
    return names.every(function (n) { var s = byName(n); return s && adv.indexOf(n) >= 0 && validate(s, f.suggest[n]) === ''; });
  }

  function renderReview() {
    var list = S.check.findings, card = el('div', { class: 'card' }, [
      el('h2', { text: 'Check my choices' }),
      el('p', { class: 'note', style: 'margin:0 0 14px', text: 'A few things worth a second look. Apply takes the suggested value; Ignore keeps yours. Your files are still written from the form.' })
    ]);
    list.forEach(function (f, i) {
      var actions = el('div', { class: 'row finding-acts' });
      var drawActions = function () {
        actions.textContent = '';
        if (f.decision) { actions.appendChild(el('span', { class: 'decided', text: f.decision === 'applied' ? 'Applied' : 'Ignored' })); return; }
        if (applicable(f)) actions.appendChild(el('button', { class: 'btn small solid', type: 'button', text: 'Apply', onclick: function () {
          Object.keys(f.suggest).forEach(function (n) { setValue(byName(n), f.suggest[n]); });
          f.decision = 'applied'; drawActions();
        } }));
        actions.appendChild(el('button', { class: 'btn small', type: 'button', text: 'Ignore', onclick: function () { f.decision = 'ignored'; drawActions(); } }));
      };
      drawActions();
      var sugg = f.suggest ? Object.keys(f.suggest).map(function (n) { return n + '=' + f.suggest[n]; }) : [];
      card.appendChild(el('div', { class: 'finding ' + (f.severity === 'warn' ? 'warn' : 'info'), id: 'finding-' + i }, [
        el('div', { class: 'finding-head' }, [
          el('span', { class: 'sev', text: f.severity === 'warn' ? 'Warning' : 'Note' }),
          el('span', { class: 'name', text: (f.settings || []).join(' · ') })
        ]),
        el('p', { text: f.message }),
        sugg.length ? el('p', { class: 'suggest' }, ['Suggested: ', el('code', { text: sugg.join(', ') })]) : null,
        actions
      ]));
    });
    app.appendChild(card);
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () {
        if (S.mode === 'advanced') { S.area = areas().length - 1; go(2); } else go(1);
      } }),
      el('button', { class: 'btn solid', type: 'button', text: (cloud() ? 'Show my commands' : 'Show my files'), onclick: function () {
        var applied = list.filter(function (f) { return f.decision === 'applied'; }).length;
        S.check.state = 'done';
        S.check.key = checkKey();
        S.check.note = 'Checked: ' + (applied ? applied + ' suggestion' + (applied > 1 ? 's' : '') + ' applied.' : 'your values kept.');
        go(3);
      } })
    ]));
  }

  function renderOutput() {
    var pl = checkPayload(), key = pl ? JSON.stringify(pl) : '';
    if (!key) { if (S.check.state === 'running') stopCheck(); S.check = { key: '', state: '', findings: [], note: '', seq: S.check.seq }; }
    else if (S.check.key !== key) { runCheck(pl, key); return; }
    else if (S.check.state === 'running') { drawChecking(); return; }
    else if (S.check.state === 'review') { renderReview(); return; }
    if (cloud()) { renderCloudOutput(cloud()); return; }
    var r = build(), card = el('div', { class: 'card', id: 'files' }), t = targetOf(S.product), agent = handoff(r);
    if (S.check.note) app.appendChild(el('p', { class: 'check-note', role: 'status', text: S.check.note }));
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
      card.appendChild(el('h2', { text: t.id === 'upcloud' ? 'Or do it by hand' : 'Your small box' }));
      card.appendChild(el('ol', { class: 'steps' }, [
        t.id === 'upcloud' ? el('li', { text: 'In the UpCloud control panel, create the server: the smallest plan with 1 CPU and 1 GB of RAM, the plain Ubuntu Server 24.04 LTS image, your SSH key.' }) : null,
        el('li', { text: 'At your domain’s DNS provider, add ' + dnsText(dnsRecords(S.basic.small.domain, S.basic.small.content || 'sites.' + S.basic.small.domain), S.basic.small.content || 'sites.' + S.basic.small.domain) +
          '. The server is a fresh Ubuntu server with ports 80 and 443 open.' }),
        el('li', { text: 'On the server, run the install command below. It installs Docker, starts Simple Host and prints the admin key: keep it. The server keeps it in /opt/simple-host/.env, and re-running the command prints it again. Sign in with it at /admin.' }),
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
    if (t.id !== 'upcloud') {
      app.appendChild(el('div', { class: 'card', id: 'agent' }, [
        el('h2', { text: 'Set it up with your AI agent' }),
        el('p', { class: 'note', style: 'margin:0 0 4px', text: 'Rather have your AI agent do it? Copy this into the agent you use in your terminal. It has what the machine needs, every step with your files in it, and how to check the result. Secrets stay blanks: the agent asks you for them.' }),
        block('For your AI agent', agent, 'simple-host-setup.md')
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

  // ── The assistant's view of the form (setup/assist.js) ──
  // The assistant reads where the visitor is and their non-secret choices,
  // and applies a proposed change exactly as typing it would: the same
  // validation, the same state, then the page drawn again.
  var STEPS = ['choose', 'basics', 'advanced', 'files'];
  // Basic answers picked from a fixed list: the only ones the assistant sees
  // or proposes (setupBasicChoices in setupassist.go).
  var BASIC_CHOICES = {
    small: {
      codes: { label: 'Sign-in with an emailed code', values: { 'true': 'Yes', 'false': 'No' } },
      google: { label: 'Sign-in with Google', values: { 'true': 'Yes', 'false': 'No' } }
    },
    ent: {
      cloud: { label: 'Where it runs', values: { diy: 'Something else / I’ll do it myself' } },
      cluster: { label: 'A Kubernetes cluster there already', values: { yes: 'Yes', no: 'No, create one' } },
      cloudRegion: { label: 'Region', values: {} },
      idp: { label: 'Identity provider', values: {} },
      certs: { label: 'Site certificates', values: { auto: 'cert-manager issues them', manual: 'I issue them myself' } },
      smtp: { label: 'Email owners about sites nobody uses', values: { 'false': 'No email', 'true': 'Through our SMTP relay' } },
      bucket: { label: 'Bucket provider', values: {} },
      creds: { label: 'Bucket credentials', values: { keys: 'Access keys', identity: 'Workload identity (no keys)' } }
    }
  };
  IDPS.forEach(function (p) { BASIC_CHOICES.ent.idp.values[p.id] = p.name; });
  BUCKETS.forEach(function (p) { BASIC_CHOICES.ent.bucket.values[p.id] = p.name; });
  CLOUDS.forEach(function (c) {
    BASIC_CHOICES.ent.cloud.values[c.id] = c.name;
    c.regions.forEach(function (r) { BASIC_CHOICES.ent.cloudRegion.values[r] = r; });
  });
  // A region is an answer only for the cloud chosen (the list above has every
  // cloud's), and the region and cluster questions only exist on a cloud.
  function basicOffered(key, value) {
    var c = cloud();
    if (['certs', 'smtp', 'bucket', 'creds'].indexOf(key) >= 0) return !c;
    if (key !== 'cluster' && key !== 'cloudRegion') return true;
    return !!c && (key === 'cluster' || c.regions.indexOf(value) >= 0);
  }

  function basicNow(key) { return String(S.basic[S.product][key]); }
  function groupName(s) {
    var gs = S.data[S.product].groups;
    for (var i = 0; i < gs.length; i++) if (gs[i].id === s.group) return gs[i].name;
    return '';
  }
  // writable is the setting when this helper writes it as a setting of its
  // own and a value for it can be sent (not a secret, not free text).
  function writable(name) {
    var s = byName(name);
    if (!s || !CHECKABLE[setupKind(s)]) return null;
    return advancedSettings().indexOf(s) >= 0 ? s : null;
  }
  function flash(names) {
    names.forEach(function (n) {
      var f = document.getElementById(uid(n)) || app.querySelector('input[name="' + uid(n) + '"]');
      var field = f && f.closest('.field');
      if (field) field.classList.add('assisted');
    });
  }

  // basicApplied: a basic answer came from the assistant since the last refresh.
  var basicApplied = false;
  window.shSetup = {
    product: function () { return S.product; },
    // ready loads the chosen product's settings list (the first step may
    // not have yet).
    ready: function () { return load(S.product); },
    context: function () {
      var p = S.product, choices = {}, basics = {};
      if (S.data[p]) {
        Object.keys(S.values[p]).forEach(function (k) { if (writable(k)) choices[k] = S.values[p][k]; });
        // The installer's own defaults are in force unless changed: say so.
        if (p === 'small') Object.keys(INSTALLER_DEFAULTS).forEach(function (k) {
          var s = writable(k);
          if (s && choices[k] == null && INSTALLER_DEFAULTS[k] !== s.default) choices[k] = INSTALLER_DEFAULTS[k];
        });
      }
      // On the quick path the file-only questions (certificates, email,
      // bucket) are not asked; elsewhere, the cloud ones are not.
      var quickOnly = ['cluster', 'cloudRegion'], fileOnly = ['certs', 'smtp', 'bucket', 'creds'];
      Object.keys(BASIC_CHOICES[p]).forEach(function (k) {
        if ((cloud() ? fileOnly : quickOnly).indexOf(k) < 0) basics[k] = basicNow(k);
      });
      var ctx = { product: p === 'small' ? 'small-box' : 'enterprise', step: STEPS[S.step], mode: S.mode, choices: choices, basics: basics };
      if (S.step === 2 && S.data[p]) { var a = areas()[S.area]; if (a) ctx.area = a.group.id; }
      return ctx;
    },
    // describe says what applying NAME=value would do here: null when this
    // helper does not write it, else its current value, default and area,
    // and error when the form would refuse the value.
    describe: function (name, value) {
      var s = writable(name);
      if (!s) return null;
      return { current: valueOf(s), def: defaultOf(s), area: groupName(s), error: validate(s, value), security: !!s.security_sensitive };
    },
    apply: function (name, value) {
      var s = writable(name);
      if (!s) return 'This helper does not write that setting.';
      var msg = validate(s, value);
      if (!msg) setValue(s, value);
      return msg;
    },
    describeBasic: function (key, value) {
      var q = BASIC_CHOICES[S.product][key];
      if (!q || q.values[value] == null || !basicOffered(key, value)) return null;
      return { label: q.label, valueLabel: q.values[value], currentLabel: q.values[basicNow(key)] || basicNow(key), same: basicNow(key) === value };
    },
    // applyBasic answers a basic question as its own control does: picking
    // a provider fills in its template address where none was typed, as the
    // list itself does.
    applyBasic: function (key, value) {
      var q = BASIC_CHOICES[S.product][key], b = S.basic[S.product];
      if (!q || q.values[value] == null || !basicOffered(key, value)) return 'Not an answer this question takes.';
      if (key === 'codes' || key === 'google' || key === 'smtp') b[key] = value === 'true';
      else if (key === 'cloud') { pickCloud(b, value); if (cloud()) S.mode = 'basic'; }
      else if (key === 'idp') pickIdp(b, value);
      else if (key === 'bucket') pickBucket(b, value);
      else b[key] = value;
      basicApplied = true;
      return '';
    },
    // refresh draws the page again after changes, keeping the scroll. On the
    // files step the changes are the visitor's decision, already checked
    // against the settings list: the files follow them without another check.
    // A basic answer changed past the Basics step goes through the Basics
    // checks again (Google needs company domains, SMTP a From address, a
    // provider's template address its real value); when they fail, the page
    // goes back to Basics with the errors shown, and no files are offered
    // until they pass.
    refresh: function (names) {
      if (basicApplied) {
        basicApplied = false;
        if (S.step > 1 && !checkBasics()) {
          if (S.check.state === 'running') stopCheck();
          go(1);
          var bad = app.querySelector('.bad');
          if (bad) bad.focus();
          return;
        }
      }
      if (S.step === 3) {
        if (S.check.state === 'running') stopCheck();
        var key = checkKey(), review = S.check.state === 'review';
        S.check.key = key;
        if (!review) S.check.state = key ? 'done' : '';
        S.check.note = key && !review ? 'Updated with the assistant.' : '';
      }
      var y = window.scrollY;
      render();
      window.scrollTo(0, y);
      flash(names || []);
    }
  };

  render();
})();
