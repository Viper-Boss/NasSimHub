import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { moduleSecurityCardLabels } from '../src/i18n/moduleSecurityCardLabels.ts'
import { getModuleSecurity } from '../src/api/dashboardCards.ts'

const card = await readFile(
  new URL('../src/components/ModuleSecurityCard.vue', import.meta.url),
  'utf8'
)
const contract = await readFile(
  new URL('../src/api/dashboardCards.ts', import.meta.url),
  'utf8'
)
const dashboard = await readFile(
  new URL('../src/views/DashboardView.vue', import.meta.url),
  'utf8'
)
const extraCards = await readFile(
  new URL('../src/components/ModuleExtraCards.vue', import.meta.url),
  'utf8'
)

/** Source with comments stripped; several assertions here are "must not appear". */
function code(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    .replace(/<!--[\s\S]*?-->/g, ' ')
}

// The module-security card exists to answer a legal question: what is actually
// restraining a box that can call and text strangers. That purpose makes it the
// card where a fabricated reassurance does the most damage -- it is the one most
// likely to be screenshotted as evidence the device is safe -- so most of these
// tests are about what it is forbidden to claim.

test('nothing on the card is hardcoded to a passing state', () => {
  const body = code(card)

  // The mock-up's risk badge read "低". Nothing computes a risk level, so no
  // level may be baked into the component.
  assert.doesNotMatch(
    body,
    /riskPending['"]?\s*:\s*['"](低|low|Low|正常|安全)/,
    'a risk level is hardcoded'
  )
  assert.match(card, /riskRow\?\.value \|\| label\('riskPending'\)/)

  // The four integrity rows must come from the payload, never from a literal.
  const rows = card.slice(card.indexOf('const integrityRows'), card.indexOf('const trust ='))
  for (const field of ['module', 'system', 'secure_boot', 'firmware']) {
    assert.match(
      rows,
      new RegExp(`rows\\?\\.${field}`),
      `the ${field} integrity row no longer reads the payload`
    )
  }
  assert.doesNotMatch(rows, /state:\s*'ok'/, 'an integrity row is hardcoded to a pass')

  // And trust: no observation period may be invented.
  assert.match(card, /trust\?\.value \|\| label\('trustPending'\)/)
  assert.doesNotMatch(body, /remaining_hours\s*[:=]\s*\d/, 'a trust countdown is hardcoded')
})

test('an absent field renders as not-wired, never as zero or a pass', async () => {
  // The reader is the enforcement point: with no endpoint it must answer null
  // rather than an object full of zeroes, or the card would draw "0 used today"
  // as though something had measured the day.
  const originalFetch = globalThis.fetch
  globalThis.fetch = async () => new Response('not found', { status: 404 })
  try {
    assert.equal(await getModuleSecurity(), null, 'a missing endpoint did not read as null')
  } finally {
    globalThis.fetch = originalFetch
  }

  // And a payload with junk in it drops the junk rather than coercing it.
  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({
        risk: { state: 'excellent' },
        guard: { call_limit: -1, call_used: 'many', unlocked: 'yes' },
        integrity: { module: { state: 'ok', value: '正常' } },
        trust: { state: 'probation', remaining_hours: 109 }
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } }
    )
  try {
    const result = await getModuleSecurity()
    assert.equal(result.risk, undefined, 'an unknown risk state was accepted')
    assert.equal(result.guard.call_limit, undefined, 'a negative limit was accepted')
    assert.equal(result.guard.call_used, undefined, 'a non-numeric count was accepted')
    assert.equal(result.guard.unlocked, undefined, 'a non-boolean unlocked flag was accepted')
    assert.equal(result.trust.state, undefined, 'an unknown trust state was accepted')
    // The one well-formed row survives, so this is a filter and not a blanket reject.
    assert.deepEqual(result.integrity.module, { state: 'ok', value: '正常', detail: undefined })
    assert.equal(result.trust.remaining_hours, 109)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('the guard limits are labelled as the shipped default when they are', () => {
  // The limits mirror Go constants. Showing them is useful; showing them as if
  // they had been read back from this gateway is not, because a changed
  // constant would leave this card quietly asserting the old number.
  assert.match(card, /DEFAULT_CALL_LIMIT = 5/)
  assert.match(card, /DEFAULT_MESSAGE_LIMIT = 3/)
  assert.match(card, /call_guard\.go/, 'the defaults no longer say where they came from')
  assert.match(card, /fromDefaults/)
  assert.match(
    card,
    /v-if="guardLimits\.fromDefaults"[\s\S]{0,200}guardDefaultNote/,
    'the card no longer says when the limits are the factory default'
  )
  for (const locale of ['zh-CN', 'en-US']) {
    assert.ok(
      moduleSecurityCardLabels(locale).guardDefaultNote,
      `${locale} has no shipped-default caveat`
    )
  }
})

test('an unlocked guard is surfaced rather than buried', () => {
  // The guard being lifted is the state in which nothing restrains the box. It
  // is the single most important thing this card can report.
  assert.match(card, /guardUnlocked/)
  assert.match(card, /guard\.value\?\.unlocked === true/)
  assert.match(card, /ShieldAlert/, 'the header no longer changes when the guard is lifted')
  assert.match(card, /modsec-card__note--alarm/)
  for (const locale of ['zh-CN', 'en-US']) {
    assert.ok(moduleSecurityCardLabels(locale).guardUnlockedNote, `${locale} does not explain it`)
  }
})

test('the two real panels read the endpoints that actually serve them', () => {
  assert.match(card, /getVowifiDiagnostics/)
  assert.match(card, /getCallFilter/)
  // allSettled, not all: one endpoint failing must not blank the other panel.
  assert.match(card, /Promise\.allSettled/)
  assert.doesNotMatch(code(card), /Promise\.all\(/, 'one failing probe can now blank both panels')

  // The mock-up put a city under the IP. Nothing geolocates the gateway, and
  // the egress lookup requests only the IP and the ISP name.
  // Word-bounded on purpose: an unanchored /city/ matches "opacity" in the
  // stylesheet, which would make this test fire on a CSS edit and teach the
  // next person to delete it.
  assert.doesNotMatch(
    code(card),
    /地理位置|\bgeoloc\w*|\blatitude\b|\blongitude\b|\bcity\b|\bprovince\b/i,
    'the card displays a location for the gateway, which nothing computes'
  )
  for (const locale of ['zh-CN', 'en-US']) {
    const labels = moduleSecurityCardLabels(locale)
    assert.ok(labels.egressNote, `${locale} does not caveat the egress panel`)
  }
})

test('the card does not duplicate the two cards that already exist', () => {
  // ModuleExtraCards already owns the transport-security rows and the 24-hour
  // call and message counts. A second card showing either would be two places
  // to disagree.
  assert.match(extraCards, /secTitle/, 'the existing transport card moved; re-check this test')
  assert.doesNotMatch(code(card), /security\/posture|stats\/communication/)
  assert.doesNotMatch(
    code(card),
    /getSecurityPosture|getCommunicationStats/,
    'the security card now reads the other cards data too'
  )
  // No 24h counters of its own.
  assert.doesNotMatch(code(card), /window_hours|verification|hourly/)
})

test('the card is admin only and lazily loaded', () => {
  assert.match(card, /sessionState\.role === 'admin'/)
  assert.match(card, /v-if="visible"/)
  assert.match(
    dashboard,
    /defineAsyncComponent\(\s*\(\) => import\('\.\.\/components\/ModuleSecurityCard\.vue'\)\s*\)/,
    'the security card is imported eagerly into the dashboard shell'
  )
  assert.match(dashboard, /<ModuleSecurityCard \/>/)
})

test('the contract documents itself and both locales carry the same keys', () => {
  // The backend will be written against this comment, so it has to say which
  // fields are already served elsewhere rather than inviting a second
  // implementation of the egress probe.
  assert.match(contract, /GET \/api\/v1\/security\/module/)
  assert.match(contract, /vowifi-diagnostics/)
  assert.match(contract, /call-filter/)

  const english = moduleSecurityCardLabels('en-US')
  const chinese = moduleSecurityCardLabels('zh-CN')
  assert.deepEqual(
    Object.keys(chinese).sort(),
    Object.keys(english).sort(),
    'the two locales have drifted, so one renders a raw key'
  )
  for (const [key, value] of Object.entries(chinese)) {
    assert.ok(value.trim().length > 0, `zh-CN.${key} is empty`)
    assert.ok(english[key].trim().length > 0, `en-US.${key} is empty`)
  }
  assert.equal(moduleSecurityCardLabels('zh-TW'), chinese)
  assert.equal(moduleSecurityCardLabels('ja-JP'), english)

  // Markdown asterisks in a plain-text label render as literal asterisks.
  for (const [key, value] of [...Object.entries(chinese), ...Object.entries(english)]) {
    assert.doesNotMatch(value, /\*\*/, `${key} carries markdown that renders as literal asterisks`)
  }
})

test('the labels merge on a locale watch, not once on mount', () => {
  assert.match(card, /watch\(\s*\(\) => i18n\.global\.locale\.value/)
  assert.match(card, /immediate: true, flush: 'sync'/)
})

test('the tall row cards cannot stretch the network drawings', async () => {
  // Both of these cards live in the tall row beside the NKN globe and the DHT
  // mesh, which are 713px of fixed-height canvas plus chrome. A grid row is as
  // tall as its tallest item, and `align-items: stretch` then stretches the
  // rest to match -- so a card whose content wanted more would not be clipped,
  // it would make the row taller and leave that much void inside each drawing.
  // Measured before this was fixed: 874px against their 713px.
  //
  // The shell is what prevents it. It is the grid item; the card is positioned
  // absolutely inside it, so the card's content has no say in the row height.
  // Remove the shell and the voids come back silently -- nothing errors, the
  // drawings just grow empty space at the bottom.
  const update = await readFile(
    new URL('../src/components/SoftwareUpdateCard.vue', import.meta.url),
    'utf8'
  )

  for (const [name, source, shell, root] of [
    ['ModuleSecurityCard', card, 'modsec-shell', 'modsec-card'],
    ['SoftwareUpdateCard', update, 'update-shell', 'update-card']
  ]) {
    assert.match(
      source,
      new RegExp(`\\.${shell}\\s*\\{[^}]*position:\\s*relative`),
      `${name}: the shell is no longer a positioning context`
    )
    assert.match(
      source,
      new RegExp(`\\.${root}\\s*\\{\\s*position:\\s*absolute;\\s*inset:\\s*0;`),
      `${name}: the card is back in flow and can stretch the row`
    )
    // And the body must actually be able to scroll, or taking it out of flow
    // would clip content with no way to reach it.
    assert.match(source, new RegExp(`\\.${root}__scroll\\s*\\{[^}]*overflow-y:\\s*auto`), name)
    assert.match(source, new RegExp(`\\.${root}__scrollwrap::after`), `${name}: no scroll fade`)
  }

  // Both sit in the tall grid, not in rows of their own.
  // Searched forward from the marker: the first </LoadingSkeletonBoundary> in
  // the file is hundreds of lines above this row.
  const tallStart = dashboard.indexOf('dashboard-module-grid--tall')
  assert.ok(tallStart > 0, 'the tall row is gone')
  const tallRow = dashboard.slice(
    tallStart,
    dashboard.indexOf('</LoadingSkeletonBoundary>', tallStart)
  )
  assert.match(tallRow, /<ModuleSecurityCard \/>/, 'the security card left the tall row')
  assert.match(tallRow, /<SoftwareUpdateCard \/>/, 'the update card left the tall row')
  assert.doesNotMatch(
    dashboard,
    /dashboard-module-grid--full/,
    'a full-width row is back; these cards belong in the tall row with the drawings'
  )
})
