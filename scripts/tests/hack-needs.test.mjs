import { test } from 'node:test'
import assert from 'node:assert/strict'
import { splitNeed } from '../../internal/webassets/static/hack/needs.js'

test('team messages retain the full text and ordinary link label', () => {
  const text = 'Message from the organisers: Please do X. Thanks!'
  assert.deepEqual(splitNeed({ kind: 'team_message', text, section: 'team' }), [text, 'Open Team'])
  assert.deepEqual(splitNeed({ kind: 'team_locked', text: 'Team locked. Ask an admin.', section: 'team' }), ['Team locked.', 'Ask an admin.'])
})
