import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import test from 'node:test'

import { parseBootstrap } from '../src/api/normalize.ts'
import { i18n } from '../src/i18n/index.ts'
import {
  lineIsOffline,
  lineSelectionState,
  offlineLineRecord,
  resolveLineChoice,
  selectLine,
  selectedLineRecord,
  selectedLiveLine
} from '../src/state/lineSelection.ts'
import { bootstrapResource } from '../src/state/workspace.ts'
import { loadComponent, render, textOf } from './support/render-sfc.mjs'

// One stable line for the top bar, the dialer, the message composer and --
// through those two -- calls, recordings and contacts.
//
// The owner's rule: 顶栏、拨号、短信、录音、联系人同一稳定线路；号码不在SIM/
// 基带中就unknown，支持经过验证/显式用户配置，不猜IMEI/ICCID变成号码。
//
// OFFLINE: the bootstrap documents are written by hand with obviously fake
// identifiers and driven through the real state modules; components are
// rendered on the server renderer. Nothing here was observed on a device or
// in a browser.

const read = path => readFile(new URL(path, import.meta.url), 'utf8')
const [strip, dialer, messages, selector, selection, calls, recordings, contacts] =
  await Promise.all([
    read('../src/components/LineStatusStrip.vue'),
    read('../src/components/DialerPanel.vue'),
    read('../src/views/MessagesView.vue'),
    read('../src/components/LineSelector.vue'),
    read('../src/state/lineSelection.ts'),
    read('../src/views/CallsView.vue'),
    read('../src/views/RecordingsView.vue'),
    read('../src/views/ContactsView.vue')
  ])

/** Source with comments stripped, for the "must not appear" assertions. */
function code(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

// Fake on purpose, and each ends in four digits no other field has, so a
// number worked out from any of them would be recognisable on screen.
const ICCID_TAIL = '4242'
const IMSI_TAIL = '4343'
const IMEI_TAIL = '4444'

function line(id, overrides = {}) {
  return {
    id,
    iccid: `8999000000000000${ICCID_TAIL}`,
    imsi: `99999000000${IMSI_TAIL}`,
    phone_number: '',
    operator: '',
    home_operator_code: '',
    home_operator_name: '',
    serving_operator_code: '',
    serving_operator_name: '',
    registration_state_known: true,
    registration_state_code: 1,
    registration_state: 'home',
    roaming: false,
    emergency_only: false,
    device_imei: `00000000000${IMEI_TAIL}`,
    device_name: 'fixture module',
    line_label: '',
    line_color: '',
    state: 'registered',
    radio_desired_enabled: true,
    radio_desired_enabled_known: true,
    capabilities: { dial: true, media: true, send_message: true },
    ...overrides
  }
}

const host = () => line('line-host', { device_name: 'fixture host modem' })
const node = () =>
  line('nsh:NSH-410-TEST01', { device_imei: 'NSH-410-TEST01', device_name: 'fixture stick' })

/** Replaces the bootstrap document, as a refresh or a runtime event would. */
function serve({ lines, catalog = lines, defaultLineID = '' }) {
  bootstrapResource.data = parseBootstrap({
    capabilities: {
      agent_connected: true,
      dial: true,
      message: true,
      webrtc_audio: true,
      device_control: true,
      volte_control: false,
      vowifi_control: false
    },
    lines,
    line_catalog: catalog,
    line_settings: { default_line_id: defaultLineID, revision: 1 },
    system_settings: { language: 'auto', revision: 1 }
  })
}

/** A fresh session: signed out, so nothing of the previous test is known. */
function fresh() {
  bootstrapResource.data = null
  assert.equal(lineSelectionState.selectedLineID, '')
  assert.equal(lineSelectionState.explicit, false)
}

test('the top bar, the dialer and the composer read one selection, and nothing keeps its own', () => {
  for (const [name, source] of [['LineStatusStrip', strip], ['DialerPanel', dialer], ['MessagesView', messages]]) {
    assert.match(source, /from '\.\.\/state\/lineSelection'/, `${name} does not read the shared selection`)
  }
  // The top bar shows THE selection and writes to it.
  assert.match(strip, /get: \(\) => lineSelectionState\.selectedLineID, set: value => \{ selectLine\(value\) \}/)
  assert.match(strip, /const line = computed\(\(\) => selectedLineRecord\(\)\)/)
  // It used to hold its own ref and fall back to "the first registered line".
  assert.doesNotMatch(code(strip), /const selectedLineID = ref\(/)
  assert.doesNotMatch(code(strip), /lines\.value\.find\(item => isRegisteredNetwork\(item\)\)/)
  assert.doesNotMatch(code(strip), /lines\.value\[0\]/)
  // Both drafts start from it, re-resolve when it changes, and choosing a
  // line in either one chooses it everywhere.
  assert.match(dialer, /resolveLineChoice\('dial', \{/)
  assert.match(messages, /resolveLineChoice\('message', \{/)
  for (const source of [dialer, messages]) {
    assert.match(source, /\(\) => lineSelectionState\.selectedLineID\s*\],\s*\(\) => sync(?:Resolved|Compose)Line\(\)/)
  }
  assert.match(dialer, /lineSelectionOverridden\.value = true[\s\S]{0,200}selectLine\(lineID\)/)
  assert.match(messages, /function changeSendingLine\(\): void \{[\s\S]{0,320}selectLine\(selectedLineKey\.value\)/)
  // The one place the selection is written from outside a user's choice is
  // its own module.
  assert.doesNotMatch(code(`${strip}${dialer}${messages}`), /lineSelectionState\.selectedLineID\s*=[^=]/)
})

test('calls, recordings and contacts start a call or a message through those two, with the record\'s own line', () => {
  // None of them resolves a line of its own: each hands the dialer (or the
  // composer's route) the line of the record it was opened from, and the
  // resolver takes it from there.
  for (const [name, source] of [['CallsView', calls], ['RecordingsView', recordings], ['ContactsView', contacts]]) {
    assert.doesNotMatch(code(source), /resolveLine(?:Choice)?\(/, `${name} resolves a line by itself`)
    assert.doesNotMatch(code(source), /lines\.value\[0\]/, `${name} takes "the first line"`)
  }
  assert.match(calls, /openDialerAndCall\(call\.remote_number, displayName\(call\), actionLineKey\(call\)\)/)
  assert.match(contacts, /openDialer\(number, contact\.display_name, contact\.preferred_line_id \|\| ''\)/)
})

test('until the user chooses, the selection follows the configured default; a choice is then kept', () => {
  fresh()
  serve({ lines: [host(), node()], defaultLineID: 'nsh:NSH-410-TEST01' })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
  assert.equal(lineSelectionState.explicit, false)

  // An administrator changes the default: an untouched selection follows.
  serve({ lines: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(lineSelectionState.selectedLineID, 'line-host')

  selectLine('nsh:NSH-410-TEST01')
  serve({ lines: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01', 'a refresh moved the user\'s choice back to the default')
  // There is no "none" choice.
  selectLine('  ')
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
})

test('with no default configured the selection is taken once and not re-picked', () => {
  fresh()
  serve({ lines: [line('line-idle', { registration_state: 'searching', state: 'searching' }), node()] })
  // The first REGISTERED line, as the top bar always started.
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
  // Another line registers first in the list: the selection does not jump.
  serve({ lines: [host(), node()] })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
})

test('the selected line\'s device goes offline: the selection stays, marked offline, and is never another line', () => {
  fresh()
  serve({ lines: [host(), node()], defaultLineID: 'line-host' })
  selectLine('nsh:NSH-410-TEST01')

  // The stick is unplugged. The host modem is still up.
  serve({ lines: [host()], catalog: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
  assert.equal(selectedLiveLine(), undefined)
  assert.equal(lineIsOffline('nsh:NSH-410-TEST01'), true)
  assert.equal(selectedLineRecord()?.id, 'nsh:NSH-410-TEST01', 'the top bar would draw another line')

  for (const capability of ['dial', 'message']) {
    const choice = resolveLineChoice(capability)
    assert.deepEqual(
      { id: choice.id, offline: choice.offline, line: choice.line },
      { id: 'nsh:NSH-410-TEST01', offline: true, line: undefined },
      `${capability}: a Node's line fell back to the modem on the NAS`
    )
  }

  // It comes back: the same selection, usable again, with no step in between.
  serve({ lines: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(resolveLineChoice('dial').line?.id, 'nsh:NSH-410-TEST01')
  assert.equal(lineIsOffline('nsh:NSH-410-TEST01'), false)
})

test('a line the gateway\'s catalog never listed is still kept when it goes offline', () => {
  // The catalog is the gateway's persisted lines; a Node's line with no
  // history may not be in it. Seen up once in this session is enough.
  fresh()
  serve({ lines: [host(), node()], catalog: [host()] })
  selectLine('nsh:NSH-410-TEST01')
  serve({ lines: [host()], catalog: [host()] })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
  assert.equal(offlineLineRecord('nsh:NSH-410-TEST01')?.device_name, 'fixture stick')
  assert.deepEqual(resolveLineChoice('message'), { id: 'nsh:NSH-410-TEST01', offline: true })

  // Also for a selection the user never made explicitly (the default).
  fresh()
  serve({ lines: [host(), node()], catalog: [host()], defaultLineID: 'nsh:NSH-410-TEST01' })
  serve({ lines: [host()], catalog: [host()], defaultLineID: 'nsh:NSH-410-TEST01' })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
  assert.equal(resolveLineChoice('dial').offline, true)

  // Signing out forgets it: the next session knows nothing of this one's lines.
  fresh()
  serve({ lines: [host()], catalog: [host()] })
  assert.equal(lineIsOffline('nsh:NSH-410-TEST01'), false)
  assert.equal(lineSelectionState.selectedLineID, 'line-host')
})

test('a draft opened from a record of an offline line stays on that line', () => {
  // Calling back from a call log entry, a recording or a contact whose line
  // is a Node that is unplugged: the draft is that line, disabled -- not the
  // selected line and not the default.
  fresh()
  serve({ lines: [host()], catalog: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(lineSelectionState.selectedLineID, 'line-host')
  assert.deepEqual(resolveLineChoice('dial', { contextKey: 'nsh:NSH-410-TEST01' }), {
    id: 'nsh:NSH-410-TEST01',
    offline: true
  })
  assert.deepEqual(resolveLineChoice('message', { preferredLineID: ' nsh:NSH-410-TEST01 ' }), {
    id: 'nsh:NSH-410-TEST01',
    offline: true
  })
  // An ID that names no line at all is passed over (nothing could be shown
  // for it), and a live line is used as it always was.
  assert.equal(resolveLineChoice('dial', { contextKey: 'line-never-existed' }).id, 'line-host')
  assert.equal(resolveLineChoice('dial', { contextKey: 'line-host' }).line?.id, 'line-host')
})

test('a named live line keeps the draft even when it cannot do the job', () => {
  fresh()
  const dataOnly = line('line-data', { capabilities: { dial: false, media: false, send_message: false } })
  serve({ lines: [host(), dataOnly], defaultLineID: 'line-host' })
  assert.equal(resolveLineChoice('message', { contextKey: 'line-data' }).id, 'line-data')
  // The selected line is the answer as it is; the caller says what it lacks.
  selectLine('line-data')
  assert.equal(resolveLineChoice('message').id, 'line-data')
  assert.equal(resolveLineChoice('message').offline, false)
})

test('the dialer and the composer disable the action with the reason and place nothing on another line', () => {
  // Neither an offline, busy nor incapable SIM may fall back to another SIM.
  const sync = dialer.slice(dialer.indexOf('function syncResolvedLine'), dialer.indexOf('function beginDraft'))
  assert.match(sync, /selectedLineId\.value = choice\.id/)
  assert.doesNotMatch(code(sync), /availableDialLines\.value\[0\]|dialLines\.value\[0\]/)
  // A draft on an offline line is not re-resolved away from it.
  assert.match(sync, /\(selectedStillExists \|\| Boolean\(selectedOfflineLine\.value\)\) &&\s*\(lineSelectionOverridden\.value \|\| Boolean\(number\.value\.trim\(\)\)\)/)
  assert.match(sync, /if \(!selectedStillExists && !selectedOfflineLine\.value\) lineSelectionOverridden\.value = false/)
  assert.match(dialer, /const selectedOfflineLine = computed\(\(\) => offlineLineRecord\(selectedLineId\.value\)\)/)
  // The reason is part of what disables the call button and guards placeCall.
  const reason = dialer.slice(dialer.indexOf('const callUnavailableReason'), dialer.indexOf('const disabledReason'))
  assert.match(reason, /selectedOfflineLine\.value \? t\('lines\.deviceOfflineNoSwitch'\) : ''/)
  assert.ok(
    reason.indexOf('selectedOfflineLine') < reason.indexOf("t('dialer.noLines')"),
    'an offline line is reported as "no lines"'
  )
  assert.match(dialer, /async function placeCall\(\)[\s\S]{0,200}callUnavailableReason\.value \|\|/)
  assert.match(dialer, /:offline-line="selectedOfflineLine"/)

  // Composer: a new message on an offline line, and a reply in a conversation
  // whose line is offline, both say why and send nothing.
  assert.match(messages, /offlineLineRecord\(composingNew\.value \? selectedLineKey\.value : selectedThread\.value\?\.line_id \|\| ''\)/)
  const disabled = messages.slice(messages.indexOf('const sendDisabledReason'), messages.indexOf('const sendDisabledReason') + 700)
  assert.match(disabled, /if \(offlineSendingLine\.value\) return t\('lines\.deviceOfflineNoSwitch'\)/)
  assert.ok(
    disabled.indexOf('offlineSendingLine') < disabled.indexOf("t('messages.selectLine')"),
    'an offline line is reported as "select a line"'
  )
  assert.match(messages, /async function submit\(\): Promise<void> \{\s*if \(sendDisabledReason\.value \|\| sending\.value\) return/)
  assert.match(messages, /if \(!selectedStillExists && !offlineLineRecord\(selectedLineKey\.value\)\) lineSelectionOverridden = false/)

  // Top bar: says offline, draws no live reading and asks the device nothing.
  assert.match(strip, /<span v-if="offlineLine" class="line-status__state" :title="t\('lines\.deviceOfflineNoSwitch'\)">\{\{ t\('lines\.deviceOffline'\) \}\}<\/span>/)
  assert.match(strip, /const registered = computed\(\(\) => Boolean\(line\.value && !offlineLine\.value && isRegisteredNetwork\(line\.value\)\)\)/)
  assert.match(strip, /if \(!value\?\.id \|\| offlineLine\.value\) \{\s*dataOn\.value = null\s*return/)
})

test('the reason exists in every locale and blames no licence', async () => {
  const directory = new URL('../src/i18n/locales/', import.meta.url)
  const files = (await readdir(directory)).filter(name => name.endsWith('.ts'))
  assert.equal(files.length, 9)
  for (const name of files) {
    const source = await readFile(new URL(name, directory), 'utf8')
    assert.match(source, /"?deviceOffline"?:\s*['"][^'"]+['"]/, `${name} has no lines.deviceOffline`)
    assert.match(source, /"?deviceOfflineNoSwitch"?:\s*['"][^'"]+['"]/, `${name} has no lines.deviceOfflineNoSwitch`)
    assert.doesNotMatch(source, /购买通话许可|購買通話許可/)
  }
})

const Selector = await loadComponent('components/LineSelector.vue')

test('rendered: a line with no number says "unknown number", never digits from its ICCID, IMSI or IMEI', async () => {
  const unknown = i18n.global.t('calls.unknownNumber')
  const live = line('line-host', { device_name: 'fixture host modem' })
  const html = await render(Selector, { modelValue: 'line-host', lines: [live] }, [i18n])
  const text = textOf(html)
  assert.ok(text.includes(unknown), `"${unknown}" is not shown: ${text}`)
  for (const tail of [ICCID_TAIL, IMSI_TAIL, IMEI_TAIL]) {
    assert.ok(!html.includes(tail), `an identifier ending ${tail} reached the selector`)
  }
  // A number the SIM or the gateway's settings do report is shown as given.
  const numbered = await render(
    Selector,
    { modelValue: 'line-host', lines: [{ ...live, phone_number: '+10000000000' }] },
    [i18n]
  )
  assert.ok(textOf(numbered).includes('+10000000000'))
  assert.ok(!textOf(numbered).includes(unknown))
})

test('rendered: the selected offline line is named in the control as offline, not replaced by a placeholder', async () => {
  const gone = node()
  const html = await render(
    Selector,
    { modelValue: gone.id, lines: [host()], offlineLine: gone },
    [i18n]
  )
  const text = textOf(html)
  assert.ok(text.includes('fixture stick'), `the offline line is not named: ${text}`)
  assert.ok(text.includes(i18n.global.t('lines.deviceOffline')))
  assert.ok(!text.includes(i18n.global.t('lines.selectLine')))
  // The control does not show the line that is up in its place.
  assert.ok(!text.includes('fixture host modem'))
  // In the list it is present and cannot be chosen.
  assert.match(selector, /details: t\('lines\.deviceOffline'\),[\s\S]{0,80}disabled: true/)
})

test('no screen works a phone number out of an ICCID, IMSI or IMEI', async () => {
  // A source scan over every component, view, state module and utility: a
  // phone number is only ever read from `phone_number` (the SIM's own number,
  // or the one set explicitly in the gateway's settings).
  const roots = ['../src/components/', '../src/views/', '../src/state/', '../src/utils/', '../src/composables/']
  async function walk(url) {
    const entries = await readdir(url, { withFileTypes: true }).catch(() => [])
    const nested = await Promise.all(
      entries.map(entry => {
        const child = new URL(`${entry.name}${entry.isDirectory() ? '/' : ''}`, url)
        if (entry.isDirectory()) return walk(child)
        return /\.(ts|vue)$/.test(entry.name) ? [child] : []
      })
    )
    return nested.flat()
  }
  const files = (await Promise.all(roots.map(root => walk(new URL(root, import.meta.url))))).flat()
  assert.ok(files.length > 100)
  const identifier = String.raw`(?:iccid|imsi|imei|device_imei)\b`
  const patterns = [
    // phone_number falling back to an identifier, either way round
    new RegExp(String.raw`phone_number[^\n;]{0,40}(?:\|\||\?\?)[^\n;]{0,40}\.${identifier}`, 'i'),
    // a phone_number field assigned from an identifier
    new RegExp(String.raw`phone_number\s*:\s*[^\n,]{0,60}\.${identifier}`, 'i'),
    // an identifier passed to a number formatter
    new RegExp(String.raw`(?:formatPhoneNumber|displayPhoneNumber|ownNumberFacts|parsePhoneNumberFromString)\([^)\n]{0,60}\.${identifier}`, 'i')
  ]
  for (const file of files) {
    const source = code(await readFile(file, 'utf8'))
    for (const pattern of patterns) {
      assert.doesNotMatch(source, pattern, `${file.pathname} derives a number from an identifier`)
    }
  }
  // The top bar and the selector fall back to the words, not to an identifier.
  assert.match(strip, /\{\{ facts\.formatted \|\| t\('calls\.unknownNumber'\) \}\}/)
  assert.match(strip, /ownNumberFacts\(line\.value\?\.phone_number \|\| '', line\.value\?\.operator\)/)
  assert.match(selector, /line\.phone_number\.trim\(\) \|\| t\('calls\.unknownNumber'\)/)
  // The selection itself is a line ID and holds no number or identifier.
  assert.doesNotMatch(code(selection), /iccid|imsi|imei/i)
})


test('fresh page skips a historical offline default when an online line exists', () => {
  fresh()
  serve({ lines: [node()], catalog: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(lineSelectionState.selectedLineID, 'nsh:NSH-410-TEST01')
  assert.equal(resolveLineChoice('dial').offline, false)
  assert.equal(resolveLineChoice('message').id, 'nsh:NSH-410-TEST01')
  serve({ lines: [host()], catalog: [host(), node()], defaultLineID: '' })
  assert.equal(resolveLineChoice('dial').id, 'nsh:NSH-410-TEST01')
  assert.equal(resolveLineChoice('dial').offline, true)
})

test('an already selected default going offline stays selected until the user changes it', () => {
  fresh()
  serve({ lines: [host(), node()], defaultLineID: 'line-host' })
  serve({ lines: [node()], catalog: [host(), node()], defaultLineID: 'line-host' })
  assert.equal(lineSelectionState.selectedLineID, 'line-host')
  assert.equal(resolveLineChoice('dial').offline, true)
  selectLine('nsh:NSH-410-TEST01')
  assert.equal(resolveLineChoice('dial').line?.id, 'nsh:NSH-410-TEST01')
})
