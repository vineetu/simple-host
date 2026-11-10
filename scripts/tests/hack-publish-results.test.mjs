import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const source = (await readFile(new URL('../../internal/webassets/static/hack/screens/_publish-results.js', import.meta.url), 'utf8'))
  .replace("import { offerStageAnnouncement } from './_stage-announce-dialog.js'", 'const offerStageAnnouncement = async ctx => { ctx.announcements++ }')
const { scoresChangedSincePublish, publishResults } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'))

test('snapshot comparison ignores manual ranks and failed preview, detects changed totals and teams', () => {
  const saved = [{ team_id: 'a', total: 80, rank: 2 }]
  assert.equal(scoresChangedSincePublish(saved, [{ ...saved[0], rank: 1 }]), false)
  assert.equal(scoresChangedSincePublish(saved, [], false), false)
  assert.equal(scoresChangedSincePublish(null, []), false)
  assert.equal(scoresChangedSincePublish(saved, [{ ...saved[0], total: 81 }]), true)
  assert.equal(scoresChangedSincePublish(saved, []), true)
})

function fixture({ tied = true, saved = true, stage = 'results', previewOk = true, confirm = true } = {}) {
  const rows = ['a', 'b'].map(team_id => ({ team_id, team_name: team_id, total: 80, rank: tied ? 1 : team_id === 'a' ? 1 : 2, tied }))
  const results = saved ? rows.map((r, i) => ({ ...r, tie_decider: 'organiser', rank: 2 - i })) : []
  const inputs = [], calls = [], messages = [], dialogs = []
  const ctx = {
    event: { event: { slug: 'demo', stage, results_published: saved }, organiser: { counts: { emailable_participants: 2 } } },
    announcements: 0, refreshed: 0, alive: () => true,
    refresh: async () => { ctx.refreshed++ },
    api: { isSignedOut: () => false, api: async (method, path, body) => {
      calls.push({ method, path, body })
      if (path.endsWith('/preview')) return { ok: previewOk, data: { results: rows } }
      return { ok: true, data: { results } }
    } },
    ui: {
      errText: () => 'Preview failed',
      el: (tag, attrs) => {
        const node = { tag, ...attrs, children: [], append(...nodes) { this.children.push(...nodes) }, appendChild(node) { this.children.push(node) } }
        if (tag === 'input') inputs.push(node)
        return node
      },
      confirm: async opts => { dialogs.push(opts); return confirm }
    }
  }
  const button = { disabled: false }
  return { ctx, button, inputs, calls, messages, dialogs, run: () => publishResults(ctx, { button, setLive: message => messages.push(message), confirmText: 'Confirm snapshot.' }) }
}

test('one Home dialog retains saved tie choices, publishes, offers announcement and refreshes', async () => {
  const f = fixture()
  await f.run()
  assert.equal(f.dialogs.length, 1)
  assert.equal(f.dialogs[0].confirmLabel, 'Publish again')
  assert.equal(f.dialogs[0].body[0].text, 'Confirm snapshot.')
  assert.ok(f.dialogs[0].body.some(n => n.tag === 'table'))
  assert.deepEqual(f.inputs.map(n => n.value), ['2', '1'])
  assert.deepEqual(f.calls.at(-1).body.rank_overrides, [{ team_id: 'a', rank: 2 }, { team_id: 'b', rank: 1 }])
  assert.equal(f.ctx.announcements, 1)
  assert.equal(f.ctx.refreshed, 1)
  assert.equal(f.button.disabled, false)
})

test('untied first publication sends an empty override list', async () => {
  const f = fixture({ tied: false, saved: false })
  await f.run()
  assert.equal(f.dialogs[0].confirmLabel, 'Publish results')
  assert.deepEqual(f.calls.at(-1).body, { rank_overrides: [] })
  assert.equal(f.inputs.length, 0)
})

test('republish after End refreshes without an announcement', async () => {
  const f = fixture({ stage: 'archived' })
  await f.run()
  assert.equal(f.calls.at(-1).method, 'POST')
  assert.equal(f.ctx.announcements, 0)
  assert.equal(f.ctx.refreshed, 1)
})

test('failed preview and cancel never publish', async () => {
  for (const options of [{ previewOk: false }, { confirm: false }]) {
    const f = fixture(options)
    await f.run()
    assert.equal(f.calls.filter(c => c.method === 'POST').length, 0)
    assert.equal(f.ctx.refreshed, 0)
    assert.equal(f.button.disabled, false)
  }
})

test('judge tour recognizes reopened scoring despite an earlier published snapshot', async () => {
  const source = await readFile(new URL('../../internal/webassets/static/hack/tours.js', import.meta.url), 'utf8')
  const { tours } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'))
  const step = tours.judge.steps.find(s => s.title === 'Is scoring open?')
  const event = { scoring_open: true, results_published: true, judging_locked_at: null }
  assert.equal(step.when({ event: { event } }), true)
  event.judging_locked_at = '2026-11-09T00:00:00Z'
  assert.equal(step.when({ event: { event } }), false)
})
