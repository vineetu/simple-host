// SIM-341: every stage move (and Publish results) offers a suggested
// announcement on organiser-overview.js's "Tell participants?" dialog.
// This checks the pure suggestion module itself -- no DOM, no server --
// for every move the screen can actually offer it for: each stage in the
// forward stage list, the synthetic 'publish_results' move, and every
// backward target the stage picker can land on.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { suggestedAnnouncement } from '../../internal/webassets/static/hack/stage-announce.js'

// The announcements endpoint's real limits -- internal/hackhttp/
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
  // closed or judging.
  for (const stage of ['draft', 'open', 'building', 'closed', 'judging']) {
    check(stage, { back: true, eventTitle: 'Generation Girl 2026' })
  }
})

test('suggestions work with no opts at all', () => {
  check('open', undefined)
})
