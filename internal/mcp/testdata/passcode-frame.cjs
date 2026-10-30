// Exercise the shipped frame's WebCrypto against the Go tool through a pipe.
const { webcrypto } = require('node:crypto');
const { readFileSync } = require('node:fs');
const { createInterface } = require('node:readline');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const pending = [];
createInterface({ input: process.stdin }).on('line', line => {
  const reply = JSON.parse(line), job = pending.shift();
  if (reply.error) job.reject(new Error(reply.error)); else job.resolve(reply.result);
});
const elements = new Map();
const get = id => {
  if (!elements.has(id)) elements.set(id, { value: '', hidden: true, textContent: '', disabled: false, focus() {}, select() {}, addEventListener() {} });
  return elements.get(id);
};
let resultHandler, teardown, context = [], copied = '', hold = null;
const send = args => new Promise((resolve, reject) => {
  pending.push({ resolve, reject });
  process.stdout.write(JSON.stringify(args) + '\n');
});
const app = {
  on(name, handler) { if (name === 'tool-result') resultHandler = handler; },
  onTeardown(handler) { teardown = handler; },
  start: async () => {},
  updateModelContext: async text => { context.push(text); },
  callTool: async (name, args) => {
    assert.equal(name, 'passcode_screen');
    assert.equal(Object.hasOwn(args, 'passcode'), false);
    assert.equal(JSON.stringify(args).includes('fixture-'), false);
    const result = await send(args);
    if (hold && args.action === 'show') await new Promise(resolve => { hold.release = resolve; });
    return result;
  }
};
vm.runInNewContext(readFileSync('apps/passcode.js', 'utf8'), {
  window: { SHApp: app, addEventListener() {} }, document: { getElementById: get },
  navigator: { clipboard: { async writeText(value) { copied = value; } } },
  crypto: webcrypto, TextEncoder, TextDecoder, Uint8Array, btoa, atob,
});
const tick = () => new Promise(resolve => setTimeout(resolve, 1));
(async () => {
  resultHandler({ structuredContent: { site: 'blog', passcode_protected: true, passcode_set_at: '2026-10-30T10:00:00Z' } });
  await get('show').onclick();
  assert.equal(get('code').value, 'fixture-passcode-947281');
  await get('copy').onclick();
  assert.equal(copied, get('code').value);
  get('new-code').value = 'fixture-unsaved';
  get('hide').onclick();
  assert.equal(get('code').value, '');
  assert.equal(get('new-code').value, '');
  assert.equal(get('revealed').hidden, true);
  await get('generate').onclick();
  assert.equal(get('code').value, 'fixture-generated-638194');
  get('new-code').value = 'short';
  get('set').onclick();
  while (get('new-code').disabled) await tick();
  assert.match(get('status').textContent, /at least 6 characters/);
  get('new-code').value = 'fixture-changed-492810';
  get('set').onclick();
  while (get('new-code').disabled) await tick();
  assert.equal(get('new-code').value, '');
  assert.equal(get('status').textContent, 'Passcode set.');
  await get('show').onclick();
  assert.equal(get('code').value, 'fixture-changed-492810');
  for (const text of context) {
    assert.equal(text, 'The person set a new passcode on blog in the passcode screen.');
  }
  assert.equal(context.length, 2);
  // A late result must not repopulate the DOM after teardown.
  hold = {};
  const late = get('show').onclick();
  while (!hold.release) await tick();
  teardown();
  hold.release();
  await late;
  assert.equal(get('code').value, '');
  assert.equal(get('new-code').value, '');
  assert.equal(get('revealed').hidden, true);
  process.exit(0);
})().catch(() => { process.stderr.write('Passcode frame check failed.\n'); process.exit(1); });
