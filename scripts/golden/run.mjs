#!/usr/bin/env node
// Golden API snapshot harness -- see scripts/golden/README.md.
//
//   node scripts/golden/run.mjs --host BINARY [--hack BINARY] --out DIR
//
// Node 22 built-ins only (global fetch, node:child_process, ...); the only
// external programs this script shells out to are `openssl`, `psql` (to make and tear
// down two throwaway databases) and the simple-host BINARY itself.
//
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync, readFileSync, mkdirSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import http from 'node:http';
import https from 'node:https';
import crypto from 'node:crypto';
import {
  Namer, normalizeValue, normalizeText, normalizeSetCookie, sha256hex,
  createTextWriter, describeZip, normalizeRecordedText, sanitizeName,
  normalizeResponseNoise, normalizeMailOutbox, selectScenarios, renderRecordedText, fixedGetRoutes, normalizeStaticText, mapStrings, isTextBody, relativeDates, validateScenarios, validatePostgresURL, responseClass, requestReplacements, mcpCallName, STARTUP_PROGRESS_WORKERS,
} from './lib.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '..', '..');
const FIXED_GETS = fixedGetRoutes(JSON.parse(readFileSync(path.join(REPO_ROOT, 'docs/generated/inventory.json'))));
// Add rules track scenarios here, for example:
// 'rules-on': { env: { host: { STORAGE_RULES: 'on' }, hack: {} }, host: seedAndCallHost, hack: seedAndCallHack }
const SCENARIOS = {
 base: { env: { host: { KV_STORE: 'postgres' }, hack: { KV_STORE: 'postgres' } }, host: seedAndCallHost, hack: seedAndCallHack },
 'mixed-store': { env: { host: { KV_STORE: 'sqlite' }, hack: { KV_STORE: 'sqlite' } }, host: seedAndCallMixedStore, hack: seedAndCallMixedStore },
};

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// The proxy forwards only the mail-provider destination to this local sink.
// It does not restrict transports that bypass proxy settings (see README).
async function localMailSink(addCleanup) {
  const folder = mkdtempSync(path.join(tmpdir(), 'golden-mail-'));
  addCleanup(() => rmSync(folder, { recursive: true, force: true }));
  const openssl = args => execFileSync('openssl', args, { cwd: folder, stdio: 'pipe', timeout: 15000 });
  openssl(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'ca.key',
    '-out', 'ca.pem', '-days', '1', '-subj', '/CN=Golden local mail CA']);
  openssl(['req', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'server.key',
    '-out', 'server.csr', '-subj', '/CN=api.resend.com']);
  writeFileSync(path.join(folder, 'extensions'), 'subjectAltName=DNS:api.resend.com\n');
  openssl(['x509', '-req', '-in', 'server.csr', '-CA', 'ca.pem', '-CAkey', 'ca.key',
    '-CAcreateserial', '-out', 'server.pem', '-days', '1', '-extfile', 'extensions']);
  const messages = [], refused = [];
  let lastMail = Date.now();
  const sink = https.createServer({ key: readFileSync(path.join(folder, 'server.key')),
    cert: readFileSync(path.join(folder, 'server.pem')) }, async (req, res) => {
    if (req.method !== 'POST' || req.url !== '/emails') {
      refused.push(`${req.method} ${req.url}`);
      res.writeHead(403).end();
      return;
    }
    try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const message = JSON.parse(Buffer.concat(chunks));
    messages.push(message);
    lastMail = Date.now();
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ id: 'golden-local-mail' }));
    } catch (err) { refused.push('invalid mail payload'); res.writeHead(400).end(); }
  });
  const proxy = http.createServer((req, res) => {
    refused.push(req.url);
    res.writeHead(403).end();
  });
  const sockets = new Set();
  for (const server of [sink, proxy]) {
    server.on('error', err => { refused.push(`local server: ${err.code}`); });
    server.on('connection', socket => {
      sockets.add(socket);
      socket.on('close', () => sockets.delete(socket));
    });
    addCleanup(() => {
      for (const socket of sockets) socket.destroy();
      server.close();
    });
  }
  const listen = server => new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  await listen(sink);
  ensureActive();
  proxy.on('connect', (req, client, head) => {
    if (req.url !== 'api.resend.com:443') {
      refused.push(req.url);
      client.end('HTTP/1.1 403 Forbidden\r\n\r\n');
      return;
    }
    const upstream = net.connect(sink.address().port, '127.0.0.1', () => {
      client.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head.length) upstream.write(head);
      client.pipe(upstream).pipe(client);
    });
    upstream.on('error', () => client.destroy());
    client.on('close', () => upstream.destroy());
  });
  await listen(proxy);
  ensureActive();
  const proxyURL = `http://127.0.0.1:${proxy.address().port}`;
  return {
    messages, refused,
    env: { HTTPS_PROXY: proxyURL, HTTP_PROXY: proxyURL, NO_PROXY: '127.0.0.1,localhost',
      SSL_CERT_FILE: path.join(folder, 'ca.pem'), RESEND_API_KEY: 'golden-local-sink-not-a-real-key',
      MAIL_FROM: 'Golden <mail@golden.invalid>' },
    async waitFor(predicate, label) {
      const deadline = Date.now() + 5000;
      while (!predicate(messages)) {
        if (Date.now() > deadline) throw new Error(`mail missing: ${label}`);
        await sleep(20);
      }
    },
    async quiet() {
      const deadline = Date.now() + 5000;
      while (Date.now() - lastMail < 350) { if (Date.now() > deadline) throw new Error('mail quiet timeout'); await sleep(25); }
    },
    close() {
      for (const socket of sockets) socket.destroy();
      proxy.close();
      sink.close();
      rmSync(folder, { recursive: true, force: true });
    },
  };
}

// ---- Postgres: one throwaway database per instance, via psql -------------

function safeError(err) {
  return String(err?.message || err).replace(/postgres(?:ql)?:\/\/[^\s"']+/gi, '<database>')
    .replace(/shk_[a-f0-9]+/gi, '<key>');
}
function psql(connURL, sql) {
  try {
    return execFileSync('psql', ['-X', connURL, '-v', 'ON_ERROR_STOP=1', '-At', '-c', sql], {
      timeout: 15000, env: { PATH: process.env.PATH || '/usr/bin:/bin', HOME: '/nonexistent', TZ: 'UTC', PSQLRC: '/dev/null' }, stdio: ['ignore', 'pipe', 'pipe'], encoding: 'utf8',
    }).trim();
  } catch (err) { throw new Error(`psql failed (${err.status ?? err.code}): ${safeError(err.stderr?.toString() || 'subprocess failure')}`); }
}
function databaseURL(prefix, database) {
  const url = new URL(prefix); url.pathname = '/' + database; url.searchParams.set('sslmode', 'disable'); return url.href;
}
function ensureActive() { if (shuttingDown) throw new Error('recording cancelled'); }
function reserveName(usedNames, name) {
  ensureActive();
  if (usedNames.has(name)) throw new Error(`duplicate request name: ${name}`);
  if ([...usedNames].some(previous => sanitizeName(previous) === sanitizeName(name))) throw new Error(`text filename collision: ${name}`);
  usedNames.add(name);
}
function pseudoCall(calls, usedNames, record) { reserveName(usedNames, record.name); calls.push(record); }

function dropDatabaseQuiet(adminURL, dbName) {
  try {
    psql(adminURL, `DROP DATABASE IF EXISTS "${dbName}"`);
  } catch (err) {
    console.error(`golden: warning: failed to drop throwaway database ${dbName}: ${safeError(err)}`);
  }
}

// ---- process plumbing ------------------------------------------------------

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

function spawnServer(binary, env) {
  // DATA_DIR, DB_DSN etc. are passed explicitly below; the child gets
  // nothing else from this process's environment except PATH (it never
  // execs another program itself, but Go's runtime wants PATH to resolve
  // its own binary name in error messages).
  ensureActive();
  const child = spawn(binary, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  child.on('error', err => { child.spawnError = err; });
  const chunks = [];
  child.stdout.on('data', (d) => chunks.push(d));
  child.stderr.on('data', (d) => chunks.push(d));
  return { child, log: () => Buffer.concat(chunks).toString('utf8') };
}

async function waitHealthy(baseURL, child, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    ensureActive();
    if (child.spawnError) throw new Error(`server spawn failed: ${child.spawnError.code}`);
    if (child.exitCode !== null) {
      throw new Error(`server exited (code ${child.exitCode}) before answering healthy`);
    }
    try {
      const res = await fetch(baseURL + '/healthz', { signal: AbortSignal.timeout(Math.min(1000, Math.max(1, deadline - Date.now()))) });
      if (res.ok) return;
    } catch {
      // not listening yet
    }
    await sleep(200);
  }
  throw new Error(`server never answered /healthz within ${timeoutMs}ms`);
}

async function stopServer(child, cleanup = false) {
  const exited = () => child.exitCode !== null || child.signalCode !== null;
  if (exited() || child.spawnError) {
    if (!cleanup) throw new Error('server exited before graceful completion');
    return;
  }
  child.kill('SIGTERM');
  const deadline = Date.now() + (cleanup ? 5000 : 30000);
  let forced = false;
  while (!exited() && Date.now() < deadline) await sleep(100);
  if (!exited()) {
    forced = true;
    child.kill('SIGKILL');
    await new Promise(resolve => child.once('exit', resolve));
  }
  if (!cleanup && (forced || child.exitCode !== 0 || child.signalCode)) throw new Error(`server did not complete gracefully (forced=${forced}, code=${child.exitCode}, signal=${child.signalCode})`);
}

// Startup progress is observed without adding requests to the recording.
async function waitWorkers(baseURL, adminKey) {
  const deadline = Date.now() + 15000;
  let observations = 0;
  while (Date.now() < deadline) {
    const response = await fetch(baseURL + '/v1/admin/usage', { headers: { 'X-API-Key': adminKey }, signal: AbortSignal.timeout(Math.max(1, deadline - Date.now())) });
    if (!response.ok) throw new Error(`worker observation failed: ${response.status}`);
    observations++;
    const { workers } = await response.json();
    if (STARTUP_PROGRESS_WORKERS.every(name => workers.some(w => w.name === name && w.last_progress))) return observations;
    await sleep(25);
  }
  throw new Error('startup worker progress timeout');
}

// ---- one running instance (Host or Hack) ----------------------------------

// Each instance migrates with its own binary and gets an isolated database,
// local listener, clean environment and temporary data directory.
async function bootInstance({ binary, pgAdminURL, extraEnv, hosted, tag, namer, exact, addCleanup, mail }) {
  ensureActive();
  const dbName = `golden_${tag}_${process.pid}_${crypto.randomBytes(4).toString('hex')}`;
  const adminDBURL = databaseURL(pgAdminURL, 'postgres');
  // Registered before the database even exists: dropping a database that
  // was never created is a harmless no-op (DROP DATABASE IF EXISTS), so
  // this is safe to run unconditionally if a SIGINT/SIGTERM lands between
  // here and the CREATE DATABASE below.
  addCleanup(() => dropDatabaseQuiet(adminDBURL, dbName));
  psql(adminDBURL, `CREATE DATABASE "${dbName}"`);
  psql(adminDBURL, `ALTER DATABASE "${dbName}" SET timezone TO 'UTC'`);
  let dataDir;
  try {
    const dbURL = databaseURL(pgAdminURL, dbName);
    if (psql(dbURL, 'SHOW timezone') !== 'UTC') throw new Error('disposable database timezone is not UTC');

    dataDir = mkdtempSync(path.join(tmpdir(), `golden-${tag}-`));
    addCleanup(() => rmSync(dataDir, { recursive: true, force: true }));
    // Fixed, not random: two runs of the same binary must produce the same
    // bytes, and these never leak into a response body anyway (only
    // server-minted ids/keys/tokens do, which the regex-based normalizers
    // in lib.mjs already catch).
    const adminKey = crypto.createHash('sha256').update(`golden-admin-key-${tag}`).digest('hex');
    const passcodeEncKey = crypto.createHash('sha256').update(`golden-passcode-enc-key-${tag}`).digest('base64');

    let child, log, baseURL, serverEnv;
    const port = await freePort();
    baseURL = `http://127.0.0.1:${port}`;
    const env = {
      PATH: process.env.PATH || '/usr/bin:/bin',
      TZ: 'UTC',
      DB_DSN: dbURL,
      ADMIN_API_KEY: adminKey,
      DATA_DIR: dataDir,
      GEOIP_DIR: path.join(dataDir, 'geoip'),
      EVENT_NAME_PEER: '',
      SITE_DOMAIN: `golden-${tag}.invalid`,
      PUBLIC_BASE_URL: baseURL,
      PORT: String(port),
      BIND_ADDR: '127.0.0.1',
      PASSCODE_ENC_KEY: passcodeEncKey,
      ...mail.env,
      // Both default to off (the shared path model); turned on here,
      // with SITE_CERT_DIR left unset, so a site/team-site "has an
      // address of its own" for the passcode and hack team-site-deploy
      // requests below -- every handle counts as cert-ready with no
      // hand-off directory configured (sitehost.go), so this needs no
      // real certificate and no network call.
      PERSON_HOSTS: 'serve',
      SITE_HOSTS: 'serve',
    };
    Object.assign(env, extraEnv, mail.env);
    serverEnv = env;
    if (hosted) env.EVENTS = 'hosted';
    const migrationStarted = Date.now();
    execFileSync(binary, ['migrate', '-init'], { env, timeout: 30000, stdio: ['ignore', 'pipe', 'pipe'] });
    console.error(`golden: ${tag} migrate: ${Date.now() - migrationStarted}ms`);
    ({ child, log } = spawnServer(binary, env));
    addCleanup(() => stopServer(child, true));
    try {
      await waitHealthy(baseURL, child, 10000);
    } catch (err) {
      await stopServer(child, true);
      throw new Error(`${err.message}\n--- server output ---\n${log().slice(-4000)}`);
    }

    if (process.env.GOLDEN_TEST_DELAY_MS) await sleep(Number(process.env.GOLDEN_TEST_DELAY_MS));
    let observationCount = await waitWorkers(baseURL, adminKey);

    exact.set(baseURL, namer.placeholderFor('base', baseURL));
    // The hack landing page's illustration also writes the bare host:port
    // (no "http://") into an inline script as example text ("yourhack.
    // 127.0.0.1:<port>") -- registered after the full baseURL above so
    // that is replaced first wherever it appears whole, and this one only
    // ever catches what that left behind.
    const bareHostPort = baseURL.replace(/^https?:\/\//, '');
    exact.set(bareHostPort, namer.placeholderFor('hostport', bareHostPort));
    exact.set(dataDir, namer.placeholderFor('tmpdir', dataDir));

    const restart = async () => {
      ensureActive(); await stopServer(child); ensureActive();
      ({ child, log } = spawnServer(binary, serverEnv));
      await waitHealthy(baseURL, child, 10000);
      observationCount += await waitWorkers(baseURL, adminKey);
    };
    const pendingMail = () => Number(psql(dbURL, `SELECT
      (SELECT count(*) FROM event_notices WHERE email_state='pending') +
      (SELECT count(*) FROM event_entry_receipts WHERE sent_at IS NULL) +
      (SELECT count(*) FROM event_announcement_deliveries WHERE sent_at IS NULL)`));
    return {
      tag, mail,
      get observationCount() { return observationCount; },
      async drainMail() {
        if (!hosted || !pendingMail()) return;
        // Fixture clock for queue eligibility; application payload and delivery stay real.
        psql(dbURL, "UPDATE event_notices SET created_at=now()-interval '2 minutes' WHERE email_state='pending'; UPDATE event_notices SET emailed_at=now()-interval '16 minutes' WHERE email_state='sent'");
        await restart();
        const deadline = Date.now() + 10000;
        while (pendingMail()) { ensureActive(); if (Date.now() > deadline) throw new Error('mail queue drain timeout'); await sleep(25); }
        await mail.quiet();
      },
      async stopProducer() { await stopServer(child); },
      baseURL,
      adminKey,
      exact, namer, binary, dbURL, env: serverEnv,
      async screens(value) {
        ensureActive();
        for (const flag of ['PASSCODE','DASHBOARD','MY_SITES','PHOTO_PICKER','SITE_PREVIEW','HACK_JUDGE','HACK_ORGANISER']) serverEnv[`MCP_APP_${flag}`] = value;
        await restart();
      },
      async stop() {
        await stopServer(child, true);
        mail.close();
        rmSync(dataDir, { recursive: true, force: true });
        dropDatabaseQuiet(adminDBURL, dbName);
      },
    };
  } catch (err) {
    // The addCleanup entries above fire on a signal, not on a normal thrown
    // error -- a synchronous boot failure here needs its own cleanup of
    // whatever got created before the failure (dataDir may not exist yet).
    if (dataDir) rmSync(dataDir, { recursive: true, force: true });
    dropDatabaseQuiet(adminDBURL, dbName);
    throw err;
  }
}

// ---- capturing requests -----------------------------------------------------

// registerSecret records a run-specific random value (a hack join/judge/
// invite code -- the only random values in this codebase that show up in a
// request *path*, not just a body field already caught by lib.mjs's regexes)
// into this product/scenario's exact-replacement map, under the given
// Namer category. Call it as soon as the value comes back from the server,
// before any later call uses it in a path.
function registerSecret(exact, namer, category, raw) {
  if (raw) exact.set(raw, namer.placeholderFor(category, raw));
}

// call() returns an `at(name, method, path, opts)` bound to one instance.
// Every call -- GET or a seeding mutation alike -- is appended to `calls`
// verbatim (raw bytes); normalization happens once, at the end, in
// finalizeCall, so the same code path handles the host's and the hack
// instance's captures identically.
//
// Request names are unique within one product/scenario recording.
// Undici fetch rewrites Host; native HTTP preserves virtual-host requests.
function fetchWithHost(url, opts) {
  return new Promise((resolve, reject) => {
    const req = http.request(url, { method: opts.method, headers: opts.headers, signal: opts.signal }, res => {
      const chunks = [];
      res.on('aborted', () => reject(new Error('response aborted')));
      res.on('error', reject);
      res.on('data', chunk => chunks.push(chunk));
      res.on('end', () => resolve({ status: res.statusCode, headers: { get: name => res.headers[name] || '', getSetCookie: () => res.headers['set-cookie'] || [] }, arrayBuffer: async () => Buffer.concat(chunks) }));
    });
    req.on('error', reject);
    req.end(opts.body);
  });
}

function call(instance, calls, usedNames) {
  return async function at(name, method, pathAndQuery, opts = {}) {
    if (method === 'GET' && !opts.apiKey && !opts.qualifier && FIXED_GETS.has(pathAndQuery) && !usedNames.has(`GET ${pathAndQuery}`)) name = `GET ${pathAndQuery}`;
    ensureActive();
    // All MCP call naming goes through this single dispatch helper.
    if (opts.body?.method === 'tools/call') {
      name = mcpCallName(name, opts.body.params);
    }
    const fullName = name;
    instance.namer.beginRequest(name);
    reserveName(usedNames, fullName);

    const headers = { ...opts.headers };
    if (opts.apiKey) headers['X-API-Key'] = opts.apiKey;
    let payload;
    if (opts.raw !== undefined) {
      payload = opts.raw;
      headers['Content-Type'] = opts.contentType || 'application/octet-stream';
    } else if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json';
      payload = JSON.stringify(opts.body);
    }
    const requestStarted = Date.now();
    let res, rawBody;
    try {
    res = await (opts.headers?.Host ? fetchWithHost : fetch)(instance.baseURL + pathAndQuery, {
      method,
      headers,
      body: payload,
      signal: AbortSignal.timeout(15000),
      redirect: 'manual', // a 3xx is a result to record, not something to chase
    });
    rawBody = Buffer.from(await res.arrayBuffer());
    } catch (err) { throw new Error(`request ${fullName} failed: ${safeError(err)}`); }
    if (Date.now() - requestStarted > 1000) console.error(`golden: slow ${name}: ${Date.now() - requestStarted}ms`);
    const record = {
      normalization: responseClass(fullName, FIXED_GETS),
      observationCount: instance.observationCount,
      name: fullName,
      method,
      path: pathAndQuery,
      status: res.status,
      contentType: res.headers.get('content-type') || '',
      cacheControl: res.headers.get('cache-control') || '',
      location: res.headers.get('location') || '',
      etag: res.headers.get('etag') || '',
      behaviourHeaders: Object.fromEntries(['x-robots-tag','www-authenticate','content-disposition','content-security-policy'].map(k => [k, res.headers.get(k)]).filter(([,v]) => v)),
      // getSetCookie() (Node 19+) returns each Set-Cookie header
      // separately; a server that sends none answers with [].
      setCookie: typeof res.headers.getSetCookie === 'function' ? res.headers.getSetCookie() : [],
      rawBody,
      exact: instance.exact,
    };
    calls.push(record);
    let json = null;
    if (record.contentType.includes('json') && rawBody.length > 0) {
      try {
        json = JSON.parse(rawBody.toString('utf8'));
      } catch {
        // left null; the request list below only reads .json on routes
        // that are documented to answer JSON, so a parse failure here is
        // itself behavior a comparison should surface.
      }
    }
    if (method !== 'GET' && method !== 'HEAD') await instance.drainMail();
    return { status: res.status, json, rawBody, cookies: record.setCookie };
  };
}

function finalizeCall(record, namer, knownTimestamps, writer) {
  namer.beginRequest(record.name);
  const isStatic = (record.normalization || responseClass(record.name, FIXED_GETS)) === 'static';
  const exact = requestReplacements(record.name, record.exact);
  const norm = value => isStatic ? normalizeStaticText(value, record.exact, record.name) : normalizeText(value, namer, exact, knownTimestamps);
  const normValue = value => isStatic ? mapStrings(value, norm) : normalizeValue(value, namer, exact, knownTimestamps);
  const headers = {};
  if (record.contentType) headers['content-type'] = record.contentType;
  if (record.cacheControl) headers['cache-control'] = record.cacheControl;
  if (record.location) headers.location = norm(record.location);
  if (record.etag) headers.etag = '<etag>';
  if (record.setCookie.length) headers['set-cookie'] = normalizeSetCookie(record.setCookie, namer, record.exact, knownTimestamps);
  for (const [key, value] of Object.entries(record.behaviourHeaders || {})) headers[key] = key === 'content-disposition' && record.path === '/v1/me/export.zip' ? norm(value.replace(/(filename="simple-host-[^"]+)-\d{4}-\d{2}-\d{2}(\.zip")/, '$1-<date>$2')) : norm(value);
  const out = { name: record.name, method: record.method, path: norm(record.path), status: record.status, headers, body_kind: 'empty' };
  if (!record.rawBody.length) return out;
  if (record.contentType.includes('json') && isTextBody(record.contentType, record.rawBody)) {
    let parsed;
    try { parsed = JSON.parse(record.rawBody.toString('utf8')); } catch { /* record invalid JSON as text */ }
    if (parsed !== undefined) {
      parsed = normalizeResponseNoise(record.name, parsed, { observationCount: record.observationCount });
      out.body_kind = 'json';
      out.body = record.name === 'mail-outbox' ? mapStrings(parsed, v => v) : normValue(parsed);
      out.text = writer(record.name, out.body, 'json'); return out;
    }
  }
  if (record.rawBody.subarray(0, 4).equals(Buffer.from([0x50,0x4b,0x03,0x04]))) {
    const zipNorm = text => /\/skills\//.test(record.path) ? normalizeStaticText(text, record.exact) : norm(text);
    out.body_kind = 'json'; out.body = describeZip(record.rawBody, (text, filename) => {
      if (filename.endsWith('.json')) {
        try { return JSON.stringify(/\/skills\//.test(record.path) ? mapStrings(JSON.parse(text), zipNorm) : normValue(JSON.parse(text)), null, 2) + '\n'; } catch { /* preserve invalid JSON as text */ }
      }
      return zipNorm(text);
    }, { accountExport: record.path === '/v1/me/export.zip' }); out.text = writer(record.name, out.body, 'json'); return out;
  }
  if (!isTextBody(record.contentType, record.rawBody)) {
    out.body_kind = 'binary'; out.body = { sha256: sha256hex(record.rawBody), length: record.rawBody.length }; return out;
  }
  out.body_kind = 'text'; out.text = writer(record.name, renderRecordedText(record.contentType, normalizeRecordedText(record.name, norm(record.rawBody.toString('utf8'))), record.name)); return out;
}

// ---- Host instance: seed + request list -----------------------------------

// R2-S4a: one pre-existing Postgres namespace beside a newly created file.
async function seedAndCallMixedStore({ instance, at }) {
 const created = await at('create-owner', 'POST', '/v1/admin/users', { apiKey: instance.adminKey, body: { emails: ['mixed-owner@example.com'] } });
 const owner = created.json.created[0].api_key;
 let base;
 const name = 'golden-mixed';
 if (instance.tag === 'hack') {
  await at('create-event', 'POST', '/v1/hack/events', { apiKey: owner, body: hackCreateBody(name) });
  await at('create-site', 'PUT', `/v1/hack/events/${name}/website/files?create=1`, { apiKey: owner, body: { files: { 'index.html': '<h1>Mixed storage</h1>' } } });
  base = `/v1/hack/events/${name}/website/storage`;
 } else {
  await at('create-site', 'POST', `/v1/sites/${name}/files`, { apiKey: owner, body: { files: { 'index.html': '<h1>Mixed storage</h1>' } } });
  base = `/v1/sites/${name}/storage`;
 }
 // Hack keeps an event's website under the event's holding account, named
 // by the event id, not the slug (ServeEventWebsiteStorage).
 const site = instance.tag === 'hack'
  ? `sites s JOIN events e ON s.user_id=e.account_id AND s.name=e.id::text WHERE e.slug='${name}'`
  : `sites s WHERE s.name='${name}'`;
 psql(instance.dbURL, `INSERT INTO site_storage_resources(site_id,name,kind,kv_store) SELECT s.id,'pg','kv','postgres' FROM ${site};
 INSERT INTO site_storage_kv(site_id,resource_name,key,value) SELECT s.id,'pg','seed','100'::jsonb FROM ${site}`);
 await at('create-file-namespace', 'PUT', `${base}/resources/file`, { apiKey: owner, body: { kind: 'kv', preset: 'private' } });
 for (const store of ['pg', 'file']) {
  await at(`${store}-set`, 'PUT', `${base}/kv/${store}/keys/entry`, { apiKey: owner, body: { value: { n: 100, text: 'saved' } } });
  await at(`${store}-get`, 'GET', `${base}/kv/${store}/keys/entry`, { apiKey: owner });
  await at(`${store}-list`, 'GET', `${base}/kv/${store}/keys`, { apiKey: owner });
 }
 await at('mixed-usage', 'GET', `${base}/usage`, { apiKey: owner });
 for (const store of ['pg', 'file']) {
  await at(`${store}-delete`, 'DELETE', `${base}/kv/${store}/keys/entry`, { apiKey: owner });
  await at(`${store}-missing`, 'GET', `${base}/kv/${store}/keys/entry`, { apiKey: owner });
 }
 await at('usage-after-delete', 'GET', `${base}/usage`, { apiKey: owner });
}

async function seedAndCallHost({ instance: host, calls, usedNames, at }) {

  await at('healthz', 'GET', '/healthz');
  await at('readyz', 'GET', '/readyz');
  await at('home', 'GET', '/');
  await at('robots-txt', 'GET', '/robots.txt');
  await at('sitemap-xml', 'GET', '/sitemap.xml');
  await at('llms-md', 'GET', '/llms.md');
  await at('agent-card', 'GET', '/.well-known/agent-card.json');
  await at('mcp-server-card', 'GET', '/.well-known/mcp/server-card.json');
  await at('ai-catalog', 'GET', '/.well-known/ai-catalog.json');
  await at('oauth-protected-resource', 'GET', '/.well-known/oauth-protected-resource');
  await at('oauth-authorization-server', 'GET', '/.well-known/oauth-authorization-server');
  await at('pricing', 'GET', '/pricing');
  await at('features', 'GET', '/features');
  await at('openapi-yaml', 'GET', '/openapi.yaml');
  await at('openapi-json', 'GET', '/openapi.json');

  // The admin mints the owner's account; everything from here acts as them.
  const created = await at('admin-create-owner', 'POST', '/v1/admin/users', {
    apiKey: host.adminKey,
    body: { emails: ['owner@example.com'] },
  });
  const owner = created.json.created[0].api_key;

  await at('admin-usage', 'GET', '/v1/admin/usage', { apiKey: host.adminKey });
  await at('admin-sites', 'GET', '/v1/admin/sites', { apiKey: host.adminKey });
  await at('admin-users', 'GET', '/v1/admin/users', { apiKey: host.adminKey });

  await at('me', 'GET', '/v1/me', { apiKey: owner });
  await at('me-keys', 'GET', '/v1/me/keys', { apiKey: owner });

  // Site 1: deployed from a tarball (the repo's own sample-site.tar.gz),
  // then a second version through the JSON files API, then a passcode.
  const tarball = readFileSync(path.join(REPO_ROOT, 'sample-site.tar.gz'));
  await at('site1-create-tarball', 'POST', '/v1/sites/site-one', {
    apiKey: owner,
    raw: tarball,
    contentType: 'application/gzip',
  });
  await at('site1-update-files-v2', 'PUT', '/v1/sites/site-one/files', {
    apiKey: owner,
    body: { files: { 'index.html': '<h1>site one, v2</h1>' } },
  });
  await at('site1-versions', 'GET', '/v1/sites/site-one/versions', { apiKey: owner });
  await at('site1-set-visibility-unlisted', 'PUT', '/v1/sites/site-one/visibility', {
    apiKey: owner,
    body: { visibility: 'unlisted' },
  });
  await at('site1-lock-generate', 'PUT', '/v1/sites/site-one/lock', {
    apiKey: owner,
    body: { generate: true },
  });
  const lock = await at('site1-lock-get', 'GET', '/v1/sites/site-one/lock', { apiKey: owner });

  const identity = await at('passcode-owner', 'GET', '/v1/me', { apiKey: owner, qualifier: true });
  const siteHost = `site-one.${identity.json.handle}.golden-host.invalid`;
  const browser = { Host: siteHost, Origin: `http://${siteHost}`, 'Sec-Fetch-Site': 'same-origin' };
  await at('passcode-anonymous-page', 'GET', '/', { headers: browser, qualifier: true });
  for (const [label, passcode] of [['wrong', 'wrong-passcode'], ['right', lock.json.passcode]]) {
    const unlocked = await at(`passcode-${label}`, 'POST', '/v1/site-unlock', {
      headers: browser, raw: new URLSearchParams({ passcode, next: '/' }).toString(),
      contentType: 'application/x-www-form-urlencoded',
    });
    if (label === 'right') await at('passcode-page-after-unlock', 'GET', '/', {
      headers: { ...browser, Cookie: unlocked.cookies.map(c => c.split(';')[0]).join('; ') }, qualifier: true,
    });
  }

  // Site 2: created via the JSON files API directly, then its access toggle.
  // Named-viewer creation and emailed-code sign-in are exercised below.
  await at('site2-create-files', 'POST', '/v1/sites/site-two/files', {
    apiKey: owner,
    body: { files: { 'index.html': '<h1>site two</h1>', 'about.html': '<p>about</p>' } },
  });
  await at('site2-set-access-anyone', 'PUT', '/v1/sites/site-two/access', {
    apiKey: owner,
    body: { access: 'anyone' },
  });
  await at('site2-get-access', 'GET', '/v1/sites/site-two/access', { apiKey: owner });

  // Site 3: a second version and an active-version change, to exercise the
  // version list beyond a single entry.
  await at('site3-create-files', 'POST', '/v1/sites/site-three/files', {
    apiKey: owner,
    body: { files: { 'index.html': '<h1>site three, v1</h1>' } },
  });
  await at('site3-update-files-v2', 'PUT', '/v1/sites/site-three/files', {
    apiKey: owner,
    body: { files: { 'index.html': '<h1>site three, v2</h1>' } },
  });
  await at('site3-versions', 'GET', '/v1/sites/site-three/versions', { apiKey: owner });
  await at('site3-set-active-v1', 'PUT', '/v1/sites/site-three/active-version', {
    apiKey: owner,
    body: { version_number: 1 },
  });

  await at('sites-list', 'GET', '/v1/sites', { apiKey: owner });

  // Storage: one resource of each kind (KV, SQLite, files), declared then used.
  await at('storage-declare-kv', 'PUT', '/v1/sites/site-one/storage/resources/settings', {
    apiKey: owner,
    body: { kind: 'kv' },
  });
  await at('storage-kv-put', 'PUT', '/v1/sites/site-one/storage/kv/settings/keys/theme', {
    apiKey: owner,
    body: { value: 'dark' },
  });
  await at('storage-kv-get', 'GET', '/v1/sites/site-one/storage/kv/settings/keys/theme', { apiKey: owner });
  await at('storage-kv-list', 'GET', '/v1/sites/site-one/storage/kv/settings/keys', { apiKey: owner });

  await at('storage-declare-sqlite', 'PUT', '/v1/sites/site-one/storage/resources/tasks', {
    apiKey: owner,
    body: { kind: 'sqlite' },
  });
  await at('storage-sql-schema', 'POST', '/v1/sites/site-one/storage/sqlite/tasks/schema', {
    apiKey: owner,
    body: { sql: 'CREATE TABLE tasks (id INTEGER PRIMARY KEY, title TEXT)' },
  });
  await at('storage-sql-execute', 'POST', '/v1/sites/site-one/storage/sqlite/tasks/execute', {
    apiKey: owner,
    body: { sql: 'INSERT INTO tasks (id, title) VALUES (?, ?)', params: [1, 'first task'] },
  });
  await at('storage-sql-query', 'POST', '/v1/sites/site-one/storage/sqlite/tasks/query', {
    apiKey: owner,
    body: { sql: 'SELECT id, title FROM tasks ORDER BY id', params: [] },
  });

  await at('storage-declare-files', 'PUT', '/v1/sites/site-one/storage/resources/gallery', {
    apiKey: owner,
    body: { kind: 'files' },
  });
  await at('storage-files-put', 'PUT', '/v1/sites/site-one/storage/files/gallery/objects/cover.txt', {
    apiKey: owner,
    raw: Buffer.from('a small file'),
    contentType: 'text/plain',
  });
  await at('storage-files-list', 'GET', '/v1/sites/site-one/storage/files/gallery/objects', { apiKey: owner });
  await at('storage-usage', 'GET', '/v1/sites/site-one/storage/usage', { apiKey: owner });
  await at('storage-resources', 'GET', '/v1/sites/site-one/storage/resources', { apiKey: owner });

  // MCP: tools/list on both legal protocol versions, then one read tool call.
  await at('tools/call list_sites', 'POST', '/mcp', {
    apiKey: owner,
    body: { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'list_sites', arguments: {} } },
  });

  await widerCoverage(host, calls, usedNames, owner);

  await at('export-me-zip-head', 'GET', '/v1/me/export.zip', { apiKey: owner });
}

// ---- Hack instance: seed + request list ------------------------------------

// The event's dates are seed input, not server-generated noise: the server
// (parseEventTime + rfc3339UTC in internal/handler/hack.go) just echoes a
// bare YYYY-MM-DD back as that day's midnight UTC, formatted RFC3339. main()
// puts their RFC3339 form into knownSeedTimestamps() so the normalizer in
// lib.mjs leaves these two exact values as-is instead of folding them into
// the generic <ts> placeholder along with every value the server actually
// generated (created_at, deleted_at, ...) -- see lib.mjs normalizeText.
const SEED_EVENT_STARTS_AT = '2020-10-01';
const SEED_EVENT_ENDS_AT = '2020-10-03';

function rfc3339UTCMidnight(dateOnly) {
  return `${dateOnly}T00:00:00Z`;
}

function knownSeedTimestamps() {
  return new Set([rfc3339UTCMidnight(SEED_EVENT_STARTS_AT), rfc3339UTCMidnight(SEED_EVENT_ENDS_AT)]);
}

function hackCreateBody(slug, extra) {
  const body = {
    slug,
    title: 'Golden Harness Hack',
    organiser_name: 'Ada Lovelace',
    organisation: 'Analytic Club',
    contact_email: 'ada@example.com',
    purpose: 'exercise every stage once',
    expected_participants: 10,
    starts_at: SEED_EVENT_STARTS_AT,
    ends_at: SEED_EVENT_ENDS_AT,
    time_zone: 'UTC',
  };
  return { ...body, ...extra };
}

async function seedAndCallHack({ instance: hack, calls, namer, exact, usedNames, at }) {

  await at('healthz', 'GET', '/healthz');
  await at('readyz', 'GET', '/readyz');
  await at('home', 'GET', '/');
  await at('robots-txt', 'GET', '/robots.txt');
  await at('llms-md', 'GET', '/llms.md');
  await at('directory', 'GET', '/directory');
  await at('agent-card', 'GET', '/.well-known/agent-card.json');
  await at('mcp-server-card', 'GET', '/.well-known/mcp/server-card.json');

  const slug = 'golden-hack';

  // Accounts: organiser, co-organiser, 3 participants, a judge. Each is a
  // bare admin-minted account that then joins the event over the API, the
  // same path a real signup takes.
  async function mintPerson(prefix) {
    const email = `${prefix}@example.com`;
    await at(`account-${prefix}-signin-start`, 'POST', '/v1/auth', { body: { email } });
    await hack.mail.waitFor(messages => messages.some(m => m.to.includes(email) && /sign-in code/.test(m.subject)), `${prefix} sign-in`);
    const message = hack.mail.messages.find(m => m.to.includes(email) && /sign-in code/.test(m.subject));
    const code = message.subject.match(/\b\d{6}\b/)[0];
    const verified = await at(`account-${prefix}-signin-verify`, 'POST', '/v1/auth/verify', { body: { email, code } });
    if (!verified.json?.api_key) throw new Error(`account sign-in failed: ${prefix}`);
    await hack.mail.waitFor(messages => messages.some(m => m.to.includes(email) && m.subject === 'New sign-in to your Simple Host account'), `${prefix} sign-in alert`);
    return verified.json.api_key;
  }
  await at('admin-create-spare', 'POST', '/v1/admin/users', { apiKey: hack.adminKey, body: { emails: ['admin-created@example.com'] } });
  const organiser = await mintPerson('organiser');
  const coorganiser = await mintPerson('coorg');
  const judge = await mintPerson('judge');
  const participants = [await mintPerson('part-a'), await mintPerson('part-b'), await mintPerson('part-c')];

  // draft -> open
  const createdEvent = await at('event-create', 'POST', '/v1/hack/events', {
    apiKey: organiser,
    body: hackCreateBody(slug),
  });
  await at('event-get-draft', 'GET', `/v1/hack/events/${slug}`, { apiKey: organiser });
  await at('event-stage-open', 'POST', `/v1/hack/events/${slug}/stage`, {
    apiKey: organiser,
    body: { stage: 'open' },
  });
  const opened = await at('event-get-open', 'GET', `/v1/hack/events/${slug}`, { apiKey: organiser });
  const joinCode = opened.json.organiser.join_code;
  const judgeCode = opened.json.organiser.judge_code;
  // These two short codes (and the organiser-invite code just below)
  // are the only random, server-minted values that show up in a
  // request *path* rather than just a body field -- registerSecret puts
  // them in the same exact-replacement map the base URL/temp dir use, so
  // every later path (and every earlier body field) that carries one
  // normalizes to the same placeholder.
  namer.beginRequest('event-create');
  registerSecret(exact, namer, 'code', joinCode);
  registerSecret(exact, namer, 'code', judgeCode);

  // Co-organiser invite.
  const invite = await at('organiser-invite-create', 'POST', `/v1/hack/events/${slug}/organiser-invite`, {
    apiKey: organiser,
  });
  const inviteCode = invite.json.url.split('/').pop();
  registerSecret(exact, namer, 'code', inviteCode);
  await at('organiser-invite-get', 'GET', `/v1/hack/organiser/${inviteCode}`, { apiKey: coorganiser });
  await at('organiser-invite-accept', 'POST', `/v1/hack/organiser/${inviteCode}`, {
    apiKey: coorganiser,
    body: { accept_coc: true, display_name: 'Co Organiser' },
  });

  // Judge joins.
  await at('judge-join', 'POST', `/v1/hack/judge/${judgeCode}`, {
    apiKey: judge,
    body: { accept_coc: true, display_name: 'Jude the Judge' },
  });

  // Three participants join and each starts their own team (one team's
  // whole lifecycle -- join, request-to-join-another-team, entry, site,
  // judging -- is exercised below via Team Alpha; the other two exist so
  // the team list and results have more than one row).
  const names = ['Pat A', 'Pat B', 'Pat C'];
  for (let i = 0; i < participants.length; i++) {
    await at(`participant-join-${i}`, 'POST', `/v1/hack/join/${joinCode}`, {
      apiKey: participants[i],
      body: { accept_coc: true, display_name: names[i] },
    });
  }
  const teamA = await at('team-a-create', 'POST', `/v1/hack/events/${slug}/teams`, {
    apiKey: participants[0],
    body: { name: 'Team Alpha' },
  });
  const teamASlug = teamA.json.slug;
  // A team's own join code/link (team.json.code) is random per run, like
  // the event's join/judge codes above, and then echoed back by
  // teams-list -- registerSecret as soon as it comes back so every later
  // occurrence normalizes to the same placeholder.
  registerSecret(exact, namer, 'code', teamA.json.code);
  const teamB = await at('team-b-create', 'POST', `/v1/hack/events/${slug}/teams`, {
    apiKey: participants[1],
    body: { name: 'Team Beta' },
  });
  const teamBSlug = teamB.json.slug;
  registerSecret(exact, namer, 'code', teamB.json.code);
  const teamC = await at('team-c-create', 'POST', `/v1/hack/events/${slug}/teams`, {
    apiKey: participants[2],
    body: { name: 'Team Gamma' },
  });
  registerSecret(exact, namer, 'code', teamC.json.code);

  const teamsList = await at('teams-list', 'GET', `/v1/hack/events/${slug}/teams`, { apiKey: organiser });
  const teamIDBySlug = new Map(teamsList.json.teams.map((t) => [t.slug, t.id]));
  const teamAID = teamIDBySlug.get(teamASlug);
  const teamBID = teamIDBySlug.get(teamBSlug);
  await at('my-teams', 'GET', '/v1/hack/my-teams', { apiKey: participants[0] });
  await at('teams-open-list', 'GET', `/v1/hack/events/${slug}/teams/open`, { apiKey: participants[0] });

  // Rubric, before judging settings need it.
  const rubric = await at('rubric-put', 'PUT', `/v1/hack/events/${slug}/rubric`, {
    apiKey: organiser,
    body: { criteria: [{ name: 'Impact', description: 'does it matter', weight: 100, max_points: 10 }] },
  });
  const criterionID = rubric.json.criteria[0].id;
  await at('rubric-get', 'GET', `/v1/hack/events/${slug}/rubric`, { apiKey: organiser });

  // building -> each team writes (and A submits) an entry.
  await at('event-stage-building', 'POST', `/v1/hack/events/${slug}/stage`, {
    apiKey: organiser,
    body: { stage: 'building' },
  });
  // The default ("build") entry preset requires a video or a live site;
  // Team Alpha satisfies it with its deployed site (below), Team Beta with
  // a video URL, so both are judgeable without a real video file.
  await at('entry-a-put', 'PUT', `/v1/hack/events/${slug}/entry`, {
    apiKey: participants[0],
    body: { title: 'Team Alpha entry', problem: 'a real problem', tagline: 'fixes it', audience: 'everyone', tools: ['Go'], maturity: 'working' },
  });
  await at('entry-b-put', 'PUT', `/v1/hack/events/${slug}/entry`, {
    apiKey: participants[1],
    body: {
      title: 'Team Beta entry', problem: 'another problem', tagline: 'also fixes it', audience: 'everyone',
      tools: ['Node'], maturity: 'working', video_url: 'https://example.com/team-beta-demo',
    },
  });
  await at('entry-b-submit', 'POST', `/v1/hack/events/${slug}/entry/submit`, { apiKey: participants[1] });
  // Content delivery runs on boot and once a minute. Restart to flush the receipt now.
  await hack.screens('off');
  await hack.mail.waitFor(messages => messages.some(m => m.to.includes('part-b@example.com') &&
    m.subject === 'Golden Harness Hack: entry received'), 'b entry receipt');

  // Team Alpha's own site, deployed from the repo's sample tarball through
  // the event (its first deploy, so ?create=1), which is what lets its
  // entry above satisfy the one-video-or-a-live-site rule.
  const tarball = readFileSync(path.join(REPO_ROOT, 'sample-site.tar.gz'));
  await at('team-a-site-deploy', 'PUT', `/v1/hack/events/${slug}/site?create=1`, {
    apiKey: participants[0],
    raw: tarball,
    contentType: 'application/gzip',
  });
  await at('sim449-team-b-deploy', 'PUT', `/v1/hack/events/${slug}/site?create=1`, {
    apiKey: participants[1], raw: tarball, contentType: 'application/gzip',
  });
  const bKey = await at('sim449-team-b-key', 'POST', `/v1/hack/events/${slug}/key`, { apiKey: participants[1] });
  await at('entry-a-submit', 'POST', `/v1/hack/events/${slug}/entry/submit`, { apiKey: participants[0] });
  // Content delivery runs on boot and once a minute. Restart to flush the receipt now.
  await hack.screens('off');
  await hack.mail.waitFor(messages => messages.some(m => m.to.includes('part-a@example.com') &&
    m.subject === 'Golden Harness Hack: entry received'), 'a entry receipt');
  await at('team-a-site-status', 'GET', `/v1/hack/events/${slug}/site`, { apiKey: participants[0] });
  const teamKeyResult = await at('team-a-key-make', 'POST', `/v1/hack/events/${slug}/key`, { apiKey: participants[0] });
  hack.teamKey = teamKeyResult.json.key;
  await at('team-a-key-get', 'GET', `/v1/hack/events/${slug}/key`, { apiKey: participants[0] });
  await at('tools/call get_site team-site', 'POST', '/mcp', {
    apiKey: hack.teamKey,
    body: { jsonrpc: '2.0', id: 10, method: 'tools/call',
      params: { name: 'get_site', arguments: { site: teamASlug } } },
  });

  await at('entries-list', 'GET', `/v1/hack/events/${slug}/entries`, { apiKey: organiser });

  await at('team-message-send', 'POST', `/v1/hack/events/${slug}/team/messages`, { apiKey: participants[0], body: { text: 'Please review our entry.' } });
  await at('organiser-message-send', 'POST', `/v1/hack/events/${slug}/team-messages`, { apiKey: organiser, body: { team_ids: [teamAID], text: 'Ready for judging.' } });
  await at('team-message-read', 'GET', `/v1/hack/events/${slug}/team/messages`, { apiKey: participants[0] });
  await at('organiser-message-read', 'GET', `/v1/hack/events/${slug}/teams/${teamASlug}/messages`, { apiKey: organiser });
  await at('announcement-create', 'POST', `/v1/hack/events/${slug}/announcements`, { apiKey: organiser, body: { title: 'Welcome', body: 'Build something useful.\nShare your idea.', pinned: true, email_participants: false } });
  await at('announcement-list', 'GET', `/v1/hack/events/${slug}/announcements`, { apiKey: participants[0] });
  await at('team-key-other-site', 'GET', '/v1/sites/some-other-site/versions', { apiKey: hack.teamKey, qualifier: true });

  await at('team-redirect-site-update', 'PUT', `/v1/sites/${teamASlug}/files`, { apiKey: hack.teamKey, body: { files: { 'index.html': '<h1>Team Alpha</h1>', 'about.html': '<p>Team project</p>', '_redirects': '/old /about 302\n' } } });
  const teamHost = `${teamASlug}.${slug}.golden-hack.invalid`;
  await at('team-redirect-old', 'GET', '/old', { headers: { Host: teamHost }, qualifier: true });
  await at('team-plain-address', 'GET', '/about', { headers: { Host: teamHost }, qualifier: true });
  await at('team-profile-list-refused', 'PUT', `/v1/sites/${teamASlug}/visibility`, { apiKey: hack.teamKey, body: { visibility: 'public' } });

  const cIdentity = await at('team-c-identity', 'GET', '/v1/me', { apiKey: participants[2], qualifier: true });
  await at('team-c-member-remove', 'DELETE', `/v1/hack/events/${slug}/teams/${teamC.json.slug}/members/${cIdentity.json.id}`, { apiKey: organiser });

  // closed -> judging.
  await at('event-stage-closed', 'POST', `/v1/hack/events/${slug}/stage`, {
    apiKey: organiser,
    body: { stage: 'closed' },
  });
  await at('judging-settings-select-organisers', 'PATCH', `/v1/hack/events/${slug}/judging/settings`, { apiKey: organiser, body: { include_organisers: true } });
  await at('judging-settings-preserve-organisers', 'PATCH', `/v1/hack/events/${slug}/judging/settings`, { apiKey: organiser, body: {} });
  await at('judging-settings-selected-get', 'GET', `/v1/hack/events/${slug}/judging/settings`, { apiKey: organiser });
  await at('judging-settings-clear-organisers', 'PATCH', `/v1/hack/events/${slug}/judging/settings`, { apiKey: organiser, body: { organiser_judges: [], include_organisers: true } });
  await at('event-stage-judging', 'POST', `/v1/hack/events/${slug}/stage`, {
    apiKey: organiser,
    body: { stage: 'judging' },
  });

  await at('judging-settings-get', 'GET', `/v1/hack/events/${slug}/judging/settings`, { apiKey: organiser });
  await at('judge-queue', 'GET', `/v1/hack/events/${slug}/judge/queue`, { apiKey: judge });

  await at('judge-score-team-a', 'PUT', `/v1/hack/events/${slug}/judge/scores/${teamAID}`, {
    apiKey: judge,
    body: { scores: [{ criterion_id: criterionID, points: 9 }], comment: 'great work' },
  });
  await at('judge-score-team-b', 'PUT', `/v1/hack/events/${slug}/judge/scores/${teamBID}`, {
    apiKey: judge,
    body: { scores: [{ criterion_id: criterionID, points: 7 }], comment: 'good work' },
  });
  await at('judging-dashboard', 'GET', `/v1/hack/events/${slug}/judging/dashboard`, { apiKey: organiser });

  // results.
  await at('event-stage-results', 'POST', `/v1/hack/events/${slug}/stage`, {
    apiKey: organiser,
    body: { stage: 'results' },
  });
  await at('results-publish', 'POST', `/v1/hack/events/${slug}/results/publish`, {
    apiKey: organiser,
    body: {},
  });
  await at('my-results', 'GET', `/v1/hack/events/${slug}/my-results`, { apiKey: participants[0] });
  await at('event-get-results', 'GET', `/v1/hack/events/${slug}`, { apiKey: organiser });
  await at('event-people', 'GET', `/v1/hack/events/${slug}/people`, { apiKey: organiser });
  await at('event-website-get', 'GET', `/v1/hack/events/${slug}/website`, { apiKey: organiser });
  await at('events-list', 'GET', '/v1/hack/events', { apiKey: organiser });
  await at('admin-hack-events', 'GET', '/v1/admin/hack/events', { apiKey: hack.adminKey });

  // MCP: tools/list for the hack edition, then a read-only tool call.
  await at('tools/call hack_get_event', 'POST', '/mcp', {
    apiKey: organiser,
    body: { jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: 'hack_get_event', arguments: { slug } } },
  });

  await at('submissions-participant', 'GET', `/v1/hack/events/${slug}/submissions`, { apiKey: participants[0] });
  await at('submissions-public-enable', 'PATCH', `/v1/hack/events/${slug}`, { apiKey: organiser, body: { submissions_public: true } });
  await at('event-public-page', 'GET', `/e/${slug}`);
  const adminSiteList = await at('admin-team-sites', 'GET', '/v1/admin/sites', { apiKey: hack.adminKey, qualifier: true });
  const adminSites = Array.isArray(adminSiteList.json) ? adminSiteList.json : adminSiteList.json.sites;
  const teamSiteID = adminSites.find(s => s.name === teamASlug).id;
  hack.roles = [['participant', participants[0]], ['judge', judge]];
  await at('sim449-team-a-page-before-delete', 'GET', '/', { headers: { Host: `${teamASlug}.${slug}.golden-hack.invalid` }, qualifier: true });
  await at('sim449-team-a-delete', 'DELETE', `/v1/admin/sites/${teamSiteID}`, { apiKey: hack.adminKey });
  await at('sim449-team-a-takedown-while-deleted', 'POST', `/v1/hack/events/${slug}/teams/${teamASlug}/takedown`, { apiKey: organiser, body: { reason: 'Golden moderation check' } });
  await at('taken-down-team-key-read', 'GET', `/v1/sites/${teamASlug}/versions`, { apiKey: hack.teamKey, qualifier: true });
  await at('sim449-team-a-restore-from-deleted', 'POST', `/v1/sites/${teamASlug}/restore`, { apiKey: hack.teamKey });
  await at('sim449-team-a-member-restore-from-deleted', 'POST', `/v1/sites/${teamASlug}/restore`, { apiKey: participants[0] });
  await at('sim449-team-a-MCP-restore-from-deleted', 'POST', '/mcp', { apiKey: hack.teamKey,
    body: { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: 'restore_site', arguments: { site: teamASlug } } } });
  await at('sim449-team-a-status-after-restore', 'GET', `/v1/hack/events/${slug}/site`, { apiKey: participants[0], qualifier: true });
  await at('sim449-team-a-page-after-restore', 'GET', '/', { headers: { Host: `${teamASlug}.${slug}.golden-hack.invalid` }, qualifier: true });
  await at('error team-key-on-another-site REST versions', 'GET', '/v1/sites/another-site/versions', { apiKey: hack.teamKey });
  await at('error team-key-on-another-site MCP list_versions', 'POST', '/mcp', { apiKey: hack.teamKey,
    body: { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: 'list_versions', arguments: { site: 'another-site' } } } });
  await at('sim449-team-b-takedown-live', 'POST', `/v1/hack/events/${slug}/teams/${teamBSlug}/takedown`, {
    apiKey: organiser, body: { reason: 'Golden mirror moderation' },
  });
  const mirrorSites = await at('sim449-team-b-admin-sites', 'GET', '/v1/admin/sites', { apiKey: hack.adminKey });
  const mirrorList = Array.isArray(mirrorSites.json) ? mirrorSites.json : mirrorSites.json.sites;
  const mirrorID = mirrorList.find(site => site.name === teamBSlug).id;
  await at('sim449-team-b-page-before-delete', 'GET', '/', {
    headers: { Host: `${teamBSlug}.${slug}.golden-hack.invalid` }, qualifier: true,
  });
  await at('sim449-team-b-delete', 'DELETE', `/v1/admin/sites/${mirrorID}`, { apiKey: hack.adminKey });
  await at('sim449-team-b-lift-while-deleted', 'POST', `/v1/hack/events/${slug}/teams/${teamBSlug}/restore`, { apiKey: organiser });
  await at('sim449-team-b-restore-from-deleted', 'POST', `/v1/sites/${teamBSlug}/restore`, { apiKey: bKey.json.key });
  await at('sim449-team-b-MCP-restore-from-deleted', 'POST', '/mcp', { apiKey: bKey.json.key,
    body: { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: 'restore_site', arguments: { site: teamBSlug } } } });
  await at('sim449-team-b-status-after-restore', 'GET', `/v1/hack/events/${slug}/site`, { apiKey: participants[1] });
  await at('sim449-team-b-page-after-restore', 'GET', '/', { headers: { Host: `${teamBSlug}.${slug}.golden-hack.invalid` }, qualifier: true });
  await widerCoverage(hack, calls, usedNames, organiser);
  const participantID = teamA.json.members?.[0]?.user_id || (await at('participant-identity', 'GET', '/v1/me', { apiKey: participants[0], qualifier: true })).json.id;
  await at('team-person-remove', 'DELETE', `/v1/hack/events/${slug}/people/${participantID}`, { apiKey: organiser });
  await at('error inactive-team-key REST sites', 'GET', '/v1/sites', { apiKey: hack.teamKey, qualifier: true });
  await at('error inactive-team-key MCP list_versions', 'POST', '/mcp', { apiKey: hack.teamKey,
    body: { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: 'list_versions', arguments: { site: teamASlug } } } });
  void createdEvent; // seeded above; its creation response is already recorded
}

// Shared discovery and boundary requests run on both editions, including refusals.
async function widerCoverage(instance, calls, usedNames, owner) {
  const at = call(instance, calls, usedNames);
  const get = async (route, opts = {}) => {
    if (usedNames.has(`GET ${route}`)) return;
    return at(`GET ${route}`, 'GET', route, opts);
  };
  // Enumerate the generated inventory, so even edition-specific 404s are kept.
  const inventory = JSON.parse(readFileSync(path.join(REPO_ROOT, 'docs/generated/inventory.json')));
  for (const route of FIXED_GETS) await get(route);
  for (const route of FIXED_GETS) {
    if (!/^\/v1\/(me(?:\/|$)|admin\/|notifications$|analytics\/sites$|hack\/)/.test(route)) continue;
    if (['/v1/me/export.zip', '/v1/me/export.tar.gz', '/v1/admin/export.tar.gz',
      '/v1/admin/paper-metrics'].includes(route)) continue;
    const who = route.startsWith('/v1/admin/') ? 'admin' : instance.tag === 'hack' ? 'organiser' : 'owner';
    await at(`GET ${route} as ${who}`, 'GET', route, {
      apiKey: who === 'admin' ? instance.adminKey : owner, qualifier: true,
    });
    if (instance.tag === 'hack' && route.startsWith('/v1/hack/')) {
      for (const [role, key] of instance.roles) await at(`GET ${route} as ${role}`, 'GET', route, {
        apiKey: key, qualifier: true,
      });
    }
  }
  const indexRecord = calls.find(r => r.name === 'GET /.well-known/skills/index.json');
  const index = JSON.parse(indexRecord.rawBody.toString());
  for (const skill of index.skills || []) {
    for (const file of skill.files) await get(`/v1/skills/${skill.name}/${file}`);
    await get(`/skills/${skill.name}.zip`);
  }
  if (!usedNames.has('GET /v1/admin/usage')) await get('/v1/admin/usage', { apiKey: instance.adminKey });
  // The admin settings surface is a command, not an HTTP endpoint.
  const settings = execFileSync(instance.binary, ['settings', '--json'], { env: instance.env, timeout: 15000 });
  pseudoCall(calls, usedNames, {
    name: 'admin-settings', method: 'COMMAND', path: 'settings --json', status: 0,
    contentType: 'application/json', cacheControl: '', location: '', etag: '',
    setCookie: [], rawBody: settings, exact: instance.exact,
  });
  const rpc = (name, method, params, apiKey = owner) => at(name, 'POST', '/mcp', { apiKey, body: { jsonrpc: '2.0', id: 10, method, params } });
  const connections = [['personal', owner]];
  const deploy = await at('deploy-key-create', 'POST', '/v1/me/keys', { apiKey: owner, body: { name: 'golden deploy', scope: 'deploy' } });
  if (deploy.json?.api_key) {
    connections.push(['deploy-only', deploy.json.api_key]);
    await at('error deploy-only-key REST account', 'GET', '/v1/me', { apiKey: deploy.json.api_key, qualifier: true });
  }
  if (deploy.json?.api_key) await rpc('error deploy-only-key MCP who_am_i', 'tools/call', { name: 'who_am_i', arguments: {} }, deploy.json.api_key);
  if (instance.teamKey) connections.push(['team-site', instance.teamKey]);
  // Restart the same local instance to change process-only app flags.
  for (const screens of ['off', 'on']) {
    await instance.screens(screens);
    for (const [kind, key] of connections) {
      const suffix = screens === 'off' ? `${kind} screens-off` : kind;
      const caps = { extensions: { 'io.modelcontextprotocol/ui': {} } };
      if (!usedNames.has(`initialize ${suffix}`)) await rpc(`initialize ${suffix}`, 'initialize', { protocolVersion: '2025-06-18', capabilities: caps, clientInfo: { name: 'golden', version: '1' } }, key);
      if (!usedNames.has(`tools/list ${suffix}`)) await rpc(`tools/list ${suffix}`, 'tools/list', {}, key);
      await rpc(`resources/list ${suffix}`, 'resources/list', {}, key);
    }
  }
  const tools = instance.tag === 'host' ? [
    ['who_am_i', {}], ['get_import_report', { import_id: 'missing' }], ['list_site_viewers', { site: 'site-two' }],
    ['storage_get_kv', { site: 'site-one', resource: 'settings', key: 'theme' }],
    ['passcode_screen', { action: 'begin', site: 'site-one' }], ['get_page_recipe', { topic: 'booking' }], ['list_notifications', {}],
  ] : [
    ['hack_get_rubric', { slug: 'golden-hack' }], ['hack_get_event_website', { slug: 'golden-hack' }],
    ['hack_event_storage_list_resources', { event: 'golden-hack' }], ['hack_show_organiser', { slug: 'golden-hack', view: 'overview' }], ['list_notifications', {}],
  ];
  tools.push(['find_tools', { query: 'site' }], ['run_tool', { name: instance.tag === 'host' ? 'list_versions' : 'hack_list_events', arguments: instance.tag === 'host' ? { site: 'site-three' } : {} }]);
  for (const [name, args] of tools) await rpc(`tools/call ${name}${name === 'run_tool' ? ' ' + args.name : ''}`, 'tools/call', { name, arguments: args });
  if (instance.tag === 'host') {
    const me = await at('profile-owner', 'GET', '/v1/me', { apiKey: owner, qualifier: true });
    const handle = me.json.handle;
    await at('redirect-site-create', 'POST', '/v1/sites/redirect-site/files', { apiKey: owner, body: { files: { 'index.html': '<p>redirect home</p>', 'new.html': '<p>new page</p>', '_redirects': '/old /new.html 302\n' } } });
    const host = `redirect-site.${handle}.golden-host.invalid`;
    await at('redirect-site-old', 'GET', '/old', { headers: { Host: host }, qualifier: true });
    await at('plain-address', 'GET', '/new', { headers: { Host: host }, qualifier: true });
    await at('profile-site-list', 'PUT', '/v1/sites/redirect-site/visibility', { apiKey: owner, body: { visibility: 'public' } });
    await get(`/v1/u/${handle}/showcase.json`);
    const listing = await at('moderation-site-create', 'POST', '/v1/sites/moderation-site/files', { apiKey: owner, body: { files: { 'index.html': '<p>Moderation</p>' } } });
    await at('moderation-site-delete', 'DELETE', '/v1/sites/moderation-site', { apiKey: owner });
    await at('moderation-site-takedown-deleted', 'POST', `/v1/admin/sites/${listing.json.id}/suspend`, { apiKey: instance.adminKey, body: { reason: 'Golden moderation check' } });
    await at('moderation-site-restore', 'POST', '/v1/sites/moderation-site/restore', { apiKey: owner });
    await at('moderation-site-page', 'GET', '/', { headers: { Host: `moderation-site.${handle}.golden-host.invalid` }, qualifier: true });
  }
  if (instance.tag === 'host') {
    for (const [name, method, route, body] of [
      ['host-hack-submissions', 'GET', '/v1/hack/events/golden-hack/submissions'],
      ['host-hack-team-message', 'POST', '/v1/hack/events/golden-hack/team/messages', { text: 'A question' }],
      ['host-hack-announcement', 'POST', '/v1/hack/events/golden-hack/announcements', { title: 'Welcome', body: 'Welcome' }],
      ['host-team-takedown', 'POST', '/v1/hack/events/golden-hack/teams/team-alpha/takedown', { reason: 'Golden moderation check' }],
    ]) await at(name, method, route, { apiKey: owner, body });
  }
  const privateSite = await at('private-create-named-viewer', 'POST', '/v1/sites/private-site/files', {
    apiKey: owner, body: { access: 'specific', viewers: ['viewer@example.com'],
      files: { 'index.html': '<p>private contents</p>' } },
  });
  if (instance.tag === 'host') {
    if (privateSite.status !== 201) throw new Error('private create failed');
    const me = await at('private-owner', 'GET', '/v1/me', { apiKey: owner, qualifier: true });
    const siteHost = `private-site.${me.json.handle}.golden-host.invalid`;
    const headers = { Host: siteHost, Origin: `http://${siteHost}`, 'Sec-Fetch-Site': 'same-origin' };
    await at('private-anonymous', 'GET', '/', { headers, qualifier: true });
    for (const person of ['viewer', 'outsider']) {
      const email = `${person}@example.com`;
      const form = fields => ({ headers, contentType: 'application/x-www-form-urlencoded',
        raw: new URLSearchParams({ email, next: '/', ...fields }).toString() });
      await at(`private-${person}-signin-start`, 'POST', '/v1/site-signin', form({}));
      await instance.mail.waitFor(messages => messages.some(m => m.to.includes(email)), email);
      const message = instance.mail.messages.find(m => m.to.includes(email));
      const code = message.subject.match(/\b\d{6}\b/)[0];
      const signedIn = await at(`private-${person}-signin-verify`, 'POST', '/v1/site-signin', form({ code }));
      const sessionHeaders = { ...headers, Cookie: signedIn.cookies.map(c => c.split(';')[0]).join('; ') };
      await at(`private-${person}-page`, 'GET', '/', { headers: sessionHeaders, qualifier: true });
      if (person === 'viewer') {
        await at('private-viewer-session', 'GET', '/v1/sites/private-site/me', { headers: sessionHeaders });
        await at('private-viewer-identity', 'GET', `/v1/u/${me.json.handle}/sites/private-site/me`, { headers: sessionHeaders });
        await at('private-viewer-logout', 'POST', '/v1/visitor/logout', { headers: { ...sessionHeaders, 'X-SH-CSRF': '1' } });
        await at('private-viewer-session-after-logout', 'GET', '/v1/sites/private-site/me', { headers: sessionHeaders });
      }
    }
  }

}

// ---- crash/signal cleanup ---------------------------------------------------
//
// Node does not run a suspended async function's try/finally blocks when the
// process is killed by a signal -- it just exits. addCleanup registers one
// resource-teardown step as soon as that resource exists (see bootInstance);
// runCleanupAndExit runs every one of them, most-recently-created first, then
// exits non-zero, so SIGINT/SIGTERM during a run still kills the servers,
// drops the throwaway databases and removes the temp data dirs instead of
// leaving them behind.
const cleanupTasks = [];
function addCleanup(fn) {
  cleanupTasks.push(fn);
}

let shuttingDown = false;
async function runCleanupAndExit(signal) {
  if (shuttingDown) return;
  shuttingDown = true;
  console.error(`golden: received ${signal}, cleaning up...`);
  for (const fn of cleanupTasks.reverse()) {
    try {
      await fn();
    } catch (err) {
      console.error(`golden: cleanup error: ${safeError(err)}`);
    }
  }
  process.exit(1);
}
process.on('SIGINT', () => runCleanupAndExit('SIGINT'));
process.on('SIGTERM', () => runCleanupAndExit('SIGTERM'));

// ---- entry point ------------------------------------------------------------

async function main() {
  const args = process.argv.slice(2), flags = {};
  function usage(message) {
    console.error(message || '');
    console.error('usage: node scripts/golden/run.mjs --host BIN [--hack BIN] --out DIR [--scenario NAME[,NAME...]|all] [--skip-scenario NAME[,NAME...]]');
    process.exit(2);
  }
  while (args.length) {
    const flag = args.shift();
    if (!['--host','--hack','--out','--scenario','--skip-scenario'].includes(flag) || !args.length || flags[flag]) usage('invalid arguments');
    flags[flag] = args.shift();
  }
  if (!flags['--host'] || !flags['--out']) usage();
  let scenarios;
  try { scenarios = selectScenarios(SCENARIOS, flags['--scenario'], flags['--skip-scenario']); }
  catch (err) { usage(err.message); }
  const outDir = path.resolve(flags['--out']);
  mkdirSync(outDir, { recursive: true });
  if (readdirSync(outDir).length) usage('output directory must be empty');
  const pgAdminURL = process.env.GOLDEN_PG_URL || 'postgres://postgres:postgres@127.0.0.1:55412';
  validatePostgresURL(pgAdminURL);
  validateScenarios(SCENARIOS);
  const runDate = new Date().toISOString().slice(0, 10);
  const started = Date.now(), runs = [];
  // Finish all proxy/CA setup before starting either product. A setup
  // failure therefore cannot launch any mail-enabled instance unprotected.
  const mailSinks = new Map();
  for (const product of ['hack', 'host']) for (const scenario of scenarios) {
    if (SCENARIOS[scenario][product]) mailSinks.set(`${product}/${scenario}`, await localMailSink(addCleanup));
  }
  for (const product of ['hack', 'host']) for (const scenario of [...scenarios].sort()) {
    const config = SCENARIOS[scenario];
    if (!config[product]) continue;
    const instanceStarted = Date.now();
    const namer = new Namer(), exact = new Map(), calls = [], usedNames = new Set();
    exact.dates = relativeDates(runDate);
    const instance = await bootInstance({
      binary: path.resolve(flags[product === 'hack' && flags['--hack'] ? '--hack' : '--host']),
      pgAdminURL, extraEnv: config.env?.[product] || {}, hosted: product === 'hack',
      tag: product, namer, exact, addCleanup, mail: mailSinks.get(`${product}/${scenario}`),
    });
    const stem = scenario === 'base' ? product : `${product}.${scenario}`;
    try {
      const at = call(instance, calls, usedNames);
      await config[product]({ instance, at, calls, usedNames, namer, exact, mail: instance.mail,
        registerSecret: (category, raw) => registerSecret(exact, namer, category, raw),
        mcp: (tool, args, apiKey, qualifier = '') => at(`tools/call ${tool} ${qualifier}`.trim(), 'POST', '/mcp', { apiKey, body: { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: tool, arguments: args } } }),
      });
      await instance.drainMail();
      await instance.stopProducer();
      await instance.mail.quiet();
      if (instance.mail.refused.length) throw new Error(`refused outbound destinations: ${instance.mail.refused.join(', ')}`);
      const writer = createTextWriter(outDir, `text/${stem}`);
      const requests = calls.map(c => finalizeCall(c, namer, knownSeedTimestamps(), writer));
      // Mail is the last recorded appearance, after all HTTP placeholders exist.
      namer.beginRequest('mail-outbox');
      const outbox = normalizeMailOutbox(instance.mail.messages, value => normalizeText(value, namer, requestReplacements('mail-outbox', exact), knownSeedTimestamps()), { namer, exact });
      const record = { name: 'mail-outbox', method: 'MAIL', path: 'outbox', status: 0,
        contentType: 'application/json', setCookie: [], rawBody: Buffer.from(JSON.stringify(outbox)), exact };
      pseudoCall(calls, usedNames, record);
      requests.push(finalizeCall(record, namer, knownSeedTimestamps(), writer));
      writeFileSync(path.join(outDir, `${stem}.json`), JSON.stringify({ format: 2, product, scenario, requests }, null, 2) + '\n');
      runs.push({ product, scenario, file: `${stem}.json`, text_dir: `text/${stem}`, requests: requests.length });
      console.error(`golden: ${product}/${scenario}: ${requests.length} requests, ${((Date.now() - instanceStarted) / 1000).toFixed(1)}s`);
    } finally {
      await instance.stop();
      if (instance.mail.refused.length) throw new Error(`refused outbound destinations: ${instance.mail.refused.join(', ')}`);
    }
  }
  if (new Date().toISOString().slice(0, 10) !== runDate) throw new Error('UTC date changed during recording; record again');
  writeFileSync(path.join(outDir, 'manifest.json'), JSON.stringify({ format: 2, runs }, null, 2) + '\n');
  console.error(`golden: captured in ${((Date.now()-started)/1000).toFixed(1)}s -> ${outDir}`);
}
main().catch(async err => {
  console.error('golden: FAILED:', safeError(err));
  await runCleanupAndExit('failure');
});
