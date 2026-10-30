export function title(ctx) { return 'Create event' }

function slugify(s, limit) {
  return String(s || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, limit || 39).replace(/-+$/g, '')
}

export async function render(ctx) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  const tz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' } catch (e) { return 'UTC' } })()
  let slugTouched = false, slugTimer = null, slugCheckSeq = 0

  main.innerHTML = `
<div class="ph"><div class="t"><h1>Create event</h1><p>Set up a draft event. You can edit the details later.</p></div><button class="btn primary" type="submit" form="new-form" id="new-submit">Create</button></div>
<p class="crumbs"><a href="/events">Your events</a> / Create event</p>
<form id="new-form"><section class="card"><h2>Create event</h2>
<div class="field"><label>Address<input class="input" id="new-slug" type="text" maxlength="39" required></label><p class="help" id="new-slug-help">yourname.simple-hack.app</p></div>
<div class="field"><label>Title<input class="input" id="new-title" type="text" maxlength="120" required></label></div>
<div class="field"><label>Your name<input class="input" id="new-organiser" type="text" maxlength="100" required></label></div>
<div class="field"><label>Organisation<input class="input" id="new-org" type="text" maxlength="120"></label></div>
<div class="field"><label>Contact email<input class="input" id="new-email" type="email" required></label></div>
<div class="field"><label>Purpose<textarea class="input" id="new-purpose" maxlength="1000" required></textarea></label></div>
<div class="grid2"><div class="field"><label>Starts<input class="input" id="new-start" type="date" required></label></div><div class="field"><label>Ends (optional)<input class="input" id="new-end" type="date"></label></div></div>
<div class="field"><label>Time zone<input class="input" id="new-tz" type="text" value="${ui.esc(tz)}" required></label></div>
<div class="field"><label>Expected participants<input class="input" id="new-n" type="number" min="1" max="100000" value="60" required></label></div>
</section></form>`

  function checkSlugNow() {
    const slug = $('new-slug').value.trim().toLowerCase()
    const help = $('new-slug-help')
    if (!slug) { help.textContent = 'yourname.simple-hack.app'; help.className = 'help'; return }
    const seq = ++slugCheckSeq
    help.textContent = 'Checking…'; help.className = 'help'
    api('GET', '/v1/hack/names/' + encodeURIComponent(slug)).then(r => {
      if (seq !== slugCheckSeq) return
      if (!r.ok) { help.textContent = ui.errText(r, { fallback: 'Could not check that address.' }); help.className = 'help bad'; return }
      if (r.data.available) { help.textContent = slug + '.simple-hack.app — available'; help.className = 'help ok' }
      else { help.textContent = r.data.error || 'That address is taken.'; help.className = 'help bad' }
    }).catch(() => { if (seq === slugCheckSeq) { help.textContent = 'Could not reach the server.'; help.className = 'help bad' } })
  }
  $('new-title').addEventListener('input', () => {
    if (!slugTouched) $('new-slug').value = slugify($('new-title').value)
    clearTimeout(slugTimer); slugTimer = setTimeout(checkSlugNow, 400)
  })
  $('new-slug').addEventListener('input', () => {
    slugTouched = true
    $('new-slug').value = $('new-slug').value.toLowerCase()
    clearTimeout(slugTimer); slugTimer = setTimeout(checkSlugNow, 400)
  })

  $('new-form').addEventListener('submit', async e => {
    e.preventDefault()
    const nRaw = $('new-n').value.trim()
    if (!/^[1-9]\d*$/.test(nRaw) || Number(nRaw) > 100000) { setLive('Expected participants must be between 1 and 100000.', 'err'); return }
    const body = {
      slug: $('new-slug').value.trim().toLowerCase(),
      title: $('new-title').value.trim(),
      organiser_name: $('new-organiser').value.trim(),
      organisation: $('new-org').value.trim(),
      contact_email: $('new-email').value.trim(),
      purpose: $('new-purpose').value.trim(),
      expected_participants: Number(nRaw),
      starts_at: $('new-start').value,
      time_zone: $('new-tz').value.trim()
    }
    if ($('new-end').value) body.ends_at = $('new-end').value
    $('new-submit').disabled = true
    try {
      const r = await api('POST', '/v1/hack/events', body)
      if (!r.ok) {
        if (r.data && ['invalid_name', 'name_reserved', 'name_taken'].includes(r.data.code)) {
          const help = $('new-slug-help'); help.textContent = ui.errText(r); help.className = 'help bad'
        } else setLive(ui.errText(r), 'err')
        $('new-submit').disabled = false
        return
      }
      const slug = (r.data && r.data.event && r.data.event.slug) || body.slug
      ctx.go('/e/' + encodeURIComponent(slug) + '/manage')
    } catch (err) { setLive('Cannot reach the server.', 'err'); $('new-submit').disabled = false }
  })
}
