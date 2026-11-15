  function productChoices(choices) {
    var hack = choice('product', 'hack', S.product, 'Simple Hack on one server', 'A hackathon platform on your own server.', redrawChoose('product'));
    if (document.body.getAttribute('data-setup-product') === 'hack') choices.unshift(hack);
    else choices.push(hack);
    return choices;
  }

  var HACK_FIELDS = ['SITE_DOMAIN', 'SUPPORT_EMAIL', 'PRIVACY_URL', 'RESEND_API_KEY', 'MAIL_FROM',
    'ASSISTANT_BASE_URL', 'ASSISTANT_MODEL', 'ASSISTANT_API_KEY'];

  function checkHackBasics() {
    var b = S.basic.hack, errors = {};
    HACK_FIELDS.forEach(function (key) {
      if (!b.assistant && key.indexOf('ASSISTANT_') === 0) return;
      var v = b[key];
      if (/[\x00-\x1f\x7f-\x9f\u2028\u2029$]/.test(v) || /^["']/.test(v) || (key !== 'MAIL_FROM' && /\s/.test(v))) {
        errors[key] = 'Use one line, without control characters, $, a leading quote or spaces (spaces are allowed in Mail from).';
      }
    });
    b.SITE_DOMAIN = b.SITE_DOMAIN.toLowerCase();
    // Exactly install.sh's SITE_DOMAIN rule, including its total length limit.
    if (b.SITE_DOMAIN.length > 253 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$/.test(b.SITE_DOMAIN)) {
      errors.SITE_DOMAIN = 'Use your DNS hostname, without a URL or wildcard.';
    }
    if (b.SUPPORT_EMAIL && !/^[^@\s]+@[^@\s]+$/.test(b.SUPPORT_EMAIL)) errors.SUPPORT_EMAIL = 'Use an email address.';
    if (b.PRIVACY_URL) {
      try {
        var url = new URL(b.PRIVACY_URL);
        if (url.protocol !== 'https:' || !url.hostname) throw new Error();
      } catch (e) { errors.PRIVACY_URL = 'Use an https URL.'; }
    }
    function pair(a, c) {
      if (!!b[a] !== !!b[c]) errors[b[a] ? c : a] = 'Fill in both fields, or leave both empty.';
    }
    pair('RESEND_API_KEY', 'MAIL_FROM');
    if (b.assistant) pair('ASSISTANT_BASE_URL', 'ASSISTANT_MODEL');
    S.errors = errors;
    return !Object.keys(errors).length;
  }

  function renderHackBasics() {
    var b = S.basic.hack, card = el('div', { class: 'card' }, [el('h2', { text: 'Simple Hack on one server' })]);
    function field(key, label, opts) {
      opts = opts || {};
      opts.name = key; opts.raw = true;
      card.appendChild(textField(key, b, label, opts));
    }
    field('SITE_DOMAIN', 'Domain', { placeholder: 'hack.example.com', help: 'Point it and *.<domain> at the server.' });
    field('SUPPORT_EMAIL', 'Support email (optional)');
    field('PRIVACY_URL', 'Privacy policy link (optional)');
    card.appendChild(el('h3', { text: 'Email sending (optional)' }));
    card.appendChild(el('p', { class: 'note', text: 'Sign-in works without email. Fill in both fields or neither.' }));
    field('RESEND_API_KEY', 'Resend API key');
    field('MAIL_FROM', 'Mail from');
    card.appendChild(radios('assistant', b, 'Ask assistant', [['false', 'Off'], ['true', 'On']], render));
    if (b.assistant) {
      card.appendChild(el('p', { class: 'note', text: 'Fill in both the base URL and model, or leave both empty.' }));
      field('ASSISTANT_BASE_URL', 'Base URL');
      field('ASSISTANT_MODEL', 'Model');
      field('ASSISTANT_API_KEY', 'API key (optional)');
    }
    app.appendChild(card);
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () { S.errors = {}; go(0); } }),
      el('button', { class: 'btn solid', type: 'button', text: 'Show my steps', onclick: function () {
        if (checkHackBasics()) go(3); else { render(); var bad = app.querySelector('input.bad'); if (bad) bad.focus(); }
      } })
    ]));
  }

  function hackSettings() {
    var b = S.basic.hack, lines = ['SITE_DOMAIN=' + b.SITE_DOMAIN];
    ['SUPPORT_EMAIL', 'PRIVACY_URL'].forEach(function (key) { if (b[key]) lines.push(key + '=' + b[key]); });
    ['RESEND_API_KEY', 'MAIL_FROM'].forEach(function (key) { if (b[key]) lines.push(key + '=' + b[key]); });
    if (b.assistant) ['ASSISTANT_BASE_URL', 'ASSISTANT_MODEL', 'ASSISTANT_API_KEY'].forEach(function (key) {
      if (b[key]) lines.push(key + '=' + b[key]);
    });
    return lines.join('\n');
  }

  function hackSubstitute(text) {
    // Replace once over the template so user values never become placeholders.
    return text.replace(/\{DOMAIN\}|\{SETTINGS\}/g, function (key) {
      return key === '{DOMAIN}' ? S.basic.hack.SITE_DOMAIN : hackSettings();
    });
  }

  function hackText(text) {
    var p = el('p'), parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*)/g);
    parts.forEach(function (part) {
      p.appendChild(part.charAt(0) === '`' ? el('code', { text: part.slice(1, -1) }) :
        part.slice(0, 2) === '**' ? el('strong', { text: part.slice(2, -2) }) : document.createTextNode(part));
    });
    return p;
  }

  function renderHackOutput() {
    var data = S.data.hack, card = el('div', { class: 'card', id: 'files' }, [el('h2', { text: 'Install Simple Hack' })]);
    var steps = el('ol', { class: 'steps' });
    data.steps.forEach(function (step) {
      var li = el('li');
      li.appendChild(step.code != null ? block(step.title, hackSubstitute(step.code)) : hackText(hackSubstitute(step.text)));
      steps.appendChild(li);
    });
    card.appendChild(steps);
    data.after.forEach(function (line) { card.appendChild(hackText(hackSubstitute(line))); });
    app.appendChild(card);
    app.appendChild(el('div', { class: 'nav' }, [
      el('button', { class: 'btn', type: 'button', text: 'Back', onclick: function () { go(1); } }),
      el('button', { class: 'btn', type: 'button', text: 'Start over', onclick: function () { location.reload(); } })
    ]));
  }

