import { navigation } from '../navigation-data.js'
import { render as renderTeams } from './_settings-teams.js'
import { render as renderEntries } from './_settings-entries.js'
import { render as renderJudging } from './_settings-judging.js'
import { render as renderPeople } from './_settings-people.js'
import { render as renderResults } from './_settings-results.js'
import { render as renderAdmin } from './_settings-admin.js'
import { render as renderLinks } from './_settings-links.js'

const sections = Object.fromEntries(navigation.resolution.settings_tabs.map(t => [t.id, t.label]))
export function title() { return 'Settings' }

export async function render(ctx) {
  const { main, ui } = ctx
  const section = Object.hasOwn(sections, ctx.params.get('tab')) ? ctx.params.get('tab') : 'signup'
  const urlFor = tab => {
    const url = new URL(location.href)
    url.searchParams.set('s', 'settings'); url.searchParams.set('tab', tab); url.hash = ''
    return url.pathname + url.search
  }
  main.innerHTML = `<div class="ph"><div class="t"><h1>Settings</h1></div></div>
<div class="field settings-select"><label for="settings-section">Settings section</label><select class="select" id="settings-section">${Object.entries(sections).map(([id, label]) => `<option value="${id}"${id === section ? ' selected' : ''}>${label}</option>`).join('')}</select></div>
<div class="subnav-split settings-layout"><nav class="subnav" aria-label="Settings sections">${Object.entries(sections).map(([id, label]) => `<a href="${ui.esc(urlFor(id))}"${id === section ? ' aria-current="page"' : ''}>${label}</a>`).join('')}</nav><section class="sp" id="settings-content"></section></div>`
  const select = main.querySelector('#settings-section')
  select.addEventListener('change', async () => { if (!await ctx.go(urlFor(select.value))) select.value = section })
  const content = main.querySelector('#settings-content')
  const subctx = { ...ctx, main: content }
  if (section === 'teams') await renderTeams(subctx)
  else if (section === 'entries' || section === 'reminders') await renderEntries(subctx, section)
  else if (section === 'judging') await renderJudging(subctx)
  else if (section === 'results') await renderResults(subctx, section)
  else if (section === 'organisers') await renderPeople(subctx, section)
  else if (section === 'links') await renderLinks(subctx)
  else if (section === 'signup') {
    const slots = ['signup', 'approval', 'directory'].map(id => { const node = document.createElement('div'); node.id = id + '-settings'; content.append(node); return node })
    await Promise.all([renderPeople({ ...subctx, main: slots[0] }, 'signup'), renderAdmin({ ...subctx, main: slots[1] }, 'signup'), renderResults({ ...subctx, main: slots[2] }, 'directory')])
  } else await renderAdmin(subctx, section)
  if (!ctx.alive()) return
  let anchor = ''
  try { anchor = decodeURIComponent(location.hash.slice(1)) } catch (_) {}
  if (anchor) content.querySelector('#' + CSS.escape(anchor))?.scrollIntoView()
}
