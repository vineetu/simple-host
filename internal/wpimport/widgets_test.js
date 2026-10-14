const assert = require('node:assert/strict');
const vm = require('node:vm');
const source = require('node:fs').readFileSync(0, 'utf8');
class Node {
  constructor(tag = 'div', text = '') { this.tag = tag; this.textContent = text; this.children = []; this.events = {}; this.hidden = false; this.disabled = false; this.dataset = {}; }
  set innerHTML(value) { throw new Error('Visitor data used as HTML'); }
  append(...children) { this.children.push(...children); }
  addEventListener(name, fn) { this.events[name] = fn; }
  setAttribute(name, value) { this[name] = value; }
}
class Form extends Node {
  constructor(kind) { super('form'); this.dataset = {shFeature: kind, page: '/post/', form: 'Contact'}; this.values = {name: '<script>Ada</script>', email: 'ada@example.org', message: '<img src=x onerror=alert(1)>', title: 'A new topic'}; this.status = new Node('p'); this.button = new Node('button'); this.resets = 0; }
  querySelector(query) { if (query === '[role=status]') return this.status; if (query === 'button') return this.button; }
  closest() { return {querySelector: () => this.list}; }
  reset() { this.resets++; }
  submit() { return this.events.submit({preventDefault() {}}); }
}
(async () => {
  const forms = ['contact', 'subscribe', 'comments', 'guestbook'].map(kind => { const f = new Form(kind); f.list = new Node(); return f; });
  forms[2].dataset.moderated = 'true'; forms[3].dataset.moderated = 'true';
  const forum = new Node(); const newTopic = new Form('forum'), reply = new Form('forum');
  const forumParts = {'[data-sh-signin]': new Node(), '[data-sh-topics]': new Node(), '[data-sh-thread]': new Node(), '[data-sh-error]': new Node(), '[data-sh-new]': newTopic, '[data-sh-reply]': reply};
  forum.querySelector = key => forumParts[key];
  const buy = new Node('a'); buy.dataset.product = 'print'; buy.hidden = true;
  const state = {add: {}, list: {}, calls: [], signins: 0, deleted: []};
  const table = resource => ({
    async list(args) { state.calls.push({op: 'list', resource, args}); if (state.list[resource]) throw state.list[resource]; return resource === 'forum' ? [{id: 10, topic: 0, title: '<b>Topic</b>', name: '<b>Name</b>', message: '<script>Text</script>', mine: true}, {id: 11, topic: 10, name: 'Reply', message: 'Reply text', mine: false}] : [{name: '<b>Name</b>', message: '<script>Text</script>'}]; },
    async add(data) { state.calls.push({op: 'add', resource, data}); if (typeof state.add[resource] === 'function') return state.add[resource](data); if (state.add[resource]) throw state.add[resource]; return {id: 12}; },
    async delete(id) { state.deleted.push(id); }
  });
  const SH = {storage: {sqlite: resource => ({table: () => table(resource)}), toObjects: result => result}, mount(selector) { assert.match(selector, /^#sh-forum-signin-/); }, async requireSignIn() { state.signins++; }};
  const fetches = [];
  const document = {currentScript: {src: 'https://site.test/sh-import/features.js'}, baseURI: 'https://site.test/product/print/', createElement: tag => new Node(tag), querySelectorAll: selector => selector === 'form[data-sh-feature]' ? forms : selector === 'section[data-sh-feature="forum"]' ? [forum] : selector === '[data-sh-feature="buy"]' ? [buy] : []};
  const context = {document, window: {SH, addEventListener() {}}, SH, location: {pathname: '/post/'}, URL, FormData: class {constructor(form) {this.form = form;} forEach(fn) {Object.entries(this.form.values).forEach(([k, v]) => fn(v, k));}}, fetch: async url => {fetches.push(String(url)); return {ok: true, async json() {return {print: 'https://checkout.example.org/print'};}};}};
  vm.runInNewContext(source, context);
  const tick = () => new Promise(resolve => setImmediate(resolve)); await tick();
  assert.deepEqual(fetches, ['https://site.test/sh-import/payment-links.json']); assert.equal(buy.hidden, false);
  assert.equal(buy.href, 'https://checkout.example.org/print');
  assert.equal(forms[2].list.children[0].children[2].textContent, '<script>Text</script>');
  let rejectAdd;
  state.add.messages = () => new Promise((resolve, reject) => {rejectAdd = reject;});
  const submitting = forms[0].submit(); await forms[0].submit();
  assert.equal(state.calls.filter(x => x.op === 'add' && x.resource === 'messages').length, 1);
  rejectAdd(new Error('Offline')); await submitting;
  assert.equal(forms[0].resets, 0); assert.equal(forms[0].values.message, '<img src=x onerror=alert(1)>');
  assert.equal(forms[0].status.textContent, 'Not sent: Offline'); assert.equal(forms[0].button.disabled, false);
  state.add.messages = {status: 404, code: 'resource_not_found', message: 'Missing'};
  await forms[0].submit(); assert.equal(forms[0].status.textContent, 'This form is not switched on yet.'); assert.equal(forms[0].resets, 0);
  delete state.add.messages;
  for (const f of forms) await f.submit();
  const contact = state.calls.filter(x => x.op === 'add' && x.resource === 'messages').at(-1).data;
  assert.equal(contact.page, '/post/'); assert.equal(contact.email, 'ada@example.org'); assert.equal(JSON.parse(contact.fields).name, '<script>Ada</script>');
  assert.equal(forms[2].status.textContent, 'Thanks, your comment shows once it is approved.');
  assert.equal(forms[3].status.textContent, 'Thanks, your entry shows once it is approved.');
  const guest = state.calls.find(x => x.op === 'add' && x.resource === 'guestbook').data; assert.equal(guest.page, undefined);
  const listing = state.calls.find(x => x.op === 'list' && x.resource === 'comments'); assert.equal(listing.args.where.page, '/post/');
  forumParts['[data-sh-topics]'].children[0].events.click(); assert.equal(reply.hidden, false);
  assert.equal(forumParts['[data-sh-thread]'].children[0].children.at(-1).textContent, 'Delete');
  await forumParts['[data-sh-thread]'].children[0].children.at(-1).events.click(); assert.deepEqual(state.deleted, [10]);
  await newTopic.submit(); await reply.submit();
  assert.equal(state.signins, 2); const posts = state.calls.filter(x => x.op === 'add' && x.resource === 'forum');
  assert.equal(posts[0].data.topic, 0); assert.equal(posts[1].data.topic, 10);
})().catch(err => {console.error(err); process.exitCode = 1;});
