import assert from 'node:assert/strict'
import test from 'node:test'
import zhCN from '../src/i18n/locales/zh-CN.ts'
import { gateway } from '../src/api/client.ts'
import { dial, callState, shutdownCallRuntime } from '../src/state/call.ts'
import { bootstrapResource } from '../src/state/workspace.ts'
import { loadRedialNumber, rememberDialedNumber, redialNumbers, resetRedial } from '../src/state/redial.ts'
import { resetUIState } from '../src/state/ui.ts'
import { selectLine } from '../src/state/lineSelection.ts'
import { redialLabel } from '../src/i18n/redialCopy.ts'
import { i18n } from '../src/i18n/index.ts'
import { loadComponent, render } from './support/render-sfc.mjs'

const boot = value => ({ ...value, line_settings: { revision: 1, default_line_id: '', ...value.line_settings }, system_settings: { revision: 1, language: 'auto' }, capabilities: { message: true, device_control: false, volte_control: false, vowifi_control: false, ...value.capabilities }, lines: value.lines.map(line => ({ line_label: '', device_imei: 'fixture-stick', device_name: 'Fixture', phone_number: '', operator: '', state: 'registered', ...line })) })
const emptyPage = () => ({ items: [], meta: { has_more: false, next_cursor: '', limit: 100 } })
const record = (line_id, remote_number, direction = 'outgoing') => ({ line_id, remote_number, direction })

test('redial recovers the selected SIM from paginated outgoing history without placing calls', async () => {
  resetRedial()
  const original = gateway.listCalls
  const originalStart = gateway.startCall
  const queries = []
  gateway.startCall = async () => { throw Error('History lookup must not dial') }
  gateway.listCalls = async (filter, query) => {
    queries.push([filter, query.cursor])
    if (!query.cursor) return { items: [record('host', '20000'), record('stick', '30000', 'incoming')], meta: { has_more: true, next_cursor: 'page2', limit: 100 } }
    return { ...emptyPage(), items: [record('stick', '10000'), record('stick', '10001')] }
  }
  try {
    await loadRedialNumber('stick')
    assert.equal(redialNumbers.stick, '10000')
    assert.equal(redialNumbers.host, undefined)
    assert.deepEqual(queries, [['outgoing', undefined], ['outgoing', 'page2']])
  } finally { gateway.listCalls = original; gateway.startCall = originalStart; resetRedial() }
})

test('a late history result cannot overwrite a newer accepted dial or survive logout', async () => {
  const original = gateway.listCalls
  let resolve
  gateway.listCalls = () => new Promise(done => { resolve = done })
  try {
    resetRedial()
    const pending = loadRedialNumber('stick')
    assert.equal(loadRedialNumber('stick'), pending)
    rememberDialedNumber('stick', '10001')
    resolve({ ...emptyPage(), items: [record('stick', '10000')] })
    await pending
    assert.equal(redialNumbers.stick, '10001')
    resetRedial()
    const stale = loadRedialNumber('stick')
    resetRedial()
    resolve({ ...emptyPage(), items: [record('stick', '10000')] })
    await stale
    assert.deepEqual(Object.keys(redialNumbers), [])
  } finally { gateway.listCalls = original; resetRedial() }
})

test('history errors, empty pages and repeated cursors never invent a redial number', async () => {
  const original = gateway.listCalls
  try {
    resetRedial()
    gateway.listCalls = async () => { throw Error('Unauthorized') }
    await loadRedialNumber('stick')
    assert.equal(redialNumbers.stick, undefined)
    let reads = 0
    gateway.listCalls = async () => { reads++; return { ...emptyPage(), meta: { has_more: true, next_cursor: 'same', limit: 100 } } }
    await loadRedialNumber('stick')
    assert.equal(reads, 2)
    assert.equal(redialNumbers.stick, undefined)
    gateway.listCalls = async () => ({ ...emptyPage(), items: [record('stick', '<script>')] })
    await loadRedialNumber('stick')
    assert.equal(redialNumbers.stick, undefined)
  } finally { gateway.listCalls = original; resetRedial() }
})

test('one explicit empty-number dial places exactly one call on the selected line and opens the existing surface', async () => {
  const original = gateway.startCall
  const bootstrap = bootstrapResource.data
  const status = bootstrapResource.status
  shutdownCallRuntime(); resetUIState()
  bootstrapResource.status = 'ready'
  bootstrapResource.data = boot({ capabilities: { agent_connected: true, dial: true, webrtc_audio: true }, lines: [{ id: 'stick', capabilities: { dial: true, media: true } }], line_catalog: [] })
  const requests = []
  gateway.startCall = async (lineID, number) => {
    requests.push([lineID, number])
    return { id: 'redial-test', line_id: lineID, remote_number: number, direction: 'outgoing', phase: 'dialing', control_state: 'owned', media_available: false, created_at: new Date().toISOString() }
  }
  try {
    rememberDialedNumber('host', '20000'); rememberDialedNumber('stick', '10000')
    assert.equal(await dial('', 'stick'), true)
    assert.deepEqual(requests, [['stick', '10000']])
    assert.equal(callState.session?.remote_number, '10000')
    assert.equal(await dial('', 'stick'), false)
    assert.equal(requests.length, 1)
    shutdownCallRuntime()
    assert.deepEqual(Object.keys(redialNumbers), [])
    assert.equal(await dial('', 'stick'), false)
    assert.equal(requests.length, 1)
  } finally { shutdownCallRuntime(); gateway.startCall = original; bootstrapResource.data = bootstrap; bootstrapResource.status = status }
})

test('redial respects line reservations, and failed commands never retry automatically', async () => {
  const original = gateway.startCall
  const bootstrap = bootstrapResource.data
  const status = bootstrapResource.status
  shutdownCallRuntime()
  bootstrapResource.status = 'ready'
  bootstrapResource.data = boot({ capabilities: { agent_connected: true, dial: true, webrtc_audio: true }, lines: [{ id: 'stick', capabilities: { dial: true, media: true } }], line_catalog: [] })
  let starts = 0
  gateway.startCall = async () => { starts++; throw Error('Offline') }
  try {
    rememberDialedNumber('stick', '10000')
    callState.reservations = [{ line_id: 'stick', request_id: 'reserved', control_state: 'occupied', created_at: new Date().toISOString() }]
    assert.equal(await dial('', 'stick'), false)
    assert.equal(starts, 0)
    callState.reservations = []
    assert.equal(await dial('', 'stick'), false)
    await new Promise(resolve => setTimeout(resolve, 20))
    assert.equal(starts, 1)
    assert.equal(redialNumbers.stick, '10000')
  } finally { shutdownCallRuntime(); gateway.startCall = original; bootstrapResource.data = bootstrap; bootstrapResource.status = status }
})

test('the original dialer exposes redial only for its selected online SIM', async () => {
  const original = gateway.listCalls
  const bootstrap = bootstrapResource.data
  const status = bootstrapResource.status
  shutdownCallRuntime(); resetUIState()
  gateway.listCalls = async () => emptyPage()
  bootstrapResource.status = 'ready'
  bootstrapResource.data = boot({ capabilities: { agent_connected: true, dial: true, webrtc_audio: true }, lines: [{ id: 'stick', device_name: 'Fixture', capabilities: { dial: true, media: true } }], line_catalog: [], line_settings: { default_line_id: 'stick' } })
  selectLine('stick')
  i18n.global.setLocaleMessage('zh-CN', zhCN)
  i18n.global.locale.value = 'zh-CN'
  try {
    const component = await loadComponent('components/DialerPanel.vue')
    let html = await render(component, { permanent: true }, [i18n])
    let button = html.match(/<button[^>]*class="call-button"[^>]*>/)?.[0]
    assert.ok(button); assert.match(button, /disabled/)
    rememberDialedNumber('host', '20000')
    html = await render(component, { permanent: true }, [i18n])
    assert.match(html.match(/<button[^>]*class="call-button"[^>]*>/)[0], /disabled/)
    rememberDialedNumber('stick', '10000')
    html = await render(component, { permanent: true }, [i18n])
    button = html.match(/<button[^>]*class="call-button"[^>]*>/)[0]
    assert.doesNotMatch(button, /disabled/)
    assert.ok(button.includes(redialLabel('zh-CN') + ' 10000'))
    bootstrapResource.data.lines = []
    html = await render(component, { permanent: true }, [i18n])
    assert.match(html.match(/<button[^>]*class="call-button"[^>]*>/)[0], /disabled/)
  } finally { shutdownCallRuntime(); resetUIState(); gateway.listCalls = original; bootstrapResource.data = bootstrap; bootstrapResource.status = status }
})
