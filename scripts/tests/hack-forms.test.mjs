import { test } from 'node:test'
import assert from 'node:assert/strict'
import { values, snapshot, changed, state, errorField } from '../../internal/webassets/static/hack/forms.js'

test('snapshot tracks text, choices, files, and returning to saved values', () => {
  const fields = [{value:'Draft'}, {type:'checkbox',checked:false}, {type:'radio',checked:true}, {type:'file',files:[{name:'site.zip',size:10,lastModified:1}]}]
  const before = snapshot(values(fields))
  assert.equal(changed(before, values(fields)), false)
  fields[0].value = 'Edited'
  assert.equal(changed(before, values(fields)), true)
  fields[0].value = 'Draft'; fields[1].checked = true
  assert.equal(changed(before, values(fields)), true)
  fields[1].checked = false
  assert.equal(changed(before, values(fields)), false)
})
test('status messages replace each other, including empty status', () => {
  const form = state()
  form.message('Not saved', true); form.message('Saved')
  assert.equal(form.status, 'Saved'); assert.equal(form.bad, false)
  form.message(''); assert.equal(form.status, '')
})
test('editing clears only that field error; saving clears every old error', () => {
  const form = state()
  form.error('track', 'Pick a track'); form.error('title', 'Add a title')
  form.edit('track')
  assert.equal(form.errors.has('track'), false); assert.equal(form.errors.has('title'), true)
  form.saved()
  assert.equal(form.errors.size, 0); assert.equal(form.bad, false); assert.equal(form.status, 'Saved')
  form.error('title', 'Add a title'); form.clear()
  assert.equal(form.errors.size, 0); assert.equal(form.status, '')
})

test('server codes identify their field, including name conflicts and consent', () => {
  assert.equal(errorField('invalid_track'), 'track')
  assert.equal(errorField('team_name_taken'), 'team_name')
  assert.equal(errorField('invalid_display_name'), 'display_name')
  assert.equal(errorField('name_taken'), 'slug')
  assert.equal(errorField('coc_required'), 'coc')
})
