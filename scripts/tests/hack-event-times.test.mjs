import { test } from 'node:test'
import assert from 'node:assert/strict'
import * as ui from '../../internal/webassets/static/hack/ui.js'
import { merge } from '../../internal/webassets/static/hack/schedule.js'

const ev = {time_zone:'Asia/Kolkata'}
test('empty times save start/end of the event day and reopen blank', () => {
  for (const [end, expected] of [[false,'00:00'],[true,'23:59']]) {
    const saved = ui.eventBoundaryValue('2027-03-12', '', end)
    assert.equal(saved, '2027-03-12T'+expected)
    const stored = saved + ':00+05:30'
    assert.deepEqual(ui.eventBoundaryInput(stored, ev, end), {date:'2027-03-12',time:''})
    assert.equal(ui.eventBoundaryDate(stored, ev, end), ui.dateOnly(stored, ev))
    assert.equal(ui.eventBoundaryValue('', '', end), '')
  }
})
test('set times round-trip in the event zone, including across UTC dates and DST', () => {
  for (const [zone, local, stored, end] of [
    ['Asia/Kolkata','2027-03-12T18:30','2027-03-12T13:00:00Z',false],
    ['Asia/Kolkata','2027-03-12T00:00','2027-03-11T18:30:00Z',true],
    ['America/New_York','2027-03-14T18:30','2027-03-14T22:30:00Z',true],
    ['America/New_York','2027-11-07T18:30','2027-11-07T23:30:00Z',false]
  ]) {
    const input = ui.eventBoundaryInput(stored, zone, end)
    assert.equal(ui.eventBoundaryValue(input.date, input.time, end), local)
    assert.equal(ui.eventBoundaryDate(stored, zone, end), ui.dateTime(stored, zone))
  }
})
test('status header and schedule rows show times only for non-default boundaries', () => {
  for (const [end, time, timed] of [[false,'00:00',false],[false,'18:30',true],[true,'23:59',false],[true,'00:00',true],[true,'20:15',true]]) {
    const value = '2099-03-12T'+time+':00+05:30'
    const next = ui.nextDeadline({...ev,[end?'ends_at':'starts_at']:value})
    assert.equal(next[2], !timed)
    assert.equal(/\d:\d{2} [ap]m/.test(ui.dateTime(next[1], ev, {dateOnly:next[2]})), timed)
    const row = merge([], [{key:end?'event_end':'event_start',start_at:value,all_day:timed}], ev)[0]
    assert.equal(row.all_day, !timed)
    assert.equal(/\d:\d{2} [ap]m/.test(ui.dateTime(row.start_at,ev,{dateOnly:row.all_day})), timed)
  }
})
test('end-of-day defaults sort after other rows without adding another day', () => {
  const rows = merge([{title:'Demos',start_at:'2027-03-12T20:00:00+05:30'}], [
    {key:'event_end',start_at:'2027-03-12T23:59:00+05:30'},
    {key:'event_start',start_at:'2027-03-12T00:00:00+05:30'}
  ], ev)
  assert.deepEqual(rows.map(r=>r.key || r.title), ['event_start','Demos','event_end'])
})
