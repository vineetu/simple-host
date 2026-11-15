import { adminLock } from './_orgA-util.js'
export async function render(ctx) {
 const { main, api: client, ui, event: data } = ctx
 const api = client.api, ev = data.event, org = data.organiser || {}
 const $ = id => main.querySelector('#' + id), setLive = ui.live(main)
 main.innerHTML = `<section class="card"><h2>Join link</h2><div class="copy"><input class="input mono" id="ov-join-url" aria-label="Join link" value="${ui.esc(org.join_url || '')}" readonly><button class="btn" type="button" id="ov-join-copy" data-admin-live>Copy</button><button class="btn danger sm" type="button" id="ov-join-new">New link</button></div></section>
<section class="card"><h2>Judge link</h2><div class="copy"><input class="input mono" id="ov-judge-url" aria-label="Judge link" value="${ui.esc(org.judge_url || '')}" readonly><button class="btn" type="button" id="ov-judge-copy" data-admin-live>Copy</button><button class="btn danger sm" type="button" id="ov-judge-new">New link</button></div></section>`
    // Same clipboard logic as ui.copyButton, against the existing readonly inputs.
    async function copyInput(input, btn) {
      try { await navigator.clipboard.writeText(input.value); btn.textContent = 'Copied'; setTimeout(() => btn.textContent = 'Copy', 2000) }
      catch (e) { setLive('Could not copy. Select the text and copy it.', 'err') }
    }
    $('ov-join-copy').addEventListener('click', () => copyInput($('ov-join-url'), $('ov-join-copy')))
    $('ov-judge-copy').addEventListener('click', () => copyInput($('ov-judge-url'), $('ov-judge-copy')))

    // B4: the shell's own top-bar "Copy join/judge link" buttons are built
    // once, at mount time, from the url the shell was given -- they do not
    // listen for a change here. ctx.refresh() re-fetches the event and
    // re-mounts the shell (app.js route()), which is the only way today to
    // get the shell's copy buttons onto the new link.
    async function regen(kind, btn) {
      const ok = await ui.confirm({ title: 'Make a new link?', body: kind === 'join' ? 'The old link stops working, even for people who have not used it yet. People already in the event stay.' : 'The old judge link stops working. Judges already in stay.', confirmLabel: 'New link', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/codes/' + kind)
        if (!r.ok) { setLive(ui.errText(r), 'err'); btn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err'); btn.disabled = false }
    }
    $('ov-join-new').addEventListener('click', () => regen('join', $('ov-join-new')))
    $('ov-judge-new').addEventListener('click', () => regen('judge', $('ov-judge-new')))


 adminLock(main, !!data.admin_view, ev)
}
