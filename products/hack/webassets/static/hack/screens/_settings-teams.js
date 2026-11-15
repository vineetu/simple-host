import { href } from '../nav.js'
import { adminLock } from './_orgA-util.js'

// Existing team rules and save handlers.
export async function render(ctx) {
 const { main, api: client, ui, event, role } = ctx
 const api = client.api, slug = event.event.slug, esc = ui.esc
 const $ = id => main.querySelector('#' + id), setLive = ui.live(main)
 let ev = event.event, teamSizeMax = ev.team_size_max
 async function load() { teamSizeMax = ev.team_size_max }
 async function paint() {
  if (!ctx.alive()) return
  paintSettings(main)
  adminLock(main, !!event.admin_view, ev)
 }
  function paintSettings(sp) {
    const locked = ev.stage === 'archived'
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Teams</h2><p>How teams form in this event.</p></div>
<section class="card flat"><div class="setting"><div class="t"><b>Team finding</b><span>Participants can browse teams, ask to join and invite people looking for a team. Team links and codes work either way.</span></div><div class="ctl"><span class="saved" id="tf-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10l4 4 8-8"/></svg>Saved</span><button class="toggle${ev.team_finding ? ' on' : ''}" type="button" role="switch" aria-checked="${ev.team_finding ? 'true' : 'false'}" aria-label="Team finding" id="tf-on"${locked ? ' disabled' : ''}></button></div></div></section>
<section class="card"><h2>Team size</h2><div class="field"><label for="ts-size">Most people per team</label><input class="input" id="ts-size" type="number" min="1" max="50" value="${teamSizeMax}"${locked ? ' disabled' : ''}></div><div class="card-foot"><span class="hint" id="ts-hint">No changes</span><button class="btn quiet" type="button" id="ts-discard" disabled>Discard</button><button class="btn primary" type="button" id="ts-save" disabled>Save</button></div></section>
`
    const tfOn = sp.querySelector('#tf-on')
    if (tfOn) tfOn.addEventListener('click', onToggleTeamFinding)
    ui.trackForm(ctx, sp.querySelector('#ts-size').closest('section'))
    const sizeInput = sp.querySelector('#ts-size')
    const discard = sp.querySelector('#ts-discard')
    const save = sp.querySelector('#ts-save')
    const hint = sp.querySelector('#ts-hint')
    sizeInput.addEventListener('input', () => {
      const dirty = Number(sizeInput.value) !== teamSizeMax
      discard.disabled = !dirty
      save.disabled = !dirty
      hint.textContent = dirty ? '' : 'No changes'
    })
    discard.addEventListener('click', () => { sizeInput.value = teamSizeMax; discard.disabled = true; save.disabled = true; hint.textContent = 'No changes' })
    save.addEventListener('click', () => onSaveTeamSize(sizeInput, save))
  }

  async function onToggleTeamFinding() {
    const on = !ev.team_finding
    setLive('')
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), { team_finding: on }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save' }), true); return }
    ev = r.data.event
    await load()
    await paint()
    const tick = $('tf-saved')
    if (tick) { tick.hidden = false; setTimeout(() => { tick.hidden = true }, 2000) }
  }

  async function onSaveTeamSize(input, btn) {
    const size = Number(input.value)
    if (!size || size < 1 || size > 50) { setLive('Most people per team must be between 1 and 50.', true); return }
    btn.disabled = true
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), { team_size_max: size }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save' }), true); btn.disabled = false; return }
    ev = r.data.event
    await load()
    await paint()
    ui.saved(main)
  }


 await paint()
}
