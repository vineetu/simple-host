# WebMCP tools on a page you build

WebMCP lets a page register JavaScript functions as tools. An AI browsing agent sharing the tab can call them directly. Gemini in Chrome does this today. ChatGPT desktop's built-in browser lists them as Site tools. It is optional. Nothing on Simple Host registers tools on a site you publish unless you add the code yourself.

Feature-detect before registering. The current call is `document.modelContext.registerTool(spec)`. `navigator.modelContext` is deprecated (Chrome 150 and later). Use it only when `document.modelContext` is missing. If neither exists, do nothing: do not throw and do not log. A browser that half-implements the draft must not break the page. Copy that approach from `https://simple-host.app/webmcp.js` (`window.SHWebMCP`), or inline the same few lines. Load `https://simple-host.app/auth.js` the way the booking recipe does, before this script.

```html
<script>
function registerTool(spec) {
  try {
    if (typeof document.modelContext?.registerTool === 'function') {
      document.modelContext.registerTool(spec);
      return;
    }
    if (typeof navigator.modelContext?.registerTool === 'function') {
      navigator.modelContext.registerTool(spec);
    }
  } catch (e) {}
}
</script>
```

`spec` is `{ name, description, inputSchema, annotations, execute }`. `inputSchema` is JSON Schema. `readOnlyHint: true` means the tool changes no server state. `consequentialHint: true` means a write is hard to undo, so a compliant agent (ChatGPT's browser included) asks the person before calling it. Do not build your own confirmation dialog. A tool calls the same function the page's form already calls. It is not a second way to write data.

A booking page from the `booking` recipe uses the `records` preset. The page keeps one table handle:

```js
var bookings = SH.storage.sqlite('bookings').table('bookings');
```

My bookings reads with `bookings.list({ order: 'id', desc: 1, limit: 50 })`. Book calls `SH.requireSignIn()`, then `bookings.add` with `name`, `phone`, `service`, `day`, `time` and `note`. The tools below call those same methods. `check_availability` returns the rows My bookings already shows (the customer's own requests, not a separate slot calendar). `submit_booking` is the same add, marked consequential because a booking is hard to undo quietly.

```js
registerTool({
  name: 'check_availability',
  description: 'List the booking requests this signed-in customer already sent.',
  inputSchema: { type: 'object', properties: {}, additionalProperties: false },
  annotations: { readOnlyHint: true },
  execute: function () {
    return bookings.list({ order: 'id', desc: 1, limit: 50 }).then(function (res) {
      return { bookings: SH.storage.toObjects(res) };
    });
  }
});

registerTool({
  name: 'submit_booking',
  description: 'Send a booking request the same way the Book button does.',
  inputSchema: {
    type: 'object',
    properties: {
      name: { type: 'string' }, service: { type: 'string' },
      day: { type: 'string' }, time: { type: 'string' }
    },
    required: ['name', 'service', 'day', 'time']
  },
  annotations: { readOnlyHint: false, consequentialHint: true },
  execute: function (args) {
    var data = { name: args.name, phone: args.phone || '', service: args.service,
      day: args.day, time: args.time, note: args.note || null };
    return SH.requireSignIn().then(function () { return bookings.add(data); })
      .then(function () { return { ok: true }; });
  }
});
```

Register a write only for someone the page already lets make that write, and keep the form. Chrome and Edge expose this API on an origin trial through 17 November 2026. After that it needs a stable API or a new trial. Claude in Chrome does not support it yet. Most other browsing agents still read the page visually. The page has to keep working for a person with no agent.
