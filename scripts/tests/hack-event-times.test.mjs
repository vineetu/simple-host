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
    const row = merge([], [{key:end?'event_end':'event_start',start_at:value,all_day:!timed}], ev)[0]
    assert.equal(row.all_day, !timed)
    assert.equal(/\d:\d{2} [ap]m/.test(ui.dateTime(row.start_at,ev,{dateOnly:row.all_day})), timed)
  }
})
test('a date-only end sorts after other rows that day without adding another day', () => {
  const rows = merge([{title:'Demos',start_at:'2027-03-12T20:00:00+05:30'}], [
    {key:'event_end',start_at:'2027-03-12T00:00:00+05:30',all_day:true},
    {key:'event_start',start_at:'2027-03-12T00:00:00+05:30',all_day:true}
  ], ev)
  assert.deepEqual(rows.map(r=>r.key || r.title), ['event_start','Demos','event_end'])
})
// DST: a date-only event end's own day isn't always 24h. America/New_York,
// 7 Nov 2027 is the 25-hour day DST falls back into (clocks go 2am -> 1am
// partway through it), so the real end-of-day boundary (the next local
// midnight, 8 Nov 00:00 NY = 2027-11-08T05:00:00Z) is an hour later than a
// flat +24h from the start of that day would give (2027-11-08T04:00:00Z) --
// late enough that a legitimate 23:30 row that same evening
// (2027-11-07T23:30 NY = 2027-11-08T04:30:00Z) falls *after* the flat +24h
// boundary and so sorted ahead of "Event ends" under the old bug.
test('a date-only end on a 25-hour DST day still sorts after a late-night row', () => {
  const nyEv = {time_zone:'America/New_York'}
  const rows = merge([{title:'Late night',start_at:'2027-11-08T04:30:00Z'}], [
    {key:'event_end',start_at:'2027-11-07T04:00:00Z',all_day:true}
  ], nyEv)
  assert.deepEqual(rows.map(r=>r.key || r.title), ['Late night','event_end'])
})
test('nextLocalMidnight resolves the real DST gap, not a flat 24h', () => {
  // 7 Nov 2027 (fall back): the day is 25 real hours, so the next local
  // midnight is an hour later than start_at + 24h.
  assert.equal(ui.nextLocalMidnight('2027-11-07T04:00:00Z', 'America/New_York'), Date.parse('2027-11-08T05:00:00Z'))
  // 14 Mar 2027 (spring forward): the day is 23 real hours, so the next
  // local midnight is an hour earlier than start_at + 24h.
  assert.equal(ui.nextLocalMidnight('2027-03-14T05:00:00Z', 'America/New_York'), Date.parse('2027-03-15T04:00:00Z'))
})
