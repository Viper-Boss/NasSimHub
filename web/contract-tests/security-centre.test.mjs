import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  communicationStatsQuery,
  getCommunicationStats,
  getSecurityPosture,
  nodePosture,
  readBoundary
} from '../src/api/dashboardCards.ts'
import { moduleSecurityCardLabels } from '../src/i18n/moduleSecurityCardLabels.ts'
import { networkCardLabels } from '../src/i18n/networkCardNotes.ts'
import { securityCentreLabels } from '../src/i18n/securityCentreLabels.ts'
import { loadComponent, render, textOf } from './support/render-sfc.mjs'

const read = path => readFile(new URL(path, import.meta.url), 'utf8')
const [centre, viewer, boundaryList, settingsPanel, card, extra, api] = await Promise.all([
  read('../src/components/SecurityCentrePanel.vue'),
  read('../src/components/SecurityJournalViewer.vue'),
  read('../src/components/LinkBoundaryList.vue'),
  read('../src/components/SecuritySettingsPanel.vue'),
  read('../src/components/ModuleSecurityCard.vue'),
  read('../src/components/ModuleExtraCards.vue'),
  read('../src/api/nodeSecurity.ts')
])

function code(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    .replace(/<!--[\s\S]*?-->/g, ' ')
}

const chinese = securityCentreLabels('zh-CN')
const english = securityCentreLabels('en-US')
const BoundaryList = await loadComponent('components/LinkBoundaryList.vue')

function json(body, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function withFetch(handler, run) {
  const original = globalThis.fetch
  const calls = []
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), init })
    return handler(String(url), init)
  }
  try {
    return await run(calls)
  } finally {
    globalThis.fetch = original
  }
}

// The administrator's security centre: device trust, the evidence behind it,
// the journal and the link boundaries. It is the page an operator would point
// at to say the box is under control, so what it must never do is draw
// something as protected, verified or supported without the gateway saying so.

// ---------------------------------------------------------------------------
// Link boundaries
// ---------------------------------------------------------------------------

test('a boundary\'s evidence and post-quantum words default to unknown', () => {
  const boundary = readBoundary({ id: 'gateway_node', evidence: 'measured', post_quantum: 'true', meets_requested: 'yes' })
  assert.equal(boundary.evidence, 'unknown')
  assert.equal(boundary.post_quantum, 'unknown', 'an unrecognised word was drawn as a yes')
  assert.equal(boundary.meets_requested, undefined, 'a non-boolean comparison was coerced')
  assert.equal(readBoundary({ evidence: 'observed' }), null, 'a boundary with no id was kept')
  assert.equal(readBoundary(null), null)
  const full = readBoundary({
    id: 'browser_gateway', evidence: 'observed', protection: 'TLS 1.3', post_quantum: 'yes',
    negotiated: 'X25519MLKEM768', meets_requested: false
  })
  assert.equal(full.post_quantum, 'yes')
  assert.equal(full.meets_requested, false)
  assert.equal(full.requested, undefined)
})

test('the posture reader carries the boundaries and whose handshake the rows describe', async () => {
  await withFetch(
    () =>
      json({
        transport: { state: 'ok', value: 'TLS 1.3' },
        transport_scope: 'proxy_hop',
        proxy: { state: 'ok', value: 'Tailscale Serve' },
        boundaries: [{ id: 'tailscale', evidence: 'reported', post_quantum: 'no' }, { evidence: 'observed' }, 'junk']
      }),
    async () => {
      const posture = await getSecurityPosture()
      assert.equal(posture.transport_scope, 'proxy_hop')
      assert.equal(posture.proxy.value, 'Tailscale Serve')
      assert.equal(posture.boundaries.length, 1)
    }
  )
  // An older gateway: no list at all, which is different from an empty one.
  await withFetch(
    () => json({ transport: { state: 'ok', value: 'TLS 1.3' }, transport_scope: 'browser' }),
    async () => {
      const posture = await getSecurityPosture()
      assert.equal(posture.boundaries, undefined)
      assert.equal(posture.transport_scope, undefined, 'an unknown scope was kept')
    }
  )
})

test('every boundary is its own row with evidence, protection, post-quantum, requested and negotiated', async () => {
  const boundaries = [
    { id: 'browser_gateway', evidence: 'unknown', post_quantum: 'unknown', detail: 'the browser\'s TLS ends at Tailscale Serve' },
    { id: 'tailscale', evidence: 'reported', protection: 'WireGuard (Tailscale)', post_quantum: 'no' },
    { id: 'tailscale_gateway', evidence: 'observed', protection: 'TLS 1.3', negotiated: 'X25519MLKEM768', post_quantum: 'yes' },
    { id: 'call_media', evidence: 'protocol', protection: 'WebRTC DTLS-SRTP', post_quantum: 'no' },
    { id: 'gateway_node', evidence: 'observed', node_id: 'NSH-410-TEST01', protection: 'TLS 1.3', requested: 'PQ', negotiated: 'X25519', post_quantum: 'no', meets_requested: false },
    { id: 'node_identity', evidence: 'observed', node_id: 'NSH-410-TEST01', protection: 'Ed25519 pin', requested: 'PQ', negotiated: 'classical_only', post_quantum: 'no' },
    { id: 'kcp', evidence: 'not_used', post_quantum: 'unknown' },
    { id: 'gateway_update', evidence: 'protocol', protection: 'Ed25519 release manifest', post_quantum: 'no' },
    { id: 'node_update', evidence: 'protocol', post_quantum: 'unknown' },
    { id: 'something_new', evidence: 'observed', post_quantum: 'yes' }
  ].map(readBoundary)
  const html = await render(BoundaryList, {
    labels: chinese,
    loaded: true,
    posture: { transport_scope: 'proxy_hop', boundaries }
  })
  const rows = html.split('<li class="boundary-list__row"').slice(1)
  assert.equal(rows.length, boundaries.length, 'a boundary was dropped or merged')

  // Each known id has its own name; an unknown one is shown by its id.
  for (const id of ['browser_gateway', 'tailscale', 'tailscale_gateway', 'call_media', 'gateway_node', 'node_identity', 'kcp', 'gateway_update', 'node_update']) {
    assert.ok(chinese.boundaryNames[id], `no name for ${id}`)
    assert.ok(english.boundaryNames[id], `no English name for ${id}`)
    assert.ok(textOf(html).includes(chinese.boundaryNames[id]), `${id} is not rendered`)
  }
  assert.match(textOf(rows[9]), /something_new/)

  // Requested and negotiated are separate rows on EVERY boundary, present or not.
  for (const row of rows) {
    const text = textOf(row)
    for (const label of [chinese.protection, chinese.postQuantum, chinese.requested, chinese.negotiated]) {
      assert.ok(text.includes(label), `a row has no "${label}"`)
    }
  }
  const node = textOf(rows[4])
  assert.match(node, /要求 PQ/)
  assert.match(node, /实际协商 X25519/)
  assert.match(node, /NSH-410-TEST01/)
  assert.match(rows[4], /role="alert" class="boundary-list__warning"/, 'a shortfall is not flagged')
  assert.match(node, new RegExp(chinese.shortfall))
  assert.equal(html.match(/role="alert"/g).length, 1, 'a boundary that was not compared is flagged as short')

  // Evidence labels, one per kind.
  assert.match(textOf(rows[0]), new RegExp(chinese.boundaryEvidence.unknown))
  assert.match(textOf(rows[1]), new RegExp(chinese.boundaryEvidence.reported))
  assert.match(textOf(rows[2]), new RegExp(chinese.boundaryEvidence.observed))
  assert.match(textOf(rows[3]), new RegExp(chinese.boundaryEvidence.protocol))
  assert.match(textOf(rows[6]), new RegExp(chinese.boundaryEvidence.not_used))

  // Post-quantum: green only for a yes the gateway observed; "no" and
  // "unknown" are plain, and an unstated value says it is not stated.
  assert.match(rows[2], /<dd class="is-ok"[^>]*>是<\/dd>/)
  assert.equal(html.match(/class="is-ok"/g).length, 2)
  assert.match(textOf(rows[0]), /后量子 未知/)
  assert.match(textOf(rows[1]), /后量子 否/)
  assert.match(textOf(rows[0]), new RegExp(`${chinese.protection} ${chinese.notStated}`))
  assert.match(textOf(rows[0]), /the browser's TLS ends at Tailscale Serve/)

  // Through Tailscale Serve the list says whose hop was observed.
  assert.ok(textOf(html).includes(chinese.proxyHopNote))
  assert.match(chinese.proxyHopNote, /Tailscale 到网关/)
  const direct = await render(BoundaryList, { labels: chinese, loaded: true, posture: { boundaries } })
  assert.ok(!textOf(direct).includes(chinese.proxyHopNote))
})

test('no boundaries, no answer and not-yet-loaded are three different sentences', async () => {
  const state = props => render(BoundaryList, { labels: chinese, ...props })
  const loading = await state({ loaded: false, posture: null })
  const unanswered = await state({ loaded: true, posture: null })
  const older = await state({ loaded: true, posture: { transport: { state: 'ok' } } })
  assert.ok(textOf(loading).includes(chinese.loading))
  assert.ok(textOf(unanswered).includes(chinese.boundariesPending))
  assert.ok(textOf(older).includes(chinese.boundariesEmpty))
  for (const html of [loading, unanswered, older]) {
    assert.doesNotMatch(html, /boundary-list__row"|is-ok|role="alert"/, 'a row is drawn with no boundary behind it')
  }
  // The list holds nothing of its own: it cannot outlive the answer it was given.
  assert.doesNotMatch(code(boundaryList), /\bref\(|getSecurityPosture|onMounted/)
})

test('a Node\'s secure-transport rows come from its own boundaries, never from constants', () => {
  const posture = {
    transport: { state: 'ok', value: 'TLS 1.3' },
    post_quantum: { state: 'ok', value: 'ML-KEM' },
    identity: { state: 'ok', value: 'ECDSA P-256' },
    pinning: { state: 'off' },
    boundaries: [
      { id: 'browser_gateway', evidence: 'observed', protection: 'TLS 1.3', post_quantum: 'yes' },
      { id: 'gateway_node', node_id: 'NSH-410-TEST01', evidence: 'observed', protection: 'TLS 1.3', requested: 'PQ', negotiated: 'X25519', post_quantum: 'no', meets_requested: false },
      { id: 'node_identity', node_id: 'NSH-410-TEST01', evidence: 'observed', protection: 'Ed25519 pin', negotiated: 'refused', post_quantum: 'no', detail: 'key changed' },
      { id: 'gateway_node', node_id: 'NSH-410-TEST02', evidence: 'observed', protection: 'TLS 1.3', negotiated: 'X25519MLKEM768', post_quantum: 'yes', meets_requested: true },
      { id: 'node_identity', node_id: 'NSH-410-TEST02', evidence: 'observed', protection: 'Ed25519 pin + ML-DSA-65 pin', negotiated: 'dual', post_quantum: 'yes' },
      { id: 'gateway_node', node_id: 'NSH-410-TEST03', evidence: 'unknown', protection: '', post_quantum: 'unknown' }
    ].map(readBoundary)
  }
  // The browser's own rows must not leak onto a device's card.
  const first = nodePosture(posture, 'NSH-410-TEST01')
  assert.deepEqual(first.transport, { state: 'ok', value: 'TLS 1.3' })
  assert.deepEqual(first.post_quantum, { state: 'warn', value: 'X25519', detail: 'PQ' })
  assert.equal(first.identity.state, 'warn')
  assert.equal(first.identity.detail, 'key changed')
  assert.deepEqual(first.pinning, { state: 'ok', value: 'Ed25519 pin' })
  for (const row of ['replay', 'packet_auth', 'ota']) {
    assert.deepEqual(first[row], { state: 'unknown' }, `${row} has no per-device evidence and must stay unknown`)
  }
  assert.equal(first.transport_scope, undefined)

  const second = nodePosture(posture, 'NSH-410-TEST02')
  assert.deepEqual(second.post_quantum, { state: 'ok', value: 'X25519MLKEM768' })
  assert.equal(second.identity.state, 'ok')
  assert.equal(second.identity.value, 'Ed25519 + PQ')

  // Not connected, or no boundary at all (a member, an older gateway): unknown.
  for (const id of ['NSH-410-TEST03', 'NSH-410-TEST99']) {
    const rows = nodePosture(posture, id)
    for (const row of ['transport', 'post_quantum', 'identity', 'pinning', 'replay', 'packet_auth', 'ota']) {
      assert.deepEqual(rows[row], { state: 'unknown' }, `${id}.${row} was given a value without evidence`)
    }
  }
  assert.equal(nodePosture(null, 'NSH-410-TEST01'), null)

  // The card uses it, and the constants it replaced are gone.
  assert.match(extra, /posture\.value = nodeID\.value \? nodePosture\(security, nodeID\.value\) : security/)
  const loader = extra.slice(extra.indexOf('async function loadCards'), extra.indexOf('const postureScope'))
  assert.doesNotMatch(code(loader), /state: 'ok'/, 'a Node card row is hardcoded to a pass again')
})

test('through a proxy, and on a Node card, the rows are captioned with whose connection they describe', () => {
  assert.match(extra, /posture\.value\.transport_scope === 'proxy_hop' \? t\('dashboard\.secScopeProxyHop'\)/)
  assert.match(extra, /if \(nodeID\.value\) return t\('dashboard\.secScopeNode'\)/)
  assert.match(extra, /<p v-if="postureScope"[^>]*>\{\{ postureScope \}\}<\/p>/)
  // The caption sits with the rows it qualifies, above the events panel.
  assert.ok(extra.indexOf('v-if="postureScope"') < extra.indexOf('class="insight-sec__events"'))
  const locales = ['zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'vi-VN', 'es-ES', 'de-DE', 'fr-FR', 'pt-BR']
  for (const locale of locales) {
    const labels = networkCardLabels(locale)
    for (const key of ['secScopeProxyHop', 'secScopeNode', 'secWarn']) {
      assert.ok(labels[key], `${locale} is missing dashboard.${key}`)
    }
    assert.match(labels.secScopeProxyHop, /Tailscale/, `${locale} does not name the proxy`)
  }
  assert.match(networkCardLabels('zh-CN').secScopeProxyHop, /不是浏览器/)
  // A warning row with no name to give is worded as a warning, not as "on".
  const fn = extra.slice(extra.indexOf('function pillText'), extra.indexOf('function pillTitle'))
  assert.match(fn, /case 'warn': return t\('dashboard\.secWarn'\)/)
})

// ---------------------------------------------------------------------------
// Per-device statistics
// ---------------------------------------------------------------------------

test('a device\'s 24-hour figures are asked for by device, so two devices never share numbers', async () => {
  assert.equal(communicationStatsQuery('line-1'), '?line_id=line-1')
  assert.equal(communicationStatsQuery(undefined, 'NSH-410-TEST01'), '?node_id=NSH-410-TEST01')
  // A node id replaces the line id: both would be one device's line scoped twice.
  assert.equal(communicationStatsQuery('line-1', 'NSH-410-TEST01'), '?node_id=NSH-410-TEST01')
  assert.equal(communicationStatsQuery(), '')
  assert.equal(communicationStatsQuery(undefined, 'a b&c'), '?node_id=a%20b%26c')

  await withFetch(
    url => {
      const node = new URL(url, 'http://gateway.invalid').searchParams.get('node_id')
      return json({ window_hours: 24, calls: { outgoing: node === 'NSH-410-TEST01' ? 3 : 7, duration_seconds: 32 } })
    },
    async calls => {
      const first = await getCommunicationStats(undefined, 'NSH-410-TEST01')
      const second = await getCommunicationStats(undefined, 'NSH-410-TEST02')
      assert.equal(first.calls.outgoing, 3)
      assert.equal(second.calls.outgoing, 7)
      assert.match(calls[0].url, /stats\/communication\?node_id=NSH-410-TEST01$/)
      assert.match(calls[1].url, /stats\/communication\?node_id=NSH-410-TEST02$/)
    }
  )
  // The card asks by device for a Node (administrators only server-side), and
  // by line otherwise.
  assert.match(extra, /const byDevice = nodeID\.value !== '' && sessionState\.role === 'admin'/)
  assert.match(extra, /byDevice \? getCommunicationStats\(undefined, nodeID\.value\) : getCommunicationStats\(props\.line\.id\)/)
})

// ---------------------------------------------------------------------------
// The panel
// ---------------------------------------------------------------------------

test('the security centre is administrators only, lazily loaded, and requests nothing for a member', () => {
  assert.match(
    settingsPanel,
    /const SecurityCentrePanel = defineAsyncComponent\(\(\) => import\('\.\/SecurityCentrePanel\.vue'\)\)/,
    'the security centre is in the settings panel chunk'
  )
  assert.doesNotMatch(settingsPanel, /import SecurityCentrePanel from/)
  assert.match(settingsPanel, /<SecurityCentrePanel v-if="sessionState\.role === 'admin'" \/>/)
  assert.match(centre, /const admin = computed\(\(\) => sessionState\.role === 'admin'\)/)
  assert.match(centre, /async function load\(\): Promise<void> \{\s*if \(!admin\.value \|\| loading\.value\) return/)
  assert.match(centre, /onMounted\(\(\) => \{\s*if \(!admin\.value\) return/)
  assert.match(centre, /<p v-if="!admin \|\| forbidden" class="security-centre__notice">\{\{ labels\.adminOnly \}\}<\/p>/)
  assert.equal(chinese.adminOnly, '需要管理员')
  // A 403 from the gateway reads the same way as not being an administrator.
  assert.match(centre, /forbidden\.value = cause instanceof NodeApiError && cause\.status === 403/)
  // Offline and "no engine on this gateway" each have their own sentence.
  assert.match(centre, /cause\.code === 'network'\) return labels\.value\.offline/)
  assert.match(centre, /cause\.code === 'trust_unavailable'\) return labels\.value\.unavailable/)
  // A failed read clears the page; the previous answer is not left up.
  const load = centre.slice(centre.indexOf('async function load()'), centre.indexOf('async function refreshAll'))
  assert.match(load, /catch \(cause\) \{\s*trust\.value = null/)
  assert.doesNotMatch(code(centre) + code(viewer), /localStorage|sessionStorage/)
})

test('monitor mode is announced and nothing in it is drawn as blocking or as a pass', () => {
  assert.equal(chinese.monitorMode, '监控模式：仅记录，不拦截')
  assert.match(english.monitorMode, /recorded only, nothing is blocked/)
  // The banner shows whenever the gateway did not say "enforce" -- monitor, or
  // a mode it did not report.
  assert.match(centre, /v-if="trust\.enforcement !== 'enforce'"\s+class="security-centre__banner"/)
  assert.match(centre, /trust\.enforcement === 'monitor' \? labels\.monitorMode : labels\.enforcementModes\.unknown/)
  assert.match(centre, /const enforcing = computed\(\(\) => trust\.value\?\.enforcement === 'enforce'\)/)
  // Trusted is green only when enforced.
  assert.match(centre, /case 'TRUSTED':\s*return enforcing\.value \? 'is-ok' : ''/)
  // A forbidden action reads "would be refused (not blocked)" outside enforcement.
  const allowance = centre.slice(centre.indexOf('function allowance('), centre.indexOf('function count('))
  assert.match(allowance, /if \(value === null\) return \{ text: labels\.value\.allowedUnknown, tone: '' \}/)
  assert.match(allowance, /if \(value\) return \{ text: labels\.value\.allowed, tone: enforcing\.value \? 'is-ok' : '' \}/)
  assert.match(allowance, /: \{ text: labels\.value\.notAllowedMonitor, tone: 'is-warn' \}/)
  assert.match(chinese.notAllowedMonitor, /未拦截/)
  // And an administrator acting in monitor mode is told it blocks nothing.
  assert.match(centre, /<li v-if="!enforcing">\{\{ labels\.adminActionMonitorNote \}\}<\/li>/)
})

test('every device shows its state, why, the time left and what it is allowed', () => {
  assert.match(centre, /<li v-for="subject in trust\.subjects" :key="subject\.node_id"/)
  assert.match(centre, /subject\.effective_reason \|\| subject\.reason \|\| labels\.noReason/)
  assert.match(centre, /formatRemaining\(subject\.remaining_seconds/)
  assert.match(centre, /<li v-for="action in TRUST_ACTIONS" :key="action">/)
  assert.match(api, /export const TRUST_ACTIONS = \[\s*'receive_call',\s*'receive_sms',\s*'dial',\s*'send_sms',\s*'forward_otp',\s*'update',\s*'diagnose'\s*\]/)
  for (const copy of [chinese, english]) {
    for (const action of ['receive_call', 'receive_sms', 'dial', 'send_sms', 'forward_otp', 'update', 'diagnose']) {
      assert.ok(copy.actions[action], `no label for the ${action} permission`)
    }
    for (const state of ['UNBOUND', 'OBSERVATION', 'TRUSTED', 'RESTRICTED', 'QUARANTINE']) {
      assert.ok(copy.states[state], `no label for ${state}`)
    }
  }
  // A missing count is unknown, never zero.
  assert.match(centre, /function count\(value: number \| undefined\): string \{\s*return value === undefined \? labels\.value\.unknown : String\(value\)/)
  // A damaged record and an empty list each say so.
  assert.match(centre, /v-if="subject\.record_verified === false" role="alert"/)
  assert.match(centre, /v-if="trust\.subjects\.length === 0"[^>]*>\{\{ labels\.devicesEmpty \}\}/)
})

test('the evidence map labels unsupported and unused inputs with the reason', () => {
  assert.match(centre, /<li v-for="key in EVIDENCE_KEYS" :key="key">/)
  // Green only for an input the engine actually observes.
  assert.match(centre, /:class="\{ 'is-ok': trust\.evidence\[key\] === 'observed' \}"/)
  assert.match(centre, /trust\.evidence\[key\] === 'unknown' \? labels\.evidenceReasons\.unknown : labels\.evidenceReasons\[key\]/)
  for (const copy of [chinese, english]) {
    for (const key of ['device_identity', 'sim_change', 'modem_change', 'otp_volume', 'location', 'node_local']) {
      assert.ok(copy.evidenceInputs[key], `no name for the ${key} input`)
      assert.ok(copy.evidenceReasons[key], `no reason for the ${key} input`)
    }
    for (const state of ['observed', 'unsupported', 'not_used', 'unknown']) assert.ok(copy.evidenceStates[state])
  }
  assert.match(chinese.evidenceReasons.modem_change, /不携带模组序列号/)
  assert.match(english.evidenceReasons.modem_change, /no modem serial/)
  assert.match(chinese.evidenceReasons.location, /从不用于信任判断/)
  assert.match(english.evidenceReasons.location, /Never used/)
  assert.match(chinese.evidenceReasons.node_local, /不保存自己的信任记录/)
  assert.match(english.evidenceReasons.node_local, /no trust record of its own/)
  assert.equal(chinese.evidenceStates.unsupported, '不支持')
  assert.equal(chinese.evidenceStates.not_used, '不使用')
})

test('hardware evidence with no backend is a fixed list that nothing can turn green', () => {
  assert.match(centre, /const HARDWARE_ITEMS = \['secure_boot', 'tpm', 'system_signature', 'geo_risk'\] as const/)
  const start = centre.indexOf('<li v-for="item in HARDWARE_ITEMS"')
  const section = centre.slice(start, centre.indexOf('</ul>', start))
  // One chip, no tone, no condition, no payload: there is no field to feed it.
  assert.match(section, /<span class="security-centre__chip">\{\{ labels\.hardwareUnsupported \}\}<\/span>/)
  assert.doesNotMatch(section, /is-ok|is-good|:class|v-if|trust\.|subject\./, 'a hardware row can be tinted or fed by a payload')
  assert.match(section, /\{\{ labels\.hardwareReasons\[item\] \}\}/)
  assert.equal(chinese.hardwareUnsupported, '不支持')
  assert.equal(chinese.hardwareReasons.tpm, 'UFI003 无 TPM/TEE 证据。')
  for (const copy of [chinese, english]) {
    for (const item of ['secure_boot', 'tpm', 'system_signature', 'geo_risk']) {
      assert.ok(copy.hardwareItems[item])
      assert.ok(copy.hardwareReasons[item], `${item} is unsupported without a reason`)
    }
  }
})

test('restrict, quarantine and restore each need a reason and a confirmation that explains them', () => {
  // The list buttons only open the form.
  assert.match(centre, /@click="open\(\{ kind: 'trust', action, device: subject\.node_id \}\)"/)
  assert.equal(code(centre).match(/runTrustAction\(/g).length, 1)
  const confirm = centre.slice(centre.indexOf('async function confirm()'), centre.indexOf('async function verify()'))
  assert.match(confirm, /runTrustAction\(current\.action, current\.device, reason\.value\)/)
  // No reason, no request -- checked before anything is sent.
  const guard = confirm.indexOf("if (reasonState.value !== '') return")
  assert.notEqual(guard, -1, 'the confirmation no longer checks the reason')
  assert.ok(guard < confirm.indexOf('runTrustAction('), 'an action can be sent before its reason is checked')
  assert.ok(guard < confirm.indexOf('resealJournal('), 'a reseal can be sent before its reason is checked')
  assert.match(centre, /const reasonState = computed\(\(\) => reasonProblem\(reason\.value\)\)/)
  // The confirm buttons are disabled until the reason is usable, and while working.
  assert.equal(centre.match(/:disabled="working \|\| reasonState !== ''"/g).length, 2)
  assert.equal(centre.match(/<textarea\b/g).length, 2)
  assert.match(centre, /labels\.reasonCount\(reasonBytes\(reason\), REASON_LIMIT_BYTES\)/)
  // Opening a form always starts with an empty reason.
  assert.match(centre, /function open\(next: Pending\): void \{\s*pending\.value = next\s*reason\.value = ''/)
  assert.match(centre, /<li v-for="line in labels\.adminActionExplain\[isPendingTrust\(subject\)\]"/)

  for (const copy of [chinese, english]) {
    for (const action of ['restrict', 'quarantine', 'restore']) {
      assert.ok(copy.adminActions[action])
      assert.ok(copy.adminActionExplain[action].length >= 2, `${action} is not explained`)
      assert.match(copy.adminActionTitles[action]('NSH-410-TEST01'), /NSH-410-TEST01/)
    }
  }
  // Restore leads to observation, never straight to trusted.
  assert.match(chinese.adminActionExplain.restore[0], /观察期/)
  assert.match(chinese.adminActionExplain.restore[0], /不会直接回到.已信任/)
  assert.match(english.adminActionExplain.restore[0], /OBSERVATION/)
  assert.match(english.adminActionExplain.restore[0], /not return straight to trusted/)
  assert.match(chinese.adminActionExplain.quarantine.join(''), /更新和诊断/)
})

test('journal health is OK, damaged with where, or not verified -- and reseal is offered only when damaged', () => {
  assert.match(centre, /const journalState = computed\(\(\) => \(journal\.value \? journalStatus\(journal\.value\) : 'unknown'\)\)/)
  assert.deepEqual(Object.keys(chinese.journalStates).sort(), ['damaged', 'ok', 'unknown'])
  assert.match(centre, /journalState === 'ok' \? 'is-ok' : journalState === 'damaged' \? 'is-bad' : ''/)
  assert.match(centre, /labels\.journalDamagedAt\(journal\.first_bad_seq\)/)
  assert.match(centre, /v-if="journal\.sealed_from !== undefined"[^>]*>\s*\{\{ labels\.journalResealedFrom\(journal\.sealed_from\) \}\}/)
  assert.match(chinese.journalDamagedAt(5), /#5/)
  assert.match(chinese.journalResealedFrom(7), /#7/)
  // A journal nobody checked is not called damaged, and shows no counts.
  assert.match(centre, /<dl v-if="journalState !== 'unknown'" class="security-centre__facts">/)

  // Reseal: only for a journal that failed, behind a reason and an explanation.
  assert.match(centre, /v-if="journal\.ok === false && journalState === 'damaged'"[\s\S]{0,260}@click="open\(\{ kind: 'reseal' \}\)"/)
  assert.equal(code(centre).match(/resealJournal\(/g).length, 1)
  for (const copy of [chinese, english]) assert.equal(copy.journalResealExplain.length, 3)
  assert.match(chinese.journalResealExplain[0], /不会删除/)
  assert.match(chinese.journalResealExplain[1], /不再为它们担保/)
  assert.match(chinese.journalResealExplain[2], /所有设备都会回到观察期/)
  assert.match(english.journalResealExplain[0], /Nothing is deleted/)
  assert.match(english.journalResealExplain[1], /no longer vouched for/)
  assert.match(english.journalResealExplain[2], /Every device returns to an observation period/)
  // Verify re-reads everything afterwards: the result changes what devices may do.
  const verify = centre.slice(centre.indexOf('async function verify()'), centre.indexOf('onMounted('))
  assert.match(verify, /await verifyJournal\(\)[\s\S]*await load\(\)/)
})

test('the journal viewer shows cause, source, time and result, and keeps hashes out of the way', () => {
  const template = viewer.slice(viewer.indexOf('<template>'), viewer.indexOf('<style'))
  for (const field of ['event.cause', 'event.source', 'event.result', 'formatTime(event.at)', 'kindLabel(event.kind)']) {
    assert.ok(template.includes(field), `the viewer no longer shows ${field}`)
  }
  // Hashes appear only inside the collapsed detail.
  const details = template.slice(template.indexOf('<details'), template.indexOf('</details>'))
  assert.match(details, /event\.hash/)
  assert.match(details, /event\.prev_hash/)
  const outside = template.replace(details, '')
  assert.doesNotMatch(outside, /hash/i, 'a hash is rendered as primary content')
  assert.doesNotMatch(template, /<details[^>]*\bopen\b/, 'the hash detail is open by default')

  // A kind with no label falls back to the raw kind, never to another label.
  assert.match(viewer, /return props\.labels\.eventKinds\[kind\] \?\? \(kind \|\| props\.labels\.unknown\)/)
  // Every kind the gateway writes today has a label in both languages.
  const kinds = [
    'PAIRING_BOUND', 'TRUST_GRANDFATHERED', 'SIM_CHANGED', 'PAIRING_CHANGED', 'INTEGRITY_FAILURE',
    'OTP_LIMIT_TRIGGERED', 'OTP_LIMIT_CLEARED', 'TRUST_DOWNGRADE', 'TRUST_UPGRADE', 'MANUAL_RESTRICT',
    'MANUAL_QUARANTINE', 'MANUAL_RESTORE', 'ACTION_REFUSED', 'AUDIT_CHAIN_RESEALED',
    'TRUST_RECORD_DAMAGED', 'IDENTITY_VERIFICATION', 'AGENT_UPDATE'
  ]
  for (const copy of [chinese, english]) {
    for (const kind of kinds) assert.ok(copy.eventKinds[kind], `no label for ${kind}`)
  }

  // Paged: newest first, older pages only on request, and not read at all
  // until the administrator opens it.
  // Opening asks for the newest page (no bound); "load older" asks below the
  // oldest entry on screen; the device filter rides on both. The paging itself
  // is exercised against a fake gateway in node-security-api.test.mjs.
  assert.match(viewer, /async function loadNewest\(\)[\s\S]{0,220}await readPage\(undefined\)/)
  assert.match(viewer, /async function loadOlder\(\)[\s\S]{0,160}await readPage\(journalOlderBound\(listing\.value\)\)/)
  assert.match(viewer, /getJournalPage\(before, JOURNAL_PAGE_SIZE, filter\.value\)/)
  assert.match(viewer, /listing\.value = appendJournalPage\(listing\.value, page, before\)/)
  assert.match(viewer, /<button v-if="more"[^>]*@click="loadOlder">/)
  assert.match(viewer, /watch\(filter, \(\) => \{\s*void loadNewest\(\)/)
  assert.doesNotMatch(viewer, /findJournalHead|journalWindow|verifiedHead/, 'the forward scan is back')
  assert.match(centre, /<SecurityJournalViewer\s+v-if="journalOpen"/)
  assert.doesNotMatch(centre, /:verified-head=/)
  // A gateway that ignored the query: said so, and the "beginning of the
  // journal" line is not claimed on its behalf.
  assert.match(viewer, /<p v-if="!newestFirst"[^>]*>\{\{ labels\.viewerMayBeIncomplete \}\}<\/p>/)
  assert.match(viewer, /v-else-if="events\.length && newestFirst"[^>]*>\{\{ labels\.viewerNoMore \}\}/)
  for (const copy of [chinese, english]) assert.ok(copy.viewerMayBeIncomplete)
  assert.match(chinese.viewerMayBeIncomplete, /可能不完整/)
  assert.match(english.viewerMayBeIncomplete, /may be incomplete/)
  // An answer that arrives after the filter changed is dropped.
  assert.match(viewer, /if \(mine !== generation\) return/)
  // Entries resealed over are marked as not vouched for.
  assert.match(viewer, /props\.sealedFrom !== undefined && event\.seq < props\.sealedFrom/)
})

// ---------------------------------------------------------------------------
// The dashboard card
// ---------------------------------------------------------------------------

test('the dashboard card reads device trust from the trust engine and is green in one case only', () => {
  assert.match(card, /trustDocument\.value = await getTrustDocument\(\)\.catch\(\(\) => null\)/)
  const tile = card.slice(card.indexOf('const trustTile = computed'), card.indexOf('const assessmentTiles'))
  // A damaged journal comes first; then the mode; then the counts.
  assert.ok(tile.indexOf("journal === 'damaged'") < tile.indexOf("document.enforcement !== 'enforce'"))
  assert.match(tile, /if \(!document\) return null/)
  // Outside enforcement the tile carries no state at all, so it cannot be green.
  const monitor = tile.slice(tile.indexOf("if (document.enforcement !== 'enforce')"), tile.indexOf('const held'))
  assert.doesNotMatch(monitor, /state:/, 'monitor mode can be drawn as a pass')
  assert.match(monitor, /trustMonitor/)
  assert.match(tile, /journal === 'ok' && \(counts\.TRUSTED \?\? 0\) > 0 && counts\.TRUSTED === document\.subjects\.length/)
  assert.match(tile, /state: held > 0 \? 'warn' : allTrusted \? 'ok' : undefined/)
  // With no trust document the old wording stands: not assessed.
  assert.match(card, /value: trustValue\(trust\.value\)/)

  const zh = moduleSecurityCardLabels('zh-CN')
  const en = moduleSecurityCardLabels('en-US')
  assert.match(zh.trustMonitorNote, /监控模式：仅记录，不拦截/)
  assert.match(en.trustMonitorNote, /recorded only, nothing is blocked/)
  // The integrity tiles have no backend: "not supported", with the reason.
  assert.equal(zh.integrityPending, '不支持')
  assert.equal(en.integrityPending, 'Not supported')
  assert.match(zh.integrityNote, /UFI003 无 TPM\/TEE 证据/)
  assert.match(en.integrityNote, /no TPM or TEE evidence/)
  assert.match(card, /value: entry\.row\?\.value \|\| label\('integrityPending'\)/)
  // The footer goes to the security centre, which now exists.
  assert.match(card, /:to="\{ name: 'settings', params: \{ section: 'security' \} \}"/)
})

// ---------------------------------------------------------------------------
// Copy and layout
// ---------------------------------------------------------------------------

test('the security centre copy is the same shape in both languages and never sells a licence', () => {
  function shape(value) {
    if (typeof value === 'function') return 'function'
    if (Array.isArray(value)) return `array(${value.length})`
    if (value && typeof value === 'object') {
      return Object.fromEntries(Object.keys(value).sort().map(key => [key, shape(value[key])]))
    }
    return typeof value
  }
  assert.deepEqual(shape(chinese), shape(english), 'the two languages have drifted')
  function strings(value) {
    if (typeof value === 'string') return [value]
    if (typeof value === 'function') return [String(value(1, 2))]
    if (value && typeof value === 'object') return Object.values(value).flatMap(strings)
    return []
  }
  const cardZh = Object.values(moduleSecurityCardLabels('zh-CN'))
  const cardEn = Object.values(moduleSecurityCardLabels('en-US'))
  for (const value of [...strings(chinese), ...strings(english), ...cardZh, ...cardEn]) {
    assert.ok(value.trim().length > 0)
    assert.doesNotMatch(value, /许可证|购买|licen[cs]e|purchase|\bbuy\b/i, value)
  }
  // The neighbouring card's rule: Chinese (both scripts) and English, and every
  // other locale reads the English.
  assert.equal(securityCentreLabels('zh-TW'), chinese)
  for (const locale of ['ja-JP', 'vi-VN', 'es-ES', 'de-DE', 'fr-FR', 'pt-BR']) {
    assert.equal(securityCentreLabels(locale), english)
  }
})

test('no hard-coded copy, no SIM identifiers, wrapping text and theme colours only', () => {
  for (const [name, source] of [
    ['SecurityCentrePanel', centre],
    ['SecurityJournalViewer', viewer],
    ['LinkBoundaryList', boundaryList]
  ]) {
    assert.doesNotMatch(code(source), /[一-鿿]/, `${name} has hard-coded Chinese`)
    const template = code(source.slice(source.indexOf('<template>'), source.indexOf('<style')))
    for (const [, literal] of template.matchAll(/>\s*([A-Za-z][A-Za-z ,.'-]{3,})\s*</g)) {
      assert.fail(`${name} has a hard-coded string in its template: ${literal}`)
    }
    assert.doesNotMatch(code(source), /\b(?:iccid|imsi|msisdn|phone_number|private_key)\b/i)
    const style = source.slice(source.indexOf('<style'))
    assert.match(style, /overflow-wrap: anywhere/, `${name} lets a long ID or sentence overflow`)
    assert.doesNotMatch(style, /white-space:\s*nowrap|text-overflow:\s*ellipsis/, `${name} truncates instead of wrapping`)
    assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b|rgba?\(/, `${name} hard-codes a colour`)
    assert.doesNotMatch(style, /min-width:\s*(?:[4-9]\d\d|\d{4,})px/, `${name} cannot fit a 360px screen`)
    assert.doesNotMatch(style, /@media/, `${name} adds a breakpoint; its grids are meant to reflow on their own`)
  }
})
