import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { readNodeSecurity } from '../src/api/nodeSecurity.ts'
import { nodeSecurityCopy } from '../src/i18n/nodeSecurityCopy.ts'
import { loadComponent, render, textOf } from './support/render-sfc.mjs'

const read = path => readFile(new URL(path, import.meta.url), 'utf8')
const [view, block, panel] = await Promise.all([
  read('../src/views/NasSimHubNodesView.vue'),
  read('../src/components/NodeSecurityBlock.vue'),
  read('../src/components/NodeUpdatePanel.vue')
])

/** Source with comments stripped; several assertions are "must not appear". */
function code(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    .replace(/<!--[\s\S]*?-->/g, ' ')
}

const chinese = nodeSecurityCopy('zh-CN')
const english = nodeSecurityCopy('en-US')
const SecurityBlock = await loadComponent('components/NodeSecurityBlock.vue')

/** The security block as a reader sees it, for one node as the gateway sent it. */
async function draw(node, extra = {}) {
  const html = await render(SecurityBlock, {
    security: readNodeSecurity(node),
    copy: chinese,
    paired: true,
    busy: false,
    locale: 'zh-CN',
    ...extra
  })
  return { html, text: textOf(html) }
}

/** The <dd> that follows a given label, with its class. */
function row(html, label) {
  const match = html.match(new RegExp(`<dt[^>]*>${label}</dt><dd(?: class="([^"]*)")?[^>]*>([\\s\\S]*?)</dd>`))
  assert.ok(match, `no row labelled ${label}`)
  return { tone: match[1] ?? '', text: textOf(match[2]) }
}

// The security block on each SIM Node card. It is rendered here, not just read,
// because the question is what an administrator SEES for a given answer from
// the gateway -- above all when the answer is missing.

test('a node the gateway said nothing about shows unknown on every row and no tone anywhere', async () => {
  const { html, text } = await draw({ node_id: 'NSH-410-TEST01' })
  assert.doesNotMatch(html, /is-ok|is-bad|is-warn/, 'a row was tinted with no evidence behind it')
  assert.doesNotMatch(html, /role="alert"/)
  assert.equal(row(html, chinese.pqIdentity).text, chinese.pqNotReported)
  assert.equal(row(html, chinese.requestedLevel).text, chinese.unknown)
  assert.equal(row(html, chinese.negotiatedGroup).text, chinese.unknown)
  assert.equal(row(html, chinese.linkPostQuantum).text, chinese.unknown)
  assert.equal(row(html, chinese.trust).text, chinese.trustUnknown)
  assert.match(text, new RegExp(chinese.linkNotObserved))
  // Nothing is said about outgoing use, monitor mode or a pin with no record.
  assert.doesNotMatch(text, /监控模式|拨出|外呼/)
  assert.doesNotMatch(html, /<button/, 'an action was offered with nothing to act on')
})

test('a gateway build that cannot verify ML-DSA says so, with neither a tick nor a cross', async () => {
  // `available: false` is about the GATEWAY. Whatever the outcome field holds
  // then is meaningless, so it must not be drawn in any tone.
  for (const outcome of ['dual', 'refused', 'classical_only', 'unknown']) {
    const { html, text } = await draw({
      identity: { available: false, backend: 'purego-none', outcome, pin_state: 'pinned', fingerprint: 'aa:bb' }
    })
    const identity = row(html, chinese.pqIdentity)
    assert.equal(identity.text, chinese.pqUnavailable('purego-none'))
    assert.match(identity.text, /无法验证 ML-DSA/)
    assert.match(identity.text, /purego-none/, 'the backend that cannot verify is not named')
    assert.equal(identity.tone, '', `outcome "${outcome}" was tinted on a gateway that cannot verify`)
    assert.doesNotMatch(text, /已验证|已记录且一致/, 'a verification result is shown that this gateway could not have made')
    assert.doesNotMatch(html, new RegExp(`<dt[^>]*>${chinese.pqPin}</dt>`), 'a pin state is shown without verification')
  }
})

test('identity outcome, pin state, algorithm and fingerprint are shown when verified', async () => {
  const { html, text } = await draw({
    identity: {
      available: true, backend: 'stdlib', outcome: 'dual', policy: 'PQ', enforced: true,
      algorithm: 'ML-DSA-65', fingerprint: 'SHA256:testfingerprint', pin_state: 'pinned',
      attestation_supported: true
    }
  })
  const identity = row(html, chinese.pqIdentity)
  assert.equal(identity.tone, 'is-ok')
  assert.match(identity.text, /Ed25519 \+ ML-DSA-65/)
  assert.deepEqual(row(html, chinese.pqPin), { tone: 'is-ok', text: chinese.pinStates.pinned })
  assert.match(row(html, chinese.pqFingerprint).text, /ML-DSA-65 · SHA256:testfingerprint/)
  assert.match(text, /PQ · 强制执行/)

  // Classical only is a fact, not a pass and not a failure.
  const classical = await draw({ identity: { available: true, outcome: 'classical_only', pin_state: 'none' } })
  assert.equal(row(classical.html, chinese.pqIdentity).tone, '')
  assert.equal(row(classical.html, chinese.pqPin).tone, '')
})

test('a refused identity is shown as a refusal with the gateway\'s reason', async () => {
  const { html, text } = await draw({
    identity_refused: true,
    identity: { available: true, outcome: 'refused', pin_state: 'changed', reason: 'the device presents a different post-quantum key' }
  })
  assert.match(html, /role="alert" class="node-security__alert"/)
  assert.match(text, new RegExp(chinese.identityRefused))
  assert.match(text, /the device presents a different post-quantum key/)
  assert.equal(row(html, chinese.pqIdentity).tone, 'is-bad')
  // Without a reason the block says there is none, not nothing.
  const bare = await draw({ identity_refused: true, identity: { available: true, outcome: 'refused' } })
  assert.match(bare.text, new RegExp(chinese.identityRefusedNoReason))
})

test('requested level and negotiated group are two facts, and a shortfall is an alert', async () => {
  const { html, text } = await draw({
    link_security: {
      known: true, tls_version: 'TLS 1.3', group: 'X25519', post_quantum: 'no',
      requested_level: 'PQ', required_group: 'X25519MLKEM768', meets_level: false
    }
  })
  assert.equal(row(html, chinese.requestedLevel).text, 'PQ')
  const negotiated = row(html, chinese.negotiatedGroup)
  assert.equal(negotiated.text, 'X25519 · TLS 1.3')
  assert.equal(negotiated.tone, 'is-warn')
  assert.equal(row(html, chinese.linkPostQuantum).text, chinese.tristate.no)
  assert.match(html, /role="alert" class="node-security__warning"/)
  assert.match(text, new RegExp(chinese.linkShortfall('X25519MLKEM768').replace(/[()（）]/g, '.')))

  // Meeting the level: no alert, and "post-quantum: yes" is the only green.
  const met = await draw({
    link_security: { known: true, group: 'X25519MLKEM768', post_quantum: 'yes', requested_level: 'PQ', meets_level: true }
  })
  assert.doesNotMatch(met.html, /role="alert"/)
  assert.equal(row(met.html, chinese.linkPostQuantum).tone, 'is-ok')
  assert.equal(row(met.html, chinese.negotiatedGroup).tone, '')
})

test('monitor mode is said in the required words and never drawn as protection', async () => {
  assert.equal(chinese.monitorMode, '监控模式：仅记录，不拦截')
  const { text } = await draw({
    trust: { known: true, state: 'OBSERVATION', effective_state: 'OBSERVATION', remaining_seconds: 5400, outgoing_allowed: false, enforced: false }
  })
  assert.match(text, /监控模式：仅记录，不拦截/)
  // The sentence that says outgoing use is refused would be false here.
  assert.doesNotMatch(text, new RegExp(chinese.outgoingBlocked), 'monitor mode claims outgoing use is refused')
  assert.match(text, new RegExp(chinese.outgoingWouldBlock))

  // A trusted device in monitor mode is not green: nothing acts on the state.
  const trusted = await draw({
    trust: { known: true, state: 'TRUSTED', effective_state: 'TRUSTED', outgoing_allowed: true, enforced: false }
  })
  assert.equal(row(trusted.html, chinese.trust).tone, '', 'monitor mode is tinted as a pass')
  assert.match(trusted.text, /监控模式：仅记录，不拦截/)
  const enforcedTrusted = await draw({
    trust: { known: true, state: 'TRUSTED', effective_state: 'TRUSTED', outgoing_allowed: true, enforced: true }
  })
  assert.equal(row(enforcedTrusted.html, chinese.trust).tone, 'is-ok')
  assert.doesNotMatch(enforcedTrusted.text, /监控模式/)
  assert.doesNotMatch(enforcedTrusted.text, new RegExp(chinese.outgoingBlocked))

  // A gateway that did not say which mode it is in gets neither sentence.
  const unsaid = await draw({ trust: { known: true, state: 'RESTRICTED', outgoing_allowed: false } })
  assert.match(unsaid.text, new RegExp(chinese.outgoingBlockedUnknownMode))
  assert.doesNotMatch(unsaid.text, new RegExp(chinese.outgoingBlocked))
})

test('when trust is enforced and outgoing use is refused, one sentence says so', async () => {
  const { html, text } = await draw({
    trust: {
      known: true, state: 'OBSERVATION', effective_state: 'OBSERVATION', reason: 'observation period',
      remaining_seconds: 2 * 86400 + 3 * 3600 + 59, outgoing_allowed: false, enforced: true
    }
  })
  assert.equal(row(html, chinese.trust).text, chinese.trustStates.OBSERVATION)
  assert.equal(row(html, chinese.trustReason).text, 'observation period')
  assert.match(text, new RegExp(chinese.outgoingBlocked))
  assert.equal(text.split(chinese.outgoingBlocked).length - 1, 1)
  assert.match(text, /观察期剩余 2 天 3 小时/)
  // 32 seconds left is under a minute, not 32 minutes and not zero hours.
  const soon = await draw({ trust: { known: true, state: 'OBSERVATION', remaining_seconds: 32, outgoing_allowed: false, enforced: true } })
  assert.match(soon.text, /观察期剩余 不到 1 分钟/)
  const minutes = await draw({ trust: { known: true, state: 'OBSERVATION', remaining_seconds: 3599, outgoing_allowed: false, enforced: true } })
  assert.match(minutes.text, /观察期剩余 59 分钟/)
  // Effective state wins over the recorded one.
  const held = await draw({ trust: { known: true, state: 'TRUSTED', effective_state: 'RESTRICTED', outgoing_allowed: false, enforced: true } })
  assert.deepEqual(row(held.html, chinese.trust), { tone: 'is-bad', text: chinese.trustStates.RESTRICTED })
})

test('accepting a new post-quantum identity is offered only for a changed or withdrawn key, behind a confirmation', async () => {
  for (const state of ['none', 'pinned', 'unknown']) {
    const { html } = await draw({ identity: { available: true, outcome: 'dual', pin_state: state } })
    assert.doesNotMatch(html, /<button/, `the reset is offered while the pin is "${state}"`)
  }
  for (const state of ['changed', 'withdrawn']) {
    const { html, text } = await draw({ identity: { available: true, outcome: 'refused', pin_state: state } })
    assert.equal(row(html, chinese.pqPin).tone, 'is-warn')
    assert.match(text, /接受新的后量子身份/)
    // First render: the offer only. The destructive button is not on screen
    // until the administrator has opened the explanation.
    assert.equal(html.match(/<button/g).length, 1)
    assert.doesNotMatch(html, /danger-button/)
    assert.doesNotMatch(text, new RegExp(chinese.resetConfirm))
  }

  // The offer opens the confirmation; only the confirmation emits.
  assert.match(block, /@click="confirming = true"/)
  assert.match(block, /v-if="confirming" class="node-security__confirm" role="group"/)
  assert.match(block, /<li v-for="line in copy\.resetExplain"/)
  assert.match(block, /class="danger-button" :disabled="busy" @click="confirmReset"/)
  assert.equal(code(block).match(/emit\('reset'\)/g).length, 1, 'the reset can be emitted without the confirmation')
  const confirm = block.slice(block.indexOf('function confirmReset'), block.indexOf('</script>'))
  assert.match(confirm, /emit\('reset'\)/)

  // And the explanation says the three things an administrator must know.
  for (const copy of [chinese, english]) {
    const explanation = copy.resetExplain.join(' ')
    assert.match(explanation, /ML-DSA/, 'the confirmation does not say which key is forgotten')
    assert.match(explanation, /Ed25519/, 'the confirmation does not say the Ed25519 pin is untouched')
    assert.equal(copy.resetExplain.length, 3)
  }
  assert.match(chinese.resetExplain[0], /忘掉/)
  assert.match(chinese.resetExplain[1], /不受影响/)
  assert.match(chinese.resetExplain[2], /下一次校验.*记录/)
  assert.match(english.resetExplain[2], /next check.*records/)
})

test('the nodes view reads every node through the reader and posts the reset with CSRF', () => {
  assert.match(view, /:security="readNodeSecurity\(node\)"/, 'a node is drawn from the raw payload')
  assert.match(view, /@reset="resetPQPin\(node\)"/)
  assert.match(view, /await resetNodePQPin\(node\.node_id\)/)
  // The raw members are typed unknown, so nothing in the template can use them.
  for (const member of ['identity', 'link_security', 'identity_refused', 'trust']) {
    assert.match(view, new RegExp(`${member}\\?: unknown`))
  }
  assert.doesNotMatch(code(view), /node\.(?:identity|trust|link_security)\./)
  // No SIM identifier or key ever reaches this page.
  assert.doesNotMatch(code(view) + code(block) + code(panel), /\b(?:iccid|imsi|msisdn|phone_number|private_key|public_key)\b/i)
})

test('the update panel is loaded on demand and opened per device', () => {
  assert.match(
    view,
    /const NodeUpdatePanel = defineAsyncComponent\(\(\) => import\('\.\.\/components\/NodeUpdatePanel\.vue'\)\)/,
    'the update panel is in the nodes view chunk'
  )
  assert.doesNotMatch(view, /import NodeUpdatePanel from/)
  assert.match(view, /<NodeUpdatePanel\s+v-if="updateNode"/)
  assert.match(view, /@click="updateNodeID = node\.node_id"/)
})

test('the update panel re-reads the device after every action and keeps no last-known status', () => {
  // Every path that changes something ends by asking the device again.
  for (const fn of ['act', 'remove', 'sendManifest', 'sendArtifact']) {
    const start = panel.indexOf(`async function ${fn}(`)
    assert.notEqual(start, -1, `${fn} is gone`)
    const body = panel.slice(start, panel.indexOf('\n}\n', start))
    assert.match(body, /await load\(\)\s*$/, `${fn} does not re-read the device when it finishes`)
  }
  assert.match(panel, /onMounted\(\(\) => \{[\s\S]{0,80}void load\(\)/)
  const load = panel.slice(panel.indexOf('async function load()'), panel.indexOf('const status = computed'))
  assert.match(load, /catch \(cause\) \{\s*device\.value = null/, 'a failed read leaves the previous device state on screen')
  assert.doesNotMatch(code(panel), /lastKnown|previousStatus|localStorage|sessionStorage/)
  // The action's own answer is not drawn; only the fresh read is.
  const act = panel.slice(panel.indexOf('async function act('), panel.indexOf('async function remove('))
  assert.doesNotMatch(code(act), /device\.value = result\.device/)

  // Not reachable: the sentence, and no status list.
  const unreachable = panel.slice(panel.indexOf('v-if="!device.reachable"'), panel.indexOf('<template v-else>'))
  assert.match(unreachable, /labels\.notAnswering/)
  assert.doesNotMatch(unreachable, /status\.|<dl/)
  assert.equal(chinese.updates.notAnswering, '设备未应答')
})

test('update steps: one request at a time, apply behind a confirmation, confirm and roll back only when pending', () => {
  assert.match(panel, /const locked = computed\(\(\) => loading\.value \|\| busy\.value !== ''\)/)
  const template = panel.slice(panel.indexOf('<template>'), panel.indexOf('<style'))
  // Every control that sends something is disabled while a request is in flight.
  const buttons = [...template.matchAll(/<button\b[\s\S]*?>/g)].map(match => match[0])
  assert.ok(buttons.length >= 10)
  for (const button of buttons) {
    if (/emit\('close'\)/.test(button)) {
      assert.match(button, /:disabled="busy !== ''"/)
      continue
    }
    assert.match(button, /:disabled="locked\b/, `a control stays live during a request: ${button}`)
  }
  for (const input of [...template.matchAll(/<input\b[\s\S]*?>/g)].map(match => match[0])) {
    assert.match(input, /:disabled="locked\b/)
  }
  assert.match(panel, /async function act\([^)]*\): Promise<void> \{\s*if \(locked\.value\) return/)

  // Apply: the list button only arms; the request is sent from the confirmation.
  assert.match(template, /@click="applyArmed = release\.release_id"/)
  assert.equal(template.match(/act\('apply'/g).length, 1)
  const confirmation = template.slice(template.indexOf('v-if="applyArmed === release.release_id"'), template.indexOf('<div v-else class="node-update__actions">'))
  assert.match(confirmation, /act\('apply', release\.release_id\)/)
  assert.match(confirmation, /<li v-for="line in labels\.applyExplain"/)
  for (const copy of [chinese, english]) assert.equal(copy.updates.applyExplain.length, 3)
  assert.match(chinese.updates.applyExplain[0], /重启/)
  assert.match(chinese.updates.applyExplain[1], /通话会中断/)
  assert.match(chinese.updates.applyExplain[2], /自动回到之前的版本/)
  assert.match(english.updates.applyExplain[0], /restarts its agent/)
  assert.match(english.updates.applyExplain[1], /Calls in progress.*drop/)
  assert.match(english.updates.applyExplain[2], /automatically/)

  // Confirm and roll back exist in exactly one state.
  assert.match(panel, /const pendingConfirm = computed\(\(\) => status\.value\?\.state === 'PENDING_CONFIRM'\)/)
  const pending = template.slice(template.indexOf('v-if="status && pendingConfirm"'))
  assert.match(pending.slice(0, 900), /act\('confirm'[\s\S]*act\('rollback'/)
  assert.equal(template.match(/act\('confirm'/g).length, 1)
  assert.equal(template.match(/act\('rollback'/g).length, 1)

  // An unconfirmed apply is shown as that outcome, in warning colours.
  assert.match(panel, /if \(result\.notConfirmed\) \{\s*outcome\.value = labels\.value\.outcomeNotConfirmed\s*outcomeWarn\.value = true/)
})

test('release badges never show a bare tick, and imports are checked before they are sent', () => {
  assert.equal(chinese.updates.gatewayVerified, '网关已验证')
  assert.equal(chinese.updates.gatewayUnverified, '网关未验证—由设备验证')
  assert.equal(chinese.updates.dualSigned, '双签名')
  assert.equal(chinese.updates.classicalOnly, '仅 Ed25519')
  const head = panel.slice(panel.indexOf('class="node-update__release-head"'), panel.indexOf('<dl class="node-update__facts">', panel.indexOf('class="node-update__release-head"')))
  assert.match(head, /release\.gateway_verification === 'verified' \? labels\.gatewayVerified : labels\.gatewayUnverified/)
  assert.match(head, /release\.gateway_verification === 'verified' \? 'is-ok' : 'is-neutral'/)
  // A signature outcome the gateway did not establish is said to be unknown.
  assert.match(head, /v-else class="node-update__badge is-neutral">\{\{ labels\.signaturesUnknown \}\}/)

  // Two file inputs, manifest first, sizes checked locally.
  assert.equal(panel.match(/type="file"/g).length, 2)
  assert.ok(panel.indexOf('@change="pickManifest"') < panel.indexOf('@change="pickArtifact"'))
  assert.match(panel, /manifestFile\.value\.size > NODE_MANIFEST_LIMIT_BYTES/)
  assert.match(panel, /file\.size > NODE_ARTIFACT_LIMIT_BYTES/)
  assert.match(panel, /:disabled="locked \|\| !artifactTarget"/, 'an artifact can be picked before any manifest')
  assert.match(panel, /role="progressbar"/)
  // Errors are the gateway's own sentence when it sent one.
  assert.match(panel, /if \(cause\.message\) return cause\.message/)
  // An unsupported device shows the reason.
  assert.match(panel, /v-if="!device\.supported"[\s\S]{0,200}\{\{ unsupportedReason \}\}/)
})

test('both languages carry the same copy, and none of it sells a licence', () => {
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
    if (typeof value === 'function') return [value('x', 'y')]
    if (value && typeof value === 'object') return Object.values(value).flatMap(strings)
    return []
  }
  for (const [name, copy] of [['zh-CN', chinese], ['en-US', english]]) {
    for (const value of strings(copy)) {
      assert.ok(String(value).trim().length > 0, `${name} has an empty string`)
      assert.doesNotMatch(String(value), /许可证|购买|licen[cs]e|purchase|\bbuy\b/i, `${name}: ${value}`)
    }
  }
  // The rule the neighbouring file follows: Chinese (both scripts) and English,
  // and every other locale reads the English.
  assert.equal(nodeSecurityCopy('zh-TW'), chinese)
  for (const locale of ['ja-JP', 'vi-VN', 'es-ES', 'de-DE', 'fr-FR', 'pt-BR']) {
    assert.equal(nodeSecurityCopy(locale), english)
  }
  // No hard-coded sentences in the two templates: CJK text outside comments
  // would be copy that skipped the label files.
  for (const [name, source] of [['NodeSecurityBlock', block], ['NodeUpdatePanel', panel]]) {
    assert.doesNotMatch(code(source), /[一-鿿]/, `${name} has hard-coded Chinese`)
    const template = code(source.slice(source.indexOf('<template>'), source.indexOf('<style')))
    for (const [, literal] of template.matchAll(/>\s*([A-Za-z][A-Za-z ,.'-]{3,})\s*</g)) {
      assert.fail(`${name} has a hard-coded string in its template: ${literal}`)
    }
  }
})

test('long values wrap and colours come from the theme', () => {
  for (const [name, source] of [['NodeSecurityBlock', block], ['NodeUpdatePanel', panel]]) {
    const style = source.slice(source.indexOf('<style'))
    assert.match(style, /overflow-wrap: anywhere/, `${name} lets a long ID overflow`)
    assert.doesNotMatch(style, /white-space:\s*nowrap|text-overflow:\s*ellipsis/, `${name} truncates instead of wrapping`)
    assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b|rgba?\(/, `${name} hard-codes a colour instead of a theme variable`)
    assert.doesNotMatch(style, /min-width:\s*(?:[4-9]\d\d|\d{4,})px/, `${name} cannot fit a 360px screen`)
  }
})
