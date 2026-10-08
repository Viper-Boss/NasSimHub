import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { gateway } from '../src/api/client.ts'
import {
  callState,
  acceptRuntimeActiveCalls,
  reconcileCallHeartbeat,
  dial,
  initializeCallRuntime,
  lineIsOccupied,
  occupiedLineIDs,
  requestActiveCallRefresh,
  selectForegroundSession,
  shutdownCallRuntime
} from '../src/state/call.ts'
import { bootstrapResource } from '../src/state/workspace.ts'
import { openDialer, resetUIState, uiState } from '../src/state/ui.ts'
import { i18n } from '../src/i18n/index.ts'
import { loadComponent, render, textOf } from './support/render-sfc.mjs'

function call(overrides) {
  return {
    id: 'call-default',
    line_id: 'line-default',
    direction: 'incoming',
    remote_number: '+818000000000',
    phase: 'ringing',
    control_state: 'available',
    media_available: false,
    created_at: '2026-07-29T00:00:00Z',
    ...overrides
  }
}

function reservation(overrides = {}) {
  return {
    request_id: 'request-default',
    line_id: 'line-reserved',
    control_state: 'occupied',
    created_at: '2026-07-29T00:00:00Z',
    ...overrides
  }
}

test('foreground selection prefers owned, then answerable, then occupied calls', () => {
  const occupied = call({
    id: 'call-occupied',
    line_id: 'line-occupied',
    phase: 'active',
    control_state: 'occupied'
  })
  const incoming = call({
    id: 'call-incoming',
    line_id: 'line-incoming'
  })
  const owned = call({
    id: 'call-owned',
    line_id: 'line-owned',
    direction: 'outgoing',
    phase: 'active',
    control_state: 'owned'
  })

  assert.equal(
    selectForegroundSession([occupied, incoming, owned], occupied.id)?.id,
    owned.id
  )
  assert.equal(
    selectForegroundSession([occupied, incoming], occupied.id)?.id,
    incoming.id
  )
  assert.equal(
    selectForegroundSession([occupied], occupied.id)?.id,
    occupied.id
  )
})

test('foreground remains stable among calls with the same priority', () => {
  const first = call({
    id: 'call-first',
    line_id: 'line-first',
    phase: 'active',
    control_state: 'occupied'
  })
  const second = call({
    id: 'call-second',
    line_id: 'line-second',
    phase: 'active',
    control_state: 'occupied'
  })

  assert.equal(
    selectForegroundSession([first, second], second.id)?.id,
    second.id
  )
})

test('line occupancy merges calls and reservations and deduplicates by line', () => {
  const calls = [
    call({ id: 'call-1', line_id: 'line-1' }),
    call({ id: 'call-2', line_id: 'line-2', control_state: 'occupied' }),
    call({ id: 'call-ended', line_id: 'line-ended', phase: 'ended' })
  ]
  const reservations = [
    reservation({ request_id: 'request-2', line_id: 'line-2' }),
    reservation({ request_id: 'request-3', line_id: 'line-3' })
  ]
  const lines = occupiedLineIDs(calls, reservations)

  assert.deepEqual([...lines].sort(), ['line-1', 'line-2', 'line-3'])
  assert.equal(lineIsOccupied('line-3', calls, reservations), true)
  assert.equal(lineIsOccupied('line-ended', calls, reservations), false)
})

test('SSE reconciliation stores reservations without turning them into calls', async () => {
  const originalGetActiveCallSnapshot = gateway.getActiveCallSnapshot
  const occupied = call({
    id: 'call-occupied',
    line_id: 'line-occupied',
    phase: 'active',
    control_state: 'occupied'
  })
  const incoming = call({
    id: 'call-incoming',
    line_id: 'line-incoming'
  })
  const owned = call({
    id: 'call-owned',
    line_id: 'line-owned',
    direction: 'outgoing',
    phase: 'active',
    control_state: 'owned'
  })
  const pending = reservation()
  let snapshot = { calls: [], reservations: [pending] }
  gateway.getActiveCallSnapshot = async () => snapshot

  try {
    initializeCallRuntime()
    await requestActiveCallRefresh()
    assert.deepEqual(callState.sessions, [])
    assert.deepEqual(callState.reservations, [pending])
    assert.equal(callState.session, null)
    assert.equal(callState.owned, false)

    snapshot = { calls: [occupied, incoming], reservations: [pending] }
    await requestActiveCallRefresh()
    assert.deepEqual(
      callState.sessions.map(session => session.id),
      [occupied.id, incoming.id]
    )
    assert.deepEqual(callState.reservations, [pending])
    assert.equal(callState.session?.id, incoming.id)
    assert.equal(callState.owned, false)

    snapshot = {
      calls: [occupied, incoming, owned],
      reservations: []
    }
    await requestActiveCallRefresh()
    assert.deepEqual(
      callState.sessions.map(session => session.id),
      [occupied.id, incoming.id, owned.id]
    )
    assert.equal(callState.session?.id, owned.id)
    assert.equal(callState.owned, true)
  } finally {
    shutdownCallRuntime()
    gateway.getActiveCallSnapshot = originalGetActiveCallSnapshot
  }
})

test('dial refuses a reserved line before invoking the gateway', async () => {
  const originalBootstrap = bootstrapResource.data
  const originalBootstrapStatus = bootstrapResource.status
  const originalStartCall = gateway.startCall
  let startRequests = 0

  bootstrapResource.data = {
    capabilities: {
      agent_connected: true,
      dial: true,
      message: true,
      webrtc_audio: true,
      device_control: false,
      volte_control: false,
      vowifi_control: false
    },
    lines: [
      {
        id: 'line-reserved',
        capabilities: {
          dial: true,
          media: true
        }
      }
    ]
  }
  bootstrapResource.status = 'ready'
  callState.reservations = [reservation()]
  gateway.startCall = async () => {
    startRequests += 1
    throw new Error('startCall must not run for a reserved line')
  }

  try {
    assert.equal(await dial('+818012345678', 'line-reserved'), false)
    assert.equal(startRequests, 0)
    assert.equal(callState.errorStatus, 409)
    assert.equal(callState.session, null)
  } finally {
    shutdownCallRuntime()
    gateway.startCall = originalStartCall
    bootstrapResource.data = originalBootstrap
    bootstrapResource.status = originalBootstrapStatus
  }
})

test('dialer keeps reservations as line status without making them call surfaces', async () => {
  const [dialer, selector, lineIdentity] = await Promise.all([
    readFile(new URL('../src/components/DialerPanel.vue', import.meta.url), 'utf8'),
    readFile(new URL('../src/components/LineSelector.vue', import.meta.url), 'utf8'),
    readFile(new URL('../src/components/LineIdentity.vue', import.meta.url), 'utf8')
  ])

  assert.match(dialer, /collectOccupiedLineIDs\(\)/)
  assert.match(dialer, /const occupiedLineCount = computed\(\(\) => occupiedLineIDs\.value\.size\)/)
  assert.match(dialer, /v-model="lineSwitcherID"/)
  assert.match(dialer, /:status-values="\[\.\.\.occupiedLineIDs\]"/)
  assert.match(dialer, /:disabled="callState\.owned"/)
  assert.match(dialer, /if \(callState\.owned\) return/)
  assert.match(
    dialer,
    /if \(lineHasActiveCall\(lineID\)\) \{[\s\S]*showActiveCallForLine\(lineID\)/
  )
  assert.match(dialer, /callState\.owned \? t\('dialer\.activeCall'\)/)
  assert.match(dialer, /v-else-if="occupiedLineCount > 0"/)
  assert.match(
    dialer,
    /const selectedStillExists = dialLines\.value\.some\([\s\S]*Boolean\(number\.value\.trim\(\)\)/
  )
  assert.doesNotMatch(
    dialer,
    /const selectedStillExists = availableDialLines\.value\.some/
  )
  assert.match(
    dialer,
    /<span class="dialer-toolbar__title">[\s\S]*<h2>[\s\S]*<span class="dialer-status-slot">[\s\S]*dialer-active-calls--busy/
  )
  assert.match(dialer, /\.dialer-toolbar__title \{[\s\S]*width: 104px;[\s\S]*grid-template-columns:/)
  assert.match(dialer, /class="dialer-header-actions">[\s\S]*v-if="!permanent"[\s\S]*class="icon-button"/)
  assert.match(dialer, /\.dialer-active-calls--busy \{[\s\S]*color: var\(--danger\);/)
  assert.match(selector, /:aria-disabled="option\.disabled"/)
  assert.match(selector, /if \(option\.disabled\) return/)
  assert.match(selector, /statusValueSet\.value\.has\(value\)/)
  assert.match(selector, /:status="displayStatus"/)
  assert.match(lineIdentity, /<em v-if="status">\{\{ status \}\}<\/em>/)
  assert.match(lineIdentity, /\.line-identity__copy small > em \{[\s\S]*color: var\(--danger\);/)
})


test('successful dial expands the call even when the same call was already visible as occupied', async () => {
  const originalBootstrap = bootstrapResource.data
  const originalStatus = bootstrapResource.status
  const originalStartCall = gateway.startCall
  const originalRenew = gateway.renewCallLease
  const outgoing = call({ id: 'call-owned-transition', line_id: 'line-voice', direction: 'outgoing', phase: 'dialing', control_state: 'owned' })
  let starts = 0
  bootstrapResource.data = { capabilities: { agent_connected: true, dial: true, webrtc_audio: true }, lines: [{ id: 'line-voice', line_label: '', device_name: 'fixture stick', phone_number: '', capabilities: { dial: true, media: true } }], line_catalog: [] }
  bootstrapResource.status = 'ready'
  gateway.renewCallLease = async () => outgoing
  gateway.startCall = async () => { starts++; return outgoing }
  callState.session = { ...outgoing, control_state: 'occupied' }
  callState.sessions = []
  openDialer('10000', '', 'line-voice')
  assert.equal(uiState.callMinimized, true)
  try {
    assert.equal(await dial('10000', 'line-voice'), true, callState.error)
    assert.equal(starts, 1)
    assert.equal(callState.session?.control_state, 'owned')
    assert.equal(uiState.callMinimized, false)
    assert.equal(uiState.dialerOpen, false)
  } finally {
    shutdownCallRuntime()
    resetUIState()
    gateway.startCall = originalStartCall
    gateway.renewCallLease = originalRenew
    bootstrapResource.data = originalBootstrap
    bootstrapResource.status = originalStatus
  }
})


test('pending dial shows the existing call surface before the request resolves, then returns to the draft on failure', async () => {
  const originalBootstrap = bootstrapResource.data
  const originalStartCall = gateway.startCall
  const originalSnapshot = gateway.getActiveCallSnapshot
  let rejectStart
  let starts = 0
  const voiceLine = { id: 'line-voice-pending', line_label: '', device_name: 'fixture stick', phone_number: '', capabilities: { dial: true, media: true } }
  shutdownCallRuntime()
  bootstrapResource.data = { capabilities: { agent_connected: true, dial: true, webrtc_audio: true }, lines: [voiceLine], line_catalog: [] }
  gateway.getActiveCallSnapshot = async () => ({ calls: [], reservations: [] })
  gateway.startCall = () => { starts++; return new Promise((_, reject) => { rejectStart = reject }) }
  openDialer('10000', '', voiceLine.id)
  const operation = dial('10000', voiceLine.id)
  try {
    assert.equal(starts, 1)
    assert.deepEqual(callState.pendingDial, { number: '10000', lineID: voiceLine.id })
    assert.equal(callState.session, null, 'the pending request invents no call')
    assert.equal(callState.owned, false)
    assert.equal(uiState.callMinimized, false)
    const Surface = await loadComponent('components/CallSurface.vue')
    const html = await render(Surface, {}, [i18n])
    const text = textOf(html)
    assert.ok(text.includes('10000'))
    assert.ok(text.includes(i18n.global.t('calls.dialing')))
    assert.ok(text.includes('fixture stick'))
    assert.match(html, /aria-busy="true"/)
    assert.ok(!text.includes(i18n.global.t('calls.inCall')))
    rejectStart(new Error('fixture call refused'))
    assert.equal(await operation, false)
    assert.equal(callState.pendingDial, null)
    assert.equal(uiState.dialerOpen, true)
    assert.equal(uiState.dialTarget, '10000', 'failed dialing retains the draft')
    assert.equal(callState.error, 'fixture call refused')
    assert.equal(starts, 1, 'no automatic redial')
  } finally {
    rejectStart?.(new Error('test cleanup'))
    await operation
    shutdownCallRuntime()
    resetUIState()
    gateway.startCall = originalStartCall
    gateway.getActiveCallSnapshot = originalSnapshot
    bootstrapResource.data = originalBootstrap
  }
})


for (const lineID of ['line-host', 'nsh:NSH-410-TEST01']) {
  test(`remote hangup during a pending dial is reconciled after the receipt: ${lineID}`, async () => {
    const originalBootstrap = bootstrapResource.data
    const originalStart = gateway.startCall
    const originalSnapshot = gateway.getActiveCallSnapshot
    const originalRenew = gateway.renewCallLease
    const outgoing = call({ id: 'call-terminal-race', line_id: lineID, direction: 'outgoing', phase: 'ringing', control_state: 'owned' })
    let releaseStart
    let reads = 0
    let starts = 0
    let snapshot = { calls: [], reservations: [] }
    shutdownCallRuntime()
    bootstrapResource.data = { capabilities: { dial: true, webrtc_audio: true }, lines: [{ id: lineID, capabilities: { dial: true, media: true } }], line_catalog: [] }
    gateway.getActiveCallSnapshot = async () => { reads++; return snapshot }
    gateway.renewCallLease = async () => ({ call_id: outgoing.id })
    gateway.startCall = () => { starts++; return new Promise(resolve => { releaseStart = resolve }) }
    try {
      initializeCallRuntime()
      await requestActiveCallRefresh()
      const baselineReads = reads
      const operation = dial('10000', lineID)
      assert.equal(callState.busy, true)
      // The server emitted the terminal state BEFORE its dial reply arrived.
      acceptRuntimeActiveCalls({ calls: [], reservations: [] })
      releaseStart(outgoing)
      assert.equal(await operation, true)
      assert.equal(callState.session, null, 'a stale dial receipt resurrected the ended call')
      assert.equal(callState.owned, false)
      assert.ok(reads > baselineReads, 'the deferred runtime event was forgotten')
      assert.equal(starts, 1)
    } finally {
      shutdownCallRuntime()
      resetUIState()
      gateway.startCall = originalStart
      gateway.getActiveCallSnapshot = originalSnapshot
      gateway.renewCallLease = originalRenew
      bootstrapResource.data = originalBootstrap
    }
  })
}


for (const lineID of ['line-host', 'nsh:NSH-410-TEST01']) {
  test(`call heartbeat clears a remote hangup even when no terminal SSE arrives: ${lineID}`, async () => {
    const originalSnapshot = gateway.getActiveCallSnapshot
    const originalRenew = gateway.renewCallLease
    let reads = 0
    let snapshot = { calls: [call({ id: 'call-lost-event', line_id: lineID, phase: 'active', direction: 'outgoing', control_state: 'owned' })], reservations: [] }
    shutdownCallRuntime()
    gateway.getActiveCallSnapshot = async () => { reads++; return snapshot }
    gateway.renewCallLease = async () => ({ call_id: 'call-lost-event' })
    try {
      initializeCallRuntime()
      await requestActiveCallRefresh()
      assert.equal(callState.session?.phase, 'active')
      snapshot = { calls: [], reservations: [] }
      const before = reads
      await reconcileCallHeartbeat()
      assert.ok(reads > before)
      assert.equal(callState.session, null)
      assert.equal(callState.owned, false)
      const after = reads
      await reconcileCallHeartbeat()
      assert.equal(reads, after, 'idle heartbeats must not poll calls')
    } finally {
      shutdownCallRuntime()
      resetUIState()
      gateway.getActiveCallSnapshot = originalSnapshot
      gateway.renewCallLease = originalRenew
    }
  })
}
