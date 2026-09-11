import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parseCalls } from '../src/api/normalize.ts'

// A call with no number must not take the history page down with it.
//
// parseCallRecord required a non-empty remote_number, so one row without one
// threw "call 缺少 remote_number" and the whole list rendered as that error
// instead of as calls. Two normal cases produce such a row: an inbound call
// with the caller ID withheld has no number to record, and the module's
// phantom CLCC entries -- reported active, voice mode, no number, type 128,
// with nobody on a call -- are recorded the same way.

const META = { limit: 20, next_cursor: '', has_more: false }

function callPage(calls) {
  return { calls, meta: META }
}

function callRow(overrides = {}) {
  return {
    id: 'call-1',
    line_id: 'line-a',
    direction: 'incoming',
    remote_number: '',
    started_at: '2026-09-11T00:00:00Z',
    duration_seconds: 12,
    missed: false,
    read: true,
    favorite: false,
    ...overrides
  }
}

test('a call with a withheld number parses', () => {
  const page = parseCalls(callPage([callRow()]))
  assert.equal(page.items.length, 1)
  assert.equal(page.items[0].remote_number, '')
})

test('one anonymous row does not discard the rest of the history', () => {
  const page = parseCalls(callPage([callRow(), callRow({ id: 'call-2', remote_number: '+8613800138000' })]))
  assert.deepEqual(
    page.items.map(call => call.id),
    ['call-1', 'call-2']
  )
})

// A genuinely malformed row -- the field absent or not a string -- is still a
// broken response and must still be rejected, or this relaxation would hide a
// real contract break.
test('a row whose number is not a string is still rejected', () => {
  assert.throws(
    () => parseCalls(callPage([callRow({ direction: 'sideways' })])),
    /direction/
  )
})

// And an anonymous call needs a label, or the row renders blank and reads as a
// rendering fault rather than as a call from a withheld number.
test('the recordings list labels a call that has no number', () => {
  const view = readFileSync(new URL('../src/views/RecordingsView.vue', import.meta.url), 'utf8')
  const displayName = view.slice(view.indexOf('function displayName'))
  assert.match(displayName.slice(0, 400), /calls\.unknownNumber/)
})
