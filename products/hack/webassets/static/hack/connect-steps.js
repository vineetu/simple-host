// Shared rendering for hack-connect-steps.json's per-app connect steps:
// participant-website.js's publish card and account.js's "Connect your AI
// app" card show the same three apps (Claude, ChatGPT, Claude Code) the
// same way, so the address/command copy rows are wired once, here, instead
// of twice.
//
// hack-connect-steps.json is static and shared with /get-started; fetched
// once per page load and cached at module scope.
let docPromise = null
export function loadConnectSteps () {
  if (!docPromise) {
    docPromise = fetch('/hack-connect-steps.json').then(r => r.ok ? r.json() : null).catch(() => null)
  }
  return docPromise
}

// stepsHTML renders one app's steps as an <ol class="howto">, same markup
// family as participant-website.js's publish card (kit.css ".howto"). Each
// step with an address or a command gets an empty mount point
// ([data-mcp-row] / [data-mcp-cmd]); wireAddresses fills them once the
// markup is in the DOM.
//
// An app with a hosted-only directory/browse path (Claude's connector
// directory) carries it as "connect_hosted" in hack-connect-steps.json,
// shown instead of "connect" only on the canonical hosted instance, with
// "fallback" appended after it as the by-URL escape hatch -- the same
// location.origin check participant-website.js's publish card uses, so the
// Account screen's "Connect your AI app" card matches it exactly.
export function stepsHTML (esc, app) {
  const hosted = location.origin === 'https://simple-hack.app'
  const connect = (hosted && app.connect_hosted) || app.connect || []
  const lis = connect.map((step, n) => {
    let extra = ''
    if (step.address) extra += '<div class="copy" data-mcp-row></div>'
    if (step.command) extra += '<div class="copy" data-mcp-cmd="' + esc(step.command) + '"></div>'
    if (step.copy) extra += '<div class="copy"><input class="input mono" aria-label="' + esc(step.copy.label) + '" value="' + esc(step.copy.value) + '" readonly></div>'
    if (n === connect.length - 1 && hosted && app.fallback) {
      extra += '<p class="help">' + app.fallback + '</p><div class="copy" data-mcp-row></div>'
    }
    return '<li>' + step.html + (step.after || '') + extra + '</li>'
  })
  return '<ol class="howto">' + lis.join('') + '</ol>'
}

// wireAddresses fills every [data-mcp-row]/[data-mcp-cmd] mount under root
// with a read-only input and a Copy button (ui.copyButton), using address
// (the real MCP URL for this install: keyData.mcp_url || location.origin +
// '/mcp') so both cards are exact on a self-hosted instance too.
export function wireAddresses (ui, root, address) {
  root.querySelectorAll('[data-mcp-row]').forEach(row => {
    const input = document.createElement('input')
    input.className = 'input mono'
    input.readOnly = true
    input.value = address
    input.setAttribute('aria-label', 'Connector address')
    row.append(input, ui.copyButton(address, 'Copy'))
  })
  root.querySelectorAll('[data-mcp-cmd]').forEach(row => {
    const text = (row.getAttribute('data-mcp-cmd') || '').split('{address}').join(address)
    const input = document.createElement('input')
    input.className = 'input mono'
    input.readOnly = true
    input.value = text
    input.setAttribute('aria-label', 'Command')
    row.append(input, ui.copyButton(text, 'Copy'))
  })
}
