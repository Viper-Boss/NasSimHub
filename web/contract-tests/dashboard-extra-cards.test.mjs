import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { networkCardLabels } from '../src/i18n/networkCardNotes.ts'

const extra = await readFile(
  new URL('../src/components/ModuleExtraCards.vue', import.meta.url),
  'utf8'
)
const api = await readFile(
  new URL('../src/api/dashboardCards.ts', import.meta.url),
  'utf8'
)
const dashboard = await readFile(
  new URL('../src/views/DashboardView.vue', import.meta.url),
  'utf8'
)

// The three cards that fill the dashboard's insight row: secure transport,
// module information and the communication summary.
//
// Two of them were drawn against a design whose backend does not exist yet.
// That is the operator's call and a reasonable way round, but it puts a
// specific burden on these cards: a card with nothing behind it has to look
// like a card with nothing behind it. Most of what follows guards that, and
// each test names the way it could go wrong.

const LOCALES = [
  'zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'vi-VN', 'es-ES', 'de-DE', 'fr-FR', 'pt-BR'
]

/** The card's template, from its head to the end of its <article>. */
function cardTemplate(marker) {
  const start = extra.indexOf(marker)
  assert.notEqual(start, -1, `the card marked ${marker} is gone`)
  const open = extra.lastIndexOf('<article', start)
  const close = extra.indexOf('</article>', start)
  assert.ok(open !== -1 && close !== -1, `${marker} is not inside an article`)
  return extra.slice(open, close)
}

test('the cards are rendered into the dashboard row beside the others', () => {
  const shortGrid = dashboard.slice(
    dashboard.indexOf('<div class="dashboard-module-grid">'),
    dashboard.indexOf('dashboard-module-grid--tall')
  )
  // The tag has to END here. Without the boundary, renaming the component to
  // ModuleExtraCardsX still matches the prefix and the test passes on a
  // dashboard that renders nothing -- which is exactly what happened when this
  // assertion was first sabotaged.
  assert.match(shortGrid, /<ModuleExtraCards[\s/>]/, 'the three cards left the module grid')
  assert.match(shortGrid, /<ModuleInsightCards[\s/>]/, 'the other four cards left the module grid')
  // The module card is what the module card reads from, so both props have to
  // be handed over; a card that invents its own source is the failure here.
  assert.match(shortGrid, /<ModuleExtraCards[\s/>][\s\S]{0,160}:line="line"/)
  assert.match(shortGrid, /<ModuleExtraCards[\s/>][\s\S]{0,160}:device="deviceFor\(line\)"/)
})

test('a card with no backend never shows a figure it does not have', () => {
  // The whole point. "0 SMS today" is a claim about the gateway's day that
  // nothing has measured, and it is a different statement from "no SMS figures
  // yet" -- so an absent field renders as a dash or the pending label, and the
  // only way that can be true is if the formatter refuses to invent a zero.
  assert.match(
    extra,
    /function figure\(value: number \| undefined\): string \{\s*return value === undefined \? '' :/,
    'the figure formatter no longer distinguishes an absent value from a zero'
  )
  assert.doesNotMatch(
    extra,
    /figure\((?:[^)]*)\) \|\| ['"`]0/,
    'a zero is being substituted for a missing figure'
  )
  // And every tile falls back to a dash rather than to a number.
  for (const marker of ['dashboard.statsTitle', 'dashboard.secTitle']) {
    const template = cardTemplate(marker)
    const fallbacks = [...template.matchAll(/\|\|\s*'([^']*)'/g)].map(match => match[1])
    for (const fallback of fallbacks) {
      assert.ok(
        !/^\d/.test(fallback),
        `a numeric fallback (${fallback}) is rendered when the field is missing`
      )
    }
  }
})

test('no figure is written into the markup of the two pending cards', () => {
  // A mock-up number that ships is the exact failure these cards invite: they
  // were drawn from a design full of plausible-looking figures.
  const standaloneNumber = /(?:^|[^A-Za-z0-9_$.:-])\d+(?:[^A-Za-z0-9_$%.:-]|$)/
  // Sizes, view boxes and bar geometry are real numbers doing layout. They are
  // STRIPPED FROM THE LINE rather than used to skip it: skipping the whole line
  // let `<h5><MessageSquareText :size="12" />{{ … }} 23</h5>` through, because
  // the attribute and the invented figure were on the same line. Measured --
  // that mutation went undetected until this changed.
  const layoutAttribute =
    /(?::?(?:size|x|y|width|height|rx|x1|y1|x2|y2|stroke-width|viewBox|preserveAspectRatio)="[^"]*")/g
  for (const marker of ['dashboard.secTitle', 'dashboard.statsTitle']) {
    for (const raw of cardTemplate(marker).split('\n')) {
      const line = raw.replace(layoutAttribute, '')
      assert.ok(
        !standaloneNumber.test(line),
        `a figure is written into the markup: ${raw.trim()}`
      )
    }
  }
})

test('a pill is only tinted by a state that arrived from the endpoint', () => {
  // A green pill says "this was checked". An absent row has not been checked by
  // anything, so it must stay neutral rather than reading as "fine".
  assert.match(
    extra,
    /function postureTone\(row: SecurityRow \| undefined\): string \{\s*if \(!row\) return ''/,
    'an absent security row can now be given a tone'
  )
  // The pending pill must not carry the ok/warn classes either.
  assert.match(extra, /class="insight-tag insight-sec__pending"/)
  const pendingRule = extra.match(/\.insight-sec__pending \{[^}]*\}/)
  assert.ok(pendingRule, 'the pending pill lost its own styling')
  assert.doesNotMatch(
    pendingRule[0],
    /--success|--warning|--danger/,
    'the pending pill is tinted like a state'
  )
})

test('each pending card says its endpoint is missing, and stops saying so once it is not', () => {
  // Without this the dashes read as "nothing happened today" rather than "we
  // have not been told". And the note has to disappear when the payload
  // arrives, or it becomes a permanent untruth.
  assert.match(
    extra,
    /v-if="!posture" class="insight-card__pending-note"/,
    'the security card no longer explains its dashes'
  )
  assert.match(
    extra,
    /v-if="!stats" class="insight-card__pending-note"/,
    'the traffic card no longer explains its dashes'
  )
})

test('the trend is drawn only when there are buckets to draw', () => {
  // An empty chart frame reads as "nothing happened", which is a stronger claim
  // than "we have not been told".
  assert.match(
    extra,
    /v-if="trendBars\.length" class="insight-stats__trend"/,
    'the trend chart renders without data'
  )
  // One bar per bucket rather than a line through them: a line interpolates
  // across an hour nobody measured.
  assert.match(extra, /trendBuckets\.value/, 'the bars no longer come from the buckets')
  assert.match(extra, /<rect\s/, 'the trend is no longer drawn as discrete bars')
})

test('both readers answer null for anything that is not a payload', () => {
  // 404, 501, a parse error, a gateway that has never heard of the path: all
  // the same thing to a card, which is "there is nothing behind me yet".
  for (const reader of ['getSecurityPosture', 'getCommunicationStats']) {
    const body = api.slice(api.indexOf(`export async function ${reader}`))
    const fn = body.slice(0, body.indexOf('\n}\n') + 2)
    assert.match(fn, /catch \{\s*(?:\/\/[^\n]*\n\s*)*return null/, `${reader} swallows a failure without answering null`)
    assert.match(
      fn,
      /if \(!payload \|\| typeof payload !== 'object'\) return null/,
      `${reader} accepts a non-object as a payload`
    )
  }
  // Every field optional, so a partial payload renders partially rather than
  // failing or filling in.
  const posture = api.slice(api.indexOf('export interface SecurityPosture'), api.indexOf('function readRow'))
  for (const field of ['transport', 'post_quantum', 'pinning', 'identity', 'replay', 'packet_auth', 'ota', 'events']) {
    assert.match(posture, new RegExp(`${field}\\?:`), `${field} is no longer optional`)
  }
})

test('a count is only a count when it really is one', () => {
  // A negative or fractional "count" from a backend that is not written yet
  // would render as a figure. It is not one.
  assert.match(
    api,
    /function readCount[\s\S]{0,200}Number\.isFinite\(value\) && value >= 0/,
    'the count reader accepts values that are not counts'
  )
  // A bucket with no hour cannot be placed on the trend, and guessing its
  // position from its index would draw a bar where nothing was measured.
  assert.match(
    api,
    /const hour = readCount\(bucket\.hour\)\s*if \(hour === undefined\) return null/,
    'a bucket with no hour is now placed on the trend anyway'
  )
})

test('the module card reads the records the app already holds, and refreshes exactly those', () => {
  // This is the one card of the three that is fully real, so the risk here is
  // the opposite one: a placeholder creeping in beside genuine readings.
  assert.match(extra, /const moduleFacts = computed<ModuleFact\[\]>/)
  const facts = extra.slice(extra.indexOf('const moduleFacts'), extra.indexOf('function portTypeLabel'))
  assert.match(facts, /props\.line/, 'the module facts no longer come from the line')
  assert.match(facts, /t\('device\.notReported'\)/, 'an unreported field is no longer named as unreported')
  // Nothing on this card may be a literal except the two identifier labels,
  // which are protocol names rather than values.
  const literals = [...facts.matchAll(/value: '([^']*)'/g)].map(match => match[1])
  for (const literal of literals) {
    assert.equal(literal, '', `the module card ships a literal value: ${literal}`)
  }
  // The refresh control re-reads the bootstrap and the devices -- the two
  // records the facts come from -- rather than reloading the page.
  assert.match(extra, /refreshDeviceWorkspace/, 'the refresh button no longer refreshes the records it shows')
  assert.doesNotMatch(extra, /location\.reload/, 'the refresh button reloads the page instead')
  // The identifiers are masked by default, like everywhere else they appear.
  assert.match(extra, /<SensitiveValue/, 'ICCID, IMSI and IMEI are no longer masked')
})

test('every new label exists in all nine locales', () => {
  // A missing key renders as its own name on screen, which is what happened the
  // first time these cards were drawn.
  const keys = [
    'cardPending', 'cardPendingNote',
    'secTitle', 'secTransport', 'secPostQuantum', 'secPinning', 'secIdentity',
    'secReplay', 'secPacketAuth', 'secOta', 'secEventsPending', 'secEventsTitle',
    'secAnomalous', 'secKeyState', 'secReplayBlocked', 'secDowngradeGuard',
    'secKeyOk', 'secKeyWarn', 'secKeyExpired', 'secOn', 'secOff',
    'modTitle', 'modPortsLabel',
    'statsTitle', 'statsWindow', 'statsSmsIn', 'statsSmsOut', 'statsSmsFailed',
    'statsSmsCode', 'statsTrend'
  ]
  for (const locale of LOCALES) {
    const labels = networkCardLabels(locale)
    for (const key of keys) {
      assert.ok(labels[key], `${locale} is missing dashboard.${key}`)
    }
  }
  // The two windowed headings interpolate a number of hours. A translation that
  // dropped the slot would silently render "Last  h".
  for (const locale of LOCALES) {
    const labels = networkCardLabels(locale)
    for (const key of ['secEventsTitle', 'statsWindow']) {
      assert.match(labels[key], /\{h\}/, `${locale}'s ${key} lost its {h} slot`)
    }
  }
})

test('the labels are merged before the first render, not after it', () => {
  // These cards have almost no changing state -- two of them sit on endpoints
  // that answer nothing -- so nothing re-renders them. Merging a message is a
  // store mutation that `t()` does not track, so a merge in `onMounted` never
  // reaches the screen: measured, every label rendered as its raw key name.
  assert.match(
    extra,
    /watch\(\s*i18n\.locale,[\s\S]{0,200}mergeLocaleMessage[\s\S]{0,120}flush: 'sync'/,
    'the labels are no longer merged synchronously during setup'
  )
  assert.doesNotMatch(
    extra,
    /onMounted\(\(\) => \{\s*i18nBridge\(\)/,
    'the labels are merged from onMounted again, which is too late to render'
  )
})

test('no glyph is imported that the app does not already bundle', () => {
  // Every Lucide icon in the app is collected into one shared chunk that sits
  // in the STARTUP SHELL, so a new glyph imported by a lazily loaded card still
  // costs every visitor bytes before the app has drawn anything. Five of them
  // measured 0.9 KiB and put the startup budget over its limit.
  const imported = [...extra.matchAll(/import \{([^}]*)\} from '@lucide\/vue'/gs)]
    .flatMap(match => match[1].split(','))
    .map(name => name.trim())
    .filter(Boolean)
  assert.ok(imported.length > 0, 'the card imports no icons at all')
  const bundled = new Set([
    'BadgeCheck', 'ChartNoAxesCombined', 'CloudUpload', 'Cpu', 'FileCheck2',
    'History', 'KeyRound', 'Lock', 'MessageSquareText', 'PhoneCall',
    'RefreshCw', 'ShieldCheck', 'Zap'
  ])
  for (const name of imported) {
    assert.ok(
      bundled.has(name),
      `${name} is a glyph this card added; check it against the icons already ` +
        'in the shared chunk before widening this list, and re-run the bundle budget'
    )
  }
})
