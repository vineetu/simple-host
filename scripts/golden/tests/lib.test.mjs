import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createTextWriter, renderJSON, sanitizeName, Namer, sha256hex, normalizeMailOutbox, normalizeResponseNoise, normalizeSetCookie, selectScenarios, normalizeText, renderRecordedText, normalizeValue, normalizeStaticText, relativeDates, isTextBody, validateScenarios, describeZip, validatePostgresURL } from '../lib.mjs';

test('every text body is written whole with byte hash and length', t => {
  const dir = mkdtempSync(path.join(tmpdir(), 'golden-text-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const writer = createTextWriter(dir, 'text/host');
  for (const size of [1, 3000, 100000]) {
    const text = 'é\n'.repeat(size);
    const record = writer(`request-${size}`, text);
    const bytes = readFileSync(path.join(dir, record.file));
    assert.equal(bytes.toString(), text);
    assert.equal(record.length, bytes.length);
    assert.equal(record.sha256, sha256hex(bytes));
  }
});
test('JSON rendering sorts keys and exposes multiline and embedded JSON strings', () => {
  const body = { z: 'one\ntwo', a: '{"z":2,"a":1}', list: ['first\nsecond'] };
  assert.equal(renderJSON(body), '{\n  "a": """\n  | {\n  |   "a": 1,\n  |   "z": 2\n  | }\n  """,\n  "list": [\n    """\n    | first\n    | second\n    """\n  ],\n  "z": """\n  | one\n  | two\n  """\n}\n');
  assert.equal(body.a, '{"z":2,"a":1}');
  assert.equal(renderJSON({ embedded: '{}' }), '{\n  "embedded": """\n  | {}\n  """\n}\n');
});
test('filename sanitizing and collisions', t => {
  const dir = mkdtempSync(path.join(tmpdir(), 'golden-collision-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  assert.equal(sanitizeName('GET /a?x=1'), 'GET__a_x_1');
  const writer = createTextWriter(dir, 'text/hack.extra');
  assert.equal(writer('a/b', 'x').file, 'text/hack.extra/a_b.txt');
  assert.throws(() => writer('a?b', 'x'), /collision/);
});
test('placeholders are request scoped and stable when another request is inserted', () => {
  function run(insert) {
    const namer = new Namer();
    assert.equal(namer.placeholderFor('base', 'local'), '<base>');
    if (insert) { namer.beginRequest('new request'); namer.placeholderFor('id', 'new'); }
    namer.beginRequest('site1-create-tarball');
    const id = namer.placeholderFor('id', 'site');
    assert.equal(namer.placeholderFor('id', 'other'), '<id:site1-create-tarball#2>');
    namer.beginRequest('later');
    assert.equal(namer.placeholderFor('id', 'site'), id);
    return id;
  }
  assert.equal(run(false), '<id:site1-create-tarball#1>');
  assert.equal(run(true), run(false));
});

test('outbox sorts mail, replaces only sign-in codes and hashes normalized HTML', () => {
  const mail = code => ({ to: ['viewer@example.com'], subject: `Simple Host sign-in code: ${code}`,
    text: `Code: ${code}`, html: `<b>${code}</b>` });
  const notice = { to: ['a@example.com'], subject: 'Receipt 123456', text: 'Order 123456', html: '<p>123456</p>' };
  const first = normalizeMailOutbox([mail('123456'), notice], value => value);
  const second = normalizeMailOutbox([notice, mail('654321')], value => value);
  assert.deepEqual(first, second);
  assert.equal(first.find(m => m.subject === 'Receipt 123456').text, 'Order 123456');
  const signin = first.find(m => m.subject.includes('sign-in'));
  assert.equal(signin.text, 'Code: <code>');
  assert.equal(signin.html_sha256, sha256hex('<b><code></b>'));
});
test('authenticated usage gets the same narrow live measurements normalization', () => {
  const body = { disk_bytes: 12, workers: [{ name: 'mail', pending: 2, enabled: true }] };
  assert.equal(normalizeResponseNoise('GET /v1/admin/usage as admin', body).disk_bytes, '<n>');
  assert.equal(body.workers[0].enabled, true);
  assert.equal(normalizeResponseNoise('different response', { disk_bytes: 12 }).disk_bytes, 12);
});

test('only per-site passcode cookie names normalize their site-ID-derived suffix', () => {
  const namer = new Namer();
  namer.beginRequest('unlock');
  const cookies = normalizeSetCookie([
    'sh_pass_0123456789abcdef=session; Path=/; HttpOnly; SameSite=Lax',
    '__Host-sh_pass_0123456789abcdef=session; Secure; Path=/',
    'other_0123456789abcdef=opaque; Path=/',
  ], namer, new Map());
  assert.deepEqual(cookies, [
    '__Host-sh_pass_<passcookie:unlock#1>=<value>; Secure; Path=/',
    'other_0123456789abcdef=<value>; Path=/',
    'sh_pass_<passcookie:unlock#1>=<value>; Path=/; HttpOnly; SameSite=Lax',
  ]);
});

test('scenario selection subtracts skips for all, explicit and default selections', () => {
  const table = { base: {}, 'rules-on': {}, converted: {} };
  assert.deepEqual(selectScenarios(table), ['base']);
  assert.deepEqual(selectScenarios(table, 'all', 'rules-on,converted'), ['base']);
  assert.deepEqual(selectScenarios(table, 'rules-on,base', 'rules-on'), ['base']);
  assert.deepEqual(selectScenarios(table, undefined, 'rules-on'), ['base']);
  assert.deepEqual(selectScenarios(table, 'converted', 'converted'), []);
  for (const [selected, skipped] of [['all', 'base'], ['all', 'missing'], ['missing', ''], ['base,base', ''], ['all', 'rules-on,rules-on']]) {
    assert.throws(() => selectScenarios(table, selected, skipped), /scenario|base/);
  }
});

test('build fields are scoped by request and path, preserving content versions and absent fields', () => {
  for (const name of ['GET /.well-known/agent-card.json', 'GET /.well-known/mcp/server-card.json', 'GET /mcp/server-card']) {
    assert.deepEqual(normalizeResponseNoise(name, { version: 'dev', plugin: { version: '2' } }), { version: '<version>', plugin: { version: '2' } });
  }
  for (const name of ['admin-usage', 'GET /v1/admin/usage', 'GET /v1/admin/usage as admin']) {
    const body = normalizeResponseNoise(name, { version: 'dev', commit: 'unknown' });
    assert.equal(body.version, '<version>'); assert.equal(body.commit, '<commit>');
  }
  assert.deepEqual(normalizeResponseNoise('initialize personal', { result: { serverInfo: { version: 'dev' }, protocolVersion: '2025-06-18' } }), { result: { serverInfo: { version: 'dev' }, protocolVersion: '2025-06-18' } });
  for (const name of ['GET /openapi.json', 'GET /plugin.json', 'GET /skills/index.json', 'unrelated']) {
    assert.deepEqual(normalizeResponseNoise(name, { version: 'dev', commit: 'unknown' }), { version: 'dev', commit: 'unknown' });
  }
  assert.deepEqual(normalizeResponseNoise('initialize personal', { result: {} }), { result: {} });
});

test('only sitemap rendering splits adjacent XML tags', t => {
  const dir = mkdtempSync(path.join(tmpdir(), 'golden-xml-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const writer = createTextWriter(dir, 'text/host');
  const raw = '<urlset><url><loc>/a</loc></url></urlset>';
  for (const type of ['application/xml', 'text/xml; charset=utf-8', 'image/svg+xml', 'text/html', 'text/javascript', 'text/css']) {
    const rendered = renderRecordedText(type, raw, 'GET /asset');
    assert.equal(rendered, raw);
    assert.equal(renderRecordedText(type, raw, 'GET /sitemap.xml'), raw.replaceAll('><', '>\n<'));
    const entry = writer(type, rendered);
    assert.equal(entry.sha256, sha256hex(readFileSync(path.join(dir, entry.file))));
  }
});

test('credential scalars preserve schema, null, type and __proto__', () => {
  const raw = JSON.parse('{"__proto__":{"visible":true},"passcode":{"type":"string"},"last4":null}');
  const out = normalizeValue(raw, new Namer(), new Map());
  assert.equal(out.__proto__.visible, true); assert.equal(out.passcode.type, 'string'); assert.equal(out.last4, null);
  for (const value of [null, false, 4, '', []]) assert.deepEqual(normalizeValue({passcode:value}, new Namer(),new Map()).passcode,value);
  assert.equal(normalizeValue({passcode:'secret'},new Namer(),new Map()).passcode,'<volatile>');
});
test('static documents preserve identifiers, checksums, dates and credentials', () => {
  const text = '2026-11-13T00:00:00Z 12345678-1234-1234-1234-123456789012 ' + 'a'.repeat(64);
  const exact = new Map([['secret','<code:x#1>']]); exact.dates = relativeDates('2026-11-13');
  assert.equal(normalizeStaticText(text+' secret',exact),text+' secret');
  assert.equal(normalizeStaticText('<lastmod>now</lastmod>',exact,'GET /sitemap.xml'),'<lastmod><ts></lastmod>');
  assert.equal(renderRecordedText('image/svg+xml','<text><tspan>A</tspan><tspan>B</tspan></text>'),'<text><tspan>A</tspan><tspan>B</tspan></text>');
});
test('calendar windows preserve relative buckets across two observation days', () => {
  const run = today => { const exact=new Map();exact.dates=relativeDates(today);return normalizeText([...exact.dates.keys()].join(' '),new Namer(),exact); };
  assert.equal(run('2026-11-13'),run('2026-11-14'));
});
test('usage preserves network structure and worker nullness', () => {
  const body=normalizeResponseNoise('admin-usage',{network:{bytes:2, nested:{ok:true,absent:null}},workers:[{last_progress:null,pending:null,enabled:false}]});
  assert.deepEqual(body.network,{bytes:'<n>',nested:{ok:true,absent:null}}); assert.equal(body.workers[0].last_progress,null); assert.equal(body.workers[0].pending,null);
});
test('mail traversal is stable before allocating identifiers and preserves payload', () => {
  const a={from:'from@example.com',to:['a@example.com'],subject:'A',text:'12345678-1234-1234-1234-123456789012',html:'<p>A</p>',cc:['cc@example.com']};
  const b={...a,to:['b@example.com'],text:'87654321-1234-1234-1234-123456789012'};
  const run=messages=>{ const n=new Namer();n.beginRequest('mail-outbox');return normalizeMailOutbox(messages,v=>normalizeText(v,n,new Map())); };
  assert.deepEqual(run([a,b]),run([b,a])); assert.equal(run([a])[0].html,a.html); assert.equal(run([a])[0].from,a.from);
});
test('binary media and invalid UTF-8 never decode lossily', () => {
  assert.equal(isTextBody('application/pdf',Buffer.from('pdf')),false);
  assert.equal(isTextBody('text/plain',Buffer.from([255])),false);
  assert.equal(isTextBody('',Buffer.from('text')),true);
});
test('cookie expiry and empty values',()=>{
  assert.deepEqual(normalizeSetCookie(['session=; Expires=Fri, 13 Nov 2026 12:00:00 GMT'],new Namer(),new Map()),['session=; Expires=<http-date>']);
});
test('scenario table validation is dry and checks callbacks',()=>{
  validateScenarios({base:{host:()=>{},hack:()=>{}}}); assert.throws(()=>validateScenarios({base:{host:3}}),/callback/);
});

import { crc32 } from 'node:zlib';
function zipFixture(entries) {
  let offset=0; const locals=[], centrals=[];
  for (const [name,text] of entries) {
    const n=Buffer.from(name), data=Buffer.from(text), local=Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50); local.writeUInt32LE(crc32(data),14); local.writeUInt32LE(data.length,18); local.writeUInt32LE(data.length,22); local.writeUInt16LE(n.length,26);
    const central=Buffer.alloc(46); central.writeUInt32LE(0x02014b50); central.writeUInt32LE(crc32(data),16); central.writeUInt32LE(data.length,20); central.writeUInt32LE(data.length,24); central.writeUInt16LE(n.length,28); central.writeUInt32LE(offset,42);
    locals.push(local,n,data); centrals.push(central,n); offset+=local.length+n.length+data.length;
  }
  const directory=Buffer.concat(centrals), end=Buffer.alloc(22); end.writeUInt32LE(0x06054b50);end.writeUInt16LE(entries.length,8);end.writeUInt16LE(entries.length,10);end.writeUInt32LE(directory.length,12);end.writeUInt32LE(offset,16);
  return Buffer.concat([...locals,directory,end]);
}
test('ZIP traversal sorts first, keeps text/attributes, and rejects CRC/size corruption',()=>{
  const entries=[['b.txt','87654321-1234-1234-1234-123456789012'],['a.txt','12345678-1234-1234-1234-123456789012']];
  const run=rows=>{const n=new Namer();n.beginRequest('zip');return describeZip(zipFixture(rows),text=>normalizeText(text,n,new Map()));};
  assert.deepEqual(run(entries),run([...entries].reverse()));assert.ok(run(entries).entries[0].text);assert.equal(run(entries).entries[0].attributes,0);
  for(const field of [16,24]) {
    const zip=zipFixture(entries),central=zip.indexOf(Buffer.from([0x50,0x4b,0x01,0x02]));zip[central+field]^=1;
    assert.throws(()=>describeZip(zip,v=>v),/integrity/);
  }
});
test('Postgres prefix rejects parameter overrides and production port',()=>{
  assert.ok(validatePostgresURL('postgres://u:p@127.0.0.1:55412'));
  for(const value of ['postgres://u:p@localhost:5432','postgres://u:p@localhost:55412?host=remote&port=5432','postgres://u:p@localhost:55412/db','postgres://u:p@localhost:55412#x','https://u:p@localhost:55412','postgres://u:p@remote:55412']) assert.throws(()=>validatePostgresURL(value));
});
test('mail attachment hashes survive normalization and sign-in alert time is scoped',()=>{
  const n=new Namer();const mail={subject:'New sign-in to your Simple Host account',text:'When: 13 Nov 2026, 01:37 UTC',attachments:[{filename:'a.txt',content:Buffer.from('a').toString('base64'),content_type:'text/plain'}]};
  const [out]=normalizeMailOutbox([mail],v=>normalizeText(v,n,new Map()));assert.equal(out.text,'When: <ts>');assert.equal(out.attachments[0].sha256,sha256hex('a'));assert.equal(out.attachments[0].length,1);
});

import { responseClass, requestReplacements, mcpCallName, TIMER_WORKERS } from '../lib.mjs';
test('document classes preserve actual settings and Hack schema dates on both days', () => {
  const examples = [
    ['admin-settings', 'YYYY-MM-DD (00:00 UTC that day): sites created on or after this date get 410 legacy_storage_closed from the old saved-state, collections and declared-data routes. Empty or off disables the gate (the default, so self-hosted boxes are unchanged). An unparsable value logs a warning at boot and disables the gate. simple-host.app: 2026-10-12.'],
    ['tools/list personal', 'When the event starts. A date (2026-10-01) means midnight in time_zone, or send a date and time. coordinator decision 2026-11-02: an incomplete draft is still judged by default'],
    ['tools/list personal screens-off', 'When the event starts. A date (2026-10-01) means midnight in time_zone, or send a date and time. coordinator decision 2026-11-02: an incomplete draft is still judged by default'],
  ];
  for (const today of ['2026-11-13', '2026-11-14']) for (const [name, text] of examples) {
    const exact = new Map(); exact.dates = relativeDates(today);
    assert.equal(responseClass(name), 'static');
    assert.equal(normalizeStaticText(text, exact, name), text);
  }
  for (const name of ['initialize personal', 'resources/list team-site', 'tools/call find_tools', 'tools/call get_page_recipe', 'tools/call hack_get_page_recipe']) assert.equal(responseClass(name), 'static');
  assert.equal(responseClass('tools/call list_sites'), 'dynamic');
  const exact = new Map(); exact.dates = relativeDates('2026-11-13');
  assert.equal(normalizeText('2026-10-12', new Namer(), requestReplacements('me', exact)), '2026-10-12');
  assert.equal(normalizeText('2026-11-13', new Namer(), requestReplacements('GET /v1/admin/growth as admin', exact)), '<today>');
});
test('build identities keep empty strings, nulls, numbers and structures', () => {
  for (const value of ['', null, 42, { version: 'nested' }, ['dev']]) {
    assert.deepEqual(normalizeResponseNoise('GET /mcp/server-card', { version: structuredClone(value) }).version, value);
    assert.deepEqual(normalizeResponseNoise('admin-usage', { commit: structuredClone(value) }).commit, value);
  }
});
test('timer fields are explicit by worker name while startup and error nullness survive', () => {
  for (const name of TIMER_WORKERS) {
    const a = normalizeResponseNoise('admin-usage', { workers: [{ name, enabled: true, last_attempt: null, last_progress: null, last_error_at: null, last_error: 'visible' }] });
    const b = normalizeResponseNoise('admin-usage', { workers: [{ name, enabled: true, last_attempt: '2026-11-13T00:00:00Z', last_progress: '2026-11-13T00:00:00Z', last_error_at: null, last_error: 'visible' }] });
    assert.deepEqual(a, b); assert.equal(a.workers[0].last_progress, '<timer>'); assert.equal(a.workers[0].last_error_at, null);
  }
  for (const name of ['operations.network_usage','operations.paper_metrics','unrecognized']) assert.equal(normalizeResponseNoise('admin-usage', {workers:[{name,last_progress:null}]}).workers[0].last_progress,null);
});
test('mail keeps established identities under rerolled UUIDs and reversed arrival', () => {
  const run = (ids, reverse) => {
    const namer = new Namer(); namer.beginRequest('earlier');
    ids.forEach(id => namer.placeholderFor('id', id)); namer.beginRequest('mail-outbox');
    const mail = ids.map(id => ({ subject: 'Notice', text: id })); if (reverse) mail.reverse();
    return normalizeMailOutbox(mail, v => normalizeText(v, namer, new Map()), { namer });
  };
  const a = ['11111111-1111-1111-1111-111111111111','eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee'];
  const b = ['ffffffff-ffff-ffff-ffff-ffffffffffff','22222222-2222-2222-2222-222222222222'];
  assert.deepEqual(run(a,false), run(b,false)); assert.deepEqual(run(a,false),run(b,true));
  assert.equal(run(a,false)[0].text, '<id:earlier#1>');
});
test('sign-in code replacement leaves envelopes, headers, attachment names and word substrings intact', () => {
  const message = {subject:'Simple Host sign-in code: 123456',to:['person123456@example.com'],headers:{'X-Code':'123456'}, text:'123456 order123456 https://example.com/verify?code=123456',html:'<a href="/verify/123456">123456</a>',attachments:[{filename:'123456.txt',content:''}]};
  const [out] = normalizeMailOutbox([message], v=>v);
  assert.deepEqual(out.to,message.to); assert.deepEqual(out.headers,message.headers); assert.equal(out.attachments[0].filename,'123456.txt');
  assert.equal(out.text,'<code> order123456 https://example.com/verify?code=<code>');
});
test('ZIP descriptors with and without signatures check CRC, compressed size and decoded size', () => {
  for (const signature of [false,true]) {
    const zip = zipFixture([['entry.txt','payload']]);
    const central = zip.indexOf(Buffer.from([0x50,0x4b,0x01,0x02]));
    const descriptor = Buffer.alloc(signature ? 16 : 12); const start = signature ? 4 : 0;
    if(signature) descriptor.writeUInt32LE(0x08074b50);
    descriptor.writeUInt32LE(crc32(Buffer.from('payload')),start); descriptor.writeUInt32LE(7,start+4); descriptor.writeUInt32LE(7,start+8);
    const withDescriptor=Buffer.concat([zip.subarray(0,central),descriptor,zip.subarray(central)]);
    withDescriptor.writeUInt16LE(8,6); withDescriptor.writeUInt16LE(8,central+descriptor.length+8);
    withDescriptor.writeUInt32LE(central+descriptor.length,withDescriptor.length-6);
    assert.equal(describeZip(withDescriptor,v=>v).entries[0].text,'payload');
    for(const field of [0,4,8]) {const bad=Buffer.from(withDescriptor);bad[central+start+field]^=1;assert.throws(()=>describeZip(bad,v=>v),/descriptor/);}
  }
});
test('only account-export ZIP context rewrites the root date', () => {
  const zip=zipFixture([['simple-host-example-2026-11-13/file.txt','doc 2026-11-13']]);
  assert.equal(describeZip(zip,v=>v).entries[0].name,'simple-host-example-2026-11-13/file.txt');
  const entry=describeZip(zip,v=>v,{accountExport:true}).entries[0];
  assert.equal(entry.name,'simple-host-example-<date>/file.txt'); assert.equal(entry.text,'doc 2026-11-13');
});
test('central MCP naming inserts wrapper target before qualifiers without duplication', () => {
  const params={name:'run_tool',arguments:{name:'list_sites',arguments:{}}};
  assert.equal(mcpCallName('tools/call run_tool extra',params),'tools/call run_tool list_sites extra');
  assert.equal(mcpCallName('tools/call run_tool list_sites extra',params),'tools/call run_tool list_sites extra');
  assert.equal(mcpCallName('extra',params),'tools/call run_tool list_sites extra');
});

// Exercise the runner's real shutdown function with a controllable child/clock.
// A virtual clock covers the 30-second escalation path without sleeping in tests.
import { EventEmitter } from 'node:events';
import vm from 'node:vm';
test('producer completion requires a live child, graceful zero exit, and no forced kill', async () => {
  const source = readFileSync(new URL('../run.mjs', import.meta.url), 'utf8');
  const start = source.indexOf('async function stopServer('), end = source.indexOf('// Startup progress', start);
  let now = 0;
  const stop = vm.runInNewContext(source.slice(start,end) + '\nstopServer', { Date: {now:()=>now}, sleep: async ms=>{now+=ms;} });
  const child = mode => {
    const c = new EventEmitter(); c.exitCode = null; c.signalCode = null; c.signals=[];
    c.kill = signal => {
      c.signals.push(signal);
      if(mode==='hang' && signal==='SIGTERM') return;
      if(signal==='SIGKILL') queueMicrotask(()=>{c.signalCode=signal;c.emit('exit');});
      else { c.exitCode=mode==='bad' ? 2 : 0; }
    }; return c;
  };
  await stop(child('ok'));
  await assert.rejects(stop(child('bad')), /did not complete gracefully/);
  const dead=child('ok'); dead.exitCode=0;
  await assert.rejects(stop(dead), /exited before graceful completion/);
  const crashed=child('ok'); crashed.signalCode='SIGSEGV';
  await assert.rejects(stop(crashed), /exited before graceful completion/);
  const hung=child('hang'); await assert.rejects(stop(hung), /forced=true/);
  assert.deepEqual(hung.signals,['SIGTERM','SIGKILL']); assert.ok(now>=30000);
  await stop(dead,true);
  const cleanup=child('hang'); await stop(cleanup,true); assert.deepEqual(cleanup.signals,['SIGTERM','SIGKILL']);
});
test('only successful unrecorded observations are subtracted from named self-call totals', () => {
  assert.equal(normalizeResponseNoise('GET /v1/admin/growth as admin',{self_calls:12},{observationCount:3}).self_calls,9);
  const body=normalizeResponseNoise('GET /v1/admin/api-analytics as admin',{self_calls_today:12,self_errors_today:2,calls_today:4},{observationCount:3});
  assert.deepEqual(body,{self_calls_today:9,self_errors_today:2,calls_today:4});
  assert.equal(normalizeResponseNoise('other',{self_calls:12},{observationCount:3}).self_calls,12);
  assert.equal(normalizeResponseNoise('GET /v1/admin/growth as admin',{self_calls:null},{observationCount:3}).self_calls,null);
});
test('only timer-populated pending counters erase the pre-first-tick null', () => {
  for(const name of ['mail.submissions','notifications.delivery']) {
    for(const pending of [null,0,2]) assert.equal(normalizeResponseNoise('admin-usage',{workers:[{name,enabled:true,pending}]}).workers[0].pending,'<timer>');
  }
  assert.equal(normalizeResponseNoise('admin-usage',{workers:[{name:'notifications.emit',enabled:true,pending:null}]}).workers[0].pending,null);
});
