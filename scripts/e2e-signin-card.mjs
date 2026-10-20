#!/usr/bin/env node
// Browser proof for the shared SHSignIn card (leg A). Serves signin.js with a
// fake message table, stubs requestCode/verifyCode, and checks the card in
// Chromium and WebKit.
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium, webkit } from '/tmp/tsx/node_modules/playwright/index.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const srcPath = path.join(root, 'internal/handler/static/signin.js');
const src = fs.readFileSync(srcPath, 'utf8');
const messages = {
  code_wrong: 'That code is not right. {n} tries left.',
  code_used_up: 'Too many wrong tries, so this code no longer works. Send a new one to try again.',
  code_expired: 'That code has expired or was already used. Codes last {min} minutes. Send a new code.',
  rate_limited: 'Too many tries. Wait a minute, then try again.',
  invalid_email: 'That email address does not look right.',
  send_failed: 'We could not send the email just now. Try again in a minute.',
  signup_blocked: "Sign-in isn't available from your region.",
  email_signin_off: 'Email sign-in is off on this server. Sign in with your company account.',
  email_unavailable: 'This server cannot send sign-in codes. Use Google if it is offered, or ask whoever runs this server.',
  link_other_browser: 'Open this link on the device where you asked for it, or type the 6-digit code from the email.',
  account_suspended: 'this account has been suspended by the operator',
  network: 'Could not reach {product}. Check your connection.',
  unknown: 'Something went wrong. Try again.',
};
const marker = '/*sh:signin-messages*/{}';
if (!src.includes(marker)) {
  throw new Error('signin.js is missing ' + marker);
}
const signinJS = src.replace(marker, '/*sh:signin-messages*/' + JSON.stringify(messages));

const pageHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>sign-in card</title>
  <style>
    body { margin: 0; padding: 16px; font: 16px system-ui; }
    #ink, #plain { max-width: 400px; }
  </style>
</head>
<body>
  <div id="ink"></div>
  <div id="plain"></div>
  <script src="/signin.js"></script>
  <script>
    window.__calls = [];
    window.__verifyN = 0;
    function fail(reason, tries) {
      var err = new Error(reason);
      err.status = 401;
      err.code = "invalid_code";
      err.reason = reason;
      if (tries != null) err.tries_left = tries;
      err.body = { error: "", code: "invalid_code", reason: reason };
      if (tries != null) err.body.tries_left = tries;
      return Promise.reject(err);
    }
    window.SHSignIn.mount(document.getElementById("ink"), {
      skin: "ink",
      providers: ["google"],
      emailEnabled: true,
      requestCode: function (email) {
        window.__calls.push({ op: "request", email: email });
        return Promise.resolve({ email: email });
      },
      verifyCode: function (email, code) {
        window.__calls.push({ op: "verify", email: email, code: code });
        window.__verifyN += 1;
        if (window.__verifyN === 1) return fail("code_wrong", 2);
        return fail("code_used_up");
      }
    });
    window.SHSignIn.mount(document.getElementById("plain"), {
      skin: "plain",
      providers: ["google"],
      emailEnabled: true,
      requestCode: function (email) { return Promise.resolve({ email: email }); },
      verifyCode: function () { return Promise.resolve({}); }
    });
  </script>
</body>
</html>`;

function serve() {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      if (req.url === '/signin.js') {
        res.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8' });
        res.end(signinJS);
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end(pageHTML);
    });
    server.listen(0, '127.0.0.1', () => {
      resolve({ server, url: 'http://127.0.0.1:' + server.address().port + '/' });
    });
  });
}

async function pasteCode(page, text) {
  await page.evaluate((t) => {
    const inp = document.querySelector('#ink input.sh-si-code');
    const dt = new DataTransfer();
    dt.setData('text/plain', t);
    dt.setData('text', t);
    const ev = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'clipboardData', { value: dt });
    inp.dispatchEvent(ev);
  }, text);
}

async function run(name, launch) {
  const browser = await launch();
  const page = await browser.newPage({ viewport: { width: 390, height: 800 } });
  await page.goto(run.url, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => window.SHSignIn && document.querySelector('#ink .sh-si'));

  const suspended = await page.evaluate(() => window.SHSignIn.message({ status: 403, code: 'account_suspended', reason: 'spam, ticket 412',
    body: { error: 'this account has been suspended by the operator: spam, ticket 412', code: 'account_suspended', reason: 'spam, ticket 412' } }));
  if (suspended !== 'this account has been suspended by the operator: spam, ticket 412') {
    throw new Error(name + ': suspension reason lost: ' + suspended);
  }
  const startButtons = await page.locator('#ink button').allTextContents();
  if (startButtons[0] !== 'Continue with Google') {
    throw new Error(name + ': Google is not first: ' + JSON.stringify(startButtons));
  }
  if (!startButtons.includes('Continue')) {
    throw new Error(name + ': Continue missing: ' + JSON.stringify(startButtons));
  }

  await page.locator('#ink input[type="email"]').fill('x@gmail.com');
  await page.locator('#ink button.sh-si-pbtn', { hasText: 'Continue' }).click();
  await page.waitForSelector('#ink input.sh-si-code');

  const codeText = await page.locator('#ink').innerText();
  if (!codeText.includes('Check your email') || !codeText.includes('We sent a 6-digit code to') || !codeText.includes('x@gmail.com')) {
    throw new Error(name + ': code step text: ' + codeText);
  }
  if (!codeText.includes('Resend in 0:')) {
    throw new Error(name + ': resend countdown missing: ' + codeText);
  }
  const gmail = page.locator('#ink a', { hasText: 'Open Gmail' });
  if (await gmail.count() !== 1) {
    throw new Error(name + ': Open Gmail missing for gmail.com');
  }
  if (await gmail.getAttribute('target') !== '_blank') {
    throw new Error(name + ': Open Gmail target is not _blank');
  }
  const rel = (await gmail.getAttribute('rel')) || '';
  if (!rel.includes('noopener')) {
    throw new Error(name + ': Open Gmail rel=' + rel);
  }

  await pasteCode(page, 'Your code is 123 456');
  await page.waitForFunction(() => (window.__calls || []).filter((c) => c.op === 'verify').length === 1);
  const calls = await page.evaluate(() => window.__calls);
  const verify = calls.filter((c) => c.op === 'verify');
  if (verify.length !== 1 || verify[0].code !== '123456') {
    throw new Error(name + ': paste verify: ' + JSON.stringify(calls));
  }
  await page.waitForFunction(() => {
    const el = document.querySelector('#ink [role="alert"]');
    return el && /tries left/.test(el.textContent || '');
  });
  const err = await page.locator('#ink [role="alert"]').innerText();
  if (!err.includes('That code is not right') || !err.includes('2 tries left')) {
    throw new Error(name + ': wrong-code error: ' + err);
  }

  await page.locator('#ink input.sh-si-code').fill('654321');
  await page.waitForFunction(() => (window.__calls || []).filter((c) => c.op === 'verify').length === 2);
  await page.waitForFunction(() => {
    const b = document.querySelector('#ink button.sh-si-pbtn');
    return b && b.textContent === 'Send a new code';
  });

  await page.locator('#ink button.sh-si-link', { hasText: 'Change' }).click();
  await page.waitForSelector('#ink input[type="email"]');
  const kept = await page.locator('#ink input[type="email"]').inputValue();
  if (kept !== 'x@gmail.com') {
    throw new Error(name + ': Change did not keep email: ' + kept);
  }

  await page.locator('#ink input[type="email"]').fill('x@example.com');
  await page.locator('#ink button.sh-si-pbtn', { hasText: 'Continue' }).click();
  await page.waitForSelector('#ink input.sh-si-code');
  if (await page.locator('#ink a', { hasText: 'Open Gmail' }).count() !== 0) {
    throw new Error(name + ': Open Gmail shown for example.com');
  }

  const shadow = await page.evaluate(() => {
    const host = document.querySelector('#plain .sh-si-host') || document.querySelector('#plain > *');
    return !!(host && host.shadowRoot);
  });
  if (!shadow) {
    throw new Error(name + ': plain skin has no shadow root');
  }

  await browser.close();
  console.log('ok', name);
}

const { server, url } = await serve();
run.url = url;
try {
  await run('chromium', () => chromium.launch({
    executablePath: '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome',
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  }));
  await run('webkit', () => webkit.launch());
} finally {
  server.close();
}
