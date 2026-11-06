// SIM-379: Event Basics' date+time pickers and the places that display
// starts_at/ends_at (status header, fixed schedule rows) agree on one rule:
// an empty time means local midnight, for Starts and Ends alike, and a
// midnight value always displays as a date with no time. This checks the
// pure helpers in ui.js and schedule.js directly -- no DOM, no server.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import * as ui from '../../internal/webassets/static/hack/ui.js'
import { merge } from '../../internal/webassets/static/hack/schedule.js'

const ev = {time_zone:'Asia/Kolkata'}
// hack_create_event: "A date (2026-10-01) means midnight in time_zone" --
// Basics' own empty-time default is local midnight, for both Starts and
// Ends alike (no 23:59 end default): a midnight Ends still lasts the whole
// of that day, but it is stored and reopens exactly like a midnight Starts.
test('empty times save local midnight and reopen blank, for Starts and Ends alike', () => {
  const saved = ui.eventBoundaryValue('2027-03-12', '')
  assert.equal(saved, '2027-03-12T00:00')
  const stored = saved + ':00+05:30'
  assert.deepEqual(ui.eventBoundaryInput(stored, ev), {date:'2027-03-12',time:''})
  assert.equal(ui.eventBoundaryDate(stored, ev), ui.dateOnly(stored, ev))
  assert.equal(ui.eventBoundaryValue('', ''), '')
})
test('set times round-trip in the event zone, including across UTC dates and DST', () => {
  for (const [zone, local, stored] of [
    ['Asia/Kolkata','2027-03-12T18:30','2027-03-12T13:00:00Z'],
    ['Asia/Kolkata','2027-03-12T00:00','2027-03-11T18:30:00Z'], // midnight is still a real value to round-trip through the fields
    ['America/New_York','2027-03-14T18:30','2027-03-14T22:30:00Z'],
    ['America/New_York','2027-11-07T18:30','2027-11-07T23:30:00Z']
  ]) {
    const input = ui.eventBoundaryInput(stored, zone)
    assert.equal(ui.eventBoundaryValue(input.date, input.time), local)
    assert.equal(ui.eventBoundaryDate(stored, zone), ui.dateTime(stored, zone, {dateOnly: ui.isLocalMidnight(stored, zone)}))
  }
})
test('status header and schedule rows show times only for non-midnight boundaries', () => {
  for (const [end, time, timed] of [[false,'00:00',false],[false,'18:30',true],[true,'00:00',false],[true,'23:59',true],[true,'20:15',true]]) {
    const value = '2099-03-12T'+time+':00+05:30'
    const next = ui.nextDeadline({...ev,[end?'ends_at':'starts_at']:value})
    assert.equal(next[2], !timed)
    assert.equal(/\d:\d{2} [ap]m/.test(ui.dateTime(next[1], ev, {dateOnly:next[2]})), timed)
    const row = merge([], [{key:end?'event_end':'event_start',start_at:value,all_day:!timed}])[0]
    assert.equal(row.all_day, !timed)
    assert.equal(/\d:\d{2} [ap]m/.test(ui.dateTime(row.start_at,ev,{dateOnly:row.all_day})), timed)
  }
})
test('a date-only end sorts after other rows that day without adding another day', () => {
  const rows = merge([{title:'Demos',start_at:'2027-03-12T20:00:00+05:30'}], [
    {key:'event_end',start_at:'2027-03-12T00:00:00+05:30',all_day:true},
    {key:'event_start',start_at:'2027-03-12T00:00:00+05:30',all_day:true}
  ])
  assert.deepEqual(rows.map(r=>r.key || r.title), ['event_start','Demos','event_end'])
})
