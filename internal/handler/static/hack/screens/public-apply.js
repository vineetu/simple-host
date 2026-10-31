// Public page reached at /e/{slug}/apply, signed in or not -- "Apply to a
// hackathon" (owner, 2026-10-31). Mirrors public-join.js's shape: a
// stand-alone screen (no event/role shell, since there is none until the
// application is decided), the slug read from location.pathname (nav.js's
// resolveRoute deliberately returns no `slug` for this route, so app.js's
// top-level router never runs its organiser/participant/judge
// api.getEvent() membership check -- that call would 401/404 for the
// stranger this screen is for).
//
// Data: GET .../apply (signed out allowed) returns what a stranger may see;
// signup_questions/coc_text/me only appear once signed in. POST .../apply
// sends the application. Both are new routes (build-plan.md §3.1/§3.2); the
// join route is untouched.
//
// Build plan §4 decision: the three optional fields (city, country code,
// age range, gender) never prefill and the organiser's questions/code of
// conduct never show before sign-in -- see spec.json public-apply
// "decisions". The country <select> is a fixed ISO 3166-1 alpha-2 list
// (below), not fetched from the server (D6/D7: these are fixed product
// choices, not organiser data); names come from the browser's own
// Intl.DisplayNames via ui.countryName, falling back to the bare code.

export function title() { return 'Apply' }

// Every region x/text's language.Region.IsCountry() accepts, minus the
// historical/deprecated/non-country aliases (AN, BU, CS, CT, DD, DY, EZ,
// FQ, FX, HV, JT, MI, NH, NQ, NT, PC, PU, PZ, RH, SU, TP, UK, UN, VD, WK,
// YD, YU, ZR), the postal-only exceptional reservations with no real
// population (AC, CP, DG, EA, IC, TA) and CQ (parses as a country but
// carries no English name in CLDR -- not a real option to show anyone).
// XK (Kosovo) is kept: it has no official ISO entry but is in day-to-day
// use (CLDR, every major OS) and resolves to a real name. 250 codes.
// NOTE for whoever writes the server validator/test (build-plan.md §3.2,
// §8 item 25): a raw `.IsCountry()` check alone also accepts UK, AN, EZ,
// UN and the other codes excluded above -- §8 item 25's "UK rejected, GB
// accepted" case only holds if the server excludes the same set. If the
// drift test enumerates raw IsCountry() and expects this exact list to
// cover it 1:1, the two need reconciling in review; this list is the
// product-sane one, not a literal mirror of every IsCountry() region.
const COUNTRY_CODES = [
'AD','AE','AF','AG','AI','AL','AM','AO','AQ','AR','AS','AT','AU','AW','AX','AZ','BA','BB','BD','BE','BF','BG','BH',
'BI','BJ','BL','BM','BN','BO','BQ','BR','BS','BT','BV','BW','BY','BZ','CA','CC','CD','CF','CG','CH','CI','CK','CL',
'CM','CN','CO','CR','CU','CV','CW','CX','CY','CZ','DE','DJ','DK','DM','DO','DZ','EC','EE','EG','EH','ER','ES','ET',
'FI','FJ','FK','FM','FO','FR','GA','GB','GD','GE','GF','GG','GH','GI','GL','GM','GN','GP','GQ','GR','GS','GT','GU',
'GW','GY','HK','HM','HN','HR','HT','HU','ID','IE','IL','IM','IN','IO','IQ','IR','IS','IT','JE','JM','JO','JP','KE',
'KG','KH','KI','KM','KN','KP','KR','KW','KY','KZ','LA','LB','LC','LI','LK','LR','LS','LT','LU','LV','LY','MA','MC',
'MD','ME','MF','MG','MH','MK','ML','MM','MN','MO','MP','MQ','MR','MS','MT','MU','MV','MW','MX','MY','MZ','NA','NC',
'NE','NF','NG','NI','NL','NO','NP','NR','NU','NZ','OM','PA','PE','PF','PG','PH','PK','PL','PM','PN','PR','PS','PT',
'PW','PY','QA','RE','RO','RS','RU','RW','SA','SB','SC','SD','SE','SG','SH','SI','SJ','SK','SL','SM','SN','SO','SR',
'SS','ST','SV','SX','SY','SZ','TC','TD','TF','TG','TH','TJ','TK','TL','TM','TN','TO','TR','TT','TV','TW','TZ','UA',
'UG','UM','US','UY','UZ','VA','VC','VE','VG','VI','VN','VU','WF','WS','XK','YE','YT','ZA','ZM','ZW'
]

// codes(): this screen's error-code -> copy overrides on top of the shared
// map. Same convention as public-join.js's own codes(ui) -- "tries", not
// "changes", since applying is a one-off attempt, not a repeated edit.
function codes(ui) {
  return {
    ...ui.ERROR_MESSAGES,
    rate_limited: 'Too many applications from this account. Wait a while and try again.',
    applications_closed: "This event isn't taking applications.",
    too_many_pending: 'You have 20 applications waiting. Wait for answers before applying to more.',
    event_applications_full: 'This event has as many people waiting as it can take right now. Try again once the organisers have answered some of them.'
  }
}

function slugFromPath() {
  const m = location.pathname.replace(/\/+$/, '').match(/^\/e\/([^/]+)\/apply$/)
  return m ? decodeURIComponent(m[1]) : ''
}

// A plain "28-29 Oct 2026" range with no weekday (same convention as
// hack-directory.html's own dateRange -- a range reads better without one).
function dateParts(iso, zone) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return null
  try {
    const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zone || 'UTC', day: 'numeric', month: 'short', year: 'numeric' }).formatToParts(d)
    const o = {}
    parts.forEach(p => { o[p.type] = p.value })
    return o
  } catch { return null }
}
function dateRangeStr(startsAt, endsAt, zone) {
  const a = startsAt ? dateParts(startsAt, zone) : null
  const b = endsAt ? dateParts(endsAt, zone) : null
  if (!a && !b) return ''
  if (a && !b) return a.day + ' ' + a.month + ' ' + a.year
  if (b && !a) return b.day + ' ' + b.month + ' ' + b.year
  if (a.year === b.year && a.month === b.month && a.day === b.day) return a.day + ' ' + a.month + ' ' + a.year
  if (a.year === b.year && a.month === b.month) return a.day + '–' + b.day + ' ' + a.month + ' ' + a.year
  if (a.year === b.year) return a.day + ' ' + a.month + '–' + b.day + ' ' + b.month + ' ' + a.year
  return a.day + ' ' + a.month + ' ' + a.year + '–' + b.day + ' ' + b.month + ' ' + b.year
}

function headerHTML(ui, info, openHref) {
  const esc = ui.esc
  const bits = []
  if (info.organiser_name) bits.push('Organised by ' + info.organiser_name + (info.organisation ? ' (' + info.organisation + ')' : ''))
  const range = dateRangeStr(info.starts_at, info.ends_at, info.time_zone)
  if (range) bits.push(range)
  const zone = info.time_zone ? ui.zoneAbbr(info.time_zone) : ''
  if (zone) bits.push('times in ' + zone)
  return `<section class="hero"><h1>Apply to ${esc(info.title || 'this event')}</h1>${bits.length ? `<p>${esc(bits.join(' · '))}</p>` : ''}<p class="small"><a href="${esc(openHref)}">See the event page</a></p></section>`
}

function countryOptions(ui) {
  return COUNTRY_CODES.map(code => ({ code, name: ui.countryName(code) || code })).sort((a, b) => a.name.localeCompare(b.name))
}

export async function render(ctx) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const CODES = codes(ui)
  const slug = slugFromPath()
  main.innerHTML = `<p class="note">Loading…</p>`
  if (!slug) { main.innerHTML = `<div class="note bad" role="alert">We couldn't find this event.</div><p><a class="btn primary" href="/directory">Find a hackathon</a></p>`; return }
  const openHref = '/e/' + encodeURIComponent(slug)

  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/apply') }
  catch (e) {
    if (!client.isSignedOut(e)) main.innerHTML = `<div class="note bad" role="alert">Cannot reach the server. Try the link again in a moment.</div>`
    return
  }
  if (!ctx.alive()) return
  if (r.status === 404 || (r.data && r.data.code === 'event_not_found')) {
    // Draft, taken down and unknown all answer with the same 404 so a
    // draft's existence never leaks through this route -- one generic
    // message covers every case, since there is no server signal to tell
    // them apart (spec.json public-apply "not open yet / not found").
    main.innerHTML = `<section class="hero"><h1>Apply</h1></section><div class="note bad" role="alert">We couldn't find this event.</div><p><a class="btn primary" href="/directory">Find a hackathon</a></p>`
    return
  }
  if (!r.ok) { main.innerHTML = `<div class="note bad" role="alert">${esc(ui.errText(r, { codes: CODES }))}</div>`; return }
  const info = r.data || {}
  const header = headerHTML(ui, info, openHref)

  // Already a member (signed in, applied or joined before): this state
  // always wins over the open/closed switch, since a decided or waiting
  // application stays visible to the person it belongs to either way.
  const me = info.me
  if (me) {
    if (me.role !== 'participant') {
      const article = /^[aeiou]/i.test(me.role || '') ? 'an' : 'a'
      main.innerHTML = header + `<div class="note">You're already part of this event as ${article} ${esc(me.role)}.</div><p><a class="btn" href="/events">Open the event</a></p>`
      return
    }
    if (me.approval_status === 'approved') {
      main.innerHTML = header + `<div class="note ok">You're in ${esc(info.title || 'this event')}.</div><p><a class="btn primary" href="${esc(openHref)}/team">Go to the event</a></p>`
      return
    }
    if (me.approval_status === 'rejected') {
      main.innerHTML = header + `<div class="note bad">Your application wasn't accepted this time.</div><section class="card"><p>Organisers decide who joins each event.</p><div class="row-flex"><a class="btn primary" href="/directory">Find another hackathon</a><a class="btn" href="${esc(openHref)}">See the event page</a></div></section>`
      return
    }
    // pending (same for an application and a join-link sign-up)
    main.innerHTML = header + `<div class="note warn"><b>Your application is waiting for the organisers.</b></div><section class="card"><p>They decide who joins ${esc(info.title || 'this event')}. You'll see their answer here and on your Home. Once you're in, you can find a team.</p><div class="row-flex"><a class="btn primary" href="/events">Go to your Home</a><a class="btn" href="${esc(openHref)}">See the event page</a></div></section>`
    return
  }

  if (!info.applications_open) {
    if (info.closed_reason === 'applications_off') {
      main.innerHTML = header + `<div class="note warn"><b>This event isn't taking applications.</b></div><section class="card"><p>The organisers only take people with their join link. If you have one, open it to join.</p><div class="row-flex"><a class="btn" href="${esc(openHref)}">See the event page</a><a class="btn" href="/directory">Find another hackathon</a></div></section>`
    } else {
      main.innerHTML = header + `<div class="note">Sign-up has closed for this event.</div><p><a class="btn" href="${esc(openHref)}">See the event page</a></p>`
    }
    return
  }

  // A stale key: the browser still has one stored, but the server didn't
  // recognise it (GET .../apply allows a signed-out caller, so it never
  // 401s here the way most routes do -- it just leaves `me` out of the
  // response instead). Treat that exactly like being signed out, not like a
  // signed-in caller with no questions: `me` is always present, even as
  // null, once the server does recognise the caller (getApply, hack_apply.go).
  if (!client.apiKey() || !('me' in info)) {
    main.innerHTML = header + `<section class="card">
<h2>Apply to join</h2>
<p>Sign in with your email, then fill in one short form:</p>
<ul class="steps">
<li><span class="what"><b>Your name</b>Shown to the organisers, and to your teammates once you're in.</span></li>
<li><span class="what"><b>Where you're based, age range and gender</b>All three are optional.</span></li>
<li><span class="what"><b>A few questions from the organisers</b>And their code of conduct to agree to.</span></li>
</ul>
<p>The organisers decide who joins. You'll see their answer here and on your Simple Hack Home.</p>
<button class="btn primary" type="button" id="ap-signin">Sign in to apply</button>
<p class="small muted" style="margin-top:12px">Have a join link from the organisers? Open that instead.</p>
</section>`
    main.querySelector('#ap-signin').addEventListener('click', () => {
      ctx.go('/signin?next=' + encodeURIComponent(location.pathname))
    })
    return
  }

  // Signed in, not a member yet, applications open: the form.
  const fields = info.fields || {}
  const ageOptions = Array.isArray(fields.age_range) && fields.age_range.length ? fields.age_range : [
    { value: 'under_18', label: ui.AGE_RANGE_LABELS.under_18 }, { value: '18_24', label: ui.AGE_RANGE_LABELS['18_24'] },
    { value: '25_34', label: ui.AGE_RANGE_LABELS['25_34'] }, { value: '35_plus', label: ui.AGE_RANGE_LABELS['35_plus'] }
  ]
  const genderOptions = Array.isArray(fields.gender) && fields.gender.length ? fields.gender : [
    { value: 'woman', label: ui.GENDER_LABELS.woman }, { value: 'man', label: ui.GENDER_LABELS.man },
    { value: 'non_binary', label: ui.GENDER_LABELS.non_binary }, { value: 'another', label: ui.GENDER_LABELS.another },
    { value: 'prefer_not_to_say', label: ui.GENDER_LABELS.prefer_not_to_say }
  ]
  // D4/review item 4: prefill from display_name only, as public-join.js
  // does -- never the email or username, so the one never becomes the
  // visible name the organisers and teammates see.
  const name = (ctx.me && ctx.me.display_name) || ''
  const email = (ctx.me && (ctx.me.email || ctx.me.username)) || ''
  const questions = Array.isArray(info.signup_questions) ? info.signup_questions : []
  const questionsHTML = questions.map((q, i) =>
    `<div class="field"><label for="ap-q-${i}">${esc(q.prompt)}${q.required ? '' : ' (optional)'}</label><textarea class="input" id="ap-q-${i}" data-question="${esc(q.id)}" maxlength="500"${q.required ? ' required' : ''}></textarea></div>`
  ).join('')
  const cocHTML = `<h3 style="margin-top:20px">Code of conduct</h3><p>${esc(info.coc_default || '')}</p>${info.coc_text ? `<p class="small"><b>From the organisers:</b> ${esc(info.coc_text)}</p>` : ''}`
  const countryHTML = countryOptions(ui).map(o => `<option value="${esc(o.code)}">${esc(o.name)}</option>`).join('')

  main.innerHTML = header + `<form class="card" id="ap-form">
<h2>Your application</h2>
<p class="sub">Signed in as ${esc(email)} · <button class="btn quiet sm" type="button" id="ap-signout">Not you? Sign out</button></p>

<div class="field"><label for="ap-name">Your name</label><input class="input" id="ap-name" type="text" maxlength="100" required value="${esc(name)}" autocomplete="name"><p class="help">Shown to the organisers, and to your teammates once you're in.</p></div>

<fieldset style="border:0;padding:0;margin:0 0 16px;min-width:0"><legend class="lab" style="font-weight:600;font-size:13.5px;padding:0;margin:0 0 6px">Where you're based (optional)</legend>
<div class="field-row">
<div class="field grow"><label for="ap-city">City</label><input class="input" id="ap-city" type="text" maxlength="80" autocomplete="address-level2"></div>
<div class="field grow"><label for="ap-country">Country</label><select class="select" id="ap-country" style="width:100%" autocomplete="country"><option value="">Choose a country</option>${countryHTML}</select></div>
</div>
</fieldset>

<fieldset style="border:0;padding:0;margin:0 0 16px;min-width:0"><legend class="lab" style="font-weight:600;font-size:13.5px;padding:0;margin:0 0 6px">Age range (optional)</legend>
<div class="choices" id="ap-age" style="grid-template-columns:repeat(auto-fit,minmax(120px,1fr))">
${ageOptions.map(o => `<label class="choice"><input type="radio" name="age" value="${esc(o.value)}"><b>${esc(o.label)}</b></label>`).join('')}
</div>
<div class="note" id="ap-minor" hidden style="margin:10px 0 0"></div>
</fieldset>

<div class="field"><label for="ap-gender">Gender (optional)</label><select class="select" id="ap-gender"><option value="">Choose</option>${genderOptions.map(o => `<option value="${esc(o.value)}">${esc(o.label)}</option>`).join('')}</select></div>

${questions.length ? `<h3 style="margin-top:20px">Questions from the organisers</h3>${questionsHTML}` : ''}

${cocHTML}
<label class="check"><input type="checkbox" id="ap-coc" required><span>I agree to the code of conduct</span></label>

<div class="note info">Only the organisers of ${esc(info.title || 'this event')} see your answers. They're never shown on the event page or to other participants.</div>
<button class="btn primary btn-block" type="submit" id="ap-submit">Send application</button>
</form>`

  const $ = id => main.querySelector('#' + id)

  // Review item 6: "Terms" is a real link to /terms, built with el() and an
  // href -- not the raw sentence (which would show the URL as plain text)
  // dropped in via innerHTML.
  $('ap-minor').append(
    "If you're under 18, a parent or guardian needs to agree to Simple Hack's ",
    ui.el('a', { href: '/terms', text: 'Terms' }),
    ' for you.'
  )

  $('ap-signout').addEventListener('click', () => { client.signOut(ui.confirm) })

  main.querySelectorAll('#ap-age input').forEach(radio => {
    radio.addEventListener('change', () => {
      main.querySelectorAll('#ap-age .choice').forEach(c => c.classList.toggle('on', c.querySelector('input').checked))
      $('ap-minor').hidden = radio.value !== 'under_18'
    })
  })

  function fieldNear(code) {
    switch (code) {
      case 'coc_required': return $('ap-coc').closest('.check')
      case 'invalid_display_name': return $('ap-name').closest('.field')
      case 'invalid_city': return $('ap-city').closest('.field')
      case 'invalid_country': return $('ap-country').closest('.field')
      case 'invalid_age_range': return $('ap-age')
      case 'invalid_gender': return $('ap-gender').closest('.field')
      default: return null
    }
  }

  $('ap-form').addEventListener('submit', async e => {
    e.preventDefault()
    const btn = $('ap-submit')
    if (!$('ap-coc').checked) { ui.toast(CODES.coc_required, $('ap-coc').closest('.check'), true); return }
    const nm = $('ap-name').value.trim()
    if (!nm) return
    const ageEl = main.querySelector('#ap-age input:checked')
    const body = {
      accept_coc: true,
      display_name: nm,
      city: $('ap-city').value.trim(),
      country: $('ap-country').value,
      age_range: ageEl ? ageEl.value : '',
      gender: $('ap-gender').value,
      answers: {}
    }
    main.querySelectorAll('[data-question]').forEach(input => { body.answers[input.getAttribute('data-question')] = input.value })
    btn.disabled = true
    let res
    try { res = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/apply', body) }
    catch (err) {
      if (!client.isSignedOut(err)) ui.toast('Cannot reach the server.', main, true)
      btn.disabled = false
      return
    }
    if (!res.ok) {
      const near = fieldNear(res.data && res.data.code)
      ui.toast(ui.errText(res, { codes: CODES }), near || main, true)
      btn.disabled = false
      return
    }
    // A real navigation, not an SPA one: the server now holds this
    // membership (pending, or already decided for an idempotent re-apply),
    // and only a fresh load renders that state from it -- same reasoning as
    // public-join.js's own post-join navigation.
    location.assign(location.pathname)
  })
}
