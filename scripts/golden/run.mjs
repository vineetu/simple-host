#!/usr/bin/env node
// Golden API snapshot harness -- see scripts/golden/README.md.
//
//   node scripts/golden/run.mjs BINARY OUT.json
//
// Node 20+ built-ins only (global fetch, node:child_process, ...); the only
// external programs this script shells out to are `psql` (to make and tear
// down two throwaway databases) and the simple-host BINARY itself.
//
// It boots BINARY twice -- once as a Host instance, once as a Hack instance
// (EVENTS=hosted) -- against a fresh throwaway Postgres database each,
// seeds each through its own API, replays a fixed list of REST and MCP
// requests, and writes every {name, method, path, status, headers, body} to
// OUT.json with run-specific noise (ids, keys, tokens, ports, temp paths,
// timestamps, the build commit, box-wide disk/network counters) normalized
// to stable placeholders. Two runs of the same binary produce byte-identical
// files; a real behaviour change between two different binaries does not.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import crypto from 'node:crypto';
import { Namer, normalizeValue, normalizeText, sha256hex } from './lib.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '..', '..');

// A text/html body at or under this size is kept verbatim (normalized) in
// OUT.json so a diff shows the actual words that changed; above it, only a
// hash+length travel (large bodies are rare in the request list below, and
// a human reading a diff does not want a multi-KB blob inline).
const TEXT_INLINE_LIMIT = 2000;

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// ---- Postgres: one throwaway database per instance, via psql -------------

function psql(connURL, sql) {
  execFileSync('psql', [connURL, '-v', 'ON_ERROR_STOP=1', '-c', sql], { stdio: ['ignore', 'pipe', 'pipe'] });
}

function psqlFile(connURL, file) {
  execFileSync('psql', [connURL, '-v', 'ON_ERROR_STOP=1', '-f', file], { stdio: ['ignore', 'pipe', 'pipe'] });
}

function dropDatabaseQuiet(adminURL, dbName) {
  try {
    psql(adminURL, `DROP DATABASE IF EXISTS "${dbName}"`);
  } catch (err) {
    console.error(`golden: warning: failed to drop throwaway database ${dbName}: ${err.message}`);
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
  const child = spawn(binary, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  const chunks = [];
  child.stdout.on('data', (d) => chunks.push(d));
  child.stderr.on('data', (d) => chunks.push(d));
  return { child, log: () => Buffer.concat(chunks).toString('utf8') };
}

async function waitHealthy(baseURL, child, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`server exited (code ${child.exitCode}) before answering healthy`);
    }
    try {
      const res = await fetch(baseURL + '/healthz');
      if (res.ok) return;
    } catch {
      // not listening yet
    }
    await sleep(200);
  }
  throw new Error(`server never answered /healthz within ${timeoutMs}ms`);
}

async function stopServer(child) {
  if (child.exitCode !== null) return;
  child.kill('SIGTERM');
  const deadline = Date.now() + 5000;
  while (child.exitCode === null && Date.now() < deadline) await sleep(100);
  if (child.exitCode === null) child.kill('SIGKILL');
}

// ---- one running instance (Host or Hack) ----------------------------------

// bootInstance creates a throwaway database, loads db/schema.sql into it
// (the same file scripts/test-integration.sh uses -- a database built from
// it already has every migration's effect, so migrations themselves are
// never applied here), and starts BINARY against it on a free local port
// with no outbound network capability (no RESEND_API_KEY, no OAuth client
// secrets, no Turnstile, no EVENT_NAME_PEER) and mail off. `hosted` sets
// EVENTS=hosted for a Hack instance.
async function bootInstance({ binary, pgAdminURL, schemaSQL, hosted, tag, namer, exact }) {
  const dbName = `golden_${tag}_${process.pid}_${crypto.randomBytes(4).toString('hex')}`;
  const adminDBURL = `${pgAdminURL}/postgres?sslmode=disable`;
  psql(adminDBURL, `CREATE DATABASE "${dbName}"`);
  try {
    const dbURL = `${pgAdminURL}/${dbName}?sslmode=disable`;
    psqlFile(dbURL, schemaSQL);

    const dataDir = mkdtempSync(path.join(tmpdir(), `golden-${tag}-`));
    // Fixed, not random: two runs of the same binary must produce the same
    // bytes, and these never leak into a response body anyway (only
    // server-minted ids/keys/tokens do, which the regex-based normalizers
    // in lib.mjs already catch).
    const adminKey = crypto.createHash('sha256').update(`golden-admin-key-${tag}`).digest('hex');
    const passcodeEncKey = crypto.createHash('sha256').update(`golden-passcode-enc-key-${tag}`).digest('base64');

    let child, log, baseURL;
    const attempts = 3;
    for (let attempt = 1; attempt <= attempts; attempt++) {
      const port = await freePort();
      baseURL = `http://127.0.0.1:${port}`;
      const env = {
        PATH: process.env.PATH || '/usr/bin:/bin',
        DB_DSN: dbURL,
        ADMIN_API_KEY: adminKey,
        DATA_DIR: dataDir,
        SITE_DOMAIN: `golden-${tag}.invalid`,
        PUBLIC_BASE_URL: baseURL,
        PORT: String(port),
        BIND_ADDR: '127.0.0.1',
        PASSCODE_ENC_KEY: passcodeEncKey,
        RESEND_API_KEY: '',
        // Both default to off (the shared path model); turned on here,
        // with SITE_CERT_DIR left unset, so a site/team-site "has an
        // address of its own" for the passcode and hack team-site-deploy
        // requests below -- every handle counts as cert-ready with no
        // hand-off directory configured (sitehost.go), so this needs no
        // real certificate and no network call.
        PERSON_HOSTS: 'serve',
        SITE_HOSTS: 'serve',
      };
      if (hosted) env.EVENTS = 'hosted';
      ({ child, log } = spawnServer(binary, env));
      try {
        await waitHealthy(baseURL, child, 30000);
        break;
      } catch (err) {
        await stopServer(child);
        if (attempt === attempts) {
          throw new Error(`${err.message}\n--- server output ---\n${log().slice(-4000)}`);
        }
        // Most likely the free port got taken between the probe and the
        // bind (another process on this shared box); try again on a new one.
      }
    }

    // Registered in the shared Namer (and the single exact-replacement map
    // every captured call shares -- see registerSecret) in the order each
    // instance boots, so the placeholder index reflects when the value
    // first appears, same as the regex-matched ids/keys/tokens.
    exact.set(baseURL, namer.placeholderFor('base', baseURL));
    exact.set(dataDir, namer.placeholderFor('tmpdir', dataDir));

    return {
      tag,
      baseURL,
      adminKey,
      exact,
      async stop() {
        await stopServer(child);
        rmSync(dataDir, { recursive: true, force: true });
        dropDatabaseQuiet(adminDBURL, dbName);
      },
    };
  } catch (err) {
    dropDatabaseQuiet(adminDBURL, dbName);
    throw err;
  }
}

// ---- capturing requests -----------------------------------------------------

// registerSecret records a run-specific random value (a hack join/judge/
// invite code -- the only random values in this codebase that show up in a
// request *path*, not just a body field already caught by lib.mjs's regexes)
// into the one exact-replacement map the whole run shares, under the given
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
function call(instance, calls) {
  return async function at(name, method, pathAndQuery, opts = {}) {
    const headers = {};
    if (opts.apiKey) headers['X-API-Key'] = opts.apiKey;
    let payload;
    if (opts.raw !== undefined) {
      payload = opts.raw;
      headers['Content-Type'] = opts.contentType || 'application/octet-stream';
    } else if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json';
      payload = JSON.stringify(opts.body);
    }
    const res = await fetch(instance.baseURL + pathAndQuery, {
      method,
      headers,
      body: payload,
      redirect: 'manual', // a 3xx is a result to record, not something to chase
    });
    const rawBody = Buffer.from(await res.arrayBuffer());
    const record = {
      name: `${instance.tag}:${name}`,
      method,
      path: pathAndQuery,
      status: res.status,
      contentType: res.headers.get('content-type') || '',
      cacheControl: res.headers.get('cache-control') || '',
      location: res.headers.get('location') || '',
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
        // itself the bug a baseline diff should surface.
      }
    }
    return { status: res.status, json };
  };
}

function textBody(record, namer) {
  const text = normalizeText(record.rawBody.toString('utf8'), namer, record.exact);
  if (Buffer.byteLength(text, 'utf8') <= TEXT_INLINE_LIMIT) return { text };
  return { sha256: sha256hex(text), length: Buffer.byteLength(text, 'utf8') };
}

// EXTRA_VOLATILE_FIELDS covers volatile values that lib.mjs's generic,
// key-name-based VOLATILE_KEYS deliberately does not: "message" and
// "status" are ordinary field names used all over the API for things that
// are NOT volatile (a version's status, a notification's message), so
// blanking them everywhere would hide real differences. But GET
// /v1/admin/usage's "message" is prose generated from the box's actual
// free disk space ("This server is 80% full, with N GB left") and its
// "status" is a threshold read off the same live number -- both can flip
// between two runs with no seeded behaviour change at all, same as the
// disk_* fields next to them. Scoped to that one request by name, not by
// field name, so no other endpoint's "message" or "status" is touched.
const EXTRA_VOLATILE_FIELDS = new Map([['host:admin-usage', ['message', 'status']]]);

function finalizeCall(record, namer) {
  // The exact-replacement map is shared and mutated throughout the run
  // (registerSecret, plus each instance's own base URL/temp dir); by the
  // time finalization runs, after every call has completed, it holds every
  // run-specific value discovered anywhere, so normalizing the path here
  // -- not just headers and body -- also catches a code or id the path
  // itself carries (POST /v1/hack/join/{code}, PUT .../scores/{team_id}).
  const normalizedPath = normalizeText(record.path, namer, record.exact);

  const headers = {};
  if (record.contentType) headers['content-type'] = record.contentType;
  if (record.cacheControl) headers['cache-control'] = record.cacheControl;
  if (record.location) headers.location = normalizeText(record.location, namer, record.exact);

  let body;
  if (record.contentType.includes('json')) {
    const text = record.rawBody.toString('utf8');
    if (text.trim() === '') {
      body = null;
    } else {
      try {
        const parsed = JSON.parse(text);
        for (const field of EXTRA_VOLATILE_FIELDS.get(record.name) || []) {
          if (parsed && typeof parsed === 'object' && field in parsed) parsed[field] = '<volatile>';
        }
        body = normalizeValue(parsed, namer, record.exact);
      } catch {
        body = textBody(record, namer);
      }
    }
  } else {
    body = textBody(record, namer);
  }

  return { name: record.name, method: record.method, path: normalizedPath, status: record.status, headers, body };
}

// ---- Host instance: seed + request list -----------------------------------

async function seedAndCallHost(host, calls) {
  const at = call(host, calls);

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
    body: { count: 1, prefix: 'owner' },
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
  await at('site1-lock-get', 'GET', '/v1/sites/site-one/lock', { apiKey: owner });

  // Site 2: created via the JSON files API directly, then its access toggle.
  // access=specific (named viewers) still needs a sign-in method
  // (RESEND_API_KEY or Google OAuth), excluded here by "no outbound
  // network", so this exercises access=anyone instead.
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
  await at('mcp-tools-list', 'POST', '/mcp', {
    apiKey: owner,
    body: { jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} },
  });
  await at('mcp-initialize', 'POST', '/mcp', {
    apiKey: owner,
    body: {
      jsonrpc: '2.0',
      id: 2,
      method: 'initialize',
      params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'golden', version: '1.0' } },
    },
  });
  await at('mcp-tools-call-list-sites', 'POST', '/mcp', {
    apiKey: owner,
    body: { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'list_sites', arguments: {} } },
  });

  await at('export-me-zip-head', 'GET', '/v1/me/export.zip', { apiKey: owner });
}

// ---- Hack instance: seed + request list ------------------------------------

function hackCreateBody(slug, extra) {
  const body = {
    slug,
    title: 'Golden Harness Hack',
    organiser_name: 'Ada Lovelace',
    organisation: 'Analytic Club',
    contact_email: 'ada@example.com',
    purpose: 'exercise every stage once',
    expected_participants: 10,
    starts_at: '2026-10-01',
    ends_at: '2026-10-03',
    time_zone: 'UTC',
  };
  return { ...body, ...extra };
}

async function seedAndCallHack(hack, calls, namer, exact) {
  const at = call(hack, calls);

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
    const r = await at(`admin-create-${prefix}`, 'POST', '/v1/admin/users', {
      apiKey: hack.adminKey,
      body: { count: 1, prefix },
    });
    return r.json.created[0].api_key;
  }
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
  const teamB = await at('team-b-create', 'POST', `/v1/hack/events/${slug}/teams`, {
    apiKey: participants[1],
    body: { name: 'Team Beta' },
  });
  const teamBSlug = teamB.json.slug;
  await at('team-c-create', 'POST', `/v1/hack/events/${slug}/teams`, {
    apiKey: participants[2],
    body: { name: 'Team Gamma' },
  });

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

  // Team Alpha's own site, deployed from the repo's sample tarball through
  // the event (its first deploy, so ?create=1), which is what lets its
  // entry above satisfy the one-video-or-a-live-site rule.
  const tarball = readFileSync(path.join(REPO_ROOT, 'sample-site.tar.gz'));
  await at('team-a-site-deploy', 'PUT', `/v1/hack/events/${slug}/site?create=1`, {
    apiKey: participants[0],
    raw: tarball,
    contentType: 'application/gzip',
  });
  await at('entry-a-submit', 'POST', `/v1/hack/events/${slug}/entry/submit`, { apiKey: participants[0] });
  await at('team-a-site-status', 'GET', `/v1/hack/events/${slug}/site`, { apiKey: participants[0] });
  await at('team-a-key-make', 'POST', `/v1/hack/events/${slug}/key`, { apiKey: participants[0] });
  await at('team-a-key-get', 'GET', `/v1/hack/events/${slug}/key`, { apiKey: participants[0] });

  await at('entries-list', 'GET', `/v1/hack/events/${slug}/entries`, { apiKey: organiser });

  // closed -> judging.
  await at('event-stage-closed', 'POST', `/v1/hack/events/${slug}/stage`, {
    apiKey: organiser,
    body: { stage: 'closed' },
  });
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
  await at('mcp-tools-list', 'POST', '/mcp', {
    apiKey: organiser,
    body: { jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} },
  });
  await at('mcp-tools-call-get-event', 'POST', '/mcp', {
    apiKey: organiser,
    body: { jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: 'hack_get_event', arguments: { slug } } },
  });

  void createdEvent; // seeded above; its creation response is already recorded
}

// ---- entry point ------------------------------------------------------------

async function main() {
  const [binary, outPath] = process.argv.slice(2);
  if (!binary || !outPath) {
    console.error('usage: node run.mjs BINARY OUT.json');
    process.exit(2);
  }

  const pgAdminURL = (process.env.GOLDEN_PG_URL || 'postgres://postgres:postgres@127.0.0.1:55412').replace(/\/+$/, '');
  const schemaSQL = process.env.GOLDEN_SCHEMA_SQL || path.join(REPO_ROOT, 'db', 'schema.sql');

  const namer = new Namer();
  // One exact-replacement map for the whole run (both instances): each
  // instance registers its own base URL/temp dir into it at boot, and the
  // seed functions register hack join/judge/invite codes into it as soon
  // as they come back from the server (registerSecret) -- see finalizeCall.
  const exact = new Map();
  const calls = [];
  const started = Date.now();

  const host = await bootInstance({ binary, pgAdminURL, schemaSQL, hosted: false, tag: 'host', namer, exact });
  try {
    await seedAndCallHost(host, calls);
  } finally {
    await host.stop();
  }

  const hack = await bootInstance({ binary, pgAdminURL, schemaSQL, hosted: true, tag: 'hack', namer, exact });
  try {
    await seedAndCallHack(hack, calls, namer, exact);
  } finally {
    await hack.stop();
  }

  const out = calls.map((c) => finalizeCall(c, namer));
  writeFileSync(outPath, JSON.stringify(out, null, 2) + '\n');
  const seconds = ((Date.now() - started) / 1000).toFixed(1);
  console.error(`golden: ${out.length} requests captured in ${seconds}s -> ${outPath}`);
}

main().catch((err) => {
  console.error('golden: FAILED:', err.stack || err.message || err);
  process.exit(1);
});
