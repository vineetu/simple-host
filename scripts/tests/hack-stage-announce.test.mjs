// SIM-341: every stage move (and Publish results) offers a suggested
// announcement on organiser-overview.js's "Tell participants?" dialog.
// This checks the pure suggestion module itself -- no DOM, no server --
// for every move the screen can actually offer it for: each stage in the
// forward stage list, the synthetic 'publish_results' move, and every
// backward target the stage picker can land on.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { offerStageAnnouncement } from '../../internal/webassets/static/hack/screens/_stage-announce-dialog.js'
import { suggestedAnnouncement } from '../../internal/webassets/static/hack/stage-announce.js'

// The announcements endpoint's real limits -- products/hack/hackhttp/
// hack_content.go postAnnouncement: checkHackLine(req.Title, "title", 1,
// 120), checkHackText(req.Body, "body", 1, 3000). Hard-coded here, not
// imported (this is a Node script, not Go), so a future limit change
// must touch both this comment and the assertion below.
const TITLE_MAX = 120
const BODY_MAX = 3000

const STAGE_ORDER = ['draft', 'open', 'building', 'closed', 'judging', 'results', 'archived']

function check(move, opts) {
  const s = suggestedAnnouncement(move, opts)
  assert.ok(s, `suggestedAnnouncement(${JSON.stringify(move)}, ${JSON.stringify(opts)}) returned nothing`)
  assert.equal(typeof s.title, 'string')
  assert.equal(typeof s.body, 'string')
  assert.ok(s.title.trim().length > 0, `${move} (back=${!!(opts && opts.back)}): empty title`)
  assert.ok(s.body.trim().length > 0, `${move} (back=${!!(opts && opts.back)}): empty body`)
  assert.ok(s.title.length <= TITLE_MAX, `${move}: title ${s.title.length} chars exceeds the server's ${TITLE_MAX}`)
  assert.ok(s.body.length <= BODY_MAX, `${move}: body ${s.body.length} chars exceeds the server's ${BODY_MAX}`)
  return s
}

test('every forward stage move the "Your next step" button can offer has a suggestion', () => {
  // draft is the starting stage, never a forward target; every other
  // stage in STAGE_ORDER is reachable going forward.
  for (const stage of STAGE_ORDER.filter(s => s !== 'draft')) {
    check(stage, { back: false, eventTitle: 'Generation Girl 2026' })
  }
})

test('Publish results has a suggestion', () => {
  check('publish_results', { back: false, eventTitle: 'Generation Girl 2026' })
})

test('every backward target the stage picker can offer has a suggestion', () => {
  // The stage picker (_orgA-util.js stagePicker) never offers archived as
  // a choice, so a backward move can only land on draft, open, building,
  // closed or judging. judgingLocked only changes the judging branch
  // below, so it's left at its default (falsy) for every other stage.
  for (const stage of ['draft', 'open', 'building', 'closed', 'judging']) {
    check(stage, { back: true, eventTitle: 'Generation Girl 2026' })
  }
})

test('back to judging, not locked: "Judging has reopened", never promising a hold', () => {
  const s = check('judging', { back: true, judgingLocked: false, eventTitle: 'Generation Girl 2026' })
  assert.equal(s.title, 'Judging has reopened')
  assert.ok(!/on hold/i.test(s.body), 'unlocked suggestion must not say "on hold"')
})

test('back to judging, locked (moving back from Results): "Judging has reopened"', () => {
  const s = check('judging', { back: true, judgingLocked: true, eventTitle: 'Generation Girl 2026' })
  assert.equal(s.title, 'Judging has reopened')
  assert.match(s.body, /reopened/i)
})

test('back to closed never promises scoring reopens, regardless of lock state', () => {
  for (const judgingLocked of [true, false]) {
    const s = check('closed', { back: true, judgingLocked, eventTitle: 'Generation Girl 2026' })
    assert.equal(s.title, 'Entries are closed')
    assert.equal(s.body, 'Entries are closed. Teams with extra time can still edit.')
  }
})

test('suggestions work with no opts at all', () => {
  check('open', undefined)
})


test('backward stage moves do not offer an announcement dialog', async () => {
  // No UI or API is needed because the backward path must return first.
  for (const move of ['draft', 'open', 'building', 'closed', 'judging']) {
    await offerStageAnnouncement({}, { move, back: true })
  }
})

test('forward moves and publishing default to Don’t send with editable text', async () => {
  const previous = globalThis.document
  globalThis.document = { createElement: () => ({ innerHTML: '' }) }
  try {
    for (const move of ['open', 'building', 'closed', 'judging', 'results', 'publish_results']) {
      let dialog
      const ctx = {
        event: { event: { slug: 'demo', title: 'Demo' } },
        api: { api: () => assert.fail('default must not send') },
        ui: { esc: s => s, dialog: async opts => { dialog = opts; return 'cancel' } }
      }
      await offerStageAnnouncement(ctx, { move, back: false, emailableCount: 2 })
      assert.equal(dialog.focus, 0)
      assert.equal(dialog.buttons[0].label, "Don't send")
      assert.equal(dialog.buttons[0].className, 'btn primary')
      assert.equal(dialog.buttons[1].label, 'Send')
      assert.equal(dialog.buttons[1].className, 'btn')
      assert.match(dialog.body.innerHTML, /<input[^>]*id="sa-title"/)
      assert.match(dialog.body.innerHTML, /<textarea[^>]*id="sa-body"/)
      assert.doesNotMatch(dialog.body.innerHTML, /readonly/)
    }
  } finally { globalThis.document = previous }
})
