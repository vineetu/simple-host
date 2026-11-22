# Chatbot: a chat box on the site that uses the owner's AI key

A page on the site talks to an AI service (Gemini, OpenAI, Anthropic or any other with an HTTPS API) with the owner's own API key. The key is never in the page, the site's files or the chat: the owner types it once on Simple Host's key page, and the page calls `SH.secret("<name>").fetch(path, init)` from auth.js. Simple Host sends that request to the key's one destination host with the key added, and streams the answer back. Every call is billed to the owner's account at the AI service; Simple Host sets no spending cap, so suggest the person sets one at the service. Point the page only at the service's own API: an endpoint that echoes the request back would show the key.

## 1. Owner setup

Ask the person which service and who may use the chat: only them (owner), people signed in on the site (signed-in, the usual choice for a public site: a visitor signs in with an emailed code first), or anyone. Then call request_secret for that service and give the person the link it returns. Never ask for the key in chat.

- Gemini: request_secret {"site": "{site}", "name": "ai", "destination": "generativelanguage.googleapis.com", "attach": "header", "attach_name": "x-goog-api-key"}
- OpenAI: request_secret {"site": "{site}", "name": "ai", "destination": "api.openai.com", "attach": "header", "attach_name": "Authorization", "attach_prefix": "Bearer "}
- Anthropic: request_secret {"site": "{site}", "name": "ai", "destination": "api.anthropic.com", "attach": "header", "attach_name": "x-api-key"}

Wait until list_secrets {"site": "{site}"} shows the key, then publish the page.

## 2. The page (chat.html)

Set PROVIDER to "gemini", "openai" or "anthropic", and MODEL to a current model of that service.

<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Chat</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 680px; padding: 16px; }
  #log p { white-space: pre-wrap; margin: 8px 0; } #log .me { text-align: right; color: #555; }
  form { display: flex; gap: 8px; } input { flex: 1; font: inherit; padding: 8px; } button { font: inherit; padding: 8px 14px; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js"></script>
</head>
<body>
<h1>Chat</h1>
<section id="sh-auth"></section>
<div id="log"></div>
<form id="ask"><input id="q" autocomplete="off" placeholder="Ask something" required><button>Send</button></form>
<script>
var PROVIDER = "gemini", MODEL = "gemini-2.0-flash", history = [];
var services = {
  gemini: {
    path: "v1beta/models/" + MODEL + ":streamGenerateContent?alt=sse",
    body: function () { return { contents: history.map(function (m) { return { role: m.role === "assistant" ? "model" : "user", parts: [{ text: m.content }] }; }) }; },
    text: function (e) { var c = e.candidates && e.candidates[0]; return c && c.content && c.content.parts ? c.content.parts.map(function (p) { return p.text || ""; }).join("") : ""; }
  },
  openai: {
    path: "v1/chat/completions",
    body: function () { return { model: MODEL, stream: true, messages: history }; },
    text: function (e) { return (e.choices && e.choices[0].delta && e.choices[0].delta.content) || ""; }
  },
  anthropic: {
    path: "v1/messages", headers: { "anthropic-version": "2023-06-01" },
    body: function () { return { model: MODEL, max_tokens: 1024, stream: true, messages: history }; },
    text: function (e) { return (e.type === "content_block_delta" && e.delta && e.delta.text) || ""; }
  }
};
function line(cls, text) {
  var p = document.createElement("p"); p.className = cls; p.textContent = text;
  document.getElementById("log").appendChild(p); return p;
}
async function ask(question) {
  var s = services[PROVIDER], out = line("ai", "…"), answer = "";
  history.push({ role: "user", content: question });
  var res = await SH.secret("ai").fetch(s.path, { method: "POST",
    headers: Object.assign({ "Content-Type": "application/json", "Accept": "text/event-stream" }, s.headers || {}),
    body: JSON.stringify(s.body()) });
  if (res.status === 401) { out.textContent = "Sign in first."; history.pop(); return SH.requireSignIn(); }
  if (!res.ok) { out.textContent = "Sorry, that did not work (" + res.status + ")."; history.pop(); return; }
  var reader = res.body.getReader(), decoder = new TextDecoder(), buffer = "";
  for (;;) {
    var chunk = await reader.read();
    if (chunk.done) break;
    buffer += decoder.decode(chunk.value, { stream: true });
    var lines = buffer.split("\n"); buffer = lines.pop();
    lines.forEach(function (l) {
      if (l.indexOf("data:") !== 0 || l.trim() === "data: [DONE]") return;
      try { answer += s.text(JSON.parse(l.slice(5))); out.textContent = answer; } catch (e) {}
    });
  }
  history.push({ role: "assistant", content: answer });
}
document.addEventListener("DOMContentLoaded", function () {
  SH.mount("#sh-auth");
  document.getElementById("ask").onsubmit = function (e) {
    e.preventDefault();
    var q = document.getElementById("q"); line("me", q.value); ask(q.value); q.value = "";
  };
});
</script>
</body>
</html>

With who set to owner or signed-in, SH.mount shows the sign-in box; with anyone it is not needed and can be removed.

## 3. Verify

Open the page on the site's own address, ask a question, and watch the answer arrive in pieces. list_secrets {"site": "{site}"} shows the key's use count going up. A 401 means the visitor must sign in (who is signed-in or owner); 403 forbidden means only the owner may use this key; 502 secret_call_failed names why the service could not be reached (redirect_refused: the service answered with a redirect, which is never passed on). The service's own errors (a wrong model name, a bad key) come back as its own status and body.
